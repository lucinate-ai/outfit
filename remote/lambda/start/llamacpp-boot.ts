/**
 * llama.cpp's half of the daemon boot: the API key in a root-only file (the
 * engine reads it via --api-key-file, so the secret never appears in `ps`),
 * then the shared daemon boot.
 */

import type { DeployConfig } from '../shared/deploy-config';
import { daemonBoot, daemonDeployConfig } from './daemon-boot';

export function llamacppDaemonBoot(cfg: DeployConfig, modelDir: string, port: number): string {
  return `mkdir -p /etc/llm
printf '%s' "$API_KEY" >/etc/llm/api-key
chmod 600 /etc/llm/api-key

${daemonBoot(daemonDeployConfig(cfg, modelDir, port, ['--api-key-file', '/etc/llm/api-key']), '')}`;
}
