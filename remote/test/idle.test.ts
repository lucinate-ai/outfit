import { describe, expect, it } from 'vitest';
import {
  decideIdle,
  metricsGrepPattern,
  parseMetrics,
  type IdleDecisionInput,
} from '../lambda/shared/idle';

const SCRAPE = `vllm:num_requests_running{model_name="Qwen/Qwen3.6-27B-FP8"} 0.0
vllm:num_requests_waiting{model_name="Qwen/Qwen3.6-27B-FP8"} 0.0
vllm:prompt_tokens_total{model_name="Qwen/Qwen3.6-27B-FP8"} 12345.0
vllm:generation_tokens_total{model_name="Qwen/Qwen3.6-27B-FP8"} 6789.0
vllm:request_success_total{model_name="Qwen/Qwen3.6-27B-FP8",finished_reason="stop"} 42.0`;

// Real names as observed on llama-server /metrics for Qwen3.6-27B.
const LLAMACPP_SCRAPE = `llamacpp:requests_processing 0
llamacpp:requests_deferred 0
llamacpp:prompt_tokens_total 5000
llamacpp:tokens_predicted_total 2500
llamacpp:n_decode_total 2500
llamacpp:n_busy_slots_per_decode 1`;

describe('parseMetrics', () => {
  it('sums running and counter metrics from a vllm scrape', () => {
    const result = parseMetrics(SCRAPE, 'vllm');
    expect(result).toEqual({ ok: true, running: 0, counter: 12345 + 6789 + 42 });
  });

  it('reports in-flight vllm requests', () => {
    const result = parseMetrics(
      'vllm:num_requests_running{model_name="m"} 2.0\nvllm:num_requests_waiting{model_name="m"} 1.0',
      'vllm',
    );
    expect(result).toEqual({ ok: true, running: 3, counter: 0 });
  });

  it('sums running and counter metrics from a llamacpp scrape', () => {
    const result = parseMetrics(LLAMACPP_SCRAPE, 'llamacpp');
    // Ignores n_busy_slots_per_decode (not an activity metric).
    expect(result).toEqual({ ok: true, running: 0, counter: 5000 + 2500 + 2500 });
  });

  it('reports in-flight llamacpp requests', () => {
    const result = parseMetrics(
      'llamacpp:requests_processing 1\nllamacpp:requests_deferred 2',
      'llamacpp',
    );
    expect(result).toEqual({ ok: true, running: 3, counter: 0 });
  });

  it('does not match the other runner’s metrics', () => {
    expect(parseMetrics(SCRAPE, 'llamacpp')).toEqual({ ok: false });
    expect(parseMetrics(LLAMACPP_SCRAPE, 'vllm')).toEqual({ ok: false });
  });

  it('fails on SCRAPE_FAILED marker', () => {
    expect(parseMetrics('SCRAPE_FAILED', 'vllm')).toEqual({ ok: false });
    expect(parseMetrics('SCRAPE_FAILED', 'llamacpp')).toEqual({ ok: false });
  });

  it('fails on empty output', () => {
    expect(parseMetrics('', 'vllm')).toEqual({ ok: false });
  });

  it('fails when no recognisable metrics are present', () => {
    expect(parseMetrics('python_gc_objects_collected_total{generation="0"} 6.0', 'vllm')).toEqual({
      ok: false,
    });
  });
});

describe('metricsGrepPattern', () => {
  it('scopes the grep to the runner prefix and its metric names', () => {
    expect(metricsGrepPattern('vllm')).toBe(
      '^vllm:(num_requests_running|num_requests_waiting|prompt_tokens_total|generation_tokens_total|request_success_total)',
    );
    expect(metricsGrepPattern('llamacpp')).toBe(
      '^llamacpp:(requests_processing|requests_deferred|prompt_tokens_total|tokens_predicted_total|n_decode_total)',
    );
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
