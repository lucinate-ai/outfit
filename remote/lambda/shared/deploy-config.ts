/**
 * The deploy-config contract — the runner-neutral description of *what* to
 * serve, kept separate from the *infra* (bucket, secret, tags) that the CDK
 * stack owns. It lives in an SSM parameter that the start Lambda reads at each
 * wake, so switching model/runner/context is a parameter write, not a redeploy.
 *
 * CDK writes the initial value from config; `outfit remote deploy` overwrites
 * it later. There is deliberately NO default runner — one of the two must be
 * chosen, and an absent/invalid config fails the wake loudly rather than
 * silently picking one.
 */

export const RUNNERS = ['vllm', 'llamacpp'] as const;
export type Runner = (typeof RUNNERS)[number];

/**
 * The placeholder CDK creates the deploy-config parameter with. It is a
 * constant, so a later `cdk deploy` never reasserts (clobbers) a real config
 * that `outfit remote deploy` or a manual edit wrote — the parameter is
 * outfit/manual-owned. A wake reading this fails loudly; `pnpm deploy` seeds a
 * real config over it, but only while it is still this placeholder.
 */
export const UNCONFIGURED_DEPLOY_CONFIG = 'unconfigured';

export function isRunner(value: unknown): value is Runner {
  return typeof value === 'string' && (RUNNERS as readonly string[]).includes(value);
}

/**
 * The environment variable carrying one runner's CloudWatch log-group name —
 * the naming convention the CDK stack writes and the start Lambda reads, so
 * both sides follow `RUNNERS` with no per-runner wiring.
 */
export function logGroupEnvVar(runner: Runner): string {
  return `${runner.toUpperCase()}_LOG_GROUP`;
}

/**
 * Where a model's weights live under the weights bucket. Derived here rather
 * than sent on the wire so callers (outfit) never need to know the S3 layout —
 * runner + modelId + quant fully determine it, which also means the same model
 * always resolves to the same prefix.
 */
export function weightsPrefixFor(runner: Runner, modelId: string, quant: string): string {
  const parts = ['models', runner, modelId];
  if (quant) {
    parts.push(quant);
  }
  return `${parts.join('/')}/`;
}

export interface DeployConfig {
  /** Which inference server runs on the instance. Required; no default. */
  runner: Runner;
  /** Hugging Face repo id the weights came from (a GGUF repo for llamacpp). */
  modelId: string;
  /** GGUF quant tag for llamacpp (e.g. "UD-Q6_K_XL"); empty for vllm. */
  quant: string;
  /**
   * S3 key prefix the instance syncs from. Always derived by
   * `weightsPrefixFor` — never taken from the request body.
   */
  weightsPrefix: string;
  /** Context window in tokens — vLLM's --max-model-len / llama.cpp's --ctx-size. */
  contextSize: number;
  /** The model name the API reports and clients request. */
  servedModelName: string;
  /** Runner-specific extra flags appended to the serve command, pre-tokenised. */
  serveArgs: string[];
}

/**
 * Parse and validate a deploy-config JSON blob. Throws with a clear message on
 * anything malformed — the start Lambda surfaces that rather than guessing.
 */
export function parseDeployConfig(raw: string | undefined): DeployConfig {
  if (!raw || !raw.trim() || raw.trim() === UNCONFIGURED_DEPLOY_CONFIG) {
    throw new Error(
      'deploy-config is not set — run `pnpm deploy` (seeds the initial config) or `outfit remote deploy`',
    );
  }
  let obj: Record<string, unknown>;
  try {
    obj = JSON.parse(raw) as Record<string, unknown>;
  } catch (err) {
    throw new Error(`deploy-config is not valid JSON: ${(err as Error).message}`);
  }
  if (!isRunner(obj.runner)) {
    throw new Error(
      `deploy-config.runner must be one of ${RUNNERS.join('/')}, got ${JSON.stringify(obj.runner)}`,
    );
  }
  const modelId = requireString(obj, 'modelId');
  const servedModelName = requireString(obj, 'servedModelName');
  const contextSize = Number(obj.contextSize);
  if (!Number.isInteger(contextSize) || contextSize <= 0) {
    throw new Error(`deploy-config.contextSize must be a positive integer, got ${obj.contextSize}`);
  }
  const serveArgs = obj.serveArgs ?? [];
  if (!Array.isArray(serveArgs) || serveArgs.some((a) => typeof a !== 'string')) {
    throw new Error('deploy-config.serveArgs must be an array of strings');
  }
  const quant = typeof obj.quant === 'string' ? obj.quant : '';
  return {
    runner: obj.runner,
    modelId,
    quant,
    // Derived, so any weightsPrefix in the request body is ignored.
    weightsPrefix: weightsPrefixFor(obj.runner, modelId, quant),
    contextSize,
    servedModelName,
    serveArgs: serveArgs as string[],
  };
}

function requireString(obj: Record<string, unknown>, key: string): string {
  const value = obj[key];
  if (typeof value !== 'string' || value === '') {
    throw new Error(`deploy-config.${key} must be a non-empty string`);
  }
  return value;
}
