/**
 * llama.cpp's runner spec: one GGUF file normalised to model.gguf, the API
 * key in a root-only file the engine reads via --api-key-file (so the secret
 * never appears in `ps`).
 */

import { daemonBoot, daemonDeployConfig } from './daemon-boot';
import type { RunnerSpec } from './spec';

const syncedModelPath = (modelDir: string): string => `${modelDir}/model.gguf`;

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
  }),

  daemonBoot: (cfg, modelDir, port) => `mkdir -p /etc/llm
printf '%s' "$API_KEY" >/etc/llm/api-key
chmod 600 /etc/llm/api-key

${daemonBoot(daemonDeployConfig(cfg, syncedModelPath(modelDir), port, ['--api-key-file', '/etc/llm/api-key']), '')}`,
};
