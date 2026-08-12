/**
 * Seeding the weights into S3. The deploy Lambda calls this when a config names
 * a model whose weights are not in the bucket yet: a disposable instance
 * downloads them from Hugging Face, syncs them to S3, then terminates itself.
 *
 * The seed runs on the AMI of the first runner whose spec has seedTooling —
 * the image carrying a Python venv with huggingface_hub — regardless of the
 * target runner; the job only needs that plus the AWS CLI.
 */

import { HeadObjectCommand, S3Client } from '@aws-sdk/client-s3';
import { runnerSpec } from '../runners';
import { errorName, findLatestAmi, runInstance } from './aws';
import { type DeployConfig, RUNNERS } from './deploy-config';

const s3 = new S3Client({});

/**
 * Whether the weights for this config are already seeded: every key the runner
 * expects — its sentinel plus any named companions — must be there. Missing
 * any one means absent, so adding a companion to an already-seeded model
 * re-seeds rather than starting an instance without it.
 */
export async function weightsPresent(bucket: string, cfg: DeployConfig): Promise<boolean> {
  const keys = runnerSpec(cfg.runner).weightsKeys(cfg, cfg.weightsPrefix);
  const found = await Promise.all(keys.map((Key) => objectExists(bucket, Key)));
  return found.every(Boolean);
}

async function objectExists(Bucket: string, Key: string): Promise<boolean> {
  try {
    await s3.send(new HeadObjectCommand({ Bucket, Key }));
    return true;
  } catch (err) {
    const name = errorName(err);
    if (name === 'NotFound' || name === 'NoSuchKey' || name === '404') {
      return false;
    }
    throw err;
  }
}

export interface SeedEnv {
  region: string;
  bucket: string;
  instanceType: string;
  subnetId: string;
  securityGroupId: string;
  instanceProfileArn: string;
  /** Empty when no Hugging Face token is configured (public repos only). */
  hfSecretArn: string;
  amiRoleTagKey: string;
  amiRoleTagValue: string;
  amiRunnerTagKey: string;
}

/**
 * The seed instance's user-data. Kept pure so it can be unit tested: shell
 * quoting bugs here surface as a silent seed failure 20 minutes later.
 */
export function buildSeedUserData(cfg: DeployConfig, env: SeedEnv): string {
  const header = `#!/bin/bash
set -euxo pipefail
HF_TOKEN=""
${
  env.hfSecretArn
    ? `HF_TOKEN=$(aws secretsmanager get-secret-value --secret-id '${env.hfSecretArn}' --region '${env.region}' --query SecretString --output text)`
    : ''
}
export HF_TOKEN
export MODEL_ID='${cfg.modelId}'
mkdir -p /opt/llm/model
`;

  // How the weights are fetched is the runner's business — one GGUF vs a
  // whole checkpoint — so the download fragment comes from its spec.
  const download = runnerSpec(cfg.runner).seedDownload(cfg);

  // The sync is last, so the sentinel only appears once the download succeeded
  // (set -e aborts before it otherwise) and the box shuts down either way.
  return `${header}${download}aws s3 sync /opt/llm/model/ 's3://${env.bucket}/${cfg.weightsPrefix}' --region '${env.region}'
shutdown -h now
`;
}

/**
 * Launch the seed instance. Returns its id, or throws if no AMI is baked yet.
 */
export async function launchSeedInstance(cfg: DeployConfig, env: SeedEnv): Promise<string> {
  const roleFilter = { Name: `tag:${env.amiRoleTagKey}`, Values: [env.amiRoleTagValue] };
  // Prefer an AMI whose runner carries the seed tooling (the Python venv);
  // fall back to any runtime AMI for images baked before the runner tag
  // existed.
  const seedRunner = RUNNERS.find((r) => runnerSpec(r).seedTooling);
  const imageId =
    (seedRunner
      ? await findLatestAmi([roleFilter, { Name: `tag:${env.amiRunnerTagKey}`, Values: [seedRunner] }])
      : null) ?? (await findLatestAmi([roleFilter]));
  if (!imageId) {
    throw new Error(`no baked runtime AMI found — run \`pnpm bake ${seedRunner ?? RUNNERS[0]}\` first`);
  }
  return runInstance({
    imageId,
    instanceType: env.instanceType,
    subnetId: env.subnetId,
    securityGroupId: env.securityGroupId,
    instanceProfileArn: env.instanceProfileArn,
    userData: buildSeedUserData(cfg, env),
    tags: { Name: 'cloud-vm-llm-seed' },
    terminateOnShutdown: true,
  });
}
