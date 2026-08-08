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

  weightsSentinel: (weightsPrefix) => `${weightsPrefix}model.gguf`,

  // One GGUF (MTP is embedded in it), normalised to model.gguf so the runtime
  // need not guess the filename; mmproj/projector files are excluded.
  seedDownload: (cfg) =>
    `export QUANT='${cfg.quant}'
mkdir -p /tmp/dl
/opt/llm/venv/bin/python -c "import os; from huggingface_hub import snapshot_download; snapshot_download(os.environ['MODEL_ID'], allow_patterns=['*'+os.environ['QUANT']+'*'], local_dir='/tmp/dl', token=(os.environ.get('HF_TOKEN') or None))"
mapfile -t GGUFS < <(find /tmp/dl -type f -name '*.gguf' ! -iname '*mmproj*' | sort)
test "\${#GGUFS[@]}" -ge 1
cp "\${GGUFS[0]}" /opt/llm/model/model.gguf
[ "\${#GGUFS[@]}" -gt 1 ] && echo "WARNING: \${#GGUFS[@]} gguf files for $QUANT; used the first (split quant not handled)" >&2 || true
`,

  daemonBoot: (cfg, modelDir, port) => `mkdir -p /etc/llm
printf '%s' "$API_KEY" >/etc/llm/api-key
chmod 600 /etc/llm/api-key

${daemonBoot(daemonDeployConfig(cfg, syncedModelPath(modelDir), port, ['--api-key-file', '/etc/llm/api-key']), '')}`,

  // The llama.cpp AMI has no Python venv; the seed runs elsewhere.
  seedTooling: false,
};
