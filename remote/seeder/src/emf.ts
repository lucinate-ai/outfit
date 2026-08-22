/**
 * The seeder's reporting channel: CloudWatch Embedded Metric Format records
 * appended to a file the CloudWatch agent tails.
 *
 * One write does two jobs — CloudWatch extracts the declared metrics (so an
 * outcome can raise an alarm without anyone reading a log line) and the record
 * itself stays readable (so a failure can be diagnosed after the instance that
 * failed is gone). Records are mirrored to stdout as well, which puts a copy in
 * the boot log for the case where the agent never started.
 */

import { appendFileSync } from 'node:fs';
import {
  SEED_METRICS,
  SEED_NAMESPACE,
  type SeedPhase,
  type SeedRecord,
} from '../../lambda/shared/seed/contract';
import type { Runner } from '../../lambda/shared/deploy-config';

export interface ReporterOptions {
  seedId: string;
  runner: Runner;
  modelId: string;
  instanceId: string;
  recordPath: string;
  /** Injectable so tests can capture records without touching the filesystem. */
  sink?: (record: SeedRecord) => void;
  now?: () => number;
}

/** Metric declarations by name, so a record only declares what it carries. */
const UNITS: Record<string, string> = {
  [SEED_METRICS.bytesTransferred]: 'Bytes',
  [SEED_METRICS.filesCompleted]: 'Count',
  [SEED_METRICS.progressPercent]: 'Percent',
  [SEED_METRICS.started]: 'Count',
  [SEED_METRICS.succeeded]: 'Count',
  [SEED_METRICS.failed]: 'Count',
  [SEED_METRICS.stopped]: 'Count',
  [SEED_METRICS.durationSeconds]: 'Seconds',
};

export class Reporter {
  private readonly opts: Required<Omit<ReporterOptions, 'sink'>> & { sink?: (r: SeedRecord) => void };
  private readonly startedAt: number;
  /** Guards the one-terminal-record rule; see terminal(). */
  private terminalEmitted = false;

  constructor(options: ReporterOptions) {
    this.opts = {
      now: () => Date.now(),
      ...options,
      sink: options.sink,
    } as Required<Omit<ReporterOptions, 'sink'>> & { sink?: (r: SeedRecord) => void };
    this.startedAt = this.opts.now();
  }

  /**
   * Emit a record. `metrics` names the values CloudWatch should extract; every
   * other field rides along as a property — free, queryable, and outside the
   * per-metric cost that a dimension would carry.
   *
   * Runner is the sole dimension. Dimensioning on SeedId would mint a
   * permanently billed custom metric for every model ever seeded.
   */
  emit(
    phase: SeedPhase,
    fields: Partial<SeedRecord> = {},
    metrics: Record<string, number> = {},
  ): void {
    const names = Object.keys(metrics);
    const record: SeedRecord = {
      SeedId: this.opts.seedId,
      Runner: this.opts.runner,
      ModelId: this.opts.modelId,
      InstanceId: this.opts.instanceId,
      Phase: phase,
      ...fields,
      ...metrics,
      ...(names.length
        ? {
            _aws: {
              Timestamp: this.opts.now(),
              CloudWatchMetrics: [
                {
                  Namespace: SEED_NAMESPACE,
                  Dimensions: [['Runner']],
                  Metrics: names.map((Name) => ({ Name, ...(UNITS[Name] ? { Unit: UNITS[Name] } : {}) })),
                },
              ],
            },
          }
        : {}),
    };
    this.write(record);
  }

  /** Progress during transfer. Percent is a metric; phase never is. */
  progress(fields: Partial<SeedRecord>, metrics: Record<string, number> = {}): void {
    this.emit('transferring', fields, metrics);
  }

  /**
   * The seed's last word. At most one is ever written: a thrown error caught at
   * the top level and the process exit handler would otherwise both fire, and
   * two terminal records would make the status read depend on which arrived
   * last. First one wins — the specific failure beats the generic exit.
   */
  terminal(phase: 'succeeded' | 'failed' | 'stopped', fields: Partial<SeedRecord> = {}): boolean {
    if (this.terminalEmitted) {
      return false;
    }
    this.terminalEmitted = true;
    const durationSeconds = Math.round((this.opts.now() - this.startedAt) / 1000);
    const outcome =
      phase === 'succeeded'
        ? SEED_METRICS.succeeded
        : phase === 'stopped'
          ? SEED_METRICS.stopped
          : SEED_METRICS.failed;
    this.emit(phase, { ...fields, DurationSeconds: durationSeconds }, {
      [outcome]: 1,
      [SEED_METRICS.durationSeconds]: durationSeconds,
    });
    return true;
  }

  get hasTerminal(): boolean {
    return this.terminalEmitted;
  }

  private write(record: SeedRecord): void {
    const line = JSON.stringify(record);
    if (this.opts.sink) {
      this.opts.sink(record);
      return;
    }
    // Reporting must never be the reason a seed fails: a full disk or a missing
    // directory costs visibility, not the transfer. stdout still carries it.
    try {
      appendFileSync(this.opts.recordPath, `${line}\n`);
    } catch (err) {
      process.stdout.write(`seed-reporter: cannot write records: ${(err as Error).message}\n`);
    }
    process.stdout.write(`${line}\n`);
  }
}
