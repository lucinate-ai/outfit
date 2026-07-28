/**
 * Seeding the weights into S3. The deploy Lambda calls this when a config names
 * a model whose weights are not in the bucket yet: a disposable instance
 * downloads them from Hugging Face, syncs them to S3, then terminates itself.
 *
 * The seed always runs on the vLLM AMI regardless of the target runner — that
 * is the image carrying a Python venv with huggingface_hub, and the job only
 * needs that plus the AWS CLI.
 */

import { HeadObjectCommand, S3Client } from '@aws-sdk/client-s3';
import { errorName, findLatestAmi, runInstance } from './aws';
import type { DeployConfig, Runner } from './deploy-config';

const s3 = new S3Client({});

/**
 * The file whose presence means "these weights are complete". A bare
 * list-under-the-prefix would also match the debris of a failed or in-flight
 * seed (Hugging Face writes .cache/**\/*.lock files first), so a per-runner
 * sentinel is checked instead — it is written last, by the S3 sync.
 */
function sentinelKey(runner: Runner, weightsPrefix: string): string {
  return runner === 'llamacpp' ? `${weightsPrefix}model.gguf` : `${weightsPrefix}config.json`;
}

/** Whether the weights for this config are already seeded. */
export async function weightsPresent(bucket: string, cfg: DeployConfig): Promise<boolean> {
  const Key = sentinelKey(cfg.runner, cfg.weightsPrefix);
  try {
    await s3.send(new HeadObjectCommand({ Bucket: bucket, Key }));
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

  // llamacpp wants one GGUF (MTP is embedded in it), normalised to model.gguf
  // so the runtime need not guess the filename; mmproj/projector files are
  // excluded. vLLM wants the whole safetensors checkpoint.
  const download =
    cfg.runner === 'llamacpp'
      ? `export QUANT='${cfg.quant}'
mkdir -p /tmp/dl
/opt/llm/venv/bin/python -c "import os; from huggingface_hub import snapshot_download; snapshot_download(os.environ['MODEL_ID'], allow_patterns=['*'+os.environ['QUANT']+'*'], local_dir='/tmp/dl', token=(os.environ.get('HF_TOKEN') or None))"
mapfile -t GGUFS < <(find /tmp/dl -type f -name '*.gguf' ! -iname '*mmproj*' | sort)
test "\${#GGUFS[@]}" -ge 1
cp "\${GGUFS[0]}" /opt/llm/model/model.gguf
[ "\${#GGUFS[@]}" -gt 1 ] && echo "WARNING: \${#GGUFS[@]} gguf files for $QUANT; used the first (split quant not handled)" >&2 || true
`
      : `/opt/llm/venv/bin/python -c "import os; from huggingface_hub import snapshot_download; snapshot_download(os.environ['MODEL_ID'], local_dir='/opt/llm/model', token=(os.environ.get('HF_TOKEN') or None))"
`;

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
  // Prefer the vLLM AMI (it has the venv); fall back to any runtime AMI for
  // images baked before the runner tag existed.
  const imageId =
    (await findLatestAmi([roleFilter, { Name: `tag:${env.amiRunnerTagKey}`, Values: ['vllm'] }])) ??
    (await findLatestAmi([roleFilter]));
  if (!imageId) {
    throw new Error('no baked runtime AMI found — run `pnpm bake vllm` first');
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
