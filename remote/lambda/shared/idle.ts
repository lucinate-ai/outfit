/**
 * Pure idle-detection logic, kept free of AWS calls so it can be unit tested.
 */

import type { Runner } from './deploy-config';

export interface IdleState {
  /** Last observed activity counter (sum of vLLM token/request counters). */
  counter?: number;
  /** When the counter last changed. */
  last_change_at?: string;
  /** When the start Lambda last reported the endpoint ready. */
  last_wake_at?: string;
}

export type MetricsResult = { ok: true; running: number; counter: number } | { ok: false };

// The Prometheus metric names each runner exposes on /metrics that signal
// activity: `running` gauges (in-flight requests) plus cumulative `counters`
// (tokens/requests) that catch a long generation starting and ending between
// two idle ticks. vLLM uses a `vllm:` prefix, llama.cpp a `llamacpp:` one, with
// different names — so the scrape is runner-aware.
interface MetricsSpec {
  prefix: string;
  running: Set<string>;
  counters: Set<string>;
}
const METRICS: Record<Runner, MetricsSpec> = {
  vllm: {
    prefix: 'vllm',
    running: new Set(['num_requests_running', 'num_requests_waiting']),
    counters: new Set(['prompt_tokens_total', 'generation_tokens_total', 'request_success_total']),
  },
  llamacpp: {
    prefix: 'llamacpp',
    running: new Set(['requests_processing', 'requests_deferred']),
    counters: new Set(['prompt_tokens_total', 'tokens_predicted_total', 'n_decode_total']),
  },
};

/** The grep -E pattern that pulls just this runner's activity metrics. */
export function metricsGrepPattern(runner: Runner): string {
  const spec = METRICS[runner];
  return `^${spec.prefix}:(${[...spec.running, ...spec.counters].join('|')})`;
}

/**
 * Parse the (pre-grepped) Prometheus metrics scrape for a runner. Returns
 * ok: false when the scrape failed or produced nothing recognisable — the
 * caller treats that as "no activity observed" so a wedged server still gets
 * stopped at the idle threshold instead of burning GPU-hours.
 */
export function parseMetrics(stdout: string, runner: Runner): MetricsResult {
  if (!stdout || stdout.includes('SCRAPE_FAILED')) {
    return { ok: false };
  }
  const spec = METRICS[runner];
  const metricLine = new RegExp(`^${spec.prefix}:([a-z_]+)(?:\\{[^}]*\\})?\\s+([0-9.eE+-]+)$`);
  let running = 0;
  let counter = 0;
  let matched = false;
  for (const line of stdout.split('\n')) {
    const match = metricLine.exec(line.trim());
    if (!match) {
      continue;
    }
    const [, name, rawValue] = match;
    const value = Number(rawValue);
    if (!Number.isFinite(value)) {
      continue;
    }
    if (spec.running.has(name)) {
      running += value;
      matched = true;
    } else if (spec.counters.has(name)) {
      counter += value;
      matched = true;
    }
  }
  return matched ? { ok: true, running, counter } : { ok: false };
}

export interface IdleDecisionInput {
  now: Date;
  launchTime: Date;
  metrics: MetricsResult;
  state: IdleState;
  idleThresholdMinutes: number;
  gracePeriodMinutes: number;
  /** Hard cap: stop this long after launch even if requests are in flight. */
  maxRuntimeMinutes: number;
  /**
   * Manual override from the instance's Retain-Until tag: while this is in the
   * future, do not terminate for any automatic reason (idle or max-runtime).
   */
  retainUntil?: Date;
}

export type IdleDecision =
  | { action: 'wait'; reason: string }
  | { action: 'update'; reason: string; newState: IdleState }
  | { action: 'stop'; reason: string };

export function decideIdle(input: IdleDecisionInput): IdleDecision {
  const {
    now,
    launchTime,
    metrics,
    state,
    idleThresholdMinutes,
    gracePeriodMinutes,
    maxRuntimeMinutes,
    retainUntil,
  } = input;

  // A manual Retain-Until override beats every automatic reason to stop,
  // including the hard cap — someone has explicitly pinned this instance alive
  // (e.g. mid-debug). Only a manual stop overrides it.
  if (retainUntil && retainUntil.getTime() > now.getTime()) {
    return {
      action: 'wait',
      reason: `retained until ${retainUntil.toISOString()}`,
    };
  }

  // The hard cap beats everything else, activity included — it is the backstop
  // against a runaway session quietly burning GPU-hours. EC2 resets
  // LaunchTime on every stop/start, so it caps a running session, not the
  // instance's lifetime.
  const minutesSinceLaunch = minutesBetween(launchTime, now);
  if (minutesSinceLaunch > maxRuntimeMinutes) {
    return {
      action: 'stop',
      reason: `running for ${minutesSinceLaunch.toFixed(1)} min, over the maximum runtime (${maxRuntimeMinutes} min)`,
    };
  }

  if (minutesSinceLaunch < gracePeriodMinutes) {
    return {
      action: 'wait',
      reason: `in grace period (${minutesSinceLaunch.toFixed(1)} min since launch)`,
    };
  }

  if (metrics.ok) {
    // "Changed" rather than "increased": a counter that reset to zero after a
    // container restart still counts as activity and renews the grace window.
    if (metrics.running > 0 || state.counter === undefined || metrics.counter !== state.counter) {
      return {
        action: 'update',
        reason:
          metrics.running > 0
            ? `${metrics.running} request(s) in flight`
            : `activity counter moved (${state.counter ?? 'unset'} -> ${metrics.counter})`,
        newState: {
          counter: metrics.counter,
          last_change_at: now.toISOString(),
          last_wake_at: state.last_wake_at,
        },
      };
    }
  }

  // The instance is only ever considered idle relative to the most recent
  // sign of life. last_wake_at closes the race where the start Lambda reports
  // "ready" moments before an idle tick would otherwise stop the instance.
  const anchor = Math.max(
    parseTime(state.last_change_at),
    parseTime(state.last_wake_at),
    launchTime.getTime(),
  );
  const idleMinutes = (now.getTime() - anchor) / 60_000;
  if (idleMinutes > idleThresholdMinutes) {
    return {
      action: 'stop',
      reason: `idle for ${idleMinutes.toFixed(1)} min (threshold ${idleThresholdMinutes})${
        metrics.ok ? '' : '; metrics scrape failed'
      }`,
    };
  }
  return {
    action: 'wait',
    reason: `idle for ${idleMinutes.toFixed(1)} min (threshold ${idleThresholdMinutes})`,
  };
}

function minutesBetween(from: Date, to: Date): number {
  return (to.getTime() - from.getTime()) / 60_000;
}

function parseTime(iso: string | undefined): number {
  if (!iso) {
    return 0;
  }
  const parsed = Date.parse(iso);
  return Number.isNaN(parsed) ? 0 : parsed;
}
