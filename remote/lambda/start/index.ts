import { randomUUID } from 'node:crypto';
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
import {
  LATEST_SPINLOOP,
  type DeployConfig,
  logGroupEnvVar,
  type Runner,
  RUNNERS,
} from '../shared/deploy-config';
import {
  baseUrlFor,
  deployConfigParam,
  ENV_TAG_KEY,
  type EnvEip,
  environmentFrom,
  findEnvEip,
  findEnvSecurityGroup,
  readEnvApiKey,
} from '../shared/environments';
import { DAEMON_STATUS_CMD, parseDaemonStatus } from '../shared/daemon';
import { jsonResponse } from '../shared/http';
import { isScheduledRunEvent, type ScheduledRunEvent } from '../shared/schedules';
import { acquireWakeLock, releaseWakeLock, wakeLockHeld } from '../shared/wake-lock';
import { weightsPresent } from '../shared/seed';
import { findSeedInstances, seedAlive } from '../shared/seed/discovery';
import { seedIdFor } from '../shared/seed/identity';
import { buildSeedJob, launchSeedInstance, seedInfraFromEnv } from '../shared/seed/launch';
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
const MAX_CONCURRENT_SEEDS = Number(requireEnv('MAX_CONCURRENT_SEEDS'));
const REGION = requireEnv('AWS_REGION');
const BOOT_LOG_GROUP = requireEnv('BOOT_LOG_GROUP');
const ENGINE_LOG_GROUP = Object.fromEntries(
  RUNNERS.map((r) => [r, requireEnv(logGroupEnvVar(r))]),
) as Record<Runner, string>;
// Where the spinloop daemon writes the engine's stdout/stderr (tailed by the
// CloudWatch agent): under the daemon's pinned config dir (SPINLOOP_CONFIG_DIR),
// a fixed system path that does not depend on $HOME.
const ENGINE_LOG_FILE = `${DAEMON_CONFIG_DIR}/daemon/engine.log`;

// Where the boot's user data syncs the weights from S3. The start Lambda
// names the same path in the start's body, so the two can never point the
// engine at different model directories.
const MODEL_DIR = '/opt/llm/model';

const DEADLINE_MARGIN_MS = 20_000;
const POLL_MS = 5_000;
const HEALTH_POLL_MS = 10_000;
// stopping/stopped are re-wakable, not terminal: the sweep stops instances
// now, so a wake must find the one it is trying to revive rather than fail on
// it. Only states headed for the scrapyard end a wake.
const TERMINAL_STATES = new Set(['shutting-down', 'terminated']);
// Once a start has launched an instance or issued its start command, any of
// these means a stop (or the sweep) got there first and the instance is not
// coming up, so the start ends rather than polling it until the deadline.
const GONE_STATES = new Set(['stopped', 'stopping', 'shutting-down', 'terminated']);

// How long a refused start is told to wait, and how far past its own time
// limit a lock stays valid.
const LOCK_RETRY_SECONDS = 15;
const LOCK_MARGIN_MS = 30_000;

const HEALTH_COMMAND =
  `curl -s -o /dev/null -w "%{http_code}" --max-time 5 http://localhost:${ENGINE_PORT}/health || true`;

// Where a dying boot leaves its reason, and where the wake reads it. Under
// /var/lib (which always exists) rather than the daemon's config dir, which a
// boot that fails early never creates; on disk rather than tmpfs so a
// re-woken instance still reports the boot that broke it — user data does not
// re-run on a stop/start, so the marker outlives the only chance to fix it.
export const BOOT_FAILED_MARKER = '/var/lib/spinloop-boot-failed';

// The boot's own word for "I died": prefixed so the boot log greps as easily
// as the marker file, matching the seed boot's SEED_BOOT_FAILED convention.
const BOOT_FAILED_PREFIX = 'SPINLOOP_BOOT_FAILED';

const BOOT_FAILURE_COMMAND = `cat ${BOOT_FAILED_MARKER} 2>/dev/null || true`;

// ~125 MB/s. A cold boot pays it twice: the S3 sync writes the weights
// through it, and the engine's model load reads them back page fault by page
// fault — an IOPS-bound read, so the load takes its limit from the provisioned
// IOPS and the sync from the throughput. Provisioning the top end moves both.
// EC2 caps provisioned throughput at 0.25 MiB/s per provisioned IOP, so the
// ceiling needs 4,000 IOPS — the 1,000 above baseline are the price of the
// last 250 MiB/s, and they are billed only while the volume exists, against a
// $1.86/hour GPU.
const ROOT_VOLUME_THROUGHPUT_MIBS = 1000;
const ROOT_VOLUME_IOPS = 4 * ROOT_VOLUME_THROUGHPUT_MIBS;

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
  event: LambdaFunctionURLEvent | ScheduledRunEvent,
  context: Context,
): Promise<LambdaFunctionURLResult> {
  if (isScheduledRunEvent(event)) {
    return scheduledStart(event, context);
  }
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

/**
 * Whether a start holds the environment's lock. A lock that cannot be read is
 * reported as no start in progress: a status read must not fail over it.
 */
async function startHoldsLock(env: string): Promise<boolean> {
  try {
    return await wakeLockHeld(env);
  } catch (err) {
    console.log(JSON.stringify({ phase: 'lock-read', environment: env, error: errorName(err) }));
    return false;
  }
}

/**
 * A `start` schedule firing: the same wake as an on-demand start, with no
 * retention deadline. A running instance is left as it is (the wake reports it
 * ready), and the wake takes the environment's start lock like any other, so a
 * schedule firing while someone starts the environment by hand launches one
 * instance, not two. Nobody is waiting on the reply, so the outcome is logged,
 * and a failure to launch is not retried before the next firing.
 */
async function scheduledStart(
  event: ScheduledRunEvent,
  context: Context,
): Promise<LambdaFunctionURLResult> {
  if (event.action !== 'start') {
    console.log(JSON.stringify({ mode: 'scheduled', action: 'ignored', scheduledAction: event.action }));
    return jsonResponse(200, { state: 'ignored', environment: event.environment });
  }
  const result = await wake(event.environment, context, null);
  console.log(
    JSON.stringify({ mode: 'scheduled', action: 'start', environment: event.environment, result }),
  );
  return result;
}

/** GET — report one environment's state without side effects. */
async function status(env: string): Promise<LambdaFunctionURLResult> {
  const eip = await findEnvEip(env);
  const baseUrl = eip ? baseUrlFor(eip.publicIp, ENGINE_PORT) : '';
  const instance = await findManagedInstance(TAG_KEY, TAG_VALUE, envFilter(env));
  const starting = await startHoldsLock(env);
  if (!instance || instance.state !== 'running') {
    // No SSM call on this branch: reaching the daemon needs a running box, so
    // a stopped environment reports no activity rather than a made-up one.
    // While a start holds the lock, an instance that is absent, stopped or
    // still coming up is `starting`: the instance lookup alone cannot show a
    // start another client began, because it lags a launch.
    const instanceState = instance?.state ?? (eip ? 'stopped' : 'undeployed');
    const comingUp = !instance || instance.state === 'stopped' || instance.state === 'pending';
    return jsonResponse(200, {
      state: starting && comingUp ? 'starting' : instanceState,
      environment: env,
      healthy: false,
      base_url: baseUrl,
      start_in_progress: starting,
    });
  }
  if (!(await isSsmAgentOnline(instance.instanceId))) {
    return jsonResponse(200, {
      state: 'running',
      environment: env,
      healthy: false,
      base_url: baseUrl,
      start_in_progress: starting,
    });
  }
  // Concurrently, not in sequence: status is what you type repeatedly while
  // waiting for a box, and this branch should cost the slowest of these calls
  // rather than their sum. The model facts come from the deploy config — the
  // same source the stats reply reads — and the activity from the daemon.
  const [healthy, activity, deploy] = await Promise.all([
    checkHealth(instance.instanceId),
    readDaemonActivity(instance.instanceId),
    readDeployFacts(env),
  ]);
  const result: Record<string, unknown> = {
    state: 'running',
    environment: env,
    healthy,
    base_url: baseUrl,
    start_in_progress: starting,
    ...deploy,
    ...activity,
  };
  if (instance.retainUntil) {
    result.retainUntil = instance.retainUntil.toISOString();
  }
  return jsonResponse(200, result);
}

/**
 * Ask the instance's daemon when its engine last did work. Every failure — an
 * SSM error, an unreachable daemon, an unparseable reply, an engine that has
 * not done any work yet — yields an empty object, so the caller spreads
 * nothing and the report is exactly what it would have been without this. It
 * is kept out of the `healthy` expression so it can never turn a working
 * status into a failing one.
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

// How often a caller should re-ask while the weights are still seeding. A
// seed runs for minutes, so nothing is learned by polling faster — and the
// reply stays in the same shape as the other 503s the wake gives out.
const SEED_RETRY_SECONDS = 60;

function seedingReply(seedId: string, message: string): LambdaFunctionURLResult {
  return jsonResponse(503, {
    state: 'seeding',
    seedId,
    retry_after_seconds: SEED_RETRY_SECONDS,
    message,
  });
}

/**
 * The weights gate: a wake must not launch an instance that will sync a
 * partial prefix.
 *
 * Weights are judged by the manifest the seeder writes last — not by the
 * silence of an error, and not by the seed's reports alone, which stop
 * arriving the moment the seed's process dies. When the weights are absent
 * the seed for them is either running, in which case the wake joins it, or
 * not, in which case the wake starts it and the caller waits. Either way the
 * reply is the same retryable state naming the seed, so the caller's loop can
 * keep polling without learning anything about how seeds work.
 *
 * Returns null when the weights are present and the wake may proceed.
 */
async function seedingGate(
  env: string,
  config: DeployConfig,
): Promise<LambdaFunctionURLResult | null> {
  let present: boolean;
  try {
    present = await weightsPresent(WEIGHTS_BUCKET, config);
  } catch (err) {
    // A failed manifest read is not "absent": read as absent, a transient
    // glitch would pay for a full re-seed; read as present, the wake would
    // boot on weights nobody verified. Say the check failed and let the
    // caller re-ask, the way deploy answers a seed it cannot start.
    console.log(JSON.stringify({ phase: 'seed-check', environment: env, error: errorName(err) }));
    return jsonResponse(502, {
      error: `could not check whether the weights are present: ${(err as Error).message}`,
    });
  }
  if (present) {
    return null;
  }
  const seedId = seedIdFor(config.runner, config.modelId, config.quant);
  const follow = `follow it with \`spinloop cloud seed status ${seedId}\``;

  const inFlight = (await findSeedInstances(TAG_KEY, seedId)).filter((i) => seedAlive(i.state));
  if (inFlight.length > 0) {
    console.log(
      JSON.stringify({ phase: 'seeding', environment: env, seedId, joined: inFlight[0].instanceId }),
    );
    return seedingReply(seedId, `seeding the weights — ${follow}`);
  }

  // Counted over alive seeds only: a stopped seed instance holds no compute,
  // and it would be a cap that a dead body keeps filled.
  const alive = (await findSeedInstances(TAG_KEY)).filter((i) => seedAlive(i.state));
  if (alive.length >= MAX_CONCURRENT_SEEDS) {
    console.log(JSON.stringify({ phase: 'seeding-cap', environment: env, seedId, running: alive.length }));
    return seedingReply(
      seedId,
      `${alive.length} seeds are running (cap ${MAX_CONCURRENT_SEEDS}) — waiting for a slot; ${follow}`,
    );
  }

  const infra = seedInfraFromEnv();
  try {
    const launched = await launchSeedInstance(buildSeedJob(config, infra, ''), infra);
    console.log(JSON.stringify({ phase: 'seed-launched', environment: env, seedId, instanceId: launched.instanceId }));
  } catch (err) {
    // A refused launch is not a reason to fail the start that wanted the model
    // served: the next poll retries it, and the idempotency token means a retry
    // can never double the compute.
    console.log(JSON.stringify({ phase: 'seed-launch', environment: env, seedId, error: errorName(err) }));
    return seedingReply(
      seedId,
      `the seed could not be started (${(err as Error).message}) — retrying; ${follow}`,
    );
  }
  return seedingReply(seedId, `seeding the weights — ${follow}`);
}

/**
 * Read the environment's stored deploy config and keep the facts that name
 * what it is serving: the runner, the model id, and the served name where the
 * deploy gave one. This is the same source the stats reply — and so the remote
 * status view — reads, so the fleet and remote views name a model identically.
 * The daemon's own model is the on-disk weights path, which a router cannot
 * match a request against, so the path is never reported here. A failed read
 * yields an empty object, so an environment whose config cannot be read still
 * reports its state and activity without a made-up model.
 */
async function readDeployFacts(env: string): Promise<{
  runner?: string;
  modelId?: string;
  servedName?: string;
}> {
  try {
    const cfg = await readDeployConfig(deployConfigParam(env));
    const facts: { runner?: string; modelId?: string; servedName?: string } = {};
    if (cfg.runner) {
      facts.runner = cfg.runner;
    }
    if (cfg.modelId) {
      facts.modelId = cfg.modelId;
    }
    if (cfg.servedModelName) {
      facts.servedName = cfg.servedModelName;
    }
    return facts;
  } catch (err) {
    console.log(JSON.stringify({ phase: 'deploy-facts', error: errorName(err) }));
    return {};
  }
}

/**
 * What a held lock, and a lock that could not be taken, are both answered
 * with: a retryable "starting", so the CLI and gateway ask again and find the
 * holder's instance. Nothing has been looked up or launched.
 */
function startInProgress(env: string, message: string): LambdaFunctionURLResult {
  return jsonResponse(
    503,
    { state: 'starting', environment: env, message, retry_after_seconds: LOCK_RETRY_SECONDS },
    { 'retry-after': String(LOCK_RETRY_SECONDS) },
  );
}

/**
 * The reply for a start whose instance was stopped or terminated under it.
 * Retryable, so a client still waiting asks again and re-wakes the instance
 * (or launches a fresh one); ending here is what releases the lock.
 */
function stoppedUnderStart(env: string, instanceId: string, state: string): LambdaFunctionURLResult {
  console.log(JSON.stringify({ phase: 'stopped-under-start', environment: env, instanceId, state }));
  return jsonResponse(
    503,
    {
      state,
      environment: env,
      instance_id: instanceId,
      message: `the instance was ${state === 'stopping' ? 'stopped' : state} while starting`,
      retry_after_seconds: LOCK_RETRY_SECONDS,
    },
    { 'retry-after': String(LOCK_RETRY_SECONDS) },
  );
}

/**
 * Whether the instance this start is waiting on has been stopped or terminated,
 * as the reply to end the start with, or null to keep waiting. A lookup that
 * fails (the instance not yet visible, a transient error) keeps waiting: the
 * poll loops have their own deadline.
 */
async function goneUnderStart(env: string, instanceId: string): Promise<LambdaFunctionURLResult | null> {
  let state: string;
  try {
    state = (await getInstance(instanceId)).state;
  } catch {
    return null;
  }
  return GONE_STATES.has(state) ? stoppedUnderStart(env, instanceId, state) : null;
}

/**
 * POST — launch the environment's instance if needed and block until serving.
 *
 * Only one start works on an environment at a time: the instance lookup that
 * decides whether to launch is eventually consistent, so two starts close
 * together could each launch one. The checks that only read (the deploy config,
 * the environment's address and security group) come first and take no lock,
 * so an environment that cannot start says so without contending for one.
 */
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
        message: `${(err as Error).message} — run \`spinloop cloud deploy\``,
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
      message: `environment ${JSON.stringify(env)} has no deployed infrastructure — run \`spinloop cloud deploy\``,
      retry_after_seconds: 300,
    });
  }
  const baseUrl = baseUrlFor(eip.publicIp, ENGINE_PORT);

  // The lock lasts as long as this invocation can run, so a start killed
  // before it could release the lock blocks the environment for no longer.
  // A lock that cannot be taken for a reason other than being held is
  // answered the same way as a held one: nothing is launched unlocked.
  const owner = context.awsRequestId ?? randomUUID();
  const expiresAt = new Date(Date.now() + context.getRemainingTimeInMillis() + LOCK_MARGIN_MS);
  try {
    if (!(await acquireWakeLock(env, owner, expiresAt))) {
      console.log(JSON.stringify({ phase: 'lock-held', environment: env }));
      return startInProgress(env, `another start for environment ${JSON.stringify(env)} is in progress`);
    }
  } catch (err) {
    console.log(JSON.stringify({ phase: 'lock-error', environment: env, error: errorName(err) }));
    return startInProgress(env, 'could not take the start lock; retrying');
  }
  try {
    return await wakeLocked(env, deadline, retainUntil, deployConfig, eip, securityGroupId, baseUrl);
  } finally {
    try {
      await releaseWakeLock(env, owner);
    } catch (err) {
      // The reply is already decided; the lock expires on its own.
      console.log(JSON.stringify({ phase: 'lock-release', environment: env, error: errorName(err) }));
    }
  }
}

/** The body of a start, run while this invocation holds the environment's lock. */
async function wakeLocked(
  env: string,
  deadline: number,
  retainUntil: string | null,
  deployConfig: DeployConfig,
  eip: EnvEip,
  securityGroupId: string,
  baseUrl: string,
): Promise<LambdaFunctionURLResult> {
  // Weights first: a launch against an incomplete prefix would boot the engine
  // on it, and a re-wake would keep an old one alive on nothing.
  const gate = await seedingGate(env, deployConfig);
  if (gate) {
    return gate;
  }

  const existing = await findManagedInstance(TAG_KEY, TAG_VALUE, envFilter(env));
  let instanceId: string;
  let startIssued = false;
  let launchedFresh = false;
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
    launchedFresh = true;
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
    if ((state === 'stopped' || state === 'stopping') && (startIssued || launchedFresh)) {
      // This start launched the instance or issued its start command, and it
      // has since been stopped: it is not coming up, so end the wake and let
      // the lock go rather than poll it until the deadline.
      return stoppedUnderStart(env, instanceId, state);
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
    const gone = await goneUnderStart(env, instanceId);
    if (gone) {
      return gone;
    }
    if (await isSsmAgentOnline(instanceId)) {
      break;
    }
    await sleep(POLL_MS);
  }

  // Phase 2b: the daemon answers. The boot's user data syncs the weights
  // before it enables the daemon, so on a fresh launch the daemon's first
  // answer is the boot's signal that the deploy config is stored; on a
  // re-wake the daemon comes back with the instance. Until it answers there
  // is no one to take a start, so this wait converts a lost start (and a
  // full-deadline health timeout) into a short pause.
  while (Date.now() < deadline) {
    const gone = await goneUnderStart(env, instanceId);
    if (gone) {
      return gone;
    }
    if (await daemonAnswers(instanceId)) {
      break;
    }
    // The daemon is installed by the boot, so a boot that died is a daemon
    // that will never answer — no amount of further polling changes that.
    // Reading the marker here turns the old silent zombie (a running GPU
    // instance serving nothing, reported as "still starting" until the
    // deadline) into a terminal answer naming the failing command.
    const failure = await bootFailure(instanceId);
    if (failure) {
      console.log(JSON.stringify({ phase: 'boot-failed', environment: env, instanceId, failure }));
      return jsonResponse(500, {
        state: 'boot-failed',
        environment: env,
        instance_id: instanceId,
        message: `the instance booted but its start-up script failed, so nothing is serving: ${failure}`,
      });
    }
    await sleep(POLL_MS);
  }

  // The control plane owns the engine's start on every path — a fresh boot
  // and a re-wake alike; the boot's own user data starts nothing. The start
  // carries the deploy config as its body, so it always names the exact
  // config the daemon runs. The daemon's /v1/start is idempotent (it refuses
  // with 409 when one already runs), so this is harmless wherever an engine
  // is already up.
  if (!(await startEngineDaemon(instanceId, startBody(deployConfig)))) {
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
    const gone = await goneUnderStart(env, instanceId);
    if (gone) {
      return gone;
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
  // The instance type this environment launches as: the deploy config's own
  // type when it names one, else the control plane's default (the env var).
  // A re-wake of a stopped instance never reaches this path — it keeps the
  // type the box was launched with; only a fresh launch reads the config here.
  const instanceType = deployConfig.instanceType ?? INSTANCE_TYPE;
  const tried: string[] = [];
  for (const subnetId of SUBNET_IDS) {
    try {
      const instanceId = await runInstance({
        imageId: ami.imageId,
        instanceType,
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
                iops: ROOT_VOLUME_IOPS,
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
        message: `no ${instanceType} capacity in any of ${tried.length} availability zone(s); retry shortly`,
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

/**
 * The spinloop daemon's binary, fetched and installed at boot rather than baked
 * into the AMI — so an spinloop release reaches an environment without a
 * re-bake. The version comes from the deploy config: a pin is exact, and the
 * absent pin's default (LATEST_SPINLOOP) is resolved from GitHub here at boot,
 * so "latest" stays the latest published release on every fresh launch.
 *
 * Idempotent: a re-run against an already-correct install skips the download,
 * comparing the installed binary's own version output. Verified against the
 * release's own checksums before installing, and install(1) lands the binary
 * by rename in its destination directory, so an interruption at any point
 * leaves the previous state — no binary, or a previously verified one — never
 * a partial or unverified binary.
 */
export function spinloopInstallStep(version: string): string {
  const pin = version === LATEST_SPINLOOP ? '' : version;
  return `# spinloop itself — the daemon that hosts the engine and answers the control
# Lambdas over its loopback API. A pin is an exact release; an empty pin is
# the deploy config's default (latest), resolved from GitHub here at boot.
SPINLOOP_VERSION='${pin}'
if [ -z "$SPINLOOP_VERSION" ]; then
  SPINLOOP_TAG=$(curl -fsSL https://api.github.com/repos/spinloop-ai/spinloop/releases/latest | grep -o '"tag_name": *"[^"]*"' | head -n1 | cut -d'"' -f4)
  SPINLOOP_VERSION=\${SPINLOOP_TAG#v}
fi
test -n "$SPINLOOP_VERSION" || { echo "spinloop version unresolved — check the deploy config's spinloopVersion" >&2; exit 1; }
if [ -x /usr/local/bin/spinloop ] && [ "$(/usr/local/bin/spinloop version)" = "$SPINLOOP_VERSION" ]; then
  echo "spinloop \${SPINLOOP_VERSION} already installed"
else
  SPINLOOP_URL="https://github.com/spinloop-ai/spinloop/releases/download/v\${SPINLOOP_VERSION}"
  mkdir -p /tmp/spinloop-dl
  # Named rather than left to the ERR trap: a rename resolves a version
  # happily (GitHub redirects the API call) and then 404s the asset, so the
  # bare curl failure points at the wrong thing. Say which asset was missing
  # from which release, because that is the whole diagnosis.
  if ! curl -fsSL "$SPINLOOP_URL/spinloop_linux_amd64.tar.gz" -o /tmp/spinloop-dl/spinloop_linux_amd64.tar.gz; then
    echo "${BOOT_FAILED_PREFIX}: no spinloop_linux_amd64.tar.gz in release v\${SPINLOOP_VERSION} ($SPINLOOP_URL) — the release may not publish that asset name (repository or binary renamed?)" >${BOOT_FAILED_MARKER}
    cat ${BOOT_FAILED_MARKER} >&2
    exit 1
  fi
  curl -fsSL "$SPINLOOP_URL/checksums.txt" -o /tmp/spinloop-dl/checksums.txt
  (cd /tmp/spinloop-dl && grep ' spinloop_linux_amd64.tar.gz$' checksums.txt | sha256sum -c -)
  tar -xzf /tmp/spinloop-dl/spinloop_linux_amd64.tar.gz -C /tmp/spinloop-dl
  install -m 0755 /tmp/spinloop-dl/spinloop /usr/local/bin/spinloop
  /usr/local/bin/spinloop version
  rm -rf /tmp/spinloop-dl
fi
`;
}

/** Exported for tests: the boot script is pure string-building. The seed twin is shared/seed.ts's buildSeedUserData. */
export function buildInferenceUserData(env: string, cfg: DeployConfig): string {
  const modelDir = MODEL_DIR;
  const runnerUnit = runnerSpec(cfg.runner).daemonBoot(cfg, modelDir, Number(ENGINE_PORT));
  const cwAgentConfig = cloudwatchAgentConfig(env, cfg.runner);
  // Common boot: log the GPU, add swap for the load spike, start the log
  // shipper, install spinloop (not baked into the AMI — see spinloopInstallStep),
  // sync the weights from S3, fetch the environment's API key. Then the
  // daemon takes over: its deploy config is written, its unit enabled, and
  // the engine's first start requested over the control API.
  return `#!/bin/bash
# -E so the ERR trap is inherited by functions, subshells and command
# substitutions: without it a failure inside \`$(...)\` would exit the boot
# without recording why.
set -Eeuxo pipefail

# Fail loudly. A boot that dies leaves a perfectly healthy-looking instance
# with no daemon on it, and the wake can then only report a generic timeout —
# so the reason is written where the control plane reads it (the marker) and
# where a human reads it (the boot log). The trap fires on the first failing
# command, before set -e ends the script, so it captures the command that
# actually broke rather than the last one to run. Cleared up front: a marker
# left by an earlier boot must not be reported as this one's.
rm -f ${BOOT_FAILED_MARKER}
trap 'rc=$?; msg="${BOOT_FAILED_PREFIX}: line $LINENO exited $rc: $BASH_COMMAND"; echo "$msg" >${BOOT_FAILED_MARKER}; echo "$msg" >&2' ERR

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

${spinloopInstallStep(cfg.spinloopVersion)}
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
 * The boot's recorded reason for dying, or null when it left none. Null is
 * the answer for every ambiguous case — no marker, an SSM error, an agent not
 * yet reachable — because this gates a terminal failure: only the marker's
 * own text ends a wake, and anything else lets the poll continue.
 */
async function bootFailure(instanceId: string): Promise<string | null> {
  try {
    const result = await runShellCommand(instanceId, BOOT_FAILURE_COMMAND, 10);
    if (result.status !== 'Success') {
      return null;
    }
    const text = result.stdout.trim();
    return text.includes(BOOT_FAILED_PREFIX) ? text : null;
  } catch {
    return null;
  }
}

/**
 * Whether the instance's daemon answers its control API. Every failure — SSM
 * error, an unreachable daemon, an unparseable reply — is "not yet": the
 * caller keeps polling to its deadline.
 */
async function daemonAnswers(instanceId: string): Promise<boolean> {
  try {
    const result = await runShellCommand(instanceId, DAEMON_STATUS_CMD, 10);
    return result.status === 'Success' && parseDaemonStatus(result.stdout) !== null;
  } catch {
    return false;
  }
}

/**
 * The start request's body: the deploy config the boot would have stored. The
 * same document the boot's user data writes, built by the same runner code,
 * so a start and a boot cannot name different configs.
 */
function startBody(cfg: DeployConfig): string {
  return runnerSpec(cfg.runner).daemonDeployConfigJson(cfg, MODEL_DIR, Number(ENGINE_PORT));
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
