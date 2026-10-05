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
import { weightsPresent } from '../shared/seed';
import { buildSeedJob, launchSeedInstance, seedInfraFromEnv } from '../shared/seed/launch';

const WEIGHTS_BUCKET = requireEnv('WEIGHTS_BUCKET');
const VPC_ID = requireEnv('VPC_ID');
const PORT = Number(requireEnv('ENGINE_PORT'));

// Matches the Go client's validation: an IPv4 CIDR like 203.0.113.7/32.
const CIDR = /^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\/\d{1,2}$/;

/**
 * The control plane `spinloop cloud deploy` calls (SigV4 Function URL). POST
 * `{environment, allowedCidr, ...deployConfig}` and the environment is created
 * on the control plane if it does not exist — its Elastic IP, security group
 * (ingress = its own allowed CIDR), API-key secret and SSM state — the weights
 * are seeded if missing, and the config is written to the environment's
 * deploy-config parameter. GET `?env=<name>` returns the current config. This
 * keeps spinloop thin (Lambda invoke only) with all validation, layout decisions
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

  // A property of this request, not of what the environment serves — so it is
  // read from the raw body rather than by parseDeployConfig, and never reaches
  // the persisted deploy-config. Stored, it would re-seed on every wake that
  // read it back.
  if (parsedBody.reseed !== undefined && typeof parsedBody.reseed !== 'boolean') {
    return jsonResponse(400, {
      error: `reseed must be a boolean, got ${JSON.stringify(parsedBody.reseed)}`,
    });
  }
  const reseed = parsedBody.reseed === true;

  // An externally provided key to store as the environment's key. A property
  // of this request, not of what the environment serves — read from the raw
  // body the way reseed is, never persisted, and never logged: it lands in
  // the environment's secret, and the reply carries only what happened to it.
  if (parsedBody.apiKey !== undefined && typeof parsedBody.apiKey !== 'string') {
    return jsonResponse(400, {
      error: `apiKey must be a string, got ${JSON.stringify(parsedBody.apiKey)}`,
    });
  }
  const apiKey = typeof parsedBody.apiKey === 'string' ? parsedBody.apiKey : '';

  // Seed before anything else: if the weights are missing and the seed cannot
  // even be launched, the environment (and any current working config) is left
  // alone rather than half-created.
  //
  // A requested re-seed skips the presence check rather than overriding it —
  // there is no point paying for HEADs whose answer is ignored — and forces
  // the launch exactly as `spinloop cloud seed start --force` does, escaping
  // the idempotency token deliberately rather than being handed back the
  // attempt it is meant to replace.
  let seeding = false;
  let seedId: string | undefined;
  try {
    if (reseed || !(await weightsPresent(WEIGHTS_BUCKET, config))) {
      const infra = seedInfraFromEnv();
      // The seed id, not the instance id: the instance is an implementation
      // detail that changes if the seed is relaunched, while the id is stable
      // and is what `spinloop cloud seed status` takes.
      const launched = await launchSeedInstance(buildSeedJob(config, infra, ''), infra, {
        force: reseed,
      });
      seedId = launched.seedId;
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
  let apiKeyAction: string;
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
    // A supplied key is a rotation (created or replaced, per the secret's
    // state); without one the secret is generated only if absent and never
    // regenerated. The reply carries the action, never the value.
    apiKeyAction = await ensureEnvApiKey(env, apiKey || undefined);
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
      seedId,
      // Distinguishes a forced re-seed from one the missing weights caused,
      // which is otherwise invisible after the fact.
      reseed,
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
    ...(seedId ? { seedId } : {}),
    // Present only when the deploy supplied a key — the action taken on the
    // environment's secret, never the value.
    ...(apiKey ? { apiKeyAction } : {}),
    // A wake before the seed finishes would sync an incomplete prefix, so the
    // reply names the command that says when it is done rather than quoting an
    // estimate and leaving the operator to guess.
    ...(seeding
      ? { message: `seeding the weights — follow it with \`spinloop cloud seed status ${seedId}\`` }
      : {}),
  });
}
