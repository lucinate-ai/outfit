import type { LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import {
  RETAIN_UNTIL_TAG,
  findManagedInstance,
  requireEnv,
  tagInstance,
} from '../shared/aws';
import { ENV_TAG_KEY, environmentFrom } from '../shared/environments';
import { jsonResponse } from '../shared/http';

const TAG_KEY = requireEnv('TAG_KEY');
const TAG_VALUE = requireEnv('TAG_VALUE');

/**
 * UpdateFn — handles arbitrary post-provision instance commands. The
 * environment is identified by the `env` query parameter; the command by
 * `cmd`. Future commands add branches to the dispatch table below.
 */
export async function handler(
  event: LambdaFunctionURLEvent,
): Promise<LambdaFunctionURLEvent | LambdaFunctionURLResult> {
  let env: string;
  try {
    env = environmentFrom(event.queryStringParameters);
  } catch (err) {
    return jsonResponse(400, { error: (err as Error).message });
  }

  const cmd = event.queryStringParameters?.cmd;
  if (!cmd) {
    return jsonResponse(400, { error: 'missing command: pass ?cmd=<name>' });
  }

  switch (cmd) {
    case 'set-keep':
      return setKeep(env, event);
    default:
      return jsonResponse(400, {
        error: `unknown command ${JSON.stringify(cmd)}; accepted: set-keep`,
      });
  }
}

/**
 * set-keep — set (or update) the Retain-Until tag on an environment's
 * instance. The `retainUntil` query parameter must be a valid ISO-8601
 * datetime; the instance is resolved by its managed tag + environment tag.
 */
async function setKeep(
  env: string,
  event: LambdaFunctionURLEvent,
): Promise<LambdaFunctionURLResult> {
  const raw = event.queryStringParameters?.retainUntil;
  if (!raw) {
    return jsonResponse(400, { error: 'missing retainUntil: pass ?retainUntil=<iso-8601 datetime>' });
  }

  const deadline = new Date(raw);
  if (Number.isNaN(deadline.getTime())) {
    return jsonResponse(400, { error: `invalid retainUntil ${JSON.stringify(raw)}: must be ISO-8601 datetime` });
  }

  const instance = await findManagedInstance(TAG_KEY, TAG_VALUE, [
    { Name: `tag:${ENV_TAG_KEY}`, Values: [env] },
  ]);
  if (!instance) {
    return jsonResponse(404, {
      error: `no running instance for environment ${JSON.stringify(env)}`,
    });
  }

  await tagInstance(instance.instanceId, RETAIN_UNTIL_TAG, deadline.toISOString());
  console.log(
    JSON.stringify({ cmd: 'set-keep', environment: env, instanceId: instance.instanceId, retainUntil: deadline.toISOString() }),
  );
  return jsonResponse(200, {
    environment: env,
    instanceId: instance.instanceId,
    retainUntil: deadline.toISOString(),
  });
}
