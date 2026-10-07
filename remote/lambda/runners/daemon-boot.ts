/**
 * The runner-neutral half of booting the instance's engine under the spinloop
 * daemon: rendering the daemon's stored deploy config from the environment's,
 * and the boot-script tail that enables the daemon and requests the first
 * start over its control API. Each runner's spec (vllm.ts, llamacpp.ts)
 * supplies its half — key delivery, env files, the synced model path — and
 * calls back into these.
 */

import type { DeployConfig } from '../shared/deploy-config';

/**
 * Render the daemon's stored deploy config: the same shape `spinloop cloud
 * deploy` produces, with the cloud-owned settings resolved in — the model as
 * the synced local path, the bind address and port, and the runner's key
 * delivery — so the daemon's ordinary start serves exactly what the old
 * per-runner unit ran — the model as the runner's synced local path
 * (spec.syncedModelPath). No --metrics here: the daemon switches the
 * engine's metrics endpoint on itself.
 */
export function daemonDeployConfig(
  cfg: DeployConfig,
  modelId: string,
  port: number,
  extraServeArgs: string[],
): string {
  return JSON.stringify(
    {
      runner: cfg.runner,
      modelId,
      quant: '',
      contextSize: cfg.contextSize,
      // JSON.stringify drops an undefined property entirely, so an unset
      // parallel is simply absent from the JSON the daemon reads — matching
      // Go's `omitempty` on the other end of this same field.
      parallel: cfg.parallel,
      servedModelName: cfg.servedModelName,
      serveArgs: ['--host', '0.0.0.0', '--port', String(port), ...extraServeArgs, ...cfg.serveArgs],
    },
    null,
    2,
  );
}

/**
 * The daemon's config directory on the instance. The unit pins
 * SPINLOOP_CONFIG_DIR to this fixed system path so the daemon's config location
 * does not depend on $HOME — a bare systemd service gets none, and the earlier
 * $HOME-based default made the daemon read a different directory than the boot
 * wrote to. The daemon's state (deploy-config.json, engine.log) lives here.
 */
export const DAEMON_CONFIG_DIR = '/var/lib/spinloop';

/**
 * The daemon boot shared by both runners: write the deploy config where the
 * daemon reads it (its pinned SPINLOOP_CONFIG_DIR) and enable
 * spinloop-daemon.service (and the baked crash-nudge timer). The engine is not
 * started here: the daemon never auto-starts, and the control plane's start
 * request issues the start — on every path, a fresh boot and a re-wake alike,
 * the same explicit API start any client performs. The daemon's first answer
 * is therefore the boot's signal that this tail ran, which the start Lambda
 * waits for before issuing the start.
 */
export function daemonBoot(deployConfigJson: string, unitExtra: string): string {
  return `mkdir -p ${DAEMON_CONFIG_DIR}/daemon
cat >${DAEMON_CONFIG_DIR}/daemon/deploy-config.json <<'DEPLOYCONFIG'
${deployConfigJson}
DEPLOYCONFIG
chmod 600 ${DAEMON_CONFIG_DIR}/daemon/deploy-config.json

cat >/etc/systemd/system/spinloop-daemon.service <<'UNIT'
[Unit]
Description=spinloop daemon (engine host)
After=network-online.target
Wants=network-online.target
[Service]
Environment=SPINLOOP_CONFIG_DIR=${DAEMON_CONFIG_DIR}
${unitExtra}ExecStart=/usr/local/bin/spinloop daemon --api-addr 127.0.0.1:4242
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now spinloop-daemon.service
systemctl enable --now spinloop-nudge.timer || echo "NUDGE_TIMER_MISSING"
`;
}
