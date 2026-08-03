/**
 * Env Lambda — returns the API key and base URL for an environment.
 * Does NOT start the instance: the API key lives in Secrets Manager and the
 * EIP is allocated at deploy, so both are available regardless of instance
 * state.
 *
 * The caller (outfit harness) uses this to inject OPENAI_API_KEY and
 * OPENAI_BASE_URL into the agent's environment, so the user never has to
 * export anything manually.
 */

import type { LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import {
  errorName,
  requireEnv,
} from '../shared/aws';
import {
  baseUrlFor,
  environmentFrom,
  findEnvEip,
  readEnvApiKey,
} from '../shared/environments';
import { jsonResponse } from '../shared/http';

const VLLM_PORT = requireEnv('VLLM_PORT');

export async function handler(event: LambdaFunctionURLEvent): Promise<LambdaFunctionURLResult> {
  let env: string;
  try {
    env = environmentFrom(event.queryStringParameters);
  } catch (err) {
    return jsonResponse(400, { error: (err as Error).message });
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
