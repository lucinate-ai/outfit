import { describe, expect, it } from 'vitest';
import type { SeedRecord, SeedJob } from '../../lambda/shared/seed/contract';
import { SEED_METRICS, SEED_NAMESPACE } from '../../lambda/shared/seed/contract';
import { Reporter } from '../src/emf';
import { buildManifest } from '../src/manifest';
import { nodeMajor, readJob, MIN_NODE_MAJOR } from '../src/index';
import { writeFileSync } from 'node:fs';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

function collector() {
  const records: SeedRecord[] = [];
  const reporter = new Reporter({
    seedId: 'vllm--org-m',
    runner: 'vllm',
    modelId: 'org/m',
    instanceId: 'i-1',
    recordPath: '/dev/null',
    sink: (r) => records.push(r),
    now: () => 1_000_000,
  });
  return { reporter, records };
}

describe('metric shape', () => {
  it('dimensions on runner only, so metric count cannot grow with models seeded', () => {
    const { reporter, records } = collector();
    reporter.emit('transferring', {}, { [SEED_METRICS.progressPercent]: 42 });
    const metrics = records[0]._aws?.CloudWatchMetrics[0];
    expect(metrics?.Dimensions).toEqual([['Runner']]);
    expect(metrics?.Namespace).toBe(SEED_NAMESPACE);
  });

  it('carries the seed id as a property, never as a dimension', () => {
    // A SeedId dimension would mint a permanently billed custom metric for
    // every model ever seeded.
    const { reporter, records } = collector();
    reporter.emit('transferring', {}, { [SEED_METRICS.progressPercent]: 1 });
    expect(records[0].SeedId).toBe('vllm--org-m');
    expect(records[0]._aws?.CloudWatchMetrics[0].Dimensions.flat()).not.toContain('SeedId');
  });

  it('never publishes the phase as a metric', () => {
    // An enum as a number is unreadable on a graph and meaningless averaged.
    const { reporter, records } = collector();
    reporter.terminal('succeeded');
    const names = records[0]._aws?.CloudWatchMetrics[0].Metrics.map((m) => m.Name) ?? [];
    expect(names).not.toContain('Phase');
    expect(records[0].Phase).toBe('succeeded');
  });

  it('declares no metric block on a record that carries none', () => {
    const { reporter, records } = collector();
    reporter.emit('resolving', { Message: 'looking' });
    expect(records[0]._aws).toBeUndefined();
  });

  it('publishes each terminal outcome as its own count, for alarms', () => {
    for (const [phase, metric] of [
      ['succeeded', SEED_METRICS.succeeded],
      ['failed', SEED_METRICS.failed],
      ['stopped', SEED_METRICS.stopped],
    ] as const) {
      const { reporter, records } = collector();
      reporter.terminal(phase);
      expect(records[0][metric]).toBe(1);
    }
  });
});

describe('the one-terminal-record rule', () => {
  it('ignores a second terminal record', () => {
    // The top-level catch and the exit handler would otherwise both fire, and
    // the status read would depend on which arrived last.
    const { reporter, records } = collector();
    expect(reporter.terminal('failed', { Error: 'the real reason' })).toBe(true);
    expect(reporter.terminal('failed', { Error: 'the generic backstop' })).toBe(false);
    expect(records).toHaveLength(1);
    expect(records[0].Error).toBe('the real reason');
  });

  it('does not let the backstop overwrite a success', () => {
    const { reporter, records } = collector();
    reporter.terminal('succeeded');
    reporter.terminal('failed', { Error: 'exited without reporting' });
    expect(records).toHaveLength(1);
    expect(records[0].Phase).toBe('succeeded');
  });

  it('reports whether a terminal record has been written', () => {
    const { reporter } = collector();
    expect(reporter.hasTerminal).toBe(false);
    reporter.terminal('succeeded');
    expect(reporter.hasTerminal).toBe(true);
  });

  it('keeps reporting progress before any terminal record', () => {
    const { reporter, records } = collector();
    reporter.progress({ CurrentFile: 'a' });
    reporter.progress({ CurrentFile: 'b' });
    expect(records).toHaveLength(2);
  });
});

describe('the manifest', () => {
  const job = {
    seedId: 'vllm--org-m',
    runner: 'vllm',
    modelId: 'org/m',
    quant: '',
    revision: '',
    bucket: 'b',
    prefix: 'models/vllm/org/m/',
  } as SeedJob;

  const files = [
    { path: 'b.bin', storeAs: 'b.bin', size: 20, sha256: 'bb', staged: false },
    { path: 'a.bin', storeAs: 'a.bin', size: 10, sha256: 'aa', staged: true },
  ];

  it('records the resolved revision, not the branch that was asked for', () => {
    expect(buildManifest(job, 'commitsha', files).revision).toBe('commitsha');
  });

  it('records what is stored, under the stored name', () => {
    const manifest = buildManifest(job, 'r', [
      { path: 'deep/model-Q6.gguf', storeAs: 'model.gguf', size: 5, sha256: 'cc', staged: false },
    ]);
    expect(manifest.files[0].path).toBe('model.gguf');
  });

  it('sorts the file list so two seeds of one revision produce one manifest', () => {
    expect(buildManifest(job, 'r', files).files.map((f) => f.path)).toEqual(['a.bin', 'b.bin']);
  });

  it('totals the bytes', () => {
    expect(buildManifest(job, 'r', files).totalBytes).toBe(30);
  });

  it('records what produced it, so a bad seeder version is traceable', () => {
    const manifest = buildManifest(job, 'r', files, new Date('2026-08-17T12:00:00Z'), 'v24.11.0');
    expect(manifest.seederNodeVersion).toBe('v24.11.0');
    expect(manifest.seederVersion).toMatch(/^\d+\.\d+\.\d+$/);
    expect(manifest.seededAt).toBe('2026-08-17T12:00:00.000Z');
  });
});

describe('startup checks', () => {
  it('parses a node major from a version string', () => {
    expect(nodeMajor('v24.11.0')).toBe(24);
    expect(nodeMajor('v18.12.1')).toBe(18);
  });

  it('treats the AL2023 default node as below the floor', () => {
    // The unversioned `nodejs` package on AL2023 is 18 — the reason the boot
    // script pins a version rather than installing `nodejs`.
    expect(nodeMajor('v18.12.1')).toBeLessThan(MIN_NODE_MAJOR);
    expect(nodeMajor('v24.11.0')).toBeGreaterThanOrEqual(MIN_NODE_MAJOR);
  });

  it('rejects a job spec missing a required field', () => {
    const dir = mkdtempSync(join(tmpdir(), 'seed-job-'));
    const path = join(dir, 'job.json');
    writeFileSync(path, JSON.stringify({ seedId: 'x', runner: 'vllm' }));
    expect(() => readJob(path)).toThrow(/missing modelId/);
  });

  it('accepts a complete job spec', () => {
    const dir = mkdtempSync(join(tmpdir(), 'seed-job-'));
    const path = join(dir, 'job.json');
    writeFileSync(
      path,
      JSON.stringify({
        seedId: 'vllm--org-m',
        runner: 'vllm',
        modelId: 'org/m',
        bucket: 'b',
        prefix: 'models/vllm/org/m/',
      }),
    );
    expect(readJob(path).modelId).toBe('org/m');
  });
});
