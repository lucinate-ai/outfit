/**
 * Layer three of the seed's termination guarantee: the control plane's own
 * judgement, for the failures the instance cannot report on its own.
 *
 * Layer one is the seeder's exit handling and the boot script's EXIT trap;
 * layer two is `shutdown -h +N` armed before anything can hang. Both run on the
 * instance, so neither survives a kernel that kills it or a boot that never got
 * as far as user-data. This does.
 *
 * The decision is kept pure so every branch is testable without EC2 or
 * CloudWatch, and separate from the inference sweep's decideIdle because the two
 * judge different things from different signals.
 */

export interface SeedReapInput {
  now: Date;
  launchTime?: Date;
  /**
   * When the seed last reported anything — the `lastEventTimestamp` of its log
   * stream. THIS is the progress signal, deliberately not the SSM daemon scrape
   * the inference sweep uses: a seed instance runs no outfit daemon, so that
   * scrape would fail against it and yield nothing usable.
   *
   * Undefined means nothing has been reported yet, which is normal for the
   * first minute or two while the instance installs its runtime.
   */
  lastReportAt?: Date;
  maxSeedMinutes: number;
  stallMinutes: number;
  /** An operator's hold, honoured so a stuck seed can be kept for inspection. */
  retainUntil?: Date;
}

export interface SeedReapDecision {
  action: 'keep' | 'reap';
  reason: string;
  /** Set when reaping, so the synthetic terminal record can say why. */
  phase?: 'failed';
}

export function decideSeedReap(input: SeedReapInput): SeedReapDecision {
  const { now, launchTime, lastReportAt, maxSeedMinutes, stallMinutes, retainUntil } = input;

  if (retainUntil && retainUntil > now) {
    return { action: 'keep', reason: `held until ${retainUntil.toISOString()}` };
  }
  if (!launchTime) {
    // Nothing to judge age by; leave it for the next sweep, by which time
    // DescribeInstances will have caught up.
    return { action: 'keep', reason: 'no launch time yet' };
  }

  const ageMinutes = (now.getTime() - launchTime.getTime()) / 60_000;
  if (ageMinutes > maxSeedMinutes) {
    return {
      action: 'reap',
      phase: 'failed',
      reason: `running ${Math.round(ageMinutes)} min, past the ${maxSeedMinutes} min cap`,
    };
  }

  // Silence is judged from the last report when there is one, and from launch
  // otherwise — so an instance that never reported at all is still reaped, just
  // measured from when it started rather than from a report it never made.
  const silentSince = lastReportAt ?? launchTime;
  const silentMinutes = (now.getTime() - silentSince.getTime()) / 60_000;
  if (silentMinutes > stallMinutes) {
    return {
      action: 'reap',
      phase: 'failed',
      reason: lastReportAt
        ? `no progress reported for ${Math.round(silentMinutes)} min`
        : `reported nothing in the ${Math.round(silentMinutes)} min since launch`,
    };
  }

  return { action: 'keep', reason: `active ${Math.round(silentMinutes)} min ago` };
}
