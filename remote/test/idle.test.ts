import { describe, expect, it } from 'vitest';
import { decideIdle, idleFromDaemonStatus, type IdleDecisionInput } from '../lambda/shared/idle';

describe('idleFromDaemonStatus', () => {
  it('lifts the idle duration out of a daemon status reply', () => {
    expect(
      idleFromDaemonStatus({ state: 'running', lastActiveAt: '2026-07-24T11:30:00Z', idleSeconds: 1800 }),
    ).toEqual({ ok: true, idleSeconds: 1800 });
  });

  it('reads a present lastActiveAt with no idleSeconds as zero idle', () => {
    // The daemon omits idleSeconds rather than sending 0 while work is in
    // flight, so an absent value is "active right now", not "unknown".
    expect(
      idleFromDaemonStatus({ state: 'running', lastActiveAt: '2026-07-24T12:00:00Z' }),
    ).toEqual({ ok: true, idleSeconds: 0 });
  });

  it('reads a reply with no lastActiveAt as no activity observed', () => {
    expect(idleFromDaemonStatus({ state: 'running' })).toEqual({ ok: false });
  });

  it('reads an absent reply as no activity observed', () => {
    expect(idleFromDaemonStatus(null)).toEqual({ ok: false });
    expect(idleFromDaemonStatus(undefined)).toEqual({ ok: false });
  });
});

function input(overrides: Partial<IdleDecisionInput>): IdleDecisionInput {
  return {
    now: new Date('2026-07-24T12:00:00Z'),
    launchTime: new Date('2026-07-24T11:00:00Z'),
    metrics: { ok: true, idleSeconds: 30 * 60 },
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

  it('waits while the daemon reports work in flight', () => {
    const decision = decideIdle(input({ metrics: { ok: true, idleSeconds: 0 } }));
    expect(decision.action).toBe('wait');
  });

  it('stops once the daemon reports idle beyond the threshold', () => {
    const decision = decideIdle(input({}));
    expect(decision.action).toBe('stop'); // idle 30 min > 15 min threshold
    expect(decision.reason).toContain('idle for 30.0 min');
  });

  it('waits when idle but within the threshold', () => {
    const decision = decideIdle(input({ metrics: { ok: true, idleSeconds: 10 * 60 } }));
    expect(decision.action).toBe('wait');
  });

  it('a bursty endpoint quiet at sweep time is kept alive', () => {
    // The whole point of moving the judgement onto the instance: the sweep
    // lands in a lull, but the daemon has been sampling every few seconds and
    // reports a small idle time.
    const decision = decideIdle(input({ metrics: { ok: true, idleSeconds: 20 } }));
    expect(decision.action).toBe('wait');
  });

  it('stops at the threshold when nothing was observed (wedged instance)', () => {
    const decision = decideIdle(input({ metrics: { ok: false } }));
    expect(decision.action).toBe('stop'); // launched 60 min ago, no signs of life
    expect(decision.reason).toContain('no activity reported');
  });

  it('an unobserved instance still gets its grace period', () => {
    const decision = decideIdle(
      input({ metrics: { ok: false }, launchTime: new Date('2026-07-24T11:55:00Z') }),
    );
    expect(decision.action).toBe('wait');
    expect(decision.reason).toContain('grace');
  });

  it('an unobserved instance waits until the threshold passes', () => {
    const decision = decideIdle(
      input({ metrics: { ok: false }, launchTime: new Date('2026-07-24T11:48:00Z') }),
    );
    expect(decision.action).toBe('wait'); // up 12 min: past grace, under threshold
  });

  it('stops at the maximum runtime even with work in flight', () => {
    const decision = decideIdle(
      input({
        launchTime: new Date('2026-07-24T07:00:00Z'), // 5 h ago > 4 h cap
        metrics: { ok: true, idleSeconds: 0 },
      }),
    );
    expect(decision.action).toBe('stop');
    expect(decision.reason).toContain('maximum runtime');
  });

  it('does not apply the maximum runtime before it is reached', () => {
    const decision = decideIdle(
      input({
        launchTime: new Date('2026-07-24T09:00:00Z'), // 3 h ago < 4 h cap
        metrics: { ok: true, idleSeconds: 0 },
      }),
    );
    expect(decision.action).toBe('wait'); // active, so no stop
  });

  it('a future Retain-Until blocks an otherwise-certain idle stop', () => {
    const decision = decideIdle(
      input({ metrics: { ok: false }, retainUntil: new Date('2026-07-24T13:00:00Z') }),
    );
    expect(decision.action).toBe('wait');
    expect(decision.reason).toContain('retained until');
  });

  it('a future Retain-Until overrides even the maximum runtime cap', () => {
    const decision = decideIdle(
      input({
        launchTime: new Date('2026-07-24T07:00:00Z'), // 5 h ago > 4 h cap
        metrics: { ok: true, idleSeconds: 0 },
        retainUntil: new Date('2026-07-24T13:00:00Z'),
      }),
    );
    expect(decision.action).toBe('wait');
    expect(decision.reason).toContain('retained until');
  });

  it('a past Retain-Until does not block a stop', () => {
    const decision = decideIdle(
      input({ metrics: { ok: false }, retainUntil: new Date('2026-07-24T11:59:00Z') }),
    );
    expect(decision.action).toBe('stop');
  });
});
