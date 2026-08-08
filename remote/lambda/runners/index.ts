/**
 * The runner registry. `Record<Runner, RunnerSpec>` is the point: adding a
 * runner to `RUNNERS` (shared/deploy-config.ts) refuses to compile until its
 * spec is written and registered here — one file per runner, no scattered
 * conditionals. See spec.ts for what a spec must provide.
 */

import type { Runner } from '../shared/deploy-config';
import { llamacpp } from './llamacpp';
import type { RunnerSpec } from './spec';
import { vllm } from './vllm';

export type { RunnerSpec } from './spec';

const RUNNER_SPECS: Record<Runner, RunnerSpec> = {
  vllm,
  llamacpp,
};

/** The spec for one runner. Total by construction — see RUNNER_SPECS. */
export function runnerSpec(runner: Runner): RunnerSpec {
  return RUNNER_SPECS[runner];
}
