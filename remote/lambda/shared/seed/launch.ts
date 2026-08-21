/**
 * Launching a seed: the boot script, the AMI, and the idempotency that makes
 * two concurrent starts converge on one instance.
 *
 * The boot script is deliberately small. Everything the old one did in shell —
 * fetching, filtering, uploading — is now the seeder program's job, so what is
 * left is: cap the instance's life, install a runtime, start the log shipper,
 * fetch the program, run it, shut down.
 */

import {
  errorName,
  getInstance,
  getParameterValue,
  requireEnv,
  runInstance,
  sleep,
} from '../aws';
import { type DeployConfig } from '../deploy-config';
import { runnerSpec } from '../../runners';
import { SEED_LOG_GROUP, seedLogStream, type SeedJob } from './contract';
import {
  AUTO_GENERATION,
  freshGeneration,
  SEED_ID_TAG_KEY,
  SEED_MODEL_TAG_KEY,
  SEED_TAG_VALUE,
  seedClientToken,
  seedIdFor,
} from './identity';

/**
 * AWS's public parameter for the current Amazon Linux 2023 arm64 image. Using
 * the stock image is what removes seeding's dependency on a bake: there is no
 * "no baked AMI found — run `pnpm bake` first" failure mode any more, and the
 * seed no longer borrows an inference runner's GPU image to get at its tooling.
 */
export const AL2023_AMI_PARAMETER =
  '/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-arm64';

/**
 * The Node package pinned on the instance. Verified present in the AL2023 core
 * repository at 24.11.0; the *unversioned* `nodejs` package there is 18.12.1,
 * which is exactly why this is pinned rather than left to the distribution.
 */
export const NODE_PACKAGE = 'nodejs24';

/** Where the seeder appends records for the CloudWatch agent to tail. */
export const RECORD_PATH = '/var/log/seed/records.jsonl';

export interface SeedInfraEnv {
  region: string;
  bucket: string;
  instanceType: string;
  subnetId: string;
  securityGroupId: string;
  instanceProfileArn: string;
  /** Empty when no Hugging Face token is configured (public repos only). */
  hfSecretArn: string;
  /** S3 location of the esbuild'd seeder bundle, published as a CDK asset. */
  seederBucket: string;
  seederKey: string;
  maxSeedMinutes: number;
  partSizeBytes: number;
  partConcurrency: number;
  partAttempts: number;
}

export function seedInfraFromEnv(): SeedInfraEnv {
  return {
    region: requireEnv('AWS_REGION'),
    bucket: requireEnv('WEIGHTS_BUCKET'),
    instanceType: requireEnv('SEED_INSTANCE_TYPE'),
    subnetId: requireEnv('SEED_SUBNET_ID'),
    securityGroupId: requireEnv('SEED_SECURITY_GROUP_ID'),
    instanceProfileArn: requireEnv('SEED_INSTANCE_PROFILE_ARN'),
    hfSecretArn: process.env.HF_TOKEN_SECRET_ARN ?? '',
    seederBucket: requireEnv('SEEDER_BUCKET'),
    seederKey: requireEnv('SEEDER_KEY'),
    maxSeedMinutes: Number(requireEnv('MAX_SEED_MINUTES')),
    partSizeBytes: Number(process.env.SEED_PART_SIZE_BYTES ?? 64 * 1024 * 1024),
    partConcurrency: Number(process.env.SEED_PART_CONCURRENCY ?? 8),
    partAttempts: Number(process.env.SEED_PART_ATTEMPTS ?? 4),
  };
}

/** The job spec the instance runs. Carries no secret — the token is an ARN. */
export function buildSeedJob(
  cfg: DeployConfig,
  env: SeedInfraEnv,
  revision: string,
): SeedJob {
  return {
    seedId: seedIdFor(cfg.runner, cfg.modelId, cfg.quant),
    runner: cfg.runner,
    modelId: cfg.modelId,
    quant: cfg.quant,
    revision,
    bucket: env.bucket,
    prefix: cfg.weightsPrefix,
    selection: runnerSpec(cfg.runner).seedSelection(cfg),
    hfSecretArn: env.hfSecretArn,
    region: env.region,
    recordPath: RECORD_PATH,
    partSizeBytes: env.partSizeBytes,
    partConcurrency: env.partConcurrency,
    partAttempts: env.partAttempts,
  };
}

/**
 * The CloudWatch agent's configuration. The stream carries the seed id and the
 * instance id, so one seed's records are addressable without scanning others'
 * and a re-seed never interleaves with the attempt before it — `{instance_id}`
 * is substituted by the agent.
 */
function agentConfig(job: SeedJob, region: string): string {
  return JSON.stringify(
    {
      agent: { region, run_as_user: 'root' },
      logs: {
        logs_collected: {
          files: {
            collect_list: [
              {
                file_path: job.recordPath,
                log_group_name: SEED_LOG_GROUP,
                log_stream_name: seedLogStream(job.seedId, '{instance_id}'),
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
 * The boot script.
 *
 * Note what is NOT here: `set -euxo pipefail`. `set -e` is what breaks
 * termination in the script this replaces — an aborted script never reaches its
 * closing `shutdown`, so a failed seed runs until someone notices. The EXIT trap
 * does that job instead, and it fires on every path. `set -x` is separately why
 * the old script traced the Hugging Face token into the boot log; the seeder
 * reads the secret itself, so no token ever enters a shell variable here.
 */
export function buildSeedUserData(job: SeedJob, env: SeedInfraEnv): string {
  return `#!/bin/bash
# Layer 2 of three: an absolute cap, armed before anything that could hang.
# Layer 1 is the trap below, layer 3 the control plane's periodic sweep.
shutdown -h +${env.maxSeedMinutes}

# The sleep gives the CloudWatch agent time to flush the terminal record before
# the box goes away; without it the seed's last word can be lost.
trap 'sleep 10; shutdown -h now' EXIT

set -o pipefail
exec > >(tee -a /var/log/seed-boot.log) 2>&1

mkdir -p "$(dirname '${job.recordPath}')"

# Pinned deliberately: the unversioned nodejs package on AL2023 is 18.
dnf install -y ${NODE_PACKAGE} amazon-cloudwatch-agent || {
  echo "SEED_BOOT_FAILED: could not install ${NODE_PACKAGE}/amazon-cloudwatch-agent"
  exit 1
}

cat >/opt/seed-agent.json <<'CWCONFIG'
${agentConfig(job, env.region)}
CWCONFIG

# A seed that transfers correctly but reports nothing is worse than one that
# fails fast: it would be reaped as stalled and diagnosed as a mystery.
/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -s \\
  -c file:/opt/seed-agent.json || {
  echo "SEED_BOOT_FAILED: the CloudWatch agent would not start"
  exit 1
}

aws s3 cp 's3://${env.seederBucket}/${env.seederKey}' /opt/seed.mjs --region '${env.region}' || {
  echo "SEED_BOOT_FAILED: could not fetch the seeder bundle"
  exit 1
}

cat >/opt/seed-job.json <<'SEEDJOB'
${JSON.stringify(job, null, 2)}
SEEDJOB

node /opt/seed.mjs /opt/seed-job.json
`;
}

export interface LaunchedSeed {
  seedId: string;
  instanceId: string;
  /** False when an already-running instance was joined rather than started. */
  started: boolean;
}

/** Instance states that mean an idempotency hit handed back a dead instance. */
const DEAD_STATES = new Set(['shutting-down', 'terminated', 'stopping', 'stopped', 'stale']);

/**
 * How many times to recheck a just-launched instance's visibility before
 * concluding the id is stale rather than merely new. A genuinely fresh
 * instance is normally visible on the first check; this budget is for the
 * rare eventual-consistency lag, not the common case.
 */
const VISIBILITY_ATTEMPTS = 4;
const VISIBILITY_DELAY_MS = 1500;

/**
 * Launch a seed instance, converging concurrent starts onto one.
 *
 * The deterministic ClientToken is what makes that work: EC2 treats a repeated
 * token within its idempotency window as the same call and returns the same
 * instance, so two Lambdas racing here produce one instance without a lock.
 *
 * The same window is also a hazard — a seed retried well after an earlier
 * attempt terminated would be handed that dead instance back. Rather than
 * trying to predict that, the state of whatever comes back is checked, and a
 * dead one is escaped with a fresh generation. Detection is reliable;
 * prediction is not.
 */
export async function launchSeedInstance(
  job: SeedJob,
  env: SeedInfraEnv,
  options: { force?: boolean } = {},
): Promise<LaunchedSeed> {
  const imageId = await getParameterValue(AL2023_AMI_PARAMETER);
  const userData = buildSeedUserData(job, env);
  const tags = {
    Name: `cloud-vm-llm-seed-${job.seedId}`.slice(0, 255),
    'cloud-vm-llm': SEED_TAG_VALUE,
    [SEED_ID_TAG_KEY]: job.seedId,
    [SEED_MODEL_TAG_KEY]: job.modelId.slice(0, 255),
  };

  const attempt = async (generation: string): Promise<{ instanceId: string; state: string }> => {
    let instanceId: string;
    try {
      instanceId = await runInstance({
        imageId,
        instanceType: env.instanceType,
        subnetId: env.subnetId,
        securityGroupId: env.securityGroupId,
        instanceProfileArn: env.instanceProfileArn,
        userData,
        tags,
        terminateOnShutdown: true,
        clientToken: seedClientToken(job.seedId, generation),
      });
    } catch (err) {
      // The fixed AUTO_GENERATION token can also collide with an earlier
      // request for the same seed whose *arguments* have since changed — a
      // boot script updated by a later deploy, most commonly — which EC2
      // refuses rather than silently returning either request's instance.
      // That refusal is itself proof the old request is unrelated to this
      // one, so it is escaped exactly like a stale instance rather than
      // failing the seed.
      if (errorName(err) === 'IdempotentParameterMismatch') {
        return { instanceId: '', state: 'stale' };
      }
      throw err;
    }
    // DescribeInstances is eventually consistent right after RunInstances, so
    // a brand-new instance may not be visible on the first check — but an
    // idempotency hit against the fixed AUTO_GENERATION token can also return
    // an instance id from a much earlier session whose record has since aged
    // out of DescribeInstances entirely, and that looks identical: NotFound.
    // A short retry absorbs genuine lag; still-not-found after it means the
    // id is stale, not new, so it is escaped exactly like a live-but-dead one.
    for (let i = 0; i < VISIBILITY_ATTEMPTS; i++) {
      try {
        return { instanceId, state: (await getInstance(instanceId)).state };
      } catch {
        if (i < VISIBILITY_ATTEMPTS - 1) {
          await sleep(VISIBILITY_DELAY_MS);
        }
      }
    }
    return { instanceId, state: 'stale' };
  };

  // A forced re-seed skips the shared token entirely — it must never be
  // deduplicated onto the attempt it is deliberately replacing.
  const first = await attempt(options.force ? freshGeneration() : AUTO_GENERATION);
  if (!DEAD_STATES.has(first.state)) {
    return { seedId: job.seedId, instanceId: first.instanceId, started: true };
  }

  const retry = await attempt(freshGeneration());
  return { seedId: job.seedId, instanceId: retry.instanceId, started: true };
}
