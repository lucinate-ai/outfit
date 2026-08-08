/**
 * The on-instance outfit daemon's control API, as seen from the control
 * plane. The instance runs `outfit daemon --api-addr 127.0.0.1:4242`
 * (loopback-only, tokenless — see the Listen rules in outfit), so every call
 * here is a curl over SSM. Metric collection lives in the daemon
 * (outfit's internal/metrics); the Lambdas only relay its JSON.
 */

import type { CpuStat, GpuStat, MemoryStat, TokenStats } from './stats';

/** Where the daemon listens on the instance. Loopback: only SSM reaches it. */
export const DAEMON_API = 'http://127.0.0.1:4242';

/** Marker echoed when the daemon does not answer, so a failed curl parses as unreachable rather than as empty output. */
export const DAEMON_UNREACHABLE = 'DAEMON_UNREACHABLE';

/** The SSM command that fetches the daemon's collected metrics. */
export const DAEMON_METRICS_CMD = `curl -s --max-time 10 ${DAEMON_API}/v1/metrics || echo ${DAEMON_UNREACHABLE}`;

/**
 * The daemon's /v1/metrics reply — the same stats dialect the Go formatters
 * render (outfit's internal/metrics.Stats), minus what only the control
 * plane knows (environment, instance id/type).
 */
export interface DaemonMetrics {
  state: string;
  runner?: string;
  modelId?: string;
  uptimeSeconds?: number;
  tokens?: TokenStats;
  gpus?: GpuStat[];
  cpu?: CpuStat;
  memory?: MemoryStat;
  errors?: string[];
}

/**
 * Parse a daemon metrics scrape. Returns null when the daemon was
 * unreachable or the output is not its JSON — the caller treats that as no
 * metrics observed.
 */
export function parseDaemonMetrics(stdout: string): DaemonMetrics | null {
  const trimmed = stdout.trim();
  if (!trimmed || trimmed.includes(DAEMON_UNREACHABLE)) {
    return null;
  }
  try {
    const parsed = JSON.parse(trimmed) as DaemonMetrics;
    if (typeof parsed !== 'object' || parsed === null || typeof parsed.state !== 'string') {
      return null;
    }
    return parsed;
  } catch {
    return null;
  }
}
