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

  weightsKeys: (cfg, weightsPrefix) => [
    `${weightsPrefix}model.gguf`,
    ...(Object.keys(cfg.companions ?? {}).sort() as CompanionRole[]).map(
      (role) => `${weightsPrefix}${companionFileName(role)}`,
    ),
  ],

  // The main GGUF (MTP is embedded in it), normalised to model.gguf so the
  // runtime need not guess the filename, plus any named companions under their
  // own fixed names.
  //
  // Companions are fetched by exact filename because the quant glob cannot
  // reach them: for Muse Glimmer the quant is `kquant-dynamic` and the drafter
  // is `dflash-kquant.gguf`, which `*kquant-dynamic*` does not match.
  seedDownload: (cfg) => {
    const companions = Object.entries(cfg.companions ?? {}) as [CompanionRole, string][];
    companions.sort(([a], [b]) => a.localeCompare(b));

    // Exact companion names join the quant glob in allow_patterns. Plain
    // single quotes are safe: COMPANION_FILENAME has already excluded
    // everything that could close them.
    const patterns = [
      "'*'+os.environ['QUANT']+'*'",
      ...companions.map(([, file]) => `'${file}'`),
    ].join(', ');

    // The main GGUF is whatever is left once the companions are excluded by
    // name. The blanket `*mmproj*` exclusion stays as well: a projector is
    // never the main model, and where the quant glob happens to match one
    // (`Q4_K_M` matching `mmproj-Q4_K_M.gguf`) it sorts *before* the real
    // weights and would be served in its place. Naming it as a companion is
    // now the way to keep it; not naming it must still not select it.
    const exclusions = [
      " ! -iname '*mmproj*'",
      ...companions.map(([, file]) => ` ! -name '${file}'`),
    ].join('');

    // Each companion is located, checked, then copied to its role name. The
    // explicit test is what turns "the repo does not have that file" into a
    // failed seed with a named cause, rather than an instance that starts
    // minutes later pointing at a file that is not there.
    const copies = companions
      .map(
        ([role, file]) => `COMPANION=$(find /tmp/dl -type f -name '${file}' | head -1)
test -n "$COMPANION" || { echo "ERROR: companion ${role} '${file}' not found in $MODEL_ID" >&2; exit 1; }
cp "$COMPANION" /opt/llm/model/${companionFileName(role)}`,
      )
      .join('\n');

    return `export QUANT='${cfg.quant}'
mkdir -p /tmp/dl
/opt/llm/venv/bin/python -c "import os; from huggingface_hub import snapshot_download; snapshot_download(os.environ['MODEL_ID'], allow_patterns=[${patterns}], local_dir='/tmp/dl', token=(os.environ.get('HF_TOKEN') or None))"
mapfile -t GGUFS < <(find /tmp/dl -type f -name '*.gguf'${exclusions} | sort)
test "\${#GGUFS[@]}" -ge 1
cp "\${GGUFS[0]}" /opt/llm/model/model.gguf
[ "\${#GGUFS[@]}" -gt 1 ] && echo "WARNING: \${#GGUFS[@]} gguf files for $QUANT; used the first (split quant not handled)" >&2 || true
${copies}${copies ? '\n' : ''}`;
  },

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

  // The llama.cpp AMI has no Python venv; the seed runs elsewhere.
  seedTooling: false,
};
