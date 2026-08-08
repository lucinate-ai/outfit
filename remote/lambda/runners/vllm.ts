/**
 * vLLM's runner spec: whole-checkpoint weights served from the model
 * directory, the API key delivered by env file (wired into the daemon unit
 * via EnvironmentFile), and the venv that makes its AMI the seed's host.
 */

import { daemonBoot, daemonDeployConfig } from './daemon-boot';
import type { RunnerSpec } from './spec';

const syncedModelPath = (modelDir: string): string => modelDir;

export const vllm: RunnerSpec = {
  // vLLM serves the whole synced checkpoint directory.
  syncedModelPath,

  // config.json is part of every checkpoint and synced last-ish; its presence
  // under the prefix marks a complete seed.
  weightsSentinel: (weightsPrefix) => `${weightsPrefix}config.json`,

  // The whole safetensors checkpoint, straight into the model dir.
  seedDownload: () =>
    `/opt/llm/venv/bin/python -c "import os; from huggingface_hub import snapshot_download; snapshot_download(os.environ['MODEL_ID'], local_dir='/opt/llm/model', token=(os.environ.get('HF_TOKEN') or None))"
`,

  daemonBoot: (cfg, modelDir, port) => `# Python dev headers: Triton JIT-compiles a CUDA stub against Python.h on the
# first model load (Qwen3.6's linear-attention path); baked into recipe 2.0.3+,
# this is a safety net for instances off an older AMI and a no-op once present.
if [ ! -f /usr/include/python3.12/Python.h ]; then
  apt-get update && apt-get install -y python3.12-dev
fi

cat >/etc/vllm.env <<ENVFILE
VLLM_API_KEY=$API_KEY
HF_HUB_OFFLINE=1
# Native Torch sampler, not FlashInfer's — FlashInfer JIT-needs nvcc, which the
# slim AMI (driver + CUDA runtime only, no toolkit) does not ship.
VLLM_USE_FLASHINFER_SAMPLER=0
ENVFILE

${daemonBoot(daemonDeployConfig(cfg, syncedModelPath(modelDir), port, ['--gpu-memory-utilization', '0.92']), 'EnvironmentFile=/etc/vllm.env\n')}`,

  // The vLLM AMI carries the Python venv with huggingface_hub.
  seedTooling: true,
};
