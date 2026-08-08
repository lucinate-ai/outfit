/**
 * The runner-neutral half of booting the instance's engine under the outfit
 * daemon: rendering the daemon's stored deploy config from the environment's,
 * and the boot-script tail that enables the daemon and requests the first
 * start over its control API. The per-runner halves (key delivery, env files)
 * live in vllm-boot.ts and llamacpp-boot.ts.
 */

import type { DeployConfig } from '../shared/deploy-config';

/**
 * Render the daemon's stored deploy config: the same shape `outfit remote
 * deploy` produces, with the cloud-owned settings resolved in — the model as
 * the synced local path, the bind address and port, and the runner's key
 * delivery — so the daemon's ordinary start serves exactly what the old
 * per-runner unit ran. No --metrics here: the daemon switches the engine's
 * metrics endpoint on itself.
 */
export function daemonDeployConfig(
  cfg: DeployConfig,
  modelDir: string,
  port: number,
  extraServeArgs: string[],
): string {
  const modelId = cfg.runner === 'llamacpp' ? `${modelDir}/model.gguf` : modelDir;
  return JSON.stringify(
    {
      runner: cfg.runner,
      modelId,
      quant: '',
      contextSize: cfg.contextSize,
      servedModelName: cfg.servedModelName,
      serveArgs: ['--host', '0.0.0.0', '--port', String(port), ...extraServeArgs, ...cfg.serveArgs],
    },
    null,
    2,
  );
}

/**
 * The daemon boot shared by both runners: write the deploy config where the
 * daemon reads it, enable outfit-daemon.service (and the baked crash-nudge
 * timer), then request the engine's first start over the control API — the
 * daemon never auto-starts, so the boot start is the same explicit API start
 * any client performs. A 409 also counts: a re-run must not fail on an
 * engine already up.
 */
export function daemonBoot(deployConfigJson: string, unitExtra: string): string {
  return `mkdir -p /root/.config/outfit/daemon
cat >/root/.config/outfit/daemon/deploy-config.json <<'DEPLOYCONFIG'
${deployConfigJson}
DEPLOYCONFIG
chmod 600 /root/.config/outfit/daemon/deploy-config.json

cat >/etc/systemd/system/outfit-daemon.service <<'UNIT'
[Unit]
Description=outfit daemon (engine host)
After=network-online.target
Wants=network-online.target
[Service]
${unitExtra}ExecStart=/usr/local/bin/outfit daemon --api-addr 127.0.0.1:4242
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now outfit-daemon.service
systemctl enable --now outfit-nudge.timer || echo "NUDGE_TIMER_MISSING"

# First engine start, retried until the daemon answers. The engine loads the
# model asynchronously; the start Lambda's health poll still gates "ready".
for attempt in $(seq 1 30); do
  code=$(curl -s -o /tmp/outfit-start.json -w '%{http_code}' --max-time 15 -X POST http://127.0.0.1:4242/v1/start || true)
  if [ "$code" = "200" ] || [ "$code" = "409" ]; then
    break
  fi
  sleep 2
done
cat /tmp/outfit-start.json || true`;
}
