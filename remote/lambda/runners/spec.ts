/**
 * Everything runner-specific in the control plane, as one spec per runner.
 * The registry (index.ts) is a `Record<Runner, RunnerSpec>`, so adding a
 * runner to the `RUNNERS` union in shared/deploy-config.ts refuses to compile
 * until its spec exists — no scattered binary conditions to hunt down. A new
 * runner also needs an AMI recipe (lib/image-stack.ts's `runnerBuilds`); the
 * stack's log groups and Lambda wiring follow `RUNNERS` automatically.
 */

import type { DeployConfig } from '../shared/deploy-config';

export interface RunnerSpec {
  /**
   * The daemon deploy-config model value once the weights are synced to
   * modelDir — a single file for a GGUF runner, the directory for a
   * checkpoint one.
   */
  syncedModelPath(modelDir: string): string;
  /**
   * Every S3 key (under the weights prefix) that must exist for this config's
   * weights to count as complete: the runner's sentinel plus one per named
   * companion. A bare list-under-the-prefix would also match the debris of a
   * failed or in-flight seed, so specific keys are checked instead — they are
   * written last, by the seed's S3 sync.
   *
   * Companions must be included, not just the sentinel. The weights prefix is
   * derived from (runner, modelId, quant), so adding a companion does not
   * change it: checking the sentinel alone would find the earlier seed's
   * main weights, skip the re-seed, and start an instance whose companion
   * flag points at a file that was never synced.
   */
  weightsKeys(cfg: DeployConfig, weightsPrefix: string): string[];
  /**
   * Seed boot-script fragment that downloads this runner's weights from
   * Hugging Face into /opt/llm/model, using the $MODEL_ID/$HF_TOKEN
   * environment the seed header exports.
   */
  seedDownload(cfg: DeployConfig): string;
  /**
   * The flags naming this runner's companion weights on disk, given where the
   * weights were synced. Role -> flag is runner knowledge, so it lives here
   * rather than in the shared layer, and a role a runner has no use for is
   * simply ignored — inert, not fatal, so naming a companion for a runner that
   * cannot use one seeds a spare file rather than failing the wake.
   *
   * Returns [] when the config names no companions, which is what keeps a
   * pre-companion deployment's command byte-identical.
   */
  companionArgs(cfg: DeployConfig, modelDir: string): string[];
  /**
   * Inference boot-script fragment: the runner's key delivery (env file or
   * key file), then the shared daemon boot that hands the engine to
   * `outfit daemon`.
   */
  daemonBoot(cfg: DeployConfig, modelDir: string, port: number): string;
  /**
   * Whether this runner's AMI carries the Python venv (huggingface_hub) the
   * seed job runs on. The seed launches the first runner that does,
   * whatever runner the weights are for.
   */
  seedTooling: boolean;
}
