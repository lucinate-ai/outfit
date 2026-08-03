/**
 * Pure metric-parsing logic for the stats handler. Kept free of AWS calls so
 * it can be unit tested.
 */

import { metricsGrepPattern, type MetricsResult } from './idle';
import type { Runner } from './deploy-config';

// ---------- GPU ----------

export interface GpuStat {
  index: number;
  name: string;
  utilization: number; // percent
  memoryUsed: number; // bytes
  memoryTotal: number; // bytes
  temperature: number; // celsius
}

/**
 * Parse nvidia-smi CSV output for per-GPU stats. The command used is:
 *   nvidia-smi --query-gpu=index,name,utilization.gpu,memory.used,memory.total,temperature.gpu --format=csv,noheader,nounits
 * which produces lines like:
 *   0, NVIDIA L40S, 12, 8589934592, 48318382080, 42
 */
export function parseGpuStats(stdout: string): GpuStat[] {
  const gpus: GpuStat[] = [];
  for (const line of stdout.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed) {
      continue;
    }
    const parts = trimmed.split(',').map((s) => s.trim());
    if (parts.length < 6) {
      continue;
    }
    gpus.push({
      index: parseInt(parts[0], 10),
      name: parts[1],
      utilization: parseInt(parts[2], 10) || 0,
      memoryUsed: parseInt(parts[3], 10) || 0,
      memoryTotal: parseInt(parts[4], 10) || 0,
      temperature: parseInt(parts[5], 10) || 0,
    });
  }
  return gpus;
}

// ---------- CPU ----------

export interface CpuStat {
  /** Percent CPU idle (from vmstat us+sy+id+wa+hi+si+st = 100; we report 100 - idle). */
  utilization: number; // percent
}

/**
 * Parse the last line of `vmstat 1 2` output. Columns are:
 *   procs -----------memory---------- ---swap-- -----io---- -system-- ------cpu-----
 *   r  b   swpd   free   buff  cache   si   so    bi    bo   in   cs us sy id wa st
 * The us+sy+id+wa+st columns give CPU breakdown (last 5 fields). Utilization is
 * 100 - id (idle).
 */
export function parseCpuStat(stdout: string): CpuStat | null {
  const lines = stdout.trim().split('\n');
  // vmstat 1 2 produces 3 lines: header, first sample (ignored), second sample.
  // tail -1 gives us the last line.
  const lastLine = lines[lines.length - 1].trim();
  if (!lastLine) {
    return null;
  }
  const fields = lastLine.split(/\s+/);
  if (fields.length < 15) {
    return null;
  }
  // CPU columns: us sy id wa st are the last 5 fields before any extras.
  // In standard vmstat, positions are: r b swpd free buff cache si so bi bo in cs us sy id wa st
  // That's 17 fields. us=index 13, sy=14, id=15, wa=16, st=17 (1-based).
  // 0-based: us=12, sy=13, id=14, wa=15, st=16
  const id = parseInt(fields[14], 10);
  if (isNaN(id)) {
    return null;
  }
  return {
    utilization: Math.max(0, 100 - id),
  };
}

// ---------- Memory ----------

export interface MemoryStat {
  total: number; // bytes
  used: number; // bytes
}

/**
 * Parse `free -b` output. Produces:
 *               total        used        free      shared  buff/cache   available
 * Mem:   33020416512   4294967296  12884901888      ...    ...         ...
 * Swap:   ...
 * We read the "Mem:" line.
 */
export function parseMemoryStat(stdout: string): MemoryStat | null {
  for (const line of stdout.split('\n')) {
    if (line.startsWith('Mem:')) {
      const fields = line.split(/\s+/);
      // fields[1] = total, fields[2] = used
      const total = parseInt(fields[1], 10);
      const used = parseInt(fields[2], 10);
      if (!isNaN(total) && !isNaN(used)) {
        return { total, used };
      }
    }
  }
  return null;
}

// ---------- Aggregate response ----------

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
  /** Token/request metrics from /metrics. */
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

/** Build token stats from a metrics scrape result and raw output for detailed counters. */
export function buildTokenStats(metrics: MetricsResult, stdout: string, runner: Runner): TokenStats | null {
  if (!metrics.ok) {
    return null;
  }

  // Parse individual counters from the raw output for a detailed breakdown.
  const promptTokens = extractCounter(stdout, runner, 'prompt_tokens_total');
  const generationTokens = extractCounter(stdout, runner, runner === 'vllm' ? 'generation_tokens_total' : 'tokens_predicted_total');
  const requests = extractCounter(stdout, runner, 'request_success_total');

  return {
    running: metrics.running,
    counter: metrics.counter,
    promptTokens,
    generationTokens,
    requests,
  };
}

function extractCounter(stdout: string, runner: Runner, name: string): number {
  const prefix = runner === 'vllm' ? 'vllm' : 'llamacpp';
  const re = new RegExp(`^${prefix}:${name}(?:\\{[^}]*\\})?\\s+([0-9.eE+-]+)$`, 'm');
  const m = re.exec(stdout);
  if (!m) return 0;
  const v = Number(m[1]);
  return Number.isFinite(v) ? v : 0;
}

/** Build the nvidia-smi query command. */
export const NVIDIA_SMI_CMD =
  'nvidia-smi --query-gpu=index,name,utilization.gpu,memory.used,memory.total,temperature.gpu --format=csv,noheader,nounits';

/** Build the vmstat command (1 sample, 1-second interval, take last line). */
export const VMSTAT_CMD = 'vmstat 1 2 | tail -1';

/** Build the free command (bytes). */
export const FREE_CMD = 'free -b';

/** Build the metrics curl command for the given runner and port. */
export function metricsCurlCommand(runner: Runner, port: number): string {
  const pattern = metricsGrepPattern(runner);
  if (runner === 'llamacpp') {
    return `curl -s -H "Authorization: Bearer $(cat /etc/llm/api-key)" --max-time 5 http://localhost:${port}/metrics | grep -E '${pattern}'`;
  }
  return `curl -s --max-time 5 http://localhost:${port}/metrics | grep -E '${pattern}'`;
}
