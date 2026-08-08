/**
 * Pure idle-detection logic, kept free of AWS calls so it can be unit tested.
 */


export interface IdleState {
  /** Last observed activity counter (sum of vLLM token/request counters). */
  counter?: number;
  /** When the counter last changed. */
  last_change_at?: string;
  /** When the start Lambda last reported the endpoint ready. */
  last_wake_at?: string;
}

// The activity signals the idle check runs on: in-flight requests plus the
// cumulative token counter, read from the on-instance daemon's metrics reply
// (its engine scrape). ok: false means the daemon (or its engine scrape) was
// unreachable — treated as "no activity observed" so a wedged server still
// stops at the threshold.
export type MetricsResult = { ok: true; running: number; counter: number } | { ok: false };

/** Lift the idle signals out of a daemon metrics reply. */
export function metricsFromDaemon(tokens?: { running: number; counter: number }): MetricsResult {
  if (!tokens) {
    return { ok: false };
  }
  return { ok: true, running: tokens.running, counter: tokens.counter };
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
