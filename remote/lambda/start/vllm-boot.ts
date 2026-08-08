/**
 * vLLM's half of the daemon boot: the env file that delivers the API key (and
 * keeps the engine offline and on the native sampler), wired into the daemon
 * unit via EnvironmentFile, then the shared daemon boot.
 */

import type { DeployConfig } from '../shared/deploy-config';
import { daemonBoot, daemonDeployConfig } from './daemon-boot';

export function vllmDaemonBoot(cfg: DeployConfig, modelDir: string, port: number): string {
  return `# Python dev headers: Triton JIT-compiles a CUDA stub against Python.h on the
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

${daemonBoot(daemonDeployConfig(cfg, modelDir, port, ['--gpu-memory-utilization', '0.92']), 'EnvironmentFile=/etc/vllm.env\n')}`;
}
