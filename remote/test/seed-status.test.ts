import { describe, expect, it } from 'vitest';
import type { InstanceInfo } from '../lambda/shared/aws';
import type { SeedRecord } from '../lambda/shared/seed/contract';
import { buildStatus, joinState } from '../lambda/shared/seed/status';
import { decideSeedReap } from '../lambda/shared/seed/reap';

function record(phase: SeedRecord['Phase'], extra: Partial<SeedRecord> = {}): SeedRecord {
  return { SeedId: 'vllm--m', Runner: 'vllm', ModelId: 'org/m', Phase: phase, ...extra };
}

const alive: InstanceInfo = { instanceId: 'i-1', state: 'running' };
const pending: InstanceInfo = { instanceId: 'i-1', state: 'pending' };
const dying: InstanceInfo = { instanceId: 'i-1', state: 'shutting-down' };

describe('joining records with the instance', () => {
  // Every cell of the table in status.ts. The two that matter are the ones
  // where the instance is gone and the seed never said it was finished.

  it('reports starting when the instance is up but has said nothing', () => {
    expect(joinState(null, alive)).toBe('starting');
    expect(joinState(null, pending)).toBe('starting');
  });

  it('reports the current phase while the instance is alive', () => {
    expect(joinState(record('transferring'), alive)).toBe('transferring');
    expect(joinState(record('resolving'), alive)).toBe('resolving');
  });

  it('reports failed when the instance vanished mid-transfer', () => {
    // The hole this join exists to close: records alone would say 41% for ever.
    expect(joinState(record('transferring'), null)).toBe('failed');
  });

  it('reports failed when the instance is gone and nothing was ever said', () => {
    expect(joinState(null, null)).toBe('failed');
  });

  it('treats a dying instance as gone, not as alive', () => {
    expect(joinState(record('transferring'), dying)).toBe('failed');
  });

  it('trusts a terminal record whatever the instance is doing', () => {
    expect(joinState(record('succeeded'), null)).toBe('succeeded');
    expect(joinState(record('failed'), null)).toBe('failed');
    expect(joinState(record('stopped'), null)).toBe('stopped');
    // Still terminal even if the box has not finished shutting down yet.
    expect(joinState(record('succeeded'), alive)).toBe('succeeded');
  });
});

describe('the reported status', () => {
  it('explains a silent death rather than leaving the reason blank', () => {
    const status = buildStatus('vllm--m', record('transferring', { ProgressPercent: 41 }), null);
    expect(status.state).toBe('failed');
    expect(status.error).toMatch(/stopped reporting while transferring/);
    // The last known progress is still reported — it is the diagnosis.
    expect(status.progressPercent).toBe(41);
  });

  it('explains an instance that produced no records at all', () => {
    expect(buildStatus('vllm--m', null, null).error).toMatch(/no records/);
  });

  it('carries a real failure message through unchanged', () => {
    const status = buildStatus('vllm--m', record('failed', { Error: 'checksum mismatch' }), null);
    expect(status.error).toBe('checksum mismatch');
  });

  it('sets no error on a healthy seed', () => {
    expect(buildStatus('vllm--m', record('transferring'), alive).error).toBeUndefined();
    expect(buildStatus('vllm--m', record('succeeded'), null).error).toBeUndefined();
  });

  it('falls back to the instance tag for the model when no record has one', () => {
    const tagged: InstanceInfo = {
      instanceId: 'i-1',
      state: 'running',
      tags: { 'cloud-vm-llm:seed-model': 'org/from-tag' },
    };
    expect(buildStatus('vllm--m', null, tagged).modelId).toBe('org/from-tag');
  });

  it('reports when the seed last spoke', () => {
    const at = Date.parse('2026-08-17T12:00:00.000Z');
    expect(buildStatus('vllm--m', record('transferring'), alive, at).lastReportAt).toBe(
      '2026-08-17T12:00:00.000Z',
    );
  });
});

describe('reaping a seed', () => {
  const now = new Date('2026-08-17T12:00:00Z');
  const minutesAgo = (n: number) => new Date(now.getTime() - n * 60_000);
  const base = { now, maxSeedMinutes: 60, stallMinutes: 10 };

  it('keeps a seed that is reporting', () => {
    const decision = decideSeedReap({
      ...base,
      launchTime: minutesAgo(20),
      lastReportAt: minutesAgo(1),
    });
    expect(decision.action).toBe('keep');
  });

  it('reaps a seed past the hard cap even while it is reporting', () => {
    // A seed making slow progress for an hour is still costing money.
    const decision = decideSeedReap({
      ...base,
      launchTime: minutesAgo(61),
      lastReportAt: minutesAgo(1),
    });
    expect(decision.action).toBe('reap');
    expect(decision.reason).toMatch(/past the 60 min cap/);
  });

  it('reaps a stalled seed early rather than waiting for the cap', () => {
    const decision = decideSeedReap({
      ...base,
      launchTime: minutesAgo(20),
      lastReportAt: minutesAgo(11),
    });
    expect(decision.action).toBe('reap');
    expect(decision.reason).toMatch(/no progress reported for 11 min/);
  });

  it('reaps an instance that never reported at all, measured from launch', () => {
    const decision = decideSeedReap({ ...base, launchTime: minutesAgo(11) });
    expect(decision.action).toBe('reap');
    expect(decision.reason).toMatch(/reported nothing in the 11 min since launch/);
  });

  it('gives a just-launched instance time to install its runtime', () => {
    expect(decideSeedReap({ ...base, launchTime: minutesAgo(2) }).action).toBe('keep');
  });

  it('honours an operator hold, so a stuck seed can be inspected', () => {
    const decision = decideSeedReap({
      ...base,
      launchTime: minutesAgo(90),
      lastReportAt: minutesAgo(80),
      retainUntil: new Date(now.getTime() + 60_000),
    });
    expect(decision.action).toBe('keep');
    expect(decision.reason).toMatch(/held until/);
  });

  it('ignores a hold that has expired', () => {
    const decision = decideSeedReap({
      ...base,
      launchTime: minutesAgo(90),
      retainUntil: minutesAgo(5),
    });
    expect(decision.action).toBe('reap');
  });

  it('waits for the next sweep when the launch time is not known yet', () => {
    expect(decideSeedReap({ ...base }).action).toBe('keep');
  });
});
