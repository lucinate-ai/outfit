import * as fs from 'fs';
import * as path from 'path';
import type { App } from 'aws-cdk-lib';
import { isRunner, RUNNERS, type Runner } from '../lambda/shared/deploy-config';

// Tag the image stack puts on every baked AMI, and the runtime start Lambda
// filters on to find the newest one to launch. The AMI is model-agnostic
// (just the driver + vLLM), so there is no model tag — the model comes from
// S3 at boot.
export const AMI_ROLE_TAG_KEY = 'cloud-vm-llm:role';
export const AMI_ROLE_TAG_VALUE = 'runtime-ami';
// The runner an AMI was baked for. Each runner has its own recipe/pipeline and
// its own AMI lineage, so the start Lambda (and seed) filter by both tags.
export const AMI_RUNNER_TAG_KEY = 'cloud-vm-llm:runner';

export interface LlmConfig {
  /** CIDR allowed to reach the vLLM port, e.g. your home IP as a /32. Required. */
  allowedCidr: string;
  region: string;
  /**
   * Which inference server to run: `vllm` or `llamacpp`. Required, no default —
   * one must be chosen. CDK writes it into the initial deploy-config; thereafter
   * `outfit remote deploy` owns it.
   */
  runner: Runner;
  /**
   * Hugging Face model id, served at runtime from S3. Must be a pre-quantised
   * checkpoint: BF16 weights for a 27B model are ~54 GB and do not fit the
   * L40S's 48 GB. FP8 is hardware-native on the L40S (Ada);
   * nvidia/Qwen3.6-27B-NVFP4 is the Blackwell-oriented alternative.
   */
  modelId: string;
  /** Optional Hugging Face token, used only for the first-boot seed of a gated repo. */
  hfToken: string;
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
  maxModelLen: number;
  /** Extra args appended to `vllm serve`, e.g. "--kv-cache-dtype fp8". */
  vllmExtraArgs: string;
  /**
   * vLLM tool-call parser (model-specific). When set, the server is started
   * with `--enable-auto-tool-choice --tool-call-parser <this>`, which coding
   * agents need — they send `tool_choice: "auto"`. Qwen3.6 emits tool calls in
   * Qwen's XML format, parsed by `qwen3_xml`. Empty disables tool calling.
   */
  toolCallParser: string;
  /**
   * vLLM reasoning parser (model-specific). When set, adds `--reasoning-parser
   * <this>`, which splits the model's `<think>` block into a separate field so
   * it neither pollutes the reply nor blocks tool-call parsing. `qwen3` for
   * Qwen3.6. Empty leaves reasoning inline in the content.
   */
  reasoningParser: string;
  /** Terminate the instance after this many minutes without requests. */
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
   * AZs the start Lambda tries, in order, when launching the instance — the
   * g6e-capable zones. It launches into the first with capacity, so this
   * replaces a single hard AZ pin with automatic per-AZ fallback.
   */
  availabilityZones: string[];
  /**
   * Instance type EC2 Image Builder uses to bake the AMI. The bake installs
   * the driver and the vLLM venv but never runs the GPU, so a cheap non-GPU
   * type is fine.
   */
  builderInstanceType: string;
  /** Root volume size (GB) of the baked AMI — fits the OS + driver + vLLM venv. */
  imageVolumeGb: number;
}

const DEFAULTS = {
  region: 'us-east-1',
  modelId: 'Qwen/Qwen3.6-27B-FP8',
  hfToken: '',
  instanceType: 'g6e.xlarge',
  // Must be new enough for the model's architecture — vLLM 0.11 predates
  // Qwen3.6 (Qwen3_5ForConditionalGeneration) and rejects it at load. Pin a
  // version that lists the model's architecture as supported.
  vllmVersion: '0.26.0',
  // ai-dock/llama.cpp-cuda release with CUDA 12.8; pin a specific build for
  // reproducible bakes. Must post-date the MTP merge (PR #22673).
  llamacppRelease: 'b10107',
  nvidiaDriverPackage: 'nvidia-driver-570-server-open',
  // 32k, not 64k: ~30 GB of FP8 weights leave ~14 GB on the 48 GB L40S, and a
  // 64k KV cache for a 27B model is ~13 GB for one sequence — enough to OOM at
  // startup. 32k halves the KV cache and is ample for a coding agent. Raise it
  // (or add `--kv-cache-dtype fp8` via vllmExtraArgs) if you have headroom.
  maxModelLen: 32768,
  // --enforce-eager skips vLLM's torch.compile / CUDA-graph capture, which on
  // a cold start with no cache takes 15-20 min and pins every core (starving
  // the SSM agent and blowing past the idle timer). Eager mode makes wakes
  // ~5 min at a modest single-user throughput cost — the right trade for a
  // scale-to-zero box. Drop it if you want peak throughput and longer waits.
  vllmExtraArgs: '--enforce-eager',
  // Qwen3.6 emits tool calls in Qwen's XML format and wraps reasoning in
  // <think>; coding agents send tool_choice: "auto", so both parsers are
  // needed. qwen3_coder and qwen3_xml are aliases for the same parser.
  toolCallParser: 'qwen3_coder',
  reasoningParser: 'qwen3',
  idleThresholdMinutes: 15,
  // Must exceed the whole cold start (S3 sync ~4 min + weight/CUDA load), or
  // the idle check terminates the instance mid-load (the metrics scrape fails
  // while vLLM is still loading, which reads as "idle").
  gracePeriodMinutes: 30,
  maxRuntimeMinutes: 240,
  vllmPort: 8000,
  availabilityZones: ['us-east-1b', 'us-east-1c', 'us-east-1d', 'us-east-1e'],
  builderInstanceType: 'm5.xlarge',
  // Big enough for the OS + driver + vLLM venv AND the ~30 GB model synced
  // from S3 at boot. The snapshot only copies used blocks (~20 GB), so this
  // does not slow the bake.
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
 * dotenv dependency. Recognised keys: ALLOWED_CIDR, HF_TOKEN.
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
  // Secrets and machine-specific values live in the gitignored .env file;
  // explicit -c context values always win over it.
  const dotEnv = loadDotEnv(dotEnvPath);

  const allowedCidr = app.node.tryGetContext('allowedCidr') ?? dotEnv.ALLOWED_CIDR;
  if (!allowedCidr) {
    throw new Error(
      'allowedCidr is not set: run `pnpm set-ip` to write your current public ' +
        'IP to .env, or pass it explicitly with `-c allowedCidr=<ip>/32`.',
    );
  }
  if (!/^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\/\d{1,2}$/.test(String(allowedCidr))) {
    throw new Error(`Context value "allowedCidr" must be an IPv4 CIDR, got: ${allowedCidr}`);
  }

  const region = contextString(app, 'region', DEFAULTS.region);

  // No default: one runner must be chosen explicitly (like allowedCidr).
  const runner = contextString(app, 'runner', '');
  if (!isRunner(runner)) {
    throw new Error(
      `runner must be one of ${RUNNERS.join('/')} — set it with \`-c runner=<runner>\` (no default)`,
    );
  }

  return {
    allowedCidr: String(allowedCidr),
    region,
    runner,
    modelId: contextString(app, 'modelId', DEFAULTS.modelId),
    hfToken: contextString(app, 'hfToken', dotEnv.HF_TOKEN ?? DEFAULTS.hfToken),
    instanceType: contextString(app, 'instanceType', DEFAULTS.instanceType),
    vllmVersion: contextString(app, 'vllmVersion', DEFAULTS.vllmVersion),
    llamacppRelease: contextString(app, 'llamacppRelease', DEFAULTS.llamacppRelease),
    nvidiaDriverPackage: contextString(app, 'nvidiaDriverPackage', DEFAULTS.nvidiaDriverPackage),
    maxModelLen: contextNumber(app, 'maxModelLen', DEFAULTS.maxModelLen),
    vllmExtraArgs: contextString(app, 'vllmExtraArgs', DEFAULTS.vllmExtraArgs),
    toolCallParser: contextString(app, 'toolCallParser', DEFAULTS.toolCallParser),
    reasoningParser: contextString(app, 'reasoningParser', DEFAULTS.reasoningParser),
    idleThresholdMinutes: contextNumber(app, 'idleThresholdMinutes', DEFAULTS.idleThresholdMinutes),
    gracePeriodMinutes: contextNumber(app, 'gracePeriodMinutes', DEFAULTS.gracePeriodMinutes),
    maxRuntimeMinutes: contextNumber(app, 'maxRuntimeMinutes', DEFAULTS.maxRuntimeMinutes),
    vllmPort: DEFAULTS.vllmPort,
    availabilityZones: contextList(app, 'availabilityZones', DEFAULTS.availabilityZones),
    builderInstanceType: contextString(app, 'builderInstanceType', DEFAULTS.builderInstanceType),
    imageVolumeGb: contextNumber(app, 'imageVolumeGb', DEFAULTS.imageVolumeGb),
  };
}
