import type { LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import {
  errorName,
  findManagedInstance,
  readDeployConfig,
  requireEnv,
  runShellCommand,
} from '../shared/aws';
import { type DeployConfig } from '../shared/deploy-config';
import { DAEMON_METRICS_CMD, parseDaemonMetrics } from '../shared/daemon';
import {
  deployConfigParam,
  ENV_TAG_KEY,
  environmentFrom,
} from '../shared/environments';
import { jsonResponse } from '../shared/http';
import { type StatsResult } from '../shared/stats';

const TAG_KEY = requireEnv('TAG_KEY');
const TAG_VALUE = requireEnv('TAG_VALUE');

/** Narrow instance discovery to one environment's instance. */
function envFilter(env: string) {
  return [{ Name: `tag:${ENV_TAG_KEY}`, Values: [env] }];
}

/**
 * The stats Lambda called by `outfit remote metrics`. The control plane
 * contributes what only it knows — environment, instance id/type, uptime
 * since launch — and everything measured (engine token counters, GPU, CPU,
 * RAM) comes from the on-instance outfit daemon's /v1/metrics, fetched with
 * one SSM curl. Collection itself lives in outfit's internal/metrics.
 */
export async function handler(event: LambdaFunctionURLEvent): Promise<LambdaFunctionURLResult> {
  let env: string;
  try {
    env = environmentFrom(event.queryStringParameters);
  } catch (err) {
    return jsonResponse(400, { error: (err as Error).message });
  }

  // Read deploy config for runner and model info.
  let deployConfig: DeployConfig;
  try {
    deployConfig = await readDeployConfig(deployConfigParam(env));
  } catch (err) {
    return jsonResponse(400, {
      error: `cannot read deploy config: ${(err as Error).message}. Run \`outfit remote deploy\` first.`,
    });
  }

  // Find the instance.
  const instance = await findManagedInstance(TAG_KEY, TAG_VALUE, envFilter(env));

  if (!instance || instance.state !== 'running') {
    const result: StatsResult = {
      environment: env,
      state: instance?.state ?? (instance ? 'stopped' : 'undeployed'),
      runner: deployConfig.runner,
      modelId: deployConfig.modelId,
    };
    return jsonResponse(200, result);
  }

  const result: StatsResult = {
    environment: env,
    state: 'running',
    instanceId: instance.instanceId,
    runner: deployConfig.runner,
    modelId: deployConfig.modelId,
  };
  if (instance.instanceType) {
    result.instanceType = instance.instanceType;
  }
  // Uptime from launch time — the instance's, not the engine's, since cost
  // estimation multiplies it by the on-demand price.
  if (instance.launchTime) {
    result.uptimeSeconds = Math.floor((Date.now() - instance.launchTime.getTime()) / 1000);
  }

  const errors: string[] = [];
  try {
    const scrape = await runShellCommand(instance.instanceId, DAEMON_METRICS_CMD, 30);
    const daemon = scrape.status === 'Success' ? parseDaemonMetrics(scrape.stdout) : null;
    if (daemon) {
      result.tokens = daemon.tokens;
      result.gpus = daemon.gpus;
      result.cpu = daemon.cpu;
      result.memory = daemon.memory;
      if (daemon.errors?.length) {
        errors.push(...daemon.errors);
      }
    } else {
      errors.push('daemon: unreachable or unrecognisable metrics reply');
    }
  } catch (err) {
    errors.push(`daemon: ${errorName(err)}`);
  }
  if (errors.length > 0) {
    result.errors = errors;
  }

  return jsonResponse(200, result);
}
