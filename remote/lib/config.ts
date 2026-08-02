import * as fs from 'fs';
import * as path from 'path';
import type { App } from 'aws-cdk-lib';

// Tag the image stack puts on every baked AMI, and the runtime start Lambda
// filters on to find the newest one to launch. The AMI is model-agnostic
// (just the driver + the runner), so there is no model tag — the model comes
// from S3 at boot.
export const AMI_ROLE_TAG_KEY = 'cloud-vm-llm:role';
export const AMI_ROLE_TAG_VALUE = 'runtime-ami';
// The runner an AMI was baked for. Each runner has its own recipe/pipeline and
// its own AMI lineage, so the start Lambda (and seed) filter by both tags.
export const AMI_RUNNER_TAG_KEY = 'cloud-vm-llm:runner';

/**
 * Configuration of the SHARED layer only. Everything per-environment — the
 * model, the runner, the context size, the serve args, the allowed ingress
 * CIDR — arrives later via `outfit remote deploy`, which creates environments
 * on top of this stack; none of it is stack configuration any more.
 */
export interface LlmConfig {
  region: string;
  /** Optional Hugging Face token, used only for the seeding of gated repos. */
  hfToken: string;
  /** Instance type every environment's runtime instance launches as. */
  instanceType: string;
  /** Pinned vLLM version installed into the AMI's venv (uv pip install vllm==...). */
  vllmVersion: string;
  /**
   * Pinned ai-dock/llama.cpp-cuda release tag (e.g. "b10107") baked into the
   * llamacpp AMI — a prebuilt CUDA `llama-server` (CUDA 12.8, amd64). ai-dock
   * tracks upstream llama.cpp; pick a build new enough for MTP (PR #22673).
   */
  llamacppRelease: string;
  /**
   * NVIDIA driver package installed in the AMI. The host needs only the driver
   * — vLLM's torch wheels bring CUDA — so this is the "-server-open" headless
   * driver (open kernel modules, required for Ada/L40S), not the CUDA toolkit.
   */
  nvidiaDriverPackage: string;
  /** Terminate an environment's instance after this many minutes without requests. */
  idleThresholdMinutes: number;
  /** Never idle-terminate within this many minutes of launch (model load time). */
  gracePeriodMinutes: number;
  /**
   * Hard cap on a running session: terminate this many minutes after launch,
   * even if requests are still flowing.
   */
  maxRuntimeMinutes: number;
  vllmPort: number;
  /**
   * AZs the start Lambda tries, in order, when launching an instance — the
   * g6e-capable zones. It launches into the first with capacity, so this
   * replaces a single hard AZ pin with automatic per-AZ fallback.
   */
  availabilityZones: string[];
  /**
   * Instance type EC2 Image Builder uses to bake the AMI. The bake installs
   * the driver and the runner but never runs the GPU, so a cheap non-GPU
   * type is fine.
   */
  builderInstanceType: string;
  /** Root volume size (GB) of the baked AMI — fits the OS + driver + runner. */
  imageVolumeGb: number;
}

const DEFAULTS = {
  region: 'us-east-1',
  hfToken: '',
  instanceType: 'g6e.xlarge',
  // Must be new enough for the models' architectures — vLLM 0.11 predates
  // Qwen3.6 (Qwen3_5ForConditionalGeneration) and rejects it at load. Pin a
  // version that lists the target architecture as supported.
  vllmVersion: '0.26.0',
  // ai-dock/llama.cpp-cuda release with CUDA 12.8; pin a specific build for
  // reproducible bakes. Must post-date the MTP merge (PR #22673).
  llamacppRelease: 'b10107',
  nvidiaDriverPackage: 'nvidia-driver-570-server-open',
  idleThresholdMinutes: 15,
  // Must exceed the whole cold start (S3 sync ~4 min + weight/CUDA load), or
  // the idle check terminates the instance mid-load (the metrics scrape fails
  // while the server is still loading, which reads as "idle").
  gracePeriodMinutes: 30,
  maxRuntimeMinutes: 240,
  vllmPort: 8000,
  availabilityZones: ['us-east-1b', 'us-east-1c', 'us-east-1d', 'us-east-1e'],
  builderInstanceType: 'm5.xlarge',
  // Big enough for the OS + driver + runner AND the ~30 GB model synced from
  // S3 at boot. The snapshot only copies used blocks (~20 GB), so this does
  // not slow the bake.
  imageVolumeGb: 80,
} as const;

function contextString(app: App, key: string, fallback: string): string {
  const value = app.node.tryGetContext(key);
  return value === undefined ? fallback : String(value);
}

function contextList(app: App, key: string, fallback: readonly string[]): string[] {
  const value = app.node.tryGetContext(key);
  if (value === undefined) {
    return [...fallback];
  }
  const items = String(value)
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean);
  if (items.length === 0) {
    throw new Error(`Context value "${key}" must be a non-empty comma-separated list`);
  }
  return items;
}

function contextNumber(app: App, key: string, fallback: number): number {
  const value = app.node.tryGetContext(key);
  if (value === undefined) {
    return fallback;
  }
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    throw new Error(`Context value "${key}" must be a positive number, got: ${value}`);
  }
  return parsed;
}

/**
 * Minimal .env parser (KEY=value lines, # comments) — enough to avoid a
 * dotenv dependency. Recognised key: HF_TOKEN.
 */
function loadDotEnv(dotEnvPath: string): Record<string, string> {
  if (!fs.existsSync(dotEnvPath)) {
    return {};
  }
  const values: Record<string, string> = {};
  for (const line of fs.readFileSync(dotEnvPath, 'utf8').split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) {
      continue;
    }
    const eq = trimmed.indexOf('=');
    if (eq === -1) {
      continue;
    }
    values[trimmed.slice(0, eq).trim()] = trimmed.slice(eq + 1).trim();
  }
  return values;
}

export function loadConfig(
  app: App,
  dotEnvPath: string = path.join(__dirname, '..', '.env'),
): LlmConfig {
  // Secrets live in the gitignored .env file; explicit -c context values
  // always win over it.
  const dotEnv = loadDotEnv(dotEnvPath);

  return {
    region: contextString(app, 'region', DEFAULTS.region),
    hfToken: contextString(app, 'hfToken', dotEnv.HF_TOKEN ?? DEFAULTS.hfToken),
    instanceType: contextString(app, 'instanceType', DEFAULTS.instanceType),
    vllmVersion: contextString(app, 'vllmVersion', DEFAULTS.vllmVersion),
    llamacppRelease: contextString(app, 'llamacppRelease', DEFAULTS.llamacppRelease),
    nvidiaDriverPackage: contextString(app, 'nvidiaDriverPackage', DEFAULTS.nvidiaDriverPackage),
    idleThresholdMinutes: contextNumber(app, 'idleThresholdMinutes', DEFAULTS.idleThresholdMinutes),
    gracePeriodMinutes: contextNumber(app, 'gracePeriodMinutes', DEFAULTS.gracePeriodMinutes),
    maxRuntimeMinutes: contextNumber(app, 'maxRuntimeMinutes', DEFAULTS.maxRuntimeMinutes),
    vllmPort: DEFAULTS.vllmPort,
    availabilityZones: contextList(app, 'availabilityZones', DEFAULTS.availabilityZones),
    builderInstanceType: contextString(app, 'builderInstanceType', DEFAULTS.builderInstanceType),
    imageVolumeGb: contextNumber(app, 'imageVolumeGb', DEFAULTS.imageVolumeGb),
  };
}
