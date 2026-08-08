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
   * The S3 key (under the weights prefix) whose presence marks a complete
   * seed. A bare list-under-the-prefix would also match the debris of a
   * failed or in-flight seed, so a per-runner sentinel is checked instead —
   * it is written last, by the seed's S3 sync.
   */
  weightsSentinel(weightsPrefix: string): string;
  /**
   * Seed boot-script fragment that downloads this runner's weights from
   * Hugging Face into /opt/llm/model, using the $MODEL_ID/$HF_TOKEN
   * environment the seed header exports.
   */
  seedDownload(cfg: DeployConfig): string;
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
