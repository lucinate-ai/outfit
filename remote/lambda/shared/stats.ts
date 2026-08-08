/**
 * The stats response types the control plane speaks. Collection itself lives
 * in the on-instance outfit daemon (outfit's internal/metrics — the Go port
 * of the parsers that used to live here); the stats Lambda merges the
 * daemon's reply with what only the control plane knows. These shapes are
 * value-compatible with the Go side, so `outfit remote metrics` renders them
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
  /** Total successful requests. */
  requests: number;
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
  /** Any errors encountered while collecting metrics. */
  errors?: string[];
}
