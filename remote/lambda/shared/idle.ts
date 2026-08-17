/**
 * Pure idle-detection logic, kept free of AWS calls so it can be unit tested.
 *
 * Engine activity is not judged here: the on-instance daemon samples its
 * engine's counters every few seconds and reports how long it has been idle,
 * and this decides what to do about that. What stays here is the policy the
 * daemon cannot know — a retention override, the maximum-runtime cap, and the
 * post-launch grace period, all of which are about the instance and the
 * session rather than about the engine.
 */

import type { DaemonStatus } from './daemon';

// The idle signal the check runs on: how long the daemon says its engine has
// been idle. ok: false means nothing was observed — the daemon was
// unreachable, or answered without a last-active time (an outfit older than
// daemon-owned idle detection). Both are treated as "no activity observed", so
// a wedged or mismatched instance still stops at the threshold rather than
// burning GPU-hours.
export type MetricsResult = { ok: true; idleSeconds: number } | { ok: false };

/** Lift the idle signal out of a daemon status reply. */
export function idleFromDaemonStatus(status?: DaemonStatus | null): MetricsResult {
  if (!status || status.lastActiveAt === undefined) {
    return { ok: false };
  }
  // idleSeconds is omitted rather than sent as 0 when the engine is active
  // right now, so an absent value with a present lastActiveAt means zero.
  return { ok: true, idleSeconds: status.idleSeconds ?? 0 };
}

export interface IdleDecisionInput {
  now: Date;
  launchTime: Date;
  metrics: MetricsResult;
  idleThresholdMinutes: number;
  gracePeriodMinutes: number;
  /** Hard cap: stop this long after launch even if requests are in flight. */
  maxRuntimeMinutes: number;
  /**
   * Manual override from the instance's Retain-Until tag: while this is in the
   * future, do not terminate for any automatic reason (idle or max-runtime).
   */
  retainUntil?: Date;
  /** Optional retention for stopped instances before termination. */
  stopRetentionMinutes?: number;
  /** When the instance entered stopped state, if applicable. */
  stoppedSince?: Date;
  /** Current instance state for tiered decision. */
  instanceState?: 'running' | 'stopped';
}

export type IdleDecision =
  | { action: 'wait'; reason: string }
  | { action: 'stop'; reason: string }
  | { action: 'terminate'; reason: string };

export function decideIdle(input: IdleDecisionInput): IdleDecision {
  const {
    now,
    launchTime,
    metrics,
    idleThresholdMinutes,
    gracePeriodMinutes,
    maxRuntimeMinutes,
    retainUntil,
    stopRetentionMinutes,
    stoppedSince,
    instanceState,
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

  // Tiered handling for stopped instances: terminate after retention
  if (instanceState === 'stopped' && stoppedSince && stopRetentionMinutes !== undefined) {
    const minutesStopped = minutesBetween(stoppedSince, now);
    if (minutesStopped > stopRetentionMinutes) {
      return {
        action: 'terminate',
        reason: `stopped for ${minutesStopped.toFixed(1)} min, over stop retention (${stopRetentionMinutes} min)`,
      };
    }
    return {
      action: 'wait',
      reason: `stopped for ${minutesStopped.toFixed(1)} min (retention ${stopRetentionMinutes} min)`,
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

  // Nothing observed: fall back to how long the instance has been up. The
  // daemon marks an engine start as activity, so on a healthy instance this
  // never decides anything — it is what stops a wedged or unreachable one.
  const idleMinutes = metrics.ok
    ? metrics.idleSeconds / 60
    : minutesSinceLaunch;
  const unobserved = metrics.ok ? '' : '; no activity reported';
  if (idleMinutes > idleThresholdMinutes) {
    return {
      action: 'stop',
      reason: `idle for ${idleMinutes.toFixed(1)} min (threshold ${idleThresholdMinutes})${unobserved}`,
    };
  }
  return {
    action: 'wait',
    reason: `idle for ${idleMinutes.toFixed(1)} min (threshold ${idleThresholdMinutes})${unobserved}`,
  };
}

function minutesBetween(from: Date, to: Date): number {
  return (to.getTime() - from.getTime()) / 60_000;
}
