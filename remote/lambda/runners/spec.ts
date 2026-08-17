/**
 * Everything runner-specific in the control plane, as one spec per runner.
 * The registry (index.ts) is a `Record<Runner, RunnerSpec>`, so adding a
 * runner to the `RUNNERS` union in shared/deploy-config.ts refuses to compile
 * until its spec exists — no scattered binary conditions to hunt down. A new
 * runner also needs an AMI recipe (lib/image-stack.ts's `runnerBuilds`); the
 * stack's log groups and Lambda wiring follow `RUNNERS` automatically.
 */

import type { DeployConfig } from '../shared/deploy-config';
import type { SeedSelection } from '../shared/seed/contract';

export interface RunnerSpec {
  /**
   * The daemon deploy-config model value once the weights are synced to
   * modelDir — a single file for a GGUF runner, the directory for a
   * checkpoint one.
   */
  syncedModelPath(modelDir: string): string;
  /**
   * Which of the model repository's files this runner needs, as a declarative
   * selection the seeder applies. Deliberately not a boot-script fragment:
   * the seeder is runner-agnostic, so adding a runner never means writing
   * shell, and the selection is unit-testable without rendering a script.
   *
   * Completeness of a seed is NOT a per-runner question — it is the manifest
   * (`_seed.json`), written last by the seeder — so there is no sentinel here.
   */
  seedSelection(cfg: DeployConfig): SeedSelection;
  /**
   * Inference boot-script fragment: the runner's key delivery (env file or
   * key file), then the shared daemon boot that hands the engine to
   * `outfit daemon`.
   */
  daemonBoot(cfg: DeployConfig, modelDir: string, port: number): string;
}
