import type { LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import { errorName, readDeployConfig, requireEnv, writeDeployConfig } from '../shared/aws';
import { parseDeployConfig } from '../shared/deploy-config';
import {
  baseUrlFor,
  deployConfigParam,
  ensureEnvApiKey,
  ensureEnvEip,
  ensureEnvSecurityGroup,
  environmentFrom,
  findEnvSecurityGroup,
} from '../shared/environments';
import { jsonResponse } from '../shared/http';
import { launchSeedInstance, weightsPresent, type SeedEnv } from '../shared/seed';

const WEIGHTS_BUCKET = requireEnv('WEIGHTS_BUCKET');
const VPC_ID = requireEnv('VPC_ID');
const PORT = Number(requireEnv('ENGINE_PORT'));

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

// Matches the Go client's validation: an IPv4 CIDR like 203.0.113.7/32.
const CIDR = /^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\/\d{1,2}$/;

/**
 * The control plane `outfit remote deploy` calls (SigV4 Function URL). POST
 * `{environment, allowedCidr, ...deployConfig}` and the environment is created
 * on the control plane if it does not exist — its Elastic IP, security group
 * (ingress = its own allowed CIDR), API-key secret and SSM state — the weights
 * are seeded if missing, and the config is written to the environment's
 * deploy-config parameter. GET `?env=<name>` returns the current config. This
 * keeps outfit thin (Lambda invoke only) with all validation, layout decisions
 * and AWS mutation server-side.
 */
export async function handler(event: LambdaFunctionURLEvent): Promise<LambdaFunctionURLResult> {
  const method = event.requestContext?.http?.method ?? 'POST';

  if (method === 'GET') {
    let env: string;
    try {
      env = environmentFrom(event.queryStringParameters);
    } catch (err) {
      return jsonResponse(400, { error: (err as Error).message });
    }
    try {
      return jsonResponse(200, await readDeployConfig(deployConfigParam(env)));
    } catch (err) {
      // Unset/invalid — nothing has been deployed to this environment yet.
      return jsonResponse(404, {
        state: 'unconfigured',
        environment: env,
        message: (err as Error).message,
      });
    }
  }

  const body =
    event.isBase64Encoded && event.body
      ? Buffer.from(event.body, 'base64').toString('utf8')
      : (event.body ?? '');

  let parsedBody: Record<string, unknown>;
  try {
    parsedBody = JSON.parse(body || '{}') as Record<string, unknown>;
  } catch (err) {
    return jsonResponse(400, { error: `request is not valid JSON: ${(err as Error).message}` });
  }

  let env: string;
  let config;
  try {
    env = environmentFrom(event.queryStringParameters, parsedBody.environment);
    config = parseDeployConfig(body);
  } catch (err) {
    return jsonResponse(400, { error: (err as Error).message });
  }
  const allowedCidr = typeof parsedBody.allowedCidr === 'string' ? parsedBody.allowedCidr : '';
  if (allowedCidr && !CIDR.test(allowedCidr)) {
    return jsonResponse(400, { error: `allowedCidr must be an IPv4 CIDR, got ${allowedCidr}` });
  }

  // Seed before anything else: if the weights are missing and the seed cannot
  // even be launched, the environment (and any current working config) is left
  // alone rather than half-created.
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

  // Create (or update) the environment's own resources. The CIDR is required
  // the first time — a security group that admits nobody is useless — and
  // optional afterwards, when an absent value means "leave ingress alone".
  let baseUrl: string;
  try {
    const eip = await ensureEnvEip(env);
    baseUrl = baseUrlFor(eip.publicIp, PORT);
    if (allowedCidr) {
      await ensureEnvSecurityGroup(env, VPC_ID, PORT, allowedCidr);
    } else if (!(await findEnvSecurityGroup(env))) {
      return jsonResponse(400, {
        error: `environment ${JSON.stringify(env)} has no security group yet — provide allowedCidr`,
      });
    }
    await ensureEnvApiKey(env);
  } catch (err) {
    console.log(JSON.stringify({ action: 'deploy', environment: env, error: errorName(err) }));
    return jsonResponse(502, {
      error: `creating environment ${JSON.stringify(env)}: ${(err as Error).message}`,
    });
  }

  await writeDeployConfig(deployConfigParam(env), config);
  console.log(
    JSON.stringify({
      action: 'deploy',
      environment: env,
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
    environment: env,
    base_url: baseUrl,
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
