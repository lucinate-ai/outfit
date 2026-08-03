import type { LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import {
  errorName,
  findManagedInstance,
  readDeployConfig,
  requireEnv,
  runShellCommand,
} from '../shared/aws';
import { type DeployConfig } from '../shared/deploy-config';
import {
  deployConfigParam,
  ENV_TAG_KEY,
  environmentFrom,
} from '../shared/environments';
import { jsonResponse } from '../shared/http';
import { parseMetrics } from '../shared/idle';
import {
  buildTokenStats,
  metricsCurlCommand,
  NVIDIA_SMI_CMD,
  parseCpuStat,
  parseGpuStats,
  parseMemoryStat,
  VMSTAT_CMD,
  FREE_CMD,
  type StatsResult,
} from '../shared/stats';

const TAG_KEY = requireEnv('TAG_KEY');
const TAG_VALUE = requireEnv('TAG_VALUE');
const PORT = Number(requireEnv('VLLM_PORT'));

/** Narrow instance discovery to one environment's instance. */
function envFilter(env: string) {
  return [{ Name: `tag:${ENV_TAG_KEY}`, Values: [env] }];
}

/**
 * The stats Lambda called by `outfit remote stats`. Returns instance metadata,
 * token usage, GPU/CPU/RAM metrics via a single SigV4 Function URL call.
 * 
 * All metric collection runs via SSM on the instance — not direct HTTP — 
 * because llama.cpp gates /metrics behind API key auth and system stats 
 * (nvidia-smi, vmstat, free) require shell commands.
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

  // Collect all metrics in parallel.
  const result: StatsResult = {
    environment: env,
    state: 'running',
    instanceId: instance.instanceId,
    runner: deployConfig.runner,
    modelId: deployConfig.modelId,
  };

  // Uptime from launch time.
  if (instance.launchTime) {
    result.uptimeSeconds = Math.floor((Date.now() - instance.launchTime.getTime()) / 1000);
  }

  const errors: string[] = [];

  // Run all SSM commands in parallel.
  const [metricsResult, gpuResult, cpuResult, memResult] = await Promise.all([
    // Metrics scrape
    (async () => {
      try {
        const apikey = ''; // The server-side key is embedded in the AMI; we don't need it for scraping.
        const cmd = metricsCurlCommand(deployConfig.runner, PORT, apikey);
        return await runShellCommand(instance.instanceId, cmd, 30);
      } catch (err) {
        errors.push(`metrics: ${errorName(err)}`);
        return null;
      }
    })(),
    // GPU stats
    (async () => {
      try {
        return await runShellCommand(instance.instanceId, NVIDIA_SMI_CMD, 15);
      } catch (err) {
        errors.push(`gpu: ${errorName(err)}`);
        return null;
      }
    })(),
    // CPU stats
    (async () => {
      try {
        return await runShellCommand(instance.instanceId, VMSTAT_CMD, 15);
      } catch (err) {
        errors.push(`cpu: ${errorName(err)}`);
        return null;
      }
    })(),
    // Memory stats
    (async () => {
      try {
        return await runShellCommand(instance.instanceId, FREE_CMD, 15);
      } catch (err) {
        errors.push(`memory: ${errorName(err)}`);
        return null;
      }
    })(),
  ]);

  // Parse metrics.
  if (metricsResult) {
    const parsed = buildTokenStats(
      parseMetrics(metricsResult.stdout, deployConfig.runner),
      metricsResult.stdout,
      deployConfig.runner,
    );
    if (parsed) {
      result.tokens = parsed;
    } else {
      errors.push('metrics: no recognisable metrics in scrape');
    }
  }

  // Parse GPU stats.
  if (gpuResult && gpuResult.status === 'Success') {
    const gpus = parseGpuStats(gpuResult.stdout);
    if (gpus.length > 0) {
      result.gpus = gpus;
    }
  }

  // Parse CPU stats.
  if (cpuResult && cpuResult.status === 'Success') {
    const cpu = parseCpuStat(cpuResult.stdout);
    if (cpu) {
      result.cpu = cpu;
    }
  }

  // Parse memory stats.
  if (memResult && memResult.status === 'Success') {
    const memory = parseMemoryStat(memResult.stdout);
    if (memory) {
      result.memory = memory;
    }
  }

  if (errors.length > 0) {
    result.errors = errors;
  }

  console.log(JSON.stringify({ action: 'stats', environment: env, instanceId: instance.instanceId, errors }));
  return jsonResponse(200, result);
}
