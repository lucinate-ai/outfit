import type { LambdaFunctionURLEvent, LambdaFunctionURLResult, ScheduledEvent } from 'aws-lambda';
import {
  errorName,
  findManagedInstance,
  findManagedInstances,
  isSsmAgentOnline,
  readDeployConfig,
  requireEnv,
  runShellCommand,
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

/** Function URL — POST terminates one environment's instance; GET reports it. */
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
  if (instance) {
    await terminateInstance(instance.instanceId);
    console.log(
      JSON.stringify({ mode: 'manual', action: 'terminate', environment: env, instanceId: instance.instanceId }),
    );
    return jsonResponse(200, { state: 'terminating', environment: env });
  }
  console.log(JSON.stringify({ mode: 'manual', action: 'noop', environment: env }));
  return jsonResponse(200, { state: 'stopped', environment: env });
}

/**
 * EventBridge tick — one shared sweep covers every environment: each running
 * instance is judged on its own environment's activity, config and state, and
 * only the idle ones are terminated.
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

/** Judge one instance and terminate it if idle past its bounds. */
async function idleCheck(instance: InstanceInfo): Promise<void> {
  const env = instance.environment;
  if (instance.state !== 'running' || !instance.launchTime) {
    console.log(
      JSON.stringify({ mode: 'idle', action: 'noop', environment: env, state: instance.state }),
    );
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
    now: new Date(),
    launchTime: instance.launchTime,
    metrics,
    idleThresholdMinutes: IDLE_THRESHOLD_MINUTES,
    gracePeriodMinutes: GRACE_PERIOD_MINUTES,
    maxRuntimeMinutes: MAX_RUNTIME_MINUTES,
    retainUntil: instance.retainUntil,
  });
  console.log(
    JSON.stringify({ mode: 'idle', environment: env, decision: decision.action, reason: decision.reason }),
  );

  if (decision.action === 'stop') {
    await terminateInstance(instance.instanceId);
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
