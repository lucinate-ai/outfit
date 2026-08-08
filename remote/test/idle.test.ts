import { describe, expect, it } from 'vitest';
import { decideIdle, metricsFromDaemon, type IdleDecisionInput } from '../lambda/shared/idle';

describe('metricsFromDaemon', () => {
  it('lifts the idle signals out of a daemon tokens object', () => {
    expect(
      metricsFromDaemon({ running: 3, counter: 6020 }),
    ).toEqual({ ok: true, running: 3, counter: 6020 });
  });

  it('reads a missing tokens object as no activity observed', () => {
    expect(metricsFromDaemon(undefined)).toEqual({ ok: false });
  });
});

function input(overrides: Partial<IdleDecisionInput>): IdleDecisionInput {
  return {
    now: new Date('2026-07-24T12:00:00Z'),
    launchTime: new Date('2026-07-24T11:00:00Z'),
    metrics: { ok: true, running: 0, counter: 100 },
    state: { counter: 100, last_change_at: '2026-07-24T11:30:00Z' },
    idleThresholdMinutes: 15,
    gracePeriodMinutes: 10,
    maxRuntimeMinutes: 240,
    ...overrides,
  };
}

describe('decideIdle', () => {
  it('waits during the launch grace period', () => {
    const decision = decideIdle(input({ launchTime: new Date('2026-07-24T11:55:00Z') }));
    expect(decision.action).toBe('wait');
    expect(decision.reason).toContain('grace');
  });

  it('records activity when requests are in flight', () => {
    const decision = decideIdle(input({ metrics: { ok: true, running: 2, counter: 100 } }));
    expect(decision.action).toBe('update');
  });

  it('records activity when the counter moved', () => {
    const decision = decideIdle(input({ metrics: { ok: true, running: 0, counter: 150 } }));
    expect(decision).toMatchObject({
      action: 'update',
      newState: { counter: 150, last_change_at: '2026-07-24T12:00:00.000Z' },
    });
  });

  it('treats a counter reset (container restart) as activity', () => {
    const decision = decideIdle(input({ metrics: { ok: true, running: 0, counter: 5 } }));
    expect(decision.action).toBe('update');
  });

  it('initialises state on first observation', () => {
    const decision = decideIdle(input({ state: {} }));
    expect(decision.action).toBe('update');
  });

  it('preserves last_wake_at when updating', () => {
    const decision = decideIdle(
      input({
        metrics: { ok: true, running: 0, counter: 150 },
        state: { counter: 100, last_wake_at: '2026-07-24T11:45:00Z' },
      }),
    );
    expect(decision).toMatchObject({
      action: 'update',
      newState: { last_wake_at: '2026-07-24T11:45:00Z' },
    });
  });

  it('stops once idle beyond the threshold', () => {
    const decision = decideIdle(input({}));
    expect(decision.action).toBe('stop'); // last change 30 min ago > 15 min threshold
  });

  it('waits when idle but within the threshold', () => {
    const decision = decideIdle(input({ state: { counter: 100, last_change_at: '2026-07-24T11:50:00Z' } }));
    expect(decision.action).toBe('wait');
  });

  it('a recent wake blocks the stop even with a stale counter', () => {
    const decision = decideIdle(
      input({
        state: {
          counter: 100,
          last_change_at: '2026-07-24T11:30:00Z',
          last_wake_at: '2026-07-24T11:55:00Z',
        },
      }),
    );
    expect(decision.action).toBe('wait');
  });

  it('stops at the threshold when the scrape failed (crashed container)', () => {
    const decision = decideIdle(input({ metrics: { ok: false } }));
    expect(decision.action).toBe('stop');
    expect(decision.reason).toContain('scrape failed');
  });

  it('anchors on launch time when there is no recorded state', () => {
    const decision = decideIdle(input({ metrics: { ok: false }, state: {} }));
    expect(decision.action).toBe('stop'); // launched 60 min ago, no signs of life
  });

  it('stops at the maximum runtime even with requests in flight', () => {
    const decision = decideIdle(
      input({
        launchTime: new Date('2026-07-24T07:00:00Z'), // 5 h ago > 4 h cap
        metrics: { ok: true, running: 3, counter: 100 },
      }),
    );
    expect(decision.action).toBe('stop');
    expect(decision.reason).toContain('maximum runtime');
  });

  it('stops at the maximum runtime even when the counter is moving', () => {
    const decision = decideIdle(
      input({
        launchTime: new Date('2026-07-24T07:00:00Z'),
        metrics: { ok: true, running: 0, counter: 9999 },
      }),
    );
    expect(decision.action).toBe('stop');
    expect(decision.reason).toContain('maximum runtime');
  });

  it('does not apply the maximum runtime before it is reached', () => {
    const decision = decideIdle(
      input({
        launchTime: new Date('2026-07-24T09:00:00Z'), // 3 h ago < 4 h cap
        metrics: { ok: true, running: 1, counter: 100 },
      }),
    );
    expect(decision.action).toBe('update'); // active, so no stop
  });

  it('a future Retain-Until blocks an otherwise-certain idle stop', () => {
    const decision = decideIdle(
      input({ metrics: { ok: false }, state: {}, retainUntil: new Date('2026-07-24T13:00:00Z') }),
    );
    expect(decision.action).toBe('wait');
    expect(decision.reason).toContain('retained until');
  });

  it('a future Retain-Until overrides even the maximum runtime cap', () => {
    const decision = decideIdle(
      input({
        launchTime: new Date('2026-07-24T07:00:00Z'), // 5 h ago > 4 h cap
        metrics: { ok: true, running: 3, counter: 100 },
        retainUntil: new Date('2026-07-24T13:00:00Z'),
      }),
    );
    expect(decision.action).toBe('wait');
    expect(decision.reason).toContain('retained until');
  });

  it('a past Retain-Until does not block a stop', () => {
    const decision = decideIdle(
      input({ metrics: { ok: false }, state: {}, retainUntil: new Date('2026-07-24T11:59:00Z') }),
    );
    expect(decision.action).toBe('stop');
  });
});
