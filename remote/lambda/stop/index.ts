import type { LambdaFunctionURLEvent, LambdaFunctionURLResult, ScheduledEvent } from 'aws-lambda';
import {
  errorName,
  findManagedInstance,
  isSsmAgentOnline,
  readDeployConfig,
  readState,
  requireEnv,
  runShellCommand,
  terminateInstance,
  writeState,
} from '../shared/aws';
import type { Runner } from '../shared/deploy-config';
import { decideIdle, metricsGrepPattern, parseMetrics, type MetricsResult } from '../shared/idle';
import { jsonResponse } from '../shared/http';

const TAG_KEY = requireEnv('TAG_KEY');
const TAG_VALUE = requireEnv('TAG_VALUE');
const VLLM_PORT = requireEnv('VLLM_PORT');
const STATE_PARAM_NAME = requireEnv('STATE_PARAM_NAME');
const IDLE_THRESHOLD_MINUTES = Number(requireEnv('IDLE_THRESHOLD_MINUTES'));
const GRACE_PERIOD_MINUTES = Number(requireEnv('GRACE_PERIOD_MINUTES'));
const MAX_RUNTIME_MINUTES = Number(requireEnv('MAX_RUNTIME_MINUTES'));
const DEPLOY_CONFIG_PARAM = requireEnv('DEPLOY_CONFIG_PARAM');

// Grep on the instance (GetCommandInvocation truncates stdout at 24 000 chars,
// and /metrics is far larger). The metric names are runner-specific, and
// llama.cpp gates /metrics behind the API key (vLLM leaves it open) — the key
// is in the same root-only file the server reads, and the SSM command runs as
// root, so the scrape can pass it.
function metricsCommand(runner: Runner): string {
  const auth = runner === 'llamacpp' ? '-H "Authorization: Bearer $(cat /etc/llm/api-key)" ' : '';
  return (
    `curl -s --max-time 5 ${auth}http://localhost:${VLLM_PORT}/metrics` +
    ` | grep -E '${metricsGrepPattern(runner)}'` +
    ` || echo SCRAPE_FAILED`
  );
}

type StopEvent = ScheduledEvent | LambdaFunctionURLEvent;

export function isScheduledEvent(event: StopEvent): event is ScheduledEvent {
  return (event as ScheduledEvent).source === 'aws.events';
}

export async function handler(event: StopEvent): Promise<LambdaFunctionURLResult | void> {
  if (isScheduledEvent(event)) {
    await idleCheck();
    return;
  }
  return manualStop(event);
}

/** Function URL — POST terminates immediately; GET just reports state. */
async function manualStop(event: LambdaFunctionURLEvent): Promise<LambdaFunctionURLResult> {
  const instance = await findManagedInstance(TAG_KEY, TAG_VALUE);
  const method = event.requestContext?.http?.method ?? 'POST';
  if (method === 'GET') {
    return jsonResponse(200, { state: instance?.state ?? 'stopped' });
  }
  if (instance) {
    await terminateInstance(instance.instanceId);
    console.log(JSON.stringify({ mode: 'manual', action: 'terminate', instanceId: instance.instanceId }));
    return jsonResponse(200, { state: 'terminating' });
  }
  console.log(JSON.stringify({ mode: 'manual', action: 'noop' }));
  return jsonResponse(200, { state: 'stopped' });
}

/** EventBridge tick — terminate the instance if it has been idle long enough. */
async function idleCheck(): Promise<void> {
  const instance = await findManagedInstance(TAG_KEY, TAG_VALUE);
  if (!instance || instance.state !== 'running' || !instance.launchTime) {
    console.log(JSON.stringify({ mode: 'idle', action: 'noop', state: instance?.state ?? 'none' }));
    return;
  }

  // The runner (which metric names to scrape) comes from the deploy-config.
  // If it is unreadable, fall through with no metrics: decideIdle then treats
  // this as "no activity observed" rather than crashing the tick.
  let metrics: MetricsResult = { ok: false };
  try {
    const { runner } = await readDeployConfig(DEPLOY_CONFIG_PARAM);
    metrics = await scrapeMetrics(instance.instanceId, runner);
  } catch (err) {
    console.log(JSON.stringify({ mode: 'idle', warning: `deploy-config unreadable: ${errorName(err)}` }));
  }
  const state = await readState(STATE_PARAM_NAME);
  const decision = decideIdle({
    now: new Date(),
    launchTime: instance.launchTime,
    metrics,
    state,
    idleThresholdMinutes: IDLE_THRESHOLD_MINUTES,
    gracePeriodMinutes: GRACE_PERIOD_MINUTES,
    maxRuntimeMinutes: MAX_RUNTIME_MINUTES,
    retainUntil: instance.retainUntil,
  });
  console.log(JSON.stringify({ mode: 'idle', decision: decision.action, reason: decision.reason }));

  if (decision.action === 'update') {
    await writeState(STATE_PARAM_NAME, decision.newState);
  } else if (decision.action === 'stop') {
    await terminateInstance(instance.instanceId);
  }
}

async function scrapeMetrics(instanceId: string, runner: Runner): Promise<MetricsResult> {
  try {
    if (!(await isSsmAgentOnline(instanceId))) {
      console.log(JSON.stringify({ mode: 'idle', warning: 'ssm agent offline' }));
      return { ok: false };
    }
    const result = await runShellCommand(instanceId, metricsCommand(runner), 30);
    if (result.status !== 'Success') {
      console.log(JSON.stringify({ mode: 'idle', warning: `scrape ${result.status}` }));
      return { ok: false };
    }
    return parseMetrics(result.stdout, runner);
  } catch (err) {
    // Treated as "no activity observed" by decideIdle: a crashed container
    // gets terminated at the threshold rather than running up GPU-hours.
    console.log(JSON.stringify({ mode: 'idle', warning: `scrape error ${errorName(err)}` }));
    return { ok: false };
  }
}
