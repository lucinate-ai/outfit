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

/** The SSM command that fetches the daemon's status — the idle check's source. */
export const DAEMON_STATUS_CMD = `curl -s --max-time 10 ${DAEMON_API}/v1/status || echo ${DAEMON_UNREACHABLE}`;

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
  /**
   * The same activity pair `/v1/status` reports, from the same record on the
   * instance. Present whatever the engine's state — a stopped engine still
   * says when it last worked — and absent until an engine has run.
   * `idleSeconds` is absent at zero too, so gate on `lastActiveAt`.
   */
  lastActiveAt?: string;
  idleSeconds?: number;
}

/**
 * The daemon's /v1/status reply (outfit's internal/daemon.StatusResponse).
 * `lastActiveAt` and `idleSeconds` are the daemon's own answer to "has this
 * engine been working?", derived on the instance from counters it samples
 * every few seconds — which is why the idle check reads this rather than
 * comparing raw counters at whatever rate it happens to run. Both are absent
 * until an engine has run.
 */
export interface DaemonStatus {
  state: string;
  runner?: string;
  model?: string;
  uptimeSeconds?: number;
  logPath?: string;
  lastActiveAt?: string;
  idleSeconds?: number;
}

/**
 * Parse a daemon metrics scrape. Returns null when the daemon was
 * unreachable or the output is not its JSON — the caller treats that as no
 * metrics observed.
 */
export function parseDaemonMetrics(stdout: string): DaemonMetrics | null {
  return parseDaemonReply<DaemonMetrics>(stdout);
}

/**
 * Parse a daemon status scrape. Null on the same terms as the metrics parse:
 * unreachable, empty, or not the daemon's JSON.
 */
export function parseDaemonStatus(stdout: string): DaemonStatus | null {
  return parseDaemonReply<DaemonStatus>(stdout);
}

/**
 * Both daemon replies are gated the same way: the unreachable marker, empty
 * output and anything without a string `state` all mean "nothing observed".
 */
function parseDaemonReply<T extends { state: string }>(stdout: string): T | null {
  const trimmed = stdout.trim();
  if (!trimmed || trimmed.includes(DAEMON_UNREACHABLE)) {
    return null;
  }
  try {
    const parsed = JSON.parse(trimmed) as T;
    if (typeof parsed !== 'object' || parsed === null || typeof parsed.state !== 'string') {
      return null;
    }
    return parsed;
  } catch {
    return null;
  }
}
