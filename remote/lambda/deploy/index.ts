import type { LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import { errorName, readDeployConfig, requireEnv, writeDeployConfig } from '../shared/aws';
import { parseDeployConfig } from '../shared/deploy-config';
import { jsonResponse } from '../shared/http';
import { launchSeedInstance, weightsPresent, type SeedEnv } from '../shared/seed';

const DEPLOY_CONFIG_PARAM = requireEnv('DEPLOY_CONFIG_PARAM');
const WEIGHTS_BUCKET = requireEnv('WEIGHTS_BUCKET');

const SEED_ENV: SeedEnv = {
  region: requireEnv('AWS_REGION'),
  bucket: WEIGHTS_BUCKET,
  instanceType: requireEnv('SEED_INSTANCE_TYPE'),
  subnetId: requireEnv('SEED_SUBNET_ID'),
  securityGroupId: requireEnv('SEED_SECURITY_GROUP_ID'),
  instanceProfileArn: requireEnv('SEED_INSTANCE_PROFILE_ARN'),
  hfSecretArn: process.env.HF_TOKEN_SECRET_ARN ?? '',
  amiRoleTagKey: requireEnv('AMI_ROLE_TAG_KEY'),
  amiRoleTagValue: requireEnv('AMI_ROLE_TAG_VALUE'),
  amiRunnerTagKey: requireEnv('AMI_RUNNER_TAG_KEY'),
};

/**
 * The control plane `outfit remote deploy` calls (SigV4 Function URL, like
 * start/stop). POST a DeployConfig — derived from an Outfit file — and it is
 * validated, the weights are seeded if they are not in S3 yet, and the config
 * is written to the deploy-config SSM parameter that the next wake reads. GET
 * returns the current config. This keeps outfit thin (Lambda invoke only) with
 * all validation, layout decisions and AWS mutation server-side.
 */
export async function handler(event: LambdaFunctionURLEvent): Promise<LambdaFunctionURLResult> {
  const method = event.requestContext?.http?.method ?? 'POST';

  if (method === 'GET') {
    try {
      return jsonResponse(200, await readDeployConfig(DEPLOY_CONFIG_PARAM));
    } catch (err) {
      // Unset/invalid — nothing has been deployed yet.
      return jsonResponse(404, { state: 'unconfigured', message: (err as Error).message });
    }
  }

  const body =
    event.isBase64Encoded && event.body
      ? Buffer.from(event.body, 'base64').toString('utf8')
      : (event.body ?? '');

  let config;
  try {
    config = parseDeployConfig(body);
  } catch (err) {
    return jsonResponse(400, { error: (err as Error).message });
  }

  // Seed before writing the config: if the weights are missing and the seed
  // cannot even be launched, the current (working) config is left alone rather
  // than replaced by one that would fail at wake.
  let seeding = false;
  let seedInstanceId: string | undefined;
  try {
    if (!(await weightsPresent(WEIGHTS_BUCKET, config))) {
      seedInstanceId = await launchSeedInstance(config, SEED_ENV);
      seeding = true;
    }
  } catch (err) {
    console.log(JSON.stringify({ action: 'deploy', error: `seed failed: ${errorName(err)}` }));
    return jsonResponse(502, {
      error: `weights are not in S3 and the seed could not be started: ${(err as Error).message}`,
    });
  }

  await writeDeployConfig(DEPLOY_CONFIG_PARAM, config);
  console.log(
    JSON.stringify({
      action: 'deploy',
      runner: config.runner,
      modelId: config.modelId,
      quant: config.quant,
      weightsPrefix: config.weightsPrefix,
      seeding,
      seedInstanceId,
    }),
  );
  return jsonResponse(200, {
    deployed: true,
    runner: config.runner,
    modelId: config.modelId,
    contextSize: config.contextSize,
    weightsPrefix: config.weightsPrefix,
    seeding,
    ...(seedInstanceId ? { seedInstanceId } : {}),
    // A wake before the seed finishes would sync an incomplete prefix.
    ...(seeding
      ? { message: 'seeding the weights (~15-20 min); wait for it to finish before starting' }
      : {}),
  });
}
