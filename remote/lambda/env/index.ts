/**
 * Env Lambda — returns the API key and base URL for a running endpoint.
 * Does NOT start the instance: if stopped, it returns 503.
 *
 * The caller (outfit harness) uses this to inject OPENAI_API_KEY and
 * OPENAI_BASE_URL into the agent's environment, so the user never has to
 * export anything manually.
 */

import type { LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import {
  errorName,
  findManagedInstance,
  requireEnv,
  type InstanceInfo,
} from '../shared/aws';
import {
  baseUrlFor,
  ENV_TAG_KEY,
  environmentFrom,
  findEnvEip,
  readEnvApiKey,
} from '../shared/environments';
import { jsonResponse } from '../shared/http';

const TAG_KEY = requireEnv('TAG_KEY');
const TAG_VALUE = requireEnv('TAG_VALUE');
const VLLM_PORT = requireEnv('VLLM_PORT');

export async function handler(event: LambdaFunctionURLEvent): Promise<LambdaFunctionURLResult> {
  let env: string;
  try {
    env = environmentFrom(event.queryStringParameters);
  } catch (err) {
    return jsonResponse(400, { error: (err as Error).message });
  }

  // Check if the environment has a running instance.
  const instance = await findManagedInstance(TAG_KEY, TAG_VALUE, [
    { Name: `tag:${ENV_TAG_KEY}`, Values: [env] },
  ]);

  if (!instance) {
    return jsonResponse(503, {
      state: 'stopped',
      message: 'instance is not running',
    });
  }

  try {
    const eip = await findEnvEip(env);
    if (!eip) {
      return jsonResponse(500, {
        error: 'environment has no Elastic IP allocated',
      });
    }

    const apiKey = await readEnvApiKey(env);
    if (!apiKey) {
      return jsonResponse(500, {
        error: 'environment has no API key',
      });
    }

    const baseURL = baseUrlFor(eip.publicIp, VLLM_PORT);

    return jsonResponse(200, {
      base_url: baseURL,
      api_key: apiKey,
    });
  } catch (err) {
    console.log(JSON.stringify({ environment: env, error: errorName(err) }));
    return jsonResponse(500, {
      error: `failed to retrieve endpoint environment: ${errorName(err)}`,
    });
  }
}
