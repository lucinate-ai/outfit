/**
 * llama.cpp's runner spec: one GGUF file normalised to model.gguf, the API
 * key in a root-only file the engine reads via --api-key-file (so the secret
 * never appears in `ps`).
 */

import { companionFileName, type CompanionRole } from '../shared/deploy-config';
import { daemonBoot, daemonDeployConfig } from './daemon-boot';
import type { RunnerSpec } from './spec';

const syncedModelPath = (modelDir: string): string => `${modelDir}/model.gguf`;

/**
 * The llama-server flag that names each companion role. Long-form spellings:
 * these are built here, never parsed, so there is no alias to match.
 */
const COMPANION_FLAG: Record<CompanionRole, string> = {
  draft: '--spec-draft-model',
  mmproj: '--mmproj',
};

/**
 * Companion flags in a stable (role-sorted) order, so the generated boot script
 * is deterministic and diffable between deploys.
 *
 * Only the *path* is set here. Selecting the speculative algorithm
 * (`--spec-type draft-dflash`) stays in the user's serveArgs, exactly as it
 * would be for a local run — the deployment owns where a file is, not how the
 * engine is asked to use it.
 */
const companionArgs = (
  companions: Partial<Record<CompanionRole, string>> | undefined,
  modelDir: string,
): string[] =>
  // Tolerates an absent map rather than relying on the parser having filled
  // it: the "a pre-companion config behaves exactly as before" guarantee is
  // worth keeping local to the code that would otherwise break it.
  (Object.keys(companions ?? {}).sort() as CompanionRole[]).flatMap((role) => [
    COMPANION_FLAG[role],
    `${modelDir}/${companionFileName(role)}`,
  ]);

export const llamacpp: RunnerSpec = {
  // llama-server is pointed at the single synced GGUF.
  syncedModelPath,

  // One GGUF (MTP is embedded in it), stored as model.gguf so the runtime need
  // not guess the filename; mmproj/projector companions are excluded.
  //
  // expectSingle makes an ambiguous match FAIL the seed. The old boot script
  // warned and took the first of N, which silently shipped one shard of a split
  // quant as though it were the whole model.
  seedSelection: (cfg) => ({
    include: [`*${cfg.quant}*.gguf`],
    exclude: ['*mmproj*', '*projector*'],
    expectSingle: 'model.gguf',
    companions: (Object.keys(cfg.companions ?? {}).sort() as CompanionRole[]).map((role) => ({
      storeAs: companionFileName(role),
      file: (cfg.companions as Record<CompanionRole, string>)[role],
    })),
  }),

  companionArgs: (cfg, modelDir) => companionArgs(cfg.companions, modelDir),

  daemonBoot: (cfg, modelDir, port) => `mkdir -p /etc/llm
printf '%s' "$API_KEY" >/etc/llm/api-key
chmod 600 /etc/llm/api-key

${daemonBoot(
  daemonDeployConfig(cfg, syncedModelPath(modelDir), port, [
    '--api-key-file',
    '/etc/llm/api-key',
    ...companionArgs(cfg.companions, modelDir),
  ]),
  '',
)}`,
};
