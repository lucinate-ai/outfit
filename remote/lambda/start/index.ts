import type { Context, LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import {
  associateEip,
  errorName,
  findLatestAmi,
  findManagedInstance,
  getInstance,
  isCapacityError,
  isSsmAgentOnline,
  readDeployConfig,
  readState,
  requireEnv,
  runInstance,
  runShellCommand,
  sleep,
  writeState,
} from '../shared/aws';
import { type DeployConfig, type Runner } from '../shared/deploy-config';
import {
  baseUrlFor,
  deployConfigParam,
  ENV_TAG_KEY,
  environmentFrom,
  findEnvEip,
  findEnvSecurityGroup,
  idleStateParam,
  readEnvApiKey,
} from '../shared/environments';
import { jsonResponse } from '../shared/http';

const TAG_KEY = requireEnv('TAG_KEY');
const TAG_VALUE = requireEnv('TAG_VALUE');
const VLLM_PORT = requireEnv('VLLM_PORT');
const AMI_ROLE_TAG_KEY = requireEnv('AMI_ROLE_TAG_KEY');
const AMI_ROLE_TAG_VALUE = requireEnv('AMI_ROLE_TAG_VALUE');
const AMI_RUNNER_TAG_KEY = requireEnv('AMI_RUNNER_TAG_KEY');
const INSTANCE_TYPE = requireEnv('INSTANCE_TYPE');
const SUBNET_IDS = requireEnv('SUBNET_IDS').split(',');
const INSTANCE_PROFILE_ARN = requireEnv('INSTANCE_PROFILE_ARN');
const WEIGHTS_BUCKET = requireEnv('WEIGHTS_BUCKET');
const REGION = requireEnv('AWS_REGION');
const BOOT_LOG_GROUP = requireEnv('BOOT_LOG_GROUP');
const ENGINE_LOG_GROUP = { llamacpp: requireEnv('LLAMACPP_LOG_GROUP'), vllm: requireEnv('VLLM_LOG_GROUP') };
// Where the outfit daemon writes the engine's stdout/stderr (tailed by the
// CloudWatch agent): the daemon's stable engine-log path, root's config home
// since the daemon runs as root.
const ENGINE_LOG_FILE = '/root/.config/outfit/daemon/engine.log';

const DEADLINE_MARGIN_MS = 20_000;
const POLL_MS = 5_000;
const HEALTH_POLL_MS = 10_000;
const TERMINAL_STATES = new Set(['shutting-down', 'terminated', 'stopping', 'stopped']);

const HEALTH_COMMAND =
  `curl -s -o /dev/null -w "%{http_code}" --max-time 5 http://localhost:${VLLM_PORT}/health || true`;

/** Narrow instance discovery to one environment's instance. */
function envFilter(env: string) {
  return [{ Name: `tag:${ENV_TAG_KEY}`, Values: [env] }];
}

export async function handler(
  event: LambdaFunctionURLEvent,
  context: Context,
): Promise<LambdaFunctionURLResult> {
  let env: string;
  try {
    env = environmentFrom(event.queryStringParameters);
  } catch (err) {
    return jsonResponse(400, { error: (err as Error).message });
  }
  const method = event.requestContext?.http?.method ?? 'POST';
  if (method === 'GET') {
    return status(env);
  }
  return wake(env, context);
}

/** GET — report one environment's state without side effects. */
async function status(env: string): Promise<LambdaFunctionURLResult> {
  const eip = await findEnvEip(env);
  const baseUrl = eip ? baseUrlFor(eip.publicIp, VLLM_PORT) : '';
  const instance = await findManagedInstance(TAG_KEY, TAG_VALUE, envFilter(env));
  if (!instance || instance.state !== 'running') {
    return jsonResponse(200, {
      state: instance?.state ?? (eip ? 'stopped' : 'undeployed'),
      environment: env,
      healthy: false,
      base_url: baseUrl,
    });
  }
  const healthy =
    (await isSsmAgentOnline(instance.instanceId)) && (await checkHealth(instance.instanceId));
  return jsonResponse(200, { state: 'running', environment: env, healthy, base_url: baseUrl });
}

/** POST — launch the environment's instance if needed and block until serving. */
async function wake(env: string, context: Context): Promise<LambdaFunctionURLResult> {
  const deadline = Date.now() + context.getRemainingTimeInMillis() - DEADLINE_MARGIN_MS;

  // What to serve comes from the environment's deploy-config. No default
  // runner: an unset/invalid config fails the wake loudly rather than
  // launching a guess.
  let deployConfig: DeployConfig;
  try {
    deployConfig = await readDeployConfig(deployConfigParam(env));
  } catch (err) {
    return jsonResponse(
      503,
      {
        state: 'unconfigured',
        environment: env,
        message: `${(err as Error).message} — run \`outfit remote deploy\``,
        retry_after_seconds: 300,
      },
      { 'retry-after': '300' },
    );
  }

  // The environment's own EIP and security group, created by the deploy
  // Lambda. Absent means the environment was never deployed.
  const eip = await findEnvEip(env);
  const securityGroupId = await findEnvSecurityGroup(env);
  if (!eip || !securityGroupId) {
    return jsonResponse(503, {
      state: 'undeployed',
      environment: env,
      message: `environment ${JSON.stringify(env)} has no deployed infrastructure — run \`outfit remote deploy\``,
      retry_after_seconds: 300,
    });
  }
  const baseUrl = baseUrlFor(eip.publicIp, VLLM_PORT);

  const existing = await findManagedInstance(TAG_KEY, TAG_VALUE, envFilter(env));
  let instanceId: string;
  if (existing) {
    // Idempotent: this environment's instance is already up (or coming up).
    instanceId = existing.instanceId;
    console.log(JSON.stringify({ phase: 'existing', environment: env, instanceId, state: existing.state }));
  } else {
    const launched = await launchAcrossAzs(env, deployConfig, securityGroupId);
    if ('error' in launched) {
      return launched.error;
    }
    instanceId = launched.instanceId;
  }

  // Phase 1: EC2 state -> running (then pin the env's EIP so its URL resolves).
  // Right after RunInstances, DescribeInstances is eventually consistent and
  // may briefly 404 the brand-new instance — tolerate that and keep polling.
  while (Date.now() < deadline) {
    let state: string;
    try {
      state = (await getInstance(instanceId)).state;
    } catch (err) {
      if (errorName(err) === 'InvalidInstanceID.NotFound') {
        await sleep(POLL_MS);
        continue;
      }
      throw err;
    }
    if (state === 'running') {
      break;
    }
    if (TERMINAL_STATES.has(state)) {
      return jsonResponse(500, { state, message: `instance went ${state} while starting` });
    }
    await sleep(POLL_MS);
  }
  try {
    await associateEip(eip.allocationId, instanceId);
  } catch (err) {
    console.log(JSON.stringify({ phase: 'eip', environment: env, error: errorName(err) }));
  }

  // Phase 2: SSM agent online (registers 30-60 s after boot).
  while (Date.now() < deadline) {
    if (await isSsmAgentOnline(instanceId)) {
      break;
    }
    await sleep(POLL_MS);
  }

  // Phase 3: server health. vLLM binds its port only once the engine has
  // loaded the weights, but llama.cpp binds immediately and serves 503 while
  // loading — so only a 200 (or a 401 from the api-key middleware) means
  // ready; a 503 or connection refused means still loading.
  while (Date.now() < deadline) {
    if (await checkHealth(instanceId)) {
      return ready(env, baseUrl);
    }
    await sleep(HEALTH_POLL_MS);
  }

  console.log(JSON.stringify({ phase: 'deadline', environment: env, instanceId }));
  return jsonResponse(503, { state: 'starting', retry_after_seconds: 60 }, { 'retry-after': '60' });
}

/** Try each AZ's subnet in turn, skipping ones without capacity. */
async function launchAcrossAzs(
  env: string,
  deployConfig: DeployConfig,
  securityGroupId: string,
): Promise<{ instanceId: string } | { error: LambdaFunctionURLResult }> {
  // Pick the newest AMI baked for THIS runner (role + runner tags).
  const amiId = await findLatestAmi([
    { Name: `tag:${AMI_ROLE_TAG_KEY}`, Values: [AMI_ROLE_TAG_VALUE] },
    { Name: `tag:${AMI_RUNNER_TAG_KEY}`, Values: [deployConfig.runner] },
    { Name: 'state', Values: ['available'] },
  ]);
  if (!amiId) {
    return {
      error: jsonResponse(503, {
        state: 'no-ami',
        message: `no baked AMI for runner "${deployConfig.runner}"; run \`pnpm bake ${deployConfig.runner}\` and wait`,
        retry_after_seconds: 300,
      }),
    };
  }
  const userData = buildUserData(env, deployConfig);
  const tried: string[] = [];
  for (const subnetId of SUBNET_IDS) {
    try {
      const instanceId = await runInstance({
        imageId: amiId,
        instanceType: INSTANCE_TYPE,
        subnetId,
        securityGroupId,
        instanceProfileArn: INSTANCE_PROFILE_ARN,
        userData,
        tags: { Name: `cloud-vm-llm-${env}`, [TAG_KEY]: TAG_VALUE, [ENV_TAG_KEY]: env },
      });
      console.log(JSON.stringify({ phase: 'launched', environment: env, instanceId, subnetId }));
      return { instanceId };
    } catch (err) {
      if (isCapacityError(err)) {
        console.log(JSON.stringify({ phase: 'capacity', subnetId, error: errorName(err) }));
        tried.push(subnetId);
        continue;
      }
      // The vCPU quota is regional, so trying other AZs won't help — return a
      // clear message instead of crashing. Usually means an instance is already
      // running, or the G-instance quota needs raising.
      if (errorName(err) === 'VcpuLimitExceeded') {
        return {
          error: jsonResponse(
            503,
            {
              state: 'quota-exceeded',
              message:
                'G-instance vCPU quota exhausted — an instance may already be running, or request a quota increase',
              retry_after_seconds: 60,
            },
            { 'retry-after': '60' },
          ),
        };
      }
      throw err;
    }
  }
  return {
    error: jsonResponse(
      503,
      {
        state: 'no-capacity',
        message: `no g6e capacity in any of ${tried.length} availability zone(s); retry shortly`,
        retry_after_seconds: 120,
      },
      { 'retry-after': '120' },
    ),
  };
}

/**
 * CloudWatch agent config for one instance: tail the engine log into its
 * per-engine group and the boot log into the shared boot group, both on a
 * `<env>/<instance-id>` stream ({instance_id} is resolved by the agent).
 * run_as_user root so it can read both root-owned files; retention_in_days -1
 * leaves retention to the CDK-managed group (and avoids needing
 * logs:PutRetentionPolicy).
 */
function cloudwatchAgentConfig(env: string, runner: Runner): string {
  const stream = `${env}/{instance_id}`;
  return JSON.stringify(
    {
      agent: { region: REGION, run_as_user: 'root' },
      logs: {
        logs_collected: {
          files: {
            collect_list: [
              {
                file_path: ENGINE_LOG_FILE,
                log_group_name: ENGINE_LOG_GROUP[runner],
                log_stream_name: stream,
                retention_in_days: -1,
              },
              {
                file_path: '/var/log/cloud-init-output.log',
                log_group_name: BOOT_LOG_GROUP,
                log_stream_name: stream,
                retention_in_days: -1,
              },
            ],
          },
        },
      },
    },
    null,
    2,
  );
}

/** Exported for tests: the boot script is pure string-building. */
export function buildUserData(env: string, cfg: DeployConfig): string {
  const modelDir = '/opt/llm/model';
  const runnerUnit = cfg.runner === 'vllm' ? vllmDaemonBoot(cfg, modelDir) : llamacppDaemonBoot(cfg, modelDir);
  const cwAgentConfig = cloudwatchAgentConfig(env, cfg.runner);
  // Common boot: log the GPU, add swap for the load spike, start the log
  // shipper, sync the weights from S3, fetch the environment's API key. Then
  // the daemon takes over: its deploy config is written, its unit enabled,
  // and the engine's first start requested over the control API.
  return `#!/bin/bash
set -euxo pipefail
# Log the GPU state up front so cloud-init-output.log shows whether the driver
# loaded — the fastest way to tell a driver problem from a serving one.
nvidia-smi || echo "NVIDIA_SMI_FAILED"

# Swap for OOM safety during model load. The FP8 checkpoint (~29 GB) is close
# to the 32 GB host RAM on g6e.xlarge, so a transient host-memory spike while
# loading could be OOM-killed. 16 GB of swap backstops that (the weights still
# live in VRAM; swap only catches host-RAM spikes). Created per boot rather
# than baked in, to keep the AMI slim; fallocate keeps it near-instant.
if ! swapon --show | grep -q /swapfile; then
  fallocate -l 16G /swapfile || dd if=/dev/zero of=/swapfile bs=1M count=16384
  chmod 600 /swapfile
  mkswap /swapfile
  swapon /swapfile
fi
free -h

# Start the log shipper before the weights sync so the boot log (this script's
# output, including an S3 pull failure) is captured, and so the engine log is
# tailed from the moment its unit starts. The config is written per boot because
# its stream carries the environment name; {instance_id} is resolved by the
# agent. The engine log directory is baked into the AMI, but ensure it exists.
mkdir -p /var/log/llm
cat >/opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json <<'CWCONFIG'
${cwAgentConfig}
CWCONFIG
/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -s \\
  -c file:/opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json || echo "CW_AGENT_START_FAILED"

MODEL_DIR=${modelDir}
mkdir -p "$MODEL_DIR"
# --no-progress: without a TTY the sync writes a "Completed … MiB" line per
# chunk, which would flood the boot log; completion and errors still print.
aws s3 sync "s3://${WEIGHTS_BUCKET}/${cfg.weightsPrefix}" "$MODEL_DIR/" --region '${REGION}' --no-progress

API_KEY=$(aws secretsmanager get-secret-value --secret-id 'cloud-vm-llm/${env}/api-key' --region '${REGION}' --query SecretString --output text)
umask 077

${runnerUnit}
`;
}

/**
 * Render the daemon's stored deploy config: the same shape `outfit remote
 * deploy` produces, with the cloud-owned settings resolved in — the model as
 * the synced local path, the bind address and port, and the runner's key
 * delivery — so the daemon's ordinary start serves exactly what the old
 * per-runner unit ran. No --metrics here: the daemon switches the engine's
 * metrics endpoint on itself.
 */
function daemonDeployConfig(cfg: DeployConfig, modelDir: string, extraServeArgs: string[]): string {
  const modelId = cfg.runner === 'llamacpp' ? `${modelDir}/model.gguf` : modelDir;
  return JSON.stringify(
    {
      runner: cfg.runner,
      modelId,
      quant: '',
      contextSize: cfg.contextSize,
      servedModelName: cfg.servedModelName,
      serveArgs: ['--host', '0.0.0.0', '--port', String(VLLM_PORT), ...extraServeArgs, ...cfg.serveArgs],
    },
    null,
    2,
  );
}

/**
 * The daemon boot shared by both runners: write the deploy config where the
 * daemon reads it, enable outfit-daemon.service (and the baked crash-nudge
 * timer), then request the engine's first start over the control API — the
 * daemon never auto-starts, so the boot start is the same explicit API start
 * any client performs. A 409 also counts: a re-run must not fail on an
 * engine already up.
 */
function daemonBoot(deployConfigJson: string, unitExtra: string): string {
  return `mkdir -p /root/.config/outfit/daemon
cat >/root/.config/outfit/daemon/deploy-config.json <<'DEPLOYCONFIG'
${deployConfigJson}
DEPLOYCONFIG
chmod 600 /root/.config/outfit/daemon/deploy-config.json

cat >/etc/systemd/system/outfit-daemon.service <<'UNIT'
[Unit]
Description=outfit daemon (engine host)
After=network-online.target
Wants=network-online.target
[Service]
${unitExtra}ExecStart=/usr/local/bin/outfit daemon --api-addr 127.0.0.1:4242
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now outfit-daemon.service
systemctl enable --now outfit-nudge.timer || echo "NUDGE_TIMER_MISSING"

# First engine start, retried until the daemon answers. The engine loads the
# model asynchronously; the start Lambda's health poll still gates "ready".
for attempt in $(seq 1 30); do
  code=$(curl -s -o /tmp/outfit-start.json -w '%{http_code}' --max-time 15 -X POST http://127.0.0.1:4242/v1/start || true)
  if [ "$code" = "200" ] || [ "$code" = "409" ]; then
    break
  fi
  sleep 2
done
cat /tmp/outfit-start.json || true`;
}

/** vLLM: its env file (API key by env, offline mode, native sampler), then the daemon boot. */
function vllmDaemonBoot(cfg: DeployConfig, modelDir: string): string {
  return `# Python dev headers: Triton JIT-compiles a CUDA stub against Python.h on the
# first model load (Qwen3.6's linear-attention path); baked into recipe 2.0.3+,
# this is a safety net for instances off an older AMI and a no-op once present.
if [ ! -f /usr/include/python3.12/Python.h ]; then
  apt-get update && apt-get install -y python3.12-dev
fi

cat >/etc/vllm.env <<ENVFILE
VLLM_API_KEY=$API_KEY
HF_HUB_OFFLINE=1
# Native Torch sampler, not FlashInfer's — FlashInfer JIT-needs nvcc, which the
# slim AMI (driver + CUDA runtime only, no toolkit) does not ship.
VLLM_USE_FLASHINFER_SAMPLER=0
ENVFILE

${daemonBoot(daemonDeployConfig(cfg, modelDir, ['--gpu-memory-utilization', '0.92']), 'EnvironmentFile=/etc/vllm.env\n')}`;
}

/** llama.cpp: the API key in a root-only file (--api-key-file), then the daemon boot. */
function llamacppDaemonBoot(cfg: DeployConfig, modelDir: string): string {
  return `mkdir -p /etc/llm
printf '%s' "$API_KEY" >/etc/llm/api-key
chmod 600 /etc/llm/api-key

${daemonBoot(daemonDeployConfig(cfg, modelDir, ['--api-key-file', '/etc/llm/api-key']), '')}`;
}

async function checkHealth(instanceId: string): Promise<boolean> {
  try {
    const result = await runShellCommand(instanceId, HEALTH_COMMAND, 30);
    const code = result.stdout.trim();
    // Only 200/401 count: llama.cpp answers 503 on /health while the model is
    // still loading, and "ready" must never hand out a URL that is not serving.
    return result.status === 'Success' && (code === '200' || code === '401');
  } catch (err) {
    console.log(JSON.stringify({ phase: 'health', error: errorName(err) }));
    return false;
  }
}

async function ready(env: string, baseUrl: string): Promise<LambdaFunctionURLResult> {
  // Record the wake so the idle check gives the first request time to land.
  const stateParam = idleStateParam(env);
  const state = await readState(stateParam);
  await writeState(stateParam, { ...state, last_wake_at: new Date().toISOString() });

  console.log(JSON.stringify({ phase: 'ready', environment: env }));
  return jsonResponse(200, {
    state: 'ready',
    environment: env,
    base_url: baseUrl,
    api_key: await readEnvApiKey(env),
  });
}
