import type { Context, LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import {
  RETAIN_UNTIL_TAG,
  associateEip,
  errorName,
  findLatestAmi,
  findManagedInstance,
  getInstance,
  isCapacityError,
  isSsmAgentOnline,
  readDeployConfig,
  requireEnv,
  runInstance,
  runShellCommand,
  sleep,
  STARTED_AT_TAG,
  startEngineDaemon,
  startInstance,
  tagInstance,
} from '../shared/aws';
import { type DeployConfig, logGroupEnvVar, type Runner, RUNNERS } from '../shared/deploy-config';
import {
  baseUrlFor,
  deployConfigParam,
  ENV_TAG_KEY,
  environmentFrom,
  findEnvEip,
  findEnvSecurityGroup,
  readEnvApiKey,
} from '../shared/environments';
import { DAEMON_STATUS_CMD, parseDaemonStatus } from '../shared/daemon';
import { jsonResponse } from '../shared/http';
import { DAEMON_CONFIG_DIR, runnerSpec } from '../runners';

const TAG_KEY = requireEnv('TAG_KEY');
const TAG_VALUE = requireEnv('TAG_VALUE');
const ENGINE_PORT = requireEnv('ENGINE_PORT');
const AMI_ROLE_TAG_KEY = requireEnv('AMI_ROLE_TAG_KEY');
const AMI_ROLE_TAG_VALUE = requireEnv('AMI_ROLE_TAG_VALUE');
const AMI_RUNNER_TAG_KEY = requireEnv('AMI_RUNNER_TAG_KEY');
const INSTANCE_TYPE = requireEnv('INSTANCE_TYPE');
const SUBNET_IDS = requireEnv('SUBNET_IDS').split(',');
const INSTANCE_PROFILE_ARN = requireEnv('INSTANCE_PROFILE_ARN');
const WEIGHTS_BUCKET = requireEnv('WEIGHTS_BUCKET');
const REGION = requireEnv('AWS_REGION');
const BOOT_LOG_GROUP = requireEnv('BOOT_LOG_GROUP');
const ENGINE_LOG_GROUP = Object.fromEntries(
  RUNNERS.map((r) => [r, requireEnv(logGroupEnvVar(r))]),
) as Record<Runner, string>;
// Where the outfit daemon writes the engine's stdout/stderr (tailed by the
// CloudWatch agent): under the daemon's pinned config dir (OUTFIT_CONFIG_DIR),
// a fixed system path that does not depend on $HOME.
const ENGINE_LOG_FILE = `${DAEMON_CONFIG_DIR}/daemon/engine.log`;

const DEADLINE_MARGIN_MS = 20_000;
const POLL_MS = 5_000;
const HEALTH_POLL_MS = 10_000;
// stopping/stopped are re-wakable, not terminal: the sweep stops instances
// now, so a wake must find the one it is trying to revive rather than fail on
// it. Only states headed for the scrapyard end a wake.
const TERMINAL_STATES = new Set(['shutting-down', 'terminated']);

const HEALTH_COMMAND =
  `curl -s -o /dev/null -w "%{http_code}" --max-time 5 http://localhost:${ENGINE_PORT}/health || true`;

// gp3's unprovisioned baseline is 3,000 IOPS and 125 MiB/s of throughput —
// whatever the "fast" reputation says, a default root volume streams at
// ~125 MB/s. That ceiling is paid twice on a cold boot: the S3 sync writes the
// weights through it, and the engine's model load reads them back through it
// (the daemon prewarms the page cache, so that read is sequential and this is
// its whole limit). Provisioning the top-end throughput cuts both; the IOPS
// stay at the baseline because sequential work is throughput-bound. The cost
// is a few dollars a month against a $1.86/hour GPU, billed only while the
// volume exists.
const ROOT_VOLUME_THROUGHPUT_MIBS = 1000;

/** Narrow instance discovery to one environment's instance. */
function envFilter(env: string) {
  return [{ Name: `tag:${ENV_TAG_KEY}`, Values: [env] }];
}

/** Parse and validate the optional retainUntil query parameter. */
function parseRetainUntil(raw: string | undefined): string | null {
  if (!raw) {
    return null;
  }
  const d = new Date(raw);
  if (Number.isNaN(d.getTime())) {
    return null;
  }
  return d.toISOString();
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
  const rawRetainUntil = event.queryStringParameters?.retainUntil;
  const retainUntil = parseRetainUntil(rawRetainUntil);
  return wake(env, context, retainUntil);
}

/** GET — report one environment's state without side effects. */
async function status(env: string): Promise<LambdaFunctionURLResult> {
  const eip = await findEnvEip(env);
  const baseUrl = eip ? baseUrlFor(eip.publicIp, ENGINE_PORT) : '';
  const instance = await findManagedInstance(TAG_KEY, TAG_VALUE, envFilter(env));
  if (!instance || instance.state !== 'running') {
    // No SSM call on this branch: reaching the daemon needs a running box, so
    // a stopped environment reports no activity rather than a made-up one.
    return jsonResponse(200, {
      state: instance?.state ?? (eip ? 'stopped' : 'undeployed'),
      environment: env,
      healthy: false,
      base_url: baseUrl,
    });
  }
  if (!(await isSsmAgentOnline(instance.instanceId))) {
    return jsonResponse(200, {
      state: 'running',
      environment: env,
      healthy: false,
      base_url: baseUrl,
    });
  }
  // Concurrently, not in sequence: status is what you type repeatedly while
  // waiting for a box, and this branch should cost the slower of the two SSM
  // calls rather than their sum.
  const [healthy, activity] = await Promise.all([
    checkHealth(instance.instanceId),
    readDaemonActivity(instance.instanceId),
  ]);
  const result: Record<string, unknown> = {
    state: 'running',
    environment: env,
    healthy,
    base_url: baseUrl,
    ...activity,
  };
  if (instance.retainUntil) {
    result.retainUntil = instance.retainUntil.toISOString();
  }
  return jsonResponse(200, result);
}

/**
 * Ask the instance's daemon when its engine last did work. Every failure —
 * SSM error, unreachable daemon, unparseable reply, an engine that has done
 * nothing yet — yields an empty object, so the caller spreads nothing and the
 * report is exactly what it would have been. This must never be able to turn
 * a working status into a failing one, which is why it is kept out of the
 * `healthy` expression.
 */
async function readDaemonActivity(
  instanceId: string,
): Promise<{ lastActiveAt?: string; idleSeconds?: number }> {
  try {
    const result = await runShellCommand(instanceId, DAEMON_STATUS_CMD, 10);
    if (result.status !== 'Success') {
      return {};
    }
    const daemon = parseDaemonStatus(result.stdout);
    if (!daemon?.lastActiveAt) {
      return {};
    }
    // idleSeconds is omitted rather than sent as 0 when the engine is working
    // right now, so an absent value with a present timestamp means zero.
    return { lastActiveAt: daemon.lastActiveAt, idleSeconds: daemon.idleSeconds ?? 0 };
  } catch (err) {
    console.log(JSON.stringify({ phase: 'daemon-activity', error: errorName(err) }));
    return {};
  }
}

/** POST — launch the environment's instance if needed and block until serving. */
async function wake(
  env: string,
  context: Context,
  retainUntil: string | null,
): Promise<LambdaFunctionURLResult> {
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
  const baseUrl = baseUrlFor(eip.publicIp, ENGINE_PORT);

  const existing = await findManagedInstance(TAG_KEY, TAG_VALUE, envFilter(env));
  let instanceId: string;
  let startIssued = false;
  if (existing) {
    // Idempotent: this environment's instance already exists (up, coming up,
    // or stopped — the sweep stops idle ones now, so this is the normal
    // re-wake path).
    instanceId = existing.instanceId;
    console.log(JSON.stringify({ phase: 'existing', environment: env, instanceId, state: existing.state }));
    if (existing.state === 'stopped') {
      try {
        await rewake(instanceId);
        startIssued = true;
      } catch (err) {
        if (errorName(err) === 'InsufficientInstanceCapacity') {
          return jsonResponse(
            503,
            { state: 'no-capacity', environment: env, retry_after_seconds: 120 },
            { 'retry-after': '120' },
          );
        }
        throw err;
      }
    }
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
      // A dying instance is not this wake's machine to adopt; the deploy
      // config is intact, so a retry launches a fresh one.
      return jsonResponse(
        503,
        { state, message: `instance went ${state} while starting`, retry_after_seconds: 300 },
        { 'retry-after': '300' },
      );
    }
    if (state === 'stopped' && !startIssued) {
      // A stop raced us between discovery and now; issue the re-wake here so
      // one wake owns at most one start call.
      try {
        await rewake(instanceId);
        startIssued = true;
      } catch (err) {
        if (errorName(err) === 'InsufficientInstanceCapacity') {
          return jsonResponse(
            503,
            { state: 'no-capacity', environment: env, retry_after_seconds: 120 },
            { 'retry-after': '120' },
          );
        }
        throw err;
      }
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

  // Make sure the engine is actually asked to start. A fresh launch's boot
  // script requests a start, but a re-wake must not bet on user data
  // re-running — if it does not, the daemon is up and idle and nothing else
  // ever starts the engine. The daemon's /v1/start is idempotent (it refuses
  // with 409 when one already runs), so this is harmless on every path; an
  // unreachable daemon only matters when user data still covers the start.
  if (!(await startEngineDaemon(instanceId))) {
    console.log(
      JSON.stringify({ phase: 'engine-start', environment: env, instanceId, warning: 'daemon did not answer a start request' }),
    );
  }

  // Phase 3: server health. vLLM binds its port only once the engine has
  // loaded the weights, but llama.cpp binds immediately and serves 503 while
  // loading — so only a 200 (or a 401 from the api-key middleware) means
  // ready; a 503 or connection refused means still loading.
  while (Date.now() < deadline) {
    if (await checkHealth(instanceId)) {
      // Best-effort retain tag: the wake must not fail because of it.
      if (retainUntil) {
        try {
          await tagInstance(instanceId, RETAIN_UNTIL_TAG, retainUntil);
        } catch (err) {
          console.log(
            JSON.stringify({ phase: 'retain', environment: env, instanceId, error: `tag ${errorName(err)}` }),
          );
        }
      }
      return ready(env, baseUrl, retainUntil);
    }
    await sleep(HEALTH_POLL_MS);
  }

  console.log(JSON.stringify({ phase: 'deadline', environment: env, instanceId }));
  return jsonResponse(503, { state: 'starting', retry_after_seconds: 60 }, { 'retry-after': '60' });
}

/**
 * Re-wake a stopped instance and record when this session began. This issues
 * only the EC2 start: a fresh boot's user data does not re-run on a
 * stop/start cycle, and the baked crash-nudge covers only a mid-session crash,
 * so the engine start itself is the control plane's job (the explicit
 * /v1/start after the SSM agent is online), with the phase polling (SSM
 * agent, health) then applying unchanged. The weights the root volume already
 * holds make the re-wake fast. The Started-At tag is best-effort: losing it
 * only makes the max-runtime judgement conservative (measuring from first
 * launch), never unsafe.
 */
async function rewake(instanceId: string): Promise<void> {
  console.log(JSON.stringify({ phase: 'rewake', instanceId }));
  await startInstance(instanceId);
  try {
    await tagInstance(instanceId, STARTED_AT_TAG, new Date().toISOString());
  } catch (err) {
    console.log(JSON.stringify({ phase: 'rewake', instanceId, error: `tag ${errorName(err)}` }));
  }
}

/** Try each AZ's subnet in turn, skipping ones without capacity. */
async function launchAcrossAzs(
  env: string,
  deployConfig: DeployConfig,
  securityGroupId: string,
): Promise<{ instanceId: string } | { error: LambdaFunctionURLResult }> {
  // Pick the newest AMI baked for THIS runner (role + runner tags).
  const ami = await findLatestAmi([
    { Name: `tag:${AMI_ROLE_TAG_KEY}`, Values: [AMI_ROLE_TAG_VALUE] },
    { Name: `tag:${AMI_RUNNER_TAG_KEY}`, Values: [deployConfig.runner] },
    { Name: 'state', Values: ['available'] },
  ]);
  if (!ami) {
    return {
      error: jsonResponse(503, {
        state: 'no-ami',
        message: `no baked AMI for runner "${deployConfig.runner}"; run \`pnpm bake ${deployConfig.runner}\` and wait`,
        retry_after_seconds: 300,
      }),
    };
  }
  const userData = buildInferenceUserData(env, deployConfig);
  const tried: string[] = [];
  for (const subnetId of SUBNET_IDS) {
    try {
      const instanceId = await runInstance({
        imageId: ami.imageId,
        instanceType: INSTANCE_TYPE,
        subnetId,
        securityGroupId,
        instanceProfileArn: INSTANCE_PROFILE_ARN,
        userData,
        tags: { Name: `cloud-vm-llm-${env}`, [TAG_KEY]: TAG_VALUE, [ENV_TAG_KEY]: env },
        // A 0 means the AMI declared no readable root mapping — launching the
        // AMI's root as-is is then the only safe choice.
        rootVolume:
          ami.rootVolumeSizeGb > 0
            ? {
                volumeSize: ami.rootVolumeSizeGb,
                throughput: ROOT_VOLUME_THROUGHPUT_MIBS,
              }
            : undefined,
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

/** Exported for tests: the boot script is pure string-building. The seed twin is shared/seed.ts's buildSeedUserData. */
export function buildInferenceUserData(env: string, cfg: DeployConfig): string {
  const modelDir = '/opt/llm/model';
  const runnerUnit = runnerSpec(cfg.runner).daemonBoot(cfg, modelDir, Number(ENGINE_PORT));
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

async function ready(
  env: string,
  baseUrl: string,
  retainUntil: string | null,
): Promise<LambdaFunctionURLResult> {
  // No wake is recorded here: the daemon counts an engine start as activity,
  // so the instance itself reports a fresh idle time and the idle check gives
  // the first request time to land without the control plane tracking it.
  console.log(JSON.stringify({ phase: 'ready', environment: env }));
  const result: Record<string, unknown> = {
    state: 'ready',
    environment: env,
    base_url: baseUrl,
    api_key: await readEnvApiKey(env),
  };
  if (retainUntil) {
    result.retainUntil = retainUntil;
  }
  return jsonResponse(200, result);
}
