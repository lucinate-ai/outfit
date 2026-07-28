import { GetSecretValueCommand, SecretsManagerClient } from '@aws-sdk/client-secrets-manager';
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
import { buildServeCommand, type DeployConfig } from '../shared/deploy-config';
import { jsonResponse } from '../shared/http';

const secretsManager = new SecretsManagerClient({});

const TAG_KEY = requireEnv('TAG_KEY');
const TAG_VALUE = requireEnv('TAG_VALUE');
const VLLM_PORT = requireEnv('VLLM_PORT');
const BASE_URL = requireEnv('BASE_URL');
const STATE_PARAM_NAME = requireEnv('STATE_PARAM_NAME');
const AMI_ROLE_TAG_KEY = requireEnv('AMI_ROLE_TAG_KEY');
const AMI_ROLE_TAG_VALUE = requireEnv('AMI_ROLE_TAG_VALUE');
const AMI_RUNNER_TAG_KEY = requireEnv('AMI_RUNNER_TAG_KEY');
const INSTANCE_TYPE = requireEnv('INSTANCE_TYPE');
const SUBNET_IDS = requireEnv('SUBNET_IDS').split(',');
const SECURITY_GROUP_ID = requireEnv('SECURITY_GROUP_ID');
const INSTANCE_PROFILE_ARN = requireEnv('INSTANCE_PROFILE_ARN');
const EIP_ALLOCATION_ID = requireEnv('EIP_ALLOCATION_ID');
const API_KEY_SECRET_ARN = requireEnv('API_KEY_SECRET_ARN');
const WEIGHTS_BUCKET = requireEnv('WEIGHTS_BUCKET');
const DEPLOY_CONFIG_PARAM = requireEnv('DEPLOY_CONFIG_PARAM');
const REGION = requireEnv('AWS_REGION');

const DEADLINE_MARGIN_MS = 20_000;
const POLL_MS = 5_000;
const HEALTH_POLL_MS = 10_000;
const TERMINAL_STATES = new Set(['shutting-down', 'terminated', 'stopping', 'stopped']);

const HEALTH_COMMAND =
  `curl -s -o /dev/null -w "%{http_code}" --max-time 5 http://localhost:${VLLM_PORT}/health || true`;

export async function handler(
  event: LambdaFunctionURLEvent,
  context: Context,
): Promise<LambdaFunctionURLResult> {
  const method = event.requestContext?.http?.method ?? 'POST';
  if (method === 'GET') {
    return status();
  }
  return wake(context);
}

/** GET — report state without side effects. Used by `outfit remote status`. */
async function status(): Promise<LambdaFunctionURLResult> {
  const instance = await findManagedInstance(TAG_KEY, TAG_VALUE);
  if (!instance || instance.state !== 'running') {
    return jsonResponse(200, { state: instance?.state ?? 'stopped', healthy: false, base_url: BASE_URL });
  }
  const healthy = (await isSsmAgentOnline(instance.instanceId)) && (await checkHealth(instance.instanceId));
  return jsonResponse(200, { state: 'running', healthy, base_url: BASE_URL });
}

/** POST — launch the instance if needed and block until vLLM is serving. */
async function wake(context: Context): Promise<LambdaFunctionURLResult> {
  const deadline = Date.now() + context.getRemainingTimeInMillis() - DEADLINE_MARGIN_MS;

  // What to serve comes from the deploy-config parameter. No default runner: an
  // unset/invalid config fails the wake loudly rather than launching a guess.
  let deployConfig: DeployConfig;
  try {
    deployConfig = await readDeployConfig(DEPLOY_CONFIG_PARAM);
  } catch (err) {
    return jsonResponse(
      503,
      {
        state: 'unconfigured',
        message: `${(err as Error).message} — run \`outfit remote deploy\``,
        retry_after_seconds: 300,
      },
      { 'retry-after': '300' },
    );
  }

  const existing = await findManagedInstance(TAG_KEY, TAG_VALUE);
  let instanceId: string;
  if (existing) {
    // Idempotent: a managed instance is already up (or coming up).
    instanceId = existing.instanceId;
    console.log(JSON.stringify({ phase: 'existing', instanceId, state: existing.state }));
  } else {
    const launched = await launchAcrossAzs(deployConfig);
    if ('error' in launched) {
      return launched.error;
    }
    instanceId = launched.instanceId;
  }

  // Phase 1: EC2 state -> running (then pin the EIP so BASE_URL resolves).
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
    await associateEip(EIP_ALLOCATION_ID, instanceId);
  } catch (err) {
    console.log(JSON.stringify({ phase: 'eip', error: errorName(err) }));
  }

  // Phase 2: SSM agent online (registers 30-60 s after boot).
  while (Date.now() < deadline) {
    if (await isSsmAgentOnline(instanceId)) {
      break;
    }
    await sleep(POLL_MS);
  }

  // Phase 3: vLLM health. The server binds its port only once the engine has
  // loaded the weights, so any HTTP answer (200, or 401 from the api-key
  // middleware) means ready; connection refused means still loading.
  while (Date.now() < deadline) {
    if (await checkHealth(instanceId)) {
      return ready();
    }
    await sleep(HEALTH_POLL_MS);
  }

  console.log(JSON.stringify({ phase: 'deadline', instanceId }));
  return jsonResponse(503, { state: 'starting', retry_after_seconds: 60 }, { 'retry-after': '60' });
}

/** Try each AZ's subnet in turn, skipping ones without capacity. */
async function launchAcrossAzs(
  deployConfig: DeployConfig,
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
  const userData = buildUserData(deployConfig);
  const tried: string[] = [];
  for (const subnetId of SUBNET_IDS) {
    try {
      const instanceId = await runInstance({
        imageId: amiId,
        instanceType: INSTANCE_TYPE,
        subnetId,
        securityGroupId: SECURITY_GROUP_ID,
        instanceProfileArn: INSTANCE_PROFILE_ARN,
        userData,
        tags: { Name: 'cloud-vm-llm', [TAG_KEY]: TAG_VALUE },
      });
      console.log(JSON.stringify({ phase: 'launched', instanceId, subnetId }));
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

function buildUserData(cfg: DeployConfig): string {
  const modelDir = '/opt/llm/model';
  const serveCommand = buildServeCommand(cfg, { modelDir, port: Number(VLLM_PORT) });
  const runnerUnit = cfg.runner === 'vllm' ? vllmUnit(serveCommand) : llamacppUnit(serveCommand);
  // Common boot: log the GPU, add swap for the load spike, sync the weights
  // from S3, fetch the API key. Then the runner-specific env file + systemd
  // unit take over.
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

MODEL_DIR=${modelDir}
mkdir -p "$MODEL_DIR"
aws s3 sync "s3://${WEIGHTS_BUCKET}/${cfg.weightsPrefix}" "$MODEL_DIR/" --region '${REGION}'

API_KEY=$(aws secretsmanager get-secret-value --secret-id '${API_KEY_SECRET_ARN}' --region '${REGION}' --query SecretString --output text)
umask 077

${runnerUnit}
`;
}

/** vLLM: env file (API key, offline, native sampler) plus its systemd unit. */
function vllmUnit(serveCommand: string): string {
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

cat >/etc/systemd/system/vllm.service <<'UNIT'
[Unit]
Description=vLLM server
After=network-online.target
Wants=network-online.target
[Service]
EnvironmentFile=/etc/vllm.env
ExecStart=${serveCommand}
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now vllm.service`;
}

/** llama.cpp: the API key in a root-only file (--api-key-file) plus its unit. */
function llamacppUnit(serveCommand: string): string {
  return `mkdir -p /etc/llm
printf '%s' "$API_KEY" >/etc/llm/api-key
chmod 600 /etc/llm/api-key

cat >/etc/systemd/system/llama-server.service <<'UNIT'
[Unit]
Description=llama.cpp server
After=network-online.target
Wants=network-online.target
[Service]
ExecStart=${serveCommand}
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now llama-server.service`;
}

async function checkHealth(instanceId: string): Promise<boolean> {
  try {
    const result = await runShellCommand(instanceId, HEALTH_COMMAND, 30);
    const code = result.stdout.trim();
    return result.status === 'Success' && code !== '' && code !== '000';
  } catch (err) {
    console.log(JSON.stringify({ phase: 'health', error: errorName(err) }));
    return false;
  }
}

async function ready(): Promise<LambdaFunctionURLResult> {
  // Record the wake so the idle check gives the first request time to land.
  const state = await readState(STATE_PARAM_NAME);
  await writeState(STATE_PARAM_NAME, { ...state, last_wake_at: new Date().toISOString() });

  const secret = await secretsManager.send(
    new GetSecretValueCommand({ SecretId: API_KEY_SECRET_ARN }),
  );
  console.log(JSON.stringify({ phase: 'ready' }));
  return jsonResponse(200, {
    state: 'ready',
    base_url: BASE_URL,
    api_key: secret.SecretString ?? '',
  });
}
