/**
 * The seeder's orchestration: what it reports, in what order, and — the one
 * that matters most — that the manifest is written only after every file has
 * transferred, since its presence is what marks the weights complete.
 */

import { describe, expect, it, vi } from 'vitest';
import type { S3Client } from '@aws-sdk/client-s3';
import type { SeedJob, SeedRecord } from '../../lambda/shared/seed/contract';
import { Reporter } from '../src/emf';
import { runSeed, type RunDeps } from '../src/index';
import type { TransferredFile } from '../src/transfer';

const JOB: SeedJob = {
  seedId: 'vllm--org-m',
  runner: 'vllm',
  modelId: 'org/m',
  quant: '',
  revision: '',
  bucket: 'b',
  prefix: 'models/vllm/org/m/',
  selection: { include: ['*'] },
  hfSecretArn: '',
  region: 'us-east-1',
  recordPath: '/dev/null',
  partSizeBytes: 1024,
  partConcurrency: 2,
  partAttempts: 3,
};

function harness(overrides: Partial<RunDeps> = {}) {
  const records: SeedRecord[] = [];
  const order: string[] = [];
  const reporter = new Reporter({
    seedId: JOB.seedId,
    runner: 'vllm',
    modelId: JOB.modelId,
    instanceId: 'i-1',
    recordPath: '/dev/null',
    sink: (r) => records.push(r),
  });

  const deps: RunDeps = {
    readToken: async () => '',
    planTransfer: async () => ({
      revision: 'resolved-sha',
      totalBytes: 300,
      files: [
        { path: 'a.bin', storeAs: 'a.bin', size: 100 },
        { path: 'b.bin', storeAs: 'b.bin', size: 200 },
      ],
    }),
    transferFile: async (file, options): Promise<TransferredFile> => {
      order.push(`transfer:${file.storeAs}`);
      options.onBytes?.(file.size);
      return { path: file.path, storeAs: file.storeAs, size: file.size, sha256: 'x', staged: false };
    },
    writeManifest: async () => {
      order.push('manifest');
    },
    ...overrides,
  };
  return { reporter, records, order, deps, s3: {} as S3Client };
}

const phases = (records: SeedRecord[]) => records.map((r) => r.Phase);

describe('a successful run', () => {
  it('writes the manifest only after every file has transferred', async () => {
    const h = harness();
    await runSeed(JOB, h.reporter, h.s3, h.deps);
    // The manifest's presence is what marks the weights complete, so it must be
    // last — a manifest written mid-transfer would mark a partial prefix done.
    expect(h.order).toEqual(['transfer:a.bin', 'transfer:b.bin', 'manifest']);
  });

  it('moves through resolving, transferring, finalising, succeeded', async () => {
    const h = harness();
    await runSeed(JOB, h.reporter, h.s3, h.deps);
    expect(phases(h.records)[0]).toBe('resolving');
    expect(phases(h.records)).toContain('transferring');
    expect(phases(h.records)).toContain('finalising');
    expect(phases(h.records).at(-1)).toBe('succeeded');
  });

  it('reports before any bytes move, so the listing phase is not a stall', async () => {
    // A big repository can spend minutes listing; silence there would be reaped.
    const h = harness();
    await runSeed(JOB, h.reporter, h.s3, h.deps);
    const beforeTransfer = h.records.slice(0, phases(h.records).indexOf('transferring'));
    expect(beforeTransfer.length).toBeGreaterThan(0);
    expect(beforeTransfer.every((r) => r.Phase === 'resolving')).toBe(true);
  });

  it('records the resolved revision on the terminal record', async () => {
    const h = harness();
    await runSeed(JOB, h.reporter, h.s3, h.deps);
    expect(h.records.at(-1)?.Revision).toBe('resolved-sha');
  });

  it('counts every byte and file exactly once', async () => {
    const h = harness();
    await runSeed(JOB, h.reporter, h.s3, h.deps);
    const done = h.records.at(-1);
    expect(done?.BytesDone).toBe(300);
    expect(done?.FilesDone).toBe(2);
    expect(done?.FilesTotal).toBe(2);
  });

  it('emits the started metric once', async () => {
    const h = harness();
    await runSeed(JOB, h.reporter, h.s3, h.deps);
    expect(h.records.filter((r) => r.Started === 1)).toHaveLength(1);
  });

  it('does not mention staging when nothing was staged', async () => {
    const h = harness();
    await runSeed(JOB, h.reporter, h.s3, h.deps);
    expect(h.records.at(-1)).not.toHaveProperty('StagedFiles');
  });
});

describe('a run that has to stage a file', () => {
  it('says which file fell back, and carries the count to the end', async () => {
    const h = harness({
      transferFile: async (file, options): Promise<TransferredFile> => {
        if (file.storeAs === 'a.bin') {
          options.onStaged?.(file.path, 'part exhausted its retries');
        }
        options.onBytes?.(file.size);
        return {
          path: file.path,
          storeAs: file.storeAs,
          size: file.size,
          sha256: 'x',
          staged: file.storeAs === 'a.bin',
        };
      },
    });
    await runSeed(JOB, h.reporter, h.s3, h.deps);

    const staged = h.records.find((r) => String(r.Message ?? '').includes('staging it on disk'));
    expect(staged?.CurrentFile).toBe('a.bin');
    // Still a success — staging is the fallback, not a failure.
    expect(h.records.at(-1)?.Phase).toBe('succeeded');
    expect(h.records.at(-1)?.StagedFiles).toBe(1);
  });
});

describe('a run that fails', () => {
  it('propagates a transfer failure and writes no manifest', async () => {
    const h = harness({
      transferFile: async () => {
        throw new Error('checksum mismatch for a.bin');
      },
    });
    await expect(runSeed(JOB, h.reporter, h.s3, h.deps)).rejects.toThrow(/checksum mismatch/);
    // The caller (main) turns this into the terminal record; what matters here
    // is that nothing marked the prefix complete.
    expect(h.order).not.toContain('manifest');
    expect(phases(h.records)).not.toContain('succeeded');
  });

  it('writes no manifest when the repository cannot be resolved', async () => {
    const h = harness({
      planTransfer: async () => {
        throw new Error('cannot resolve org/m@main: HTTP 404');
      },
    });
    await expect(runSeed(JOB, h.reporter, h.s3, h.deps)).rejects.toThrow(/HTTP 404/);
    expect(h.order).toHaveLength(0);
  });

  it('does not swallow an ambiguous selection', async () => {
    const h = harness({
      planTransfer: async () => {
        throw new Error('expected exactly one file for this runner but 2 match');
      },
    });
    await expect(runSeed(JOB, h.reporter, h.s3, h.deps)).rejects.toThrow(/exactly one file/);
  });
});

describe('progress reporting', () => {
  it('throttles progress rather than emitting a record per chunk', async () => {
    // A 30 GB model at 64 MiB a part is ~500 callbacks; one record each would
    // flood the log group for no extra information.
    const h = harness({
      transferFile: async (file, options): Promise<TransferredFile> => {
        for (let i = 0; i < 50; i += 1) {
          options.onBytes?.(1);
        }
        return { path: file.path, storeAs: file.storeAs, size: file.size, sha256: 'x', staged: false };
      },
    });
    await runSeed(JOB, h.reporter, h.s3, h.deps);
    const transferring = h.records.filter((r) => r.Phase === 'transferring');
    // Two forced records per file (one before, one after) — the 100 throttled
    // byte callbacks in between add none.
    expect(transferring.length).toBeLessThanOrEqual(4);
  });

  it('emits a fresh record per file even when throttled', async () => {
    const h = harness();
    await runSeed(JOB, h.reporter, h.s3, h.deps);
    const files = h.records
      .filter((r) => r.Phase === 'transferring' && r.CurrentFile)
      .map((r) => r.CurrentFile);
    expect(new Set(files)).toEqual(new Set(['a.bin', 'b.bin']));
  });

  it('reports zero percent rather than dividing by zero on an empty repository', async () => {
    const h = harness({
      planTransfer: async () => ({
        revision: 'r',
        totalBytes: 0,
        files: [{ path: 'empty', storeAs: 'empty', size: 0 }],
      }),
    });
    await runSeed(JOB, h.reporter, h.s3, h.deps);
    const progress = h.records.find((r) => r.ProgressPercent !== undefined);
    expect(progress?.ProgressPercent).toBe(0);
  });
});

describe('the token', () => {
  it('is read once and handed to the transfer, never logged', async () => {
    const readToken = vi.fn().mockResolvedValue('hf_secret_value');
    let sawToken = '';
    const h = harness({
      readToken,
      transferFile: async (file, options): Promise<TransferredFile> => {
        sawToken = options.token;
        return { path: file.path, storeAs: file.storeAs, size: file.size, sha256: 'x', staged: false };
      },
    });
    await runSeed(JOB, h.reporter, h.s3, h.deps);

    expect(readToken).toHaveBeenCalledTimes(1);
    expect(sawToken).toBe('hf_secret_value');
    // The token must never reach a record — those are shipped to CloudWatch.
    expect(JSON.stringify(h.records)).not.toContain('hf_secret_value');
  });
});
