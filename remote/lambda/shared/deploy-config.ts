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
 * The companion weights a deployment may name beside its main weights: further
 * files from the *same* Hugging Face repo that the engine loads in addition to
 * them. The role, not the filename, says what the engine does with the file —
 * so the runtime never guesses from what the publisher happened to call it.
 *
 * A closed set, validated when the config is accepted: an unknown role is a
 * deploy-time error rather than an instance that silently starts without the
 * flag it was meant to carry.
 */
export const COMPANION_ROLES = ['draft', 'mmproj'] as const;
export type CompanionRole = (typeof COMPANION_ROLES)[number];

export function isCompanionRole(value: unknown): value is CompanionRole {
  return typeof value === 'string' && (COMPANION_ROLES as readonly string[]).includes(value);
}

/**
 * The fixed on-disk name a companion is normalised to, by role. The seed
 * renames to this and the runner spec points its flag at it, so neither has to
 * know the source filename and the two cannot disagree.
 */
export function companionFileName(role: CompanionRole): string {
  return `${role}.gguf`;
}

/**
 * What a companion filename may contain. Deliberately narrow: the value is
 * interpolated into generated shell, so the charset is the guard rather than
 * multi-layer quoting.
 */
export const COMPANION_FILENAME = /^[A-Za-z0-9._-]+$/;

/**
 * The placeholder CDK creates the deploy-config parameter with. It is a
 * constant, so a later `cdk deploy` never reasserts (clobbers) a real config
 * that `outfit remote deploy` or a manual edit wrote — the parameter is
 * outfit/manual-owned. A wake reading this fails loudly; `pnpm run deploy` seeds a
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
  /**
   * Optional companion weights, keyed by role. Each value is a *filename
   * within the model's own repo* (never a path) — a companion ships beside the
   * weights, so naming one adds no new source, credential or trust boundary.
   * Absent or `{}` reproduces the pre-companion behaviour exactly.
   */
  companions: Partial<Record<CompanionRole, string>>;
}

/**
 * Parse and validate a deploy-config JSON blob. Throws with a clear message on
 * anything malformed — the start Lambda surfaces that rather than guessing.
 */
export function parseDeployConfig(raw: string | undefined): DeployConfig {
  if (!raw || !raw.trim() || raw.trim() === UNCONFIGURED_DEPLOY_CONFIG) {
    throw new Error(
      'deploy-config is not set — run `pnpm run deploy` (seeds the initial config) or `outfit remote deploy`',
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
    companions: parseCompanions(obj.companions),
  };
}

/**
 * Validate the optional companions map. Absent is `{}` — the pre-companion
 * shape — so configs stored before companions existed parse unchanged.
 *
 * A value is a filename *in the model's repo*, so a path separator is rejected
 * rather than quietly accepted: it would either escape the download directory
 * or name a file the repo does not have, and both fail 20 minutes later inside
 * a seed instance where nobody is watching.
 */
function parseCompanions(raw: unknown): Partial<Record<CompanionRole, string>> {
  if (raw === undefined || raw === null) {
    return {};
  }
  if (typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error('deploy-config.companions must be an object keyed by companion role');
  }
  const companions: Partial<Record<CompanionRole, string>> = {};
  for (const [role, value] of Object.entries(raw as Record<string, unknown>)) {
    if (!isCompanionRole(role)) {
      throw new Error(
        `deploy-config.companions has unknown role ${JSON.stringify(role)}; supported roles are ${COMPANION_ROLES.join('/')}`,
      );
    }
    if (typeof value !== 'string' || value === '') {
      throw new Error(`deploy-config.companions.${role} must be a non-empty filename`);
    }
    if (value.includes('/') || value.includes('\\')) {
      throw new Error(
        `deploy-config.companions.${role} must be a filename within the model repo, not a path (got ${JSON.stringify(value)})`,
      );
    }
    // The name is interpolated into the seed's shell script and into a Python
    // literal inside it. Rather than quote correctly through both layers, the
    // charset is restricted to what real GGUF filenames use — so a name that
    // could break out of either is rejected here, where the error is visible,
    // instead of inside a seed instance nobody is watching.
    if (!COMPANION_FILENAME.test(value)) {
      throw new Error(
        `deploy-config.companions.${role} must match ${COMPANION_FILENAME} (got ${JSON.stringify(value)})`,
      );
    }
    companions[role] = value;
  }
  return companions;
}

function requireString(obj: Record<string, unknown>, key: string): string {
  const value = obj[key];
  if (typeof value !== 'string' || value === '') {
    throw new Error(`deploy-config.${key} must be a non-empty string`);
  }
  return value;
}
