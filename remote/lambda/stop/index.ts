import type { LambdaFunctionURLEvent, LambdaFunctionURLResult, ScheduledEvent } from 'aws-lambda';
import {
  errorName,
  findManagedInstance,
  findManagedInstances,
  isSsmAgentOnline,
  readDeployConfig,
  requireEnv,
  runShellCommand,
  STOPPED_AT_TAG,
  stopEngineDaemon,
  stopInstance,
  tagInstance,
  terminateInstance,
  type InstanceInfo,
} from '../shared/aws';
import { deployConfigParam, ENV_TAG_KEY, environmentFrom } from '../shared/environments';
import { DAEMON_STATUS_CMD, parseDaemonStatus } from '../shared/daemon';
import { decideIdle, idleFromDaemonStatus, type MetricsResult } from '../shared/idle';
import { jsonResponse } from '../shared/http';

const TAG_KEY = requireEnv('TAG_KEY');
const TAG_VALUE = requireEnv('TAG_VALUE');
const IDLE_THRESHOLD_MINUTES = Number(requireEnv('IDLE_THRESHOLD_MINUTES'));
const GRACE_PERIOD_MINUTES = Number(requireEnv('GRACE_PERIOD_MINUTES'));
const MAX_RUNTIME_MINUTES = Number(requireEnv('MAX_RUNTIME_MINUTES'));
const STOP_RETENTION_MINUTES = Number(requireEnv('STOP_RETENTION_MINUTES'));


type StopEvent = ScheduledEvent | LambdaFunctionURLEvent;

export function isScheduledEvent(event: StopEvent): event is ScheduledEvent {
  return (event as ScheduledEvent).source === 'aws.events';
}

export async function handler(event: StopEvent): Promise<LambdaFunctionURLResult | void> {
  if (isScheduledEvent(event)) {
    await idleSweep();
    return;
  }
  return manualStop(event);
}

/**
 * Function URL — POST stops one environment's instance; GET reports it. The
 * `action` query parameter chooses the shutdown: `pause` (the default for a
 * manual `outfit remote pause`) stops without terminating, so the instance can
 * be re-woken; anything else terminates, which is what a manual
 * `outfit remote stop` wants. A further query parameter, `force=true`, marks
 * the stop as forced: the engine is not asked to shut down first, so a wedged
 * engine or daemon cannot prevent the box from going down (a manual
 * `outfit remote restart -F`). The stop-time tag, the EC2 call and the reply
 * are the same either way.
 */
async function manualStop(event: LambdaFunctionURLEvent): Promise<LambdaFunctionURLResult> {
  let env: string;
  try {
    env = environmentFrom(event.queryStringParameters);
  } catch (err) {
    return jsonResponse(400, { error: (err as Error).message });
  }
  const instance = await findManagedInstance(TAG_KEY, TAG_VALUE, [
    { Name: `tag:${ENV_TAG_KEY}`, Values: [env] },
  ]);
  const method = event.requestContext?.http?.method ?? 'POST';
  if (method === 'GET') {
    return jsonResponse(200, { state: instance?.state ?? 'stopped', environment: env });
  }
  const force = event.queryStringParameters?.force === 'true';
  if (instance) {
    if (event.queryStringParameters?.action === 'pause') {
      return pauseInstance(instance, env, force);
    }
    if (!force) {
      await stopEngineDaemon(instance.instanceId);
    }
    await terminateInstance(instance.instanceId);
    console.log(
      JSON.stringify({
        mode: 'manual',
        action: 'terminate',
        environment: env,
        instanceId: instance.instanceId,
        force,
      }),
    );
    return jsonResponse(200, { state: 'terminating', environment: env });
  }
  console.log(JSON.stringify({ mode: 'manual', action: 'noop', environment: env }));
  return jsonResponse(200, { state: 'stopped', environment: env });
}

/**
 * Stop (never terminate) one environment's instance. Tagging before the stop
 * means a crash in between leaves a stopped instance with its stop time
 * recorded; a tagless already-stopped instance is self-healed the same way the
 * sweep does. The sweep then owns the eventual termination after retention.
 * When force is set the engine is not asked to shut down first — the EC2 stop
 * takes the box down with the engine still in it, which is what a wedged
 * engine needs — but everything else about the stop is unchanged.
 */
async function pauseInstance(
  instance: InstanceInfo,
  env: string,
  force: boolean,
): Promise<LambdaFunctionURLResult> {
  if (instance.state === 'stopped') {
    if (!instance.stoppedAt) {
      await tagInstance(instance.instanceId, STOPPED_AT_TAG, new Date().toISOString());
    }
    console.log(
      JSON.stringify({ mode: 'manual', action: 'noop', environment: env, instanceId: instance.instanceId }),
    );
    return jsonResponse(200, { state: 'stopped', environment: env });
  }
  await tagInstance(instance.instanceId, STOPPED_AT_TAG, new Date().toISOString());
  if (!force) {
    await stopEngineDaemon(instance.instanceId);
  }
  await stopInstance(instance.instanceId);
  console.log(
    JSON.stringify({
      mode: 'manual',
      action: 'stop',
      environment: env,
      instanceId: instance.instanceId,
      force,
    }),
  );
  return jsonResponse(200, { state: 'stopping', environment: env });
}

/**
 * EventBridge tick — one shared sweep covers every environment: each running
 * instance is judged on its own environment's activity and idle ones are
 * stopped (not terminated, so a re-wake is fast), and each stopped instance is
 * terminated once its stop retention passes.
 */
async function idleSweep(): Promise<void> {
  const instances = await findManagedInstances(TAG_KEY, TAG_VALUE);
  if (instances.length === 0) {
    console.log(JSON.stringify({ mode: 'idle', action: 'noop', state: 'none' }));
    return;
  }
  for (const instance of instances) {
    try {
      await idleCheck(instance);
    } catch (err) {
      console.log(
        JSON.stringify({ mode: 'idle', environment: instance.environment, error: errorName(err) }),
      );
    }
  }
}

/** Judge one instance: stop a running idle one, terminate one stopped past its retention. */
async function idleCheck(instance: InstanceInfo): Promise<void> {
  const env = instance.environment;
  const now = new Date();
  // Session start: the re-wake time when the instance was recently re-woken,
  // else its first launch. Max runtime and the grace period bound one running
  // session, so a stop/start cycle must not inherit the previous one's age.
  const sessionStart = instance.startedAt ?? instance.launchTime;
  if (!sessionStart) {
    console.log(
      JSON.stringify({ mode: 'idle', action: 'noop', environment: env, state: instance.state }),
    );
    return;
  }
  if (instance.state !== 'running' && instance.state !== 'stopped') {
    // stopping/shutting-down are transient: acting here could race the
    // transition, so the sweep waits them out.
    console.log(
      JSON.stringify({ mode: 'idle', action: 'noop', environment: env, state: instance.state }),
    );
    return;
  }

  if (instance.state === 'stopped') {
    await idleCheckStopped(instance, env, now, sessionStart);
    return;
  }

  // The idle signal comes from the on-instance daemon's status reply: it
  // samples its engine every few seconds, so it can tell a lull between
  // requests from real idleness in a way one scrape per sweep never could.
  // An instance with no environment tag is an anomaly (launched outside the
  // deploy flow): nothing is assumed for it — no scrape — so it is judged on
  // launch time alone and cleaned up at the threshold rather than burning
  // GPU-hours. For a tagged instance whose config is unreadable, decideIdle
  // likewise treats "nothing observed" as no activity.
  let metrics: MetricsResult = { ok: false };
  if (env) {
    try {
      await readDeployConfig(deployConfigParam(env));
      metrics = await scrapeIdle(instance.instanceId);
    } catch (err) {
      console.log(
        JSON.stringify({
          mode: 'idle',
          environment: env,
          warning: `deploy-config unreadable: ${errorName(err)}`,
        }),
      );
    }
  } else {
    console.log(
      JSON.stringify({ mode: 'idle', warning: `untagged instance ${instance.instanceId}` }),
    );
  }
  const decision = decideIdle({
    now,
    launchTime: sessionStart,
    metrics,
    idleThresholdMinutes: IDLE_THRESHOLD_MINUTES,
    gracePeriodMinutes: GRACE_PERIOD_MINUTES,
    maxRuntimeMinutes: MAX_RUNTIME_MINUTES,
    retainUntil: instance.retainUntil,
    instanceState: 'running',
  });
  console.log(
    JSON.stringify({ mode: 'idle', environment: env, decision: decision.action, reason: decision.reason }),
  );

  if (decision.action === 'stop') {
    // Tag before the stop: a Lambda crash between the two then leaves a
    // stopped instance with its stop time already recorded, and a stale tag on
    // a still-running instance is ignored by the running path.
    await tagInstance(instance.instanceId, STOPPED_AT_TAG, now.toISOString());
    await stopEngineDaemon(instance.instanceId);
    await stopInstance(instance.instanceId);
    console.log(
      JSON.stringify({ mode: 'idle', action: 'stop', environment: env, instanceId: instance.instanceId }),
    );
  }
}

/**
 * A stopped instance is judged on stop retention alone — it holds no running
 * engine, so there is no activity to scrape. Without a stop time it is
 * self-healed: a stop issued outside the control plane (or a crash between the
 * stop call and its tag) buys the full retention from now rather than an
 * immediate death.
 */
async function idleCheckStopped(
  instance: InstanceInfo,
  env: string | undefined,
  now: Date,
  sessionStart: Date,
): Promise<void> {
  if (!instance.stoppedAt) {
    console.log(
      JSON.stringify({
        mode: 'idle',
        action: 'self-heal',
        environment: env,
        instanceId: instance.instanceId,
        warning: `stopped instance has no ${STOPPED_AT_TAG} tag; recording now and retrying after retention`,
      }),
    );
    await tagInstance(instance.instanceId, STOPPED_AT_TAG, now.toISOString());
    return;
  }
  const decision = decideIdle({
    now,
    launchTime: sessionStart,
    metrics: { ok: false },
    idleThresholdMinutes: IDLE_THRESHOLD_MINUTES,
    gracePeriodMinutes: GRACE_PERIOD_MINUTES,
    maxRuntimeMinutes: MAX_RUNTIME_MINUTES,
    retainUntil: instance.retainUntil,
    stopRetentionMinutes: STOP_RETENTION_MINUTES,
    stoppedSince: instance.stoppedAt,
    instanceState: 'stopped',
  });
  console.log(
    JSON.stringify({ mode: 'idle', environment: env, decision: decision.action, reason: decision.reason }),
  );
  if (decision.action === 'terminate') {
    await terminateInstance(instance.instanceId);
    console.log(
      JSON.stringify({ mode: 'idle', action: 'terminate', environment: env, instanceId: instance.instanceId }),
    );
  }
}

async function scrapeIdle(instanceId: string): Promise<MetricsResult> {
  try {
    if (!(await isSsmAgentOnline(instanceId))) {
      console.log(JSON.stringify({ mode: 'idle', warning: 'ssm agent offline' }));
      return { ok: false };
    }
    const result = await runShellCommand(instanceId, DAEMON_STATUS_CMD, 30);
    if (result.status !== 'Success') {
      console.log(JSON.stringify({ mode: 'idle', warning: `scrape ${result.status}` }));
      return { ok: false };
    }
    const idle = idleFromDaemonStatus(parseDaemonStatus(result.stdout));
    if (!idle.ok) {
      // Either the reply was not the daemon's, or it carried no last-active
      // time — an outfit baked before daemon-owned idle detection. Both mean
      // no activity observed; there is deliberately no second way to judge it.
      console.log(JSON.stringify({ mode: 'idle', warning: 'daemon reported no activity time' }));
    }
    return idle;
  } catch (err) {
    // Treated as "no activity observed" by decideIdle: a crashed container
    // gets terminated at the threshold rather than running up GPU-hours.
    console.log(JSON.stringify({ mode: 'idle', warning: `scrape error ${errorName(err)}` }));
    return { ok: false };
  }
}
