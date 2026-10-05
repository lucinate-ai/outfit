/**
 * The stats response types the control plane speaks. Collection itself lives
 * in the on-instance spinloop daemon (spinloop's internal/metrics — the Go port
 * of the parsers that used to live here); the stats Lambda merges the
 * daemon's reply with what only the control plane knows. These shapes are
 * value-compatible with the Go side, so `spinloop remote metrics` renders them
 * unchanged.
 */

export interface GpuStat {
  index: number;
  name: string;
  utilization: number; // percent
  memoryUsed: number; // bytes
  memoryTotal: number; // bytes
  temperature: number; // celsius
}

export interface CpuStat {
  /** Percent CPU busy (100 - idle). */
  utilization: number; // percent
}

export interface MemoryStat {
  total: number; // bytes
  used: number; // bytes
}

export interface TokenStats {
  /** Total number of in-flight requests. */
  running: number;
  /** Cumulative token counter for activity tracking. */
  counter: number;
  /** Total prompt tokens processed. */
  promptTokens: number;
  /** Total generation/predicted tokens. */
  generationTokens: number;
  /**
   * The engine's cumulative request counter, present only where the engine
   * exposes one — vLLM's request_success_total. Absent for an engine family
   * whose metrics carry no cumulative request count (llama.cpp's, today).
   */
  requests?: number;
}

/**
 * One GPU's figures in a retained system reading, relayed verbatim from the
 * daemon (spinloop's metrics.HistoryGPU). One-letter fields, like its parent:
 * the readings cross SSM, whose command output truncates at 4KB, so the
 * window's forty samples must fit that budget alongside the current reading.
 */
export interface HistoryGPU {
  /** The GPU's index. */
  i: number;
  /** Utilisation, percent. */
  u: number;
  /** Memory used over total, percent. Absent where the GPU reports no total. */
  m?: number;
}

/** One retained system reading, as the bar format plots it: percent per series. */
export interface HistorySample {
  /** When the reading was taken, unix seconds. */
  t: number;
  /** Whole-host CPU utilisation, percent. */
  c?: number;
  /** System memory used over total, percent. */
  m?: number;
  /** Each GPU's figures, percent. */
  g?: HistoryGPU[];
}

export interface StatsResult {
  /** Environment name. */
  environment: string;
  /** Instance state (running, stopped, undeployed). */
  state: string;
  /** Instance id, if running. */
  instanceId?: string;
  /** Instance type (e.g. g6e.xlarge). */
  instanceType?: string;
  /** Runner name from deploy config. */
  runner?: string;
  /** Model id from deploy config. */
  modelId?: string;
  /**
   * The name the engine answers to beside the model id, from the deploy
   * config's ALIAS — the same field the start/env Lambdas' status reply
   * carries, so a stopped environment's wakeable name matches what it
   * reported while running.
   */
  servedName?: string;
  /** Uptime in seconds since launch. */
  uptimeSeconds?: number;
  /** Token/request metrics from the daemon's engine scrape. */
  tokens?: TokenStats;
  /** Per-GPU stats. */
  gpus?: GpuStat[];
  /** CPU stats. */
  cpu?: CpuStat;
  /** System memory stats. */
  memory?: MemoryStat;
  /**
   * The daemon's retained system readings, oldest first — one per sampler
   * tick while an engine ran, covering at most the last 10 minutes. They
   * survive a stop and clear when the next engine starts. Absent for a daemon
   * that predates the field or has never run an engine: the bar format falls
   * back to the gauge for a series with no readings.
   */
  history?: HistorySample[];
  /** Any errors encountered while collecting metrics. */
  errors?: string[];
  /** When the engine last did any work, RFC 3339, as the daemon reports it. */
  lastActiveAt?: string;
  /** Seconds since `lastActiveAt`. Absent when that is, and absent at zero. */
  idleSeconds?: number;
  /**
   * The spinloop binary's build-time version string, relayed verbatim from the
   * daemon's /v1/status. Absent when the daemon was unreachable or predates
   * the field — the formatters simply omit the line.
   */
  version?: string;
  /**
   * The instance's retention deadline, RFC 3339: the idle sweep will not
   * terminate it before this. Carried only while the instance's Retain-Until
   * tag is still a time in the future — a passed deadline keeps nothing, so it
   * is dropped here, and an untagged instance has none. It is a property of the
   * cloud instance, not the engine, so it is present whatever the engine's
   * state — a stopped, retained instance still reports it.
   */
  retainUntil?: string;
}
