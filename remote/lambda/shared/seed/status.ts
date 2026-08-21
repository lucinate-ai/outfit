/**
 * What a seed's state is.
 *
 * Neither source is sufficient alone. CloudWatch only knows what the seed
 * managed to say, and a job killed by the kernel says nothing more — so records
 * alone report "41%" for ever. EC2 only knows whether compute exists, which
 * does not distinguish a seed that succeeded from one that died. The state is
 * therefore the join of the two, and the interesting cell is the one where the
 * instance is gone and the last record was not terminal: that is a failure, and
 * must never be reported as progress.
 *
 *                     last record
 *                 (none)   in-flight   terminal
 *   pending/      starting  running     finishing
 *   running
 *   gone          failed    FAILED      that outcome
 */

import {
  CloudWatchLogsClient,
  DescribeLogStreamsCommand,
  GetLogEventsCommand,
  PutLogEventsCommand,
  CreateLogStreamCommand,
} from '@aws-sdk/client-cloudwatch-logs';
import { errorName, type InstanceInfo } from '../aws';
import {
  isTerminalPhase,
  parseSeedRecord,
  SEED_LOG_GROUP,
  type SeedPhase,
  type SeedRecord,
} from './contract';

const logs = new CloudWatchLogsClient({});

export interface SeedStatus {
  seedId: string;
  /** The reported state after joining records with the instance's existence. */
  state: SeedPhase;
  modelId?: string;
  revision?: string;
  instanceId?: string;
  /** Present while running and on a terminal record that carried it. */
  progressPercent?: number;
  bytesDone?: number;
  bytesTotal?: number;
  filesDone?: number;
  filesTotal?: number;
  currentFile?: string;
  message?: string;
  error?: string;
  /** When the seed last said anything. The stall signal for the sweep. */
  lastReportAt?: string;
  startedAt?: string;
  durationSeconds?: number;
}

export interface StreamSummary {
  streamName: string;
  lastEventTimestamp?: number;
}

/**
 * The most recently written stream for a seed. One stream per attempt
 * (`<seedId>/<instanceId>`), so picking the one with the newest last-event
 * time selects the current attempt and never mixes it with an earlier one's
 * records.
 *
 * `orderBy: 'LastEventTime'` is not requested — CloudWatch Logs rejects it
 * combined with `logStreamNamePrefix` ("Cannot order by LastEventTime with a
 * logStreamNamePrefix"). Streams are fetched by prefix instead (its default
 * order, by name, does not matter here) and the newest is picked client-side —
 * cheap, since a seed realistically has only a handful of attempt-streams.
 */
export async function latestStream(seedId: string): Promise<StreamSummary | null> {
  try {
    const result = await logs.send(
      new DescribeLogStreamsCommand({
        logGroupName: SEED_LOG_GROUP,
        logStreamNamePrefix: `${seedId}/`,
      }),
    );
    const streams = result.logStreams ?? [];
    let newest: StreamSummary | null = null;
    for (const stream of streams) {
      if (!stream.logStreamName) {
        continue;
      }
      if (!newest || (stream.lastEventTimestamp ?? 0) > (newest.lastEventTimestamp ?? 0)) {
        newest = { streamName: stream.logStreamName, lastEventTimestamp: stream.lastEventTimestamp };
      }
    }
    return newest;
  } catch (err) {
    if (errorName(err) === 'ResourceNotFoundException') {
      return null;
    }
    throw err;
  }
}

/**
 * The newest parseable record on a stream. Reading from the tail and walking
 * backwards means a truncated final line — the agent can ship a partial write —
 * degrades to the previous record instead of losing the status.
 */
export async function latestRecord(streamName: string): Promise<SeedRecord | null> {
  try {
    const result = await logs.send(
      new GetLogEventsCommand({
        logGroupName: SEED_LOG_GROUP,
        logStreamName: streamName,
        startFromHead: false,
        limit: 25,
      }),
    );
    const events = result.events ?? [];
    for (let i = events.length - 1; i >= 0; i -= 1) {
      const record = parseSeedRecord(events[i].message ?? '');
      if (record) {
        return record;
      }
    }
    return null;
  } catch (err) {
    if (errorName(err) === 'ResourceNotFoundException') {
      return null;
    }
    throw err;
  }
}

/**
 * Join a seed's records with whether its compute still exists.
 *
 * Exported separately from the I/O so every cell of the table above is
 * testable without stubbing CloudWatch or EC2.
 */
export function joinState(
  record: SeedRecord | null,
  instance: InstanceInfo | null,
): SeedPhase {
  const alive = !!instance && (instance.state === 'pending' || instance.state === 'running');

  if (!record) {
    // No word at all: alive means it has not reported yet, gone means it died
    // before it could — never "in progress".
    return alive ? 'starting' : 'failed';
  }
  if (isTerminalPhase(record.Phase)) {
    return record.Phase;
  }
  // Mid-flight record. Alive: that phase is current. Gone without a terminal
  // record: the process died — the hole this join exists to close.
  return alive ? record.Phase : 'failed';
}

/** Assemble the reported status for one seed. */
export function buildStatus(
  seedId: string,
  record: SeedRecord | null,
  instance: InstanceInfo | null,
  lastEventTimestamp?: number,
): SeedStatus {
  const state = joinState(record, instance);
  const diedSilently = state === 'failed' && record !== null && !isTerminalPhase(record.Phase);
  return {
    seedId,
    state,
    modelId: record?.ModelId ?? instance?.tags?.['cloud-vm-llm:seed-model'],
    revision: record?.Revision,
    instanceId: instance?.instanceId ?? record?.InstanceId,
    progressPercent: record?.ProgressPercent,
    bytesDone: record?.BytesDone,
    bytesTotal: record?.BytesTotal,
    filesDone: record?.FilesDone,
    filesTotal: record?.FilesTotal,
    currentFile: record?.CurrentFile,
    message: record?.Message,
    error:
      record?.Error ??
      (diedSilently
        ? `the seed stopped reporting while ${record?.Phase} and its instance is gone`
        : state === 'failed' && !record
          ? 'the seed produced no records before its instance went away'
          : undefined),
    lastReportAt: lastEventTimestamp ? new Date(lastEventTimestamp).toISOString() : undefined,
    startedAt: instance?.launchTime?.toISOString(),
    durationSeconds: record?.DurationSeconds,
  };
}

export async function readSeedStatus(
  seedId: string,
  instance: InstanceInfo | null,
): Promise<SeedStatus> {
  const stream = await latestStream(seedId);
  const record = stream ? await latestRecord(stream.streamName) : null;
  return buildStatus(seedId, record, instance, stream?.lastEventTimestamp);
}

/**
 * Write a terminal record on a seed's behalf.
 *
 * This is what stops a reaped or stopped seed reading as in progress for ever:
 * the instance is gone and cannot report, so the control plane says the last
 * word instead. Without it the join above would infer "failed" but the records
 * would still trail off mid-transfer, and `stopped` would be indistinguishable
 * from a crash.
 */
export async function writeTerminalRecord(
  seedId: string,
  instanceId: string,
  phase: 'failed' | 'stopped',
  message: string,
  extra: Partial<SeedRecord> = {},
): Promise<void> {
  const streamName = `${seedId}/${instanceId}`;
  const record: Partial<SeedRecord> = {
    SeedId: seedId,
    Phase: phase,
    InstanceId: instanceId,
    Message: message,
    ...(phase === 'failed' ? { Error: message } : {}),
    ...extra,
  };
  try {
    // The stream usually exists (the agent created it); creating it is only for
    // the case where the instance died before shipping anything.
    await logs
      .send(new CreateLogStreamCommand({ logGroupName: SEED_LOG_GROUP, logStreamName: streamName }))
      .catch((err) => {
        if (errorName(err) !== 'ResourceAlreadyExistsException') {
          throw err;
        }
      });
    await logs.send(
      new PutLogEventsCommand({
        logGroupName: SEED_LOG_GROUP,
        logStreamName: streamName,
        logEvents: [{ timestamp: Date.now(), message: JSON.stringify(record) }],
      }),
    );
  } catch (err) {
    // Never let bookkeeping fail the action that prompted it — a stop that
    // terminated the instance has done the important half.
    console.log(JSON.stringify({ action: 'seed-terminal-record', seedId, error: errorName(err) }));
  }
}
