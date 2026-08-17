/**
 * Moving bytes from Hugging Face into S3 without staging the model on disk.
 *
 * Each file is an S3 multipart upload whose parts are filled by ranged GETs, a
 * bounded number in flight at once:
 *
 *     for each 64 MiB window:  GET …/resolve/… Range: bytes=lo-hi  ──▶ UploadPart(n)
 *
 * The shape is chosen for failure, not for speed. A part is independently
 * retryable, so a blip 25 GB into a file costs 64 MiB rather than the file —
 * finer-grained than a whole-file resume. A part that exhausts its retries does
 * not fail the seed: that one file falls back to disk staging, which bounds disk
 * use to a single file rather than the whole model. Streaming is the default;
 * robustness is the fallback.
 */

import { createHash, type Hash } from 'node:crypto';
import { createReadStream, createWriteStream } from 'node:fs';
import { mkdir, rm, stat } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import {
  AbortMultipartUploadCommand,
  CompleteMultipartUploadCommand,
  CreateMultipartUploadCommand,
  PutObjectCommand,
  type S3Client,
  UploadPartCommand,
} from '@aws-sdk/client-s3';
import { authHeaders, resolveUrl, type RemoteFile } from './hf';

export interface TransferDeps {
  s3: S3Client;
  fetchImpl?: typeof fetch;
  /** Where a fallback file is staged. Bounded to one file at a time. */
  stagingDir?: string;
  sleep?: (ms: number) => Promise<void>;
}

export interface TransferOptions {
  bucket: string;
  prefix: string;
  modelId: string;
  revision: string;
  token: string;
  partSizeBytes: number;
  partConcurrency: number;
  partAttempts: number;
  /** Called as bytes land, for progress reporting. */
  onBytes?: (delta: number) => void;
  /** Called when a file could only be completed by staging it on disk. */
  onStaged?: (path: string, reason: string) => void;
}

export interface TransferredFile {
  path: string;
  storeAs: string;
  size: number;
  /** sha256 of what was actually stored — computed here, always present. */
  sha256: string;
  staged: boolean;
}

const defaultSleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

/** S3 refuses a multipart upload with more than 10 000 parts. */
const MAX_PARTS = 10_000;

/**
 * Part size for a file: the configured size, raised if the file is large enough
 * that the configured size would exceed the part limit. Without this, a
 * sufficiently large file fails at CompleteMultipartUpload — after transferring
 * every byte.
 */
export function partSizeFor(fileSize: number, configured: number): number {
  const needed = Math.ceil(fileSize / MAX_PARTS);
  return Math.max(configured, needed);
}

/** Byte windows covering a file. */
export function partRanges(size: number, partSize: number): { lo: number; hi: number }[] {
  if (size === 0) {
    return [];
  }
  const ranges: { lo: number; hi: number }[] = [];
  for (let lo = 0; lo < size; lo += partSize) {
    ranges.push({ lo, hi: Math.min(lo + partSize, size) - 1 });
  }
  return ranges;
}

/** Retryable transport failures. A 403 is included: a signature can expire. */
function isRetryableStatus(status: number): boolean {
  return status === 403 || status === 408 || status === 429 || status >= 500;
}

class PartFailure extends Error {}

/**
 * Fetch one window. Returns the bytes; throws PartFailure on anything that
 * should be retried, and a plain Error on something that will not improve
 * (a 404, an auth failure).
 */
async function fetchPart(
  url: string,
  token: string,
  lo: number,
  hi: number,
  fetchImpl: typeof fetch,
): Promise<Uint8Array> {
  let response: Response;
  try {
    response = await fetchImpl(url, {
      headers: { ...authHeaders(token), range: `bytes=${lo}-${hi}` },
      redirect: 'follow',
    });
  } catch (err) {
    // Network-level failure (reset, DNS, timeout) — always worth another go.
    throw new PartFailure(`fetching bytes ${lo}-${hi}: ${(err as Error).message}`);
  }
  if (!response.ok) {
    const message = `fetching bytes ${lo}-${hi}: HTTP ${response.status}`;
    throw isRetryableStatus(response.status) ? new PartFailure(message) : new Error(message);
  }
  // A 200 where a 206 was asked for means the server ignored Range and is
  // sending the whole file — treat it as a failure rather than writing the
  // entire file into one part and corrupting the object.
  if (response.status !== 206 && !(lo === 0 && hi >= 0 && response.status === 200)) {
    throw new PartFailure(`fetching bytes ${lo}-${hi}: expected 206, got ${response.status}`);
  }
  const body = new Uint8Array(await response.arrayBuffer());
  const expected = hi - lo + 1;
  if (body.byteLength !== expected) {
    throw new PartFailure(`bytes ${lo}-${hi}: expected ${expected} bytes, got ${body.byteLength}`);
  }
  return body;
}

/** Fetch a window with bounded retries and exponential backoff. */
async function fetchPartWithRetry(
  url: string,
  token: string,
  lo: number,
  hi: number,
  attempts: number,
  fetchImpl: typeof fetch,
  sleep: (ms: number) => Promise<void>,
): Promise<Uint8Array> {
  let last: Error | undefined;
  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    try {
      return await fetchPart(url, token, lo, hi, fetchImpl);
    } catch (err) {
      if (!(err instanceof PartFailure)) {
        throw err;
      }
      last = err;
      if (attempt < attempts) {
        await sleep(Math.min(2 ** (attempt - 1) * 500, 8000));
      }
    }
  }
  throw new PartFailure(`${last?.message ?? 'unknown'} (after ${attempts} attempts)`);
}

/**
 * Stream one file into S3 as ranged parts.
 *
 * sha256 is computed over the parts in order, which is why completed parts are
 * held until their turn: hashing must see the file's bytes sequentially even
 * though they arrive out of order. Only the parts between the lowest
 * outstanding one and the highest completed one are retained, so memory stays
 * bounded by concurrency rather than by file size.
 */
async function streamFile(
  file: RemoteFile,
  options: TransferOptions,
  deps: TransferDeps,
): Promise<{ sha256: string }> {
  const fetchImpl = deps.fetchImpl ?? fetch;
  const sleep = deps.sleep ?? defaultSleep;
  const url = resolveUrl(options.modelId, options.revision, file.path);
  const key = `${options.prefix}${file.storeAs}`;
  const partSize = partSizeFor(file.size, options.partSizeBytes);
  const ranges = partRanges(file.size, partSize);

  const created = await deps.s3.send(
    new CreateMultipartUploadCommand({ Bucket: options.bucket, Key: key }),
  );
  const uploadId = created.UploadId;
  if (!uploadId) {
    throw new Error(`S3 did not return an upload id for ${key}`);
  }

  const hash = createHash('sha256');
  const etags: { PartNumber: number; ETag: string }[] = [];
  // Parts fetched but not yet hashed, keyed by index; drained in order.
  const pending = new Map<number, Uint8Array>();
  let nextToHash = 0;

  const drain = () => {
    while (pending.has(nextToHash)) {
      hash.update(pending.get(nextToHash)!);
      pending.delete(nextToHash);
      nextToHash += 1;
    }
  };

  try {
    let cursor = 0;
    const inFlight = new Set<Promise<void>>();
    const failures: Error[] = [];

    const startPart = (index: number) => {
      const { lo, hi } = ranges[index];
      const task = (async () => {
        const body = await fetchPartWithRetry(
          url,
          options.token,
          lo,
          hi,
          options.partAttempts,
          fetchImpl,
          sleep,
        );
        const uploaded = await deps.s3.send(
          new UploadPartCommand({
            Bucket: options.bucket,
            Key: key,
            UploadId: uploadId,
            PartNumber: index + 1,
            Body: body,
          }),
        );
        etags.push({ PartNumber: index + 1, ETag: uploaded.ETag ?? '' });
        pending.set(index, body);
        drain();
        options.onBytes?.(body.byteLength);
      })().catch((err: Error) => {
        failures.push(err);
      });
      inFlight.add(task);
      void task.finally(() => inFlight.delete(task));
    };

    while (cursor < ranges.length && failures.length === 0) {
      while (inFlight.size < options.partConcurrency && cursor < ranges.length) {
        startPart(cursor);
        cursor += 1;
      }
      if (inFlight.size > 0) {
        await Promise.race(inFlight);
      }
    }
    await Promise.all([...inFlight]);
    if (failures.length > 0) {
      throw failures[0];
    }
    drain();

    etags.sort((a, b) => a.PartNumber - b.PartNumber);
    await deps.s3.send(
      new CompleteMultipartUploadCommand({
        Bucket: options.bucket,
        Key: key,
        UploadId: uploadId,
        MultipartUpload: { Parts: etags },
      }),
    );
    return { sha256: hash.digest('hex') };
  } catch (err) {
    // Leaving an incomplete multipart upload behind would accrue storage
    // charges invisibly, so abort before rethrowing.
    await deps.s3
      .send(new AbortMultipartUploadCommand({ Bucket: options.bucket, Key: key, UploadId: uploadId }))
      .catch(() => undefined);
    throw err;
  }
}

/**
 * The fallback for a file whose parts would not go through: stage it on disk
 * in one sequential read, upload it, delete it.
 *
 * This is what stops streaming from becoming a way to get stuck. Disk use is
 * bounded to one file because the staged copy is removed before the next file
 * begins — a 30 GB model still needs only its largest file's worth of disk, and
 * only when something has already gone wrong.
 */
async function stageFile(
  file: RemoteFile,
  options: TransferOptions,
  deps: TransferDeps,
): Promise<{ sha256: string }> {
  const fetchImpl = deps.fetchImpl ?? fetch;
  const stagingDir = deps.stagingDir ?? '/tmp/seed-staging';
  const localPath = join(stagingDir, file.storeAs);
  await mkdir(dirname(localPath), { recursive: true });

  try {
    const response = await fetchImpl(resolveUrl(options.modelId, options.revision, file.path), {
      headers: authHeaders(options.token),
      redirect: 'follow',
    });
    if (!response.ok || !response.body) {
      throw new Error(`staging ${file.path}: HTTP ${response.status}`);
    }
    const hash = createHash('sha256');
    await pipeline(
      Readable.fromWeb(response.body as Parameters<typeof Readable.fromWeb>[0]),
      async function* (source: AsyncIterable<Buffer>) {
        for await (const chunk of source) {
          hash.update(chunk);
          options.onBytes?.(chunk.byteLength);
          yield chunk;
        }
      },
      createWriteStream(localPath),
    );

    const written = await stat(localPath);
    if (file.size > 0 && written.size !== file.size) {
      throw new Error(
        `staging ${file.path}: expected ${file.size} bytes, wrote ${written.size}`,
      );
    }
    await deps.s3.send(
      new PutObjectCommand({
        Bucket: options.bucket,
        Key: `${options.prefix}${file.storeAs}`,
        Body: createReadStream(localPath),
        ContentLength: written.size,
      }),
    );
    return { sha256: hash.digest('hex') };
  } finally {
    await rm(localPath, { force: true }).catch(() => undefined);
  }
}

/**
 * Transfer one file, streaming by default and staging if streaming will not
 * complete. Verifies what was stored against what the source published.
 */
export async function transferFile(
  file: RemoteFile,
  options: TransferOptions,
  deps: TransferDeps,
): Promise<TransferredFile> {
  let sha256: string;
  let staged = false;
  try {
    ({ sha256 } = await streamFile(file, options, deps));
  } catch (err) {
    if (!(err instanceof PartFailure)) {
      // A 404 or an auth failure will not be fixed by staging it instead.
      throw err;
    }
    staged = true;
    options.onStaged?.(file.path, (err as Error).message);
    ({ sha256 } = await stageFile(file, options, deps));
  }

  // Verify against the source's published sha256 where there is one. Small
  // plain-git files publish a git blob hash rather than a content sha256, so
  // those are checked by size; the computed hash is recorded either way, so the
  // manifest always states what is actually stored.
  if (file.sha256 && file.sha256 !== sha256) {
    throw new Error(
      `checksum mismatch for ${file.path}: source published ${file.sha256}, transferred ${sha256}`,
    );
  }
  return { path: file.path, storeAs: file.storeAs, size: file.size, sha256, staged };
}
