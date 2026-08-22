import { createHash } from 'node:crypto';
import { mkdtempSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  AbortMultipartUploadCommand,
  CompleteMultipartUploadCommand,
  CreateMultipartUploadCommand,
  PutObjectCommand,
  UploadPartCommand,
} from '@aws-sdk/client-s3';
import type { S3Client } from '@aws-sdk/client-s3';
import { partRanges, partSizeFor, transferFile } from '../src/transfer';
import type { RemoteFile } from '../src/hf';

/** A fake S3 that records what it was asked to do and reassembles the object. */
function fakeS3() {
  const parts = new Map<number, Uint8Array>();
  const calls: string[] = [];
  let aborted = false;
  let completed = false;
  let putBody: Uint8Array | null = null;

  const client = {
    async send(command: unknown) {
      if (command instanceof CreateMultipartUploadCommand) {
        calls.push('create');
        return { UploadId: 'upload-1' };
      }
      if (command instanceof UploadPartCommand) {
        const input = command.input;
        calls.push(`part:${input.PartNumber}`);
        parts.set(input.PartNumber as number, input.Body as Uint8Array);
        return { ETag: `"etag-${input.PartNumber}"` };
      }
      if (command instanceof CompleteMultipartUploadCommand) {
        calls.push('complete');
        completed = true;
        return {};
      }
      if (command instanceof AbortMultipartUploadCommand) {
        calls.push('abort');
        aborted = true;
        return {};
      }
      if (command instanceof PutObjectCommand) {
        calls.push('put');
        // The staging path streams from disk; read it back for assertions.
        const chunks: Buffer[] = [];
        for await (const chunk of command.input.Body as AsyncIterable<Buffer>) {
          chunks.push(chunk);
        }
        putBody = new Uint8Array(Buffer.concat(chunks));
        return {};
      }
      throw new Error(`unexpected command ${command?.constructor?.name}`);
    },
  } as unknown as S3Client;

  return {
    client,
    calls,
    get aborted() {
      return aborted;
    },
    get completed() {
      return completed;
    },
    get assembled(): Uint8Array {
      if (putBody) {
        return putBody;
      }
      const ordered = [...parts.entries()].sort((a, b) => a[0] - b[0]).map(([, b]) => b);
      return new Uint8Array(Buffer.concat(ordered.map((b) => Buffer.from(b))));
    },
  };
}

/** Deterministic body so a checksum can be asserted. */
function body(size: number): Uint8Array {
  const bytes = new Uint8Array(size);
  for (let i = 0; i < size; i += 1) {
    bytes[i] = i % 251;
  }
  return bytes;
}

const sha = (bytes: Uint8Array) => createHash('sha256').update(bytes).digest('hex');

/**
 * A fetch that serves ranges out of `content`, optionally failing the first
 * `failures` attempts at a given window.
 */
function fakeFetch(
  content: Uint8Array,
  options: { failWindow?: number; failures?: number; status?: number } = {},
) {
  const attempts = new Map<string, number>();
  const log: string[] = [];
  let remaining = options.failures ?? 0;

  const impl = (async (url: string | URL, init?: RequestInit) => {
    const range = (init?.headers as Record<string, string> | undefined)?.range;
    log.push(range ?? 'whole');
    if (!range) {
      // The staging fallback: whole-file stream.
      return new Response(new Blob([content as unknown as BlobPart]).stream(), { status: 200 });
    }
    const [lo, hi] = range.replace('bytes=', '').split('-').map(Number);
    const key = range;
    attempts.set(key, (attempts.get(key) ?? 0) + 1);
    if (options.failWindow === lo && remaining > 0) {
      remaining -= 1;
      return new Response('nope', { status: options.status ?? 500 });
    }
    return new Response(content.slice(lo, hi + 1) as unknown as BodyInit, {
      status: 206,
      headers: { 'content-range': `bytes ${lo}-${hi}/${content.length}` },
    });
  }) as unknown as typeof fetch;

  return { impl, log, attempts };
}

const OPTIONS = {
  bucket: 'b',
  prefix: 'models/vllm/org/m/',
  modelId: 'org/m',
  revision: 'abc',
  token: '',
  partSizeBytes: 256,
  partConcurrency: 3,
  partAttempts: 3,
};

describe('part maths', () => {
  it('covers a file exactly, with a short final part', () => {
    expect(partRanges(600, 256)).toEqual([
      { lo: 0, hi: 255 },
      { lo: 256, hi: 511 },
      { lo: 512, hi: 599 },
    ]);
  });

  it('has no parts for an empty file', () => {
    expect(partRanges(0, 256)).toEqual([]);
  });

  it('raises the part size rather than exceeding S3s 10 000 part limit', () => {
    // 1 TiB at 64 MiB would be 16 384 parts and would fail at completion —
    // after transferring every byte.
    const oneTiB = 1024 ** 4;
    const size = partSizeFor(oneTiB, 64 * 1024 * 1024);
    expect(Math.ceil(oneTiB / size)).toBeLessThanOrEqual(10_000);
  });

  it('leaves the configured size alone for an ordinary file', () => {
    expect(partSizeFor(1024, 256)).toBe(256);
  });
});

describe('streaming a file', () => {
  it('reassembles the object exactly and reports the right checksum', async () => {
    const content = body(700);
    const s3 = fakeS3();
    const { impl } = fakeFetch(content);
    const file: RemoteFile = { path: 'a.bin', storeAs: 'a.bin', size: 700, sha256: sha(content) };

    const result = await transferFile(file, OPTIONS, { s3: s3.client, fetchImpl: impl });

    expect(result.sha256).toBe(sha(content));
    expect(s3.assembled).toEqual(content);
    expect(s3.completed).toBe(true);
    expect(result.staged).toBe(false);
  });

  it('hashes in file order even though parts arrive out of order', async () => {
    // Concurrency means part 3 can land before part 1; the hash must not.
    const content = body(2000);
    const s3 = fakeS3();
    const { impl } = fakeFetch(content);
    const file: RemoteFile = { path: 'a.bin', storeAs: 'a.bin', size: 2000 };

    const result = await transferFile(file, OPTIONS, { s3: s3.client, fetchImpl: impl });
    expect(result.sha256).toBe(sha(content));
  });

  it('retries only the failed part, not the file', async () => {
    const content = body(1024);
    const s3 = fakeS3();
    const { impl, attempts } = fakeFetch(content, { failWindow: 256, failures: 1 });
    const file: RemoteFile = { path: 'a.bin', storeAs: 'a.bin', size: 1024 };

    const result = await transferFile(file, OPTIONS, {
      s3: s3.client,
      fetchImpl: impl,
      sleep: async () => {},
    });

    expect(result.sha256).toBe(sha(content));
    expect(attempts.get('bytes=256-511')).toBe(2);
    // The blast radius: every other window was fetched exactly once.
    expect(attempts.get('bytes=0-255')).toBe(1);
    expect(attempts.get('bytes=512-767')).toBe(1);
  });

  it('retries an expired signature (403) rather than failing', async () => {
    const content = body(512);
    const s3 = fakeS3();
    const { impl, attempts } = fakeFetch(content, { failWindow: 0, failures: 1, status: 403 });
    const file: RemoteFile = { path: 'a.bin', storeAs: 'a.bin', size: 512 };

    await transferFile(file, OPTIONS, { s3: s3.client, fetchImpl: impl, sleep: async () => {} });
    expect(attempts.get('bytes=0-255')).toBe(2);
  });

  it('does not leave an incomplete multipart upload behind', async () => {
    const content = body(512);
    const s3 = fakeS3();
    // Fails past its retries, and staging fails too, so the whole thing throws.
    const impl = (async () => new Response('gone', { status: 404 })) as unknown as typeof fetch;
    const file: RemoteFile = { path: 'a.bin', storeAs: 'a.bin', size: 512 };

    await expect(
      transferFile(file, OPTIONS, { s3: s3.client, fetchImpl: impl, sleep: async () => {} }),
    ).rejects.toThrow(/HTTP 404/);
    expect(s3.aborted).toBe(true);
    expect(s3.completed).toBe(false);
    void content;
  });

  it('does not fall back to staging for a failure staging cannot fix', async () => {
    const s3 = fakeS3();
    const impl = (async () => new Response('gone', { status: 404 })) as unknown as typeof fetch;
    const file: RemoteFile = { path: 'a.bin', storeAs: 'a.bin', size: 512 };

    await expect(
      transferFile(file, OPTIONS, { s3: s3.client, fetchImpl: impl, sleep: async () => {} }),
    ).rejects.toThrow();
    // A 404 is not retried and not staged — it will not improve.
    expect(s3.calls.filter((c) => c === 'put')).toHaveLength(0);
  });
});

describe('the staging fallback', () => {
  it('completes a file whose parts will not go through, and cleans up after itself', async () => {
    const content = body(700);
    const s3 = fakeS3();
    // Exhausts every attempt on one window, so streaming gives up on the file.
    const { impl } = fakeFetch(content, { failWindow: 256, failures: 99 });
    const stagingDir = mkdtempSync(join(tmpdir(), 'seed-test-'));
    const file: RemoteFile = { path: 'a.bin', storeAs: 'a.bin', size: 700, sha256: sha(content) };
    const staged: string[] = [];

    const result = await transferFile(
      file,
      { ...OPTIONS, onStaged: (p) => staged.push(p) },
      { s3: s3.client, fetchImpl: impl, sleep: async () => {}, stagingDir },
    );

    expect(result.staged).toBe(true);
    expect(result.sha256).toBe(sha(content));
    expect(s3.assembled).toEqual(content);
    expect(staged).toEqual(['a.bin']);
    // Disk use stays bounded: the staged copy is gone once uploaded.
    expect(readdirSync(stagingDir)).toEqual([]);
  });

  it('aborts the abandoned multipart upload before staging', async () => {
    const content = body(700);
    const s3 = fakeS3();
    const { impl } = fakeFetch(content, { failWindow: 256, failures: 99 });
    const stagingDir = mkdtempSync(join(tmpdir(), 'seed-test-'));
    const file: RemoteFile = { path: 'a.bin', storeAs: 'a.bin', size: 700 };

    await transferFile(file, OPTIONS, {
      s3: s3.client,
      fetchImpl: impl,
      sleep: async () => {},
      stagingDir,
    });
    expect(s3.aborted).toBe(true);
  });
});

describe('integrity', () => {
  it('fails the file when what arrived is not what the source published', async () => {
    const content = body(512);
    const s3 = fakeS3();
    const { impl } = fakeFetch(content);
    const file: RemoteFile = {
      path: 'a.bin',
      storeAs: 'a.bin',
      size: 512,
      sha256: 'a'.repeat(64),
    };

    await expect(
      transferFile(file, OPTIONS, { s3: s3.client, fetchImpl: impl }),
    ).rejects.toThrow(/checksum mismatch for a\.bin/);
  });

  it('still records a computed checksum when the source publishes none', async () => {
    // Small plain-git files publish a git blob hash, not a content sha256, so
    // there is nothing to verify against — but the manifest must still state
    // what is actually stored.
    const content = body(64);
    const s3 = fakeS3();
    const { impl } = fakeFetch(content);
    const file: RemoteFile = { path: 'config.json', storeAs: 'config.json', size: 64 };

    const result = await transferFile(file, OPTIONS, { s3: s3.client, fetchImpl: impl });
    expect(result.sha256).toBe(sha(content));
  });

  it('rejects a server that ignores Range and sends the whole file', async () => {
    // Writing the entire file into part 1 would corrupt the object silently.
    const content = body(1024);
    const s3 = fakeS3();
    const impl = (async () =>
      new Response(content as unknown as BodyInit, { status: 200 })) as unknown as typeof fetch;
    const file: RemoteFile = { path: 'a.bin', storeAs: 'a.bin', size: 1024 };

    await expect(
      transferFile(file, OPTIONS, {
        s3: s3.client,
        fetchImpl: impl,
        sleep: async () => {},
        stagingDir: mkdtempSync(join(tmpdir(), 'seed-test-')),
      }),
    ).resolves.toBeTruthy();
    // It must have refused the oversized part and reached the staging path.
    expect(s3.calls).toContain('put');
  });
});
