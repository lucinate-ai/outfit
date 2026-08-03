import { describe, expect, it } from 'vitest';
import {
  buildTokenStats,
  metricsCurlCommand,
  NVIDIA_SMI_CMD,
  parseCpuStat,
  parseGpuStats,
  parseMemoryStat,
  VMSTAT_CMD,
  FREE_CMD,
  type GpuStat,
  type MemoryStat,
} from '../lambda/shared/stats';
import { parseMetrics } from '../lambda/shared/idle';

describe('parseGpuStats', () => {
  it('parses a single GPU', () => {
    const gpus = parseGpuStats('0, NVIDIA L40S, 12, 8589934592, 48318382080, 42');
    expect(gpus).toHaveLength(1);
    expect(gpus[0]).toEqual<GpuStat>({
      index: 0,
      name: 'NVIDIA L40S',
      utilization: 12,
      memoryUsed: 8589934592,
      memoryTotal: 48318382080,
      temperature: 42,
    });
  });

  it('parses multiple GPUs', () => {
    const stdout = [
      '0, NVIDIA L40S, 12, 8589934592, 48318382080, 42',
      '1, NVIDIA L40S, 8, 4294967296, 48318382080, 38',
    ].join('\n');
    const gpus = parseGpuStats(stdout);
    expect(gpus).toHaveLength(2);
    expect(gpus[0].index).toBe(0);
    expect(gpus[1].index).toBe(1);
    expect(gpus[1].utilization).toBe(8);
  });

  it('returns empty array for empty input', () => {
    expect(parseGpuStats('')).toEqual([]);
  });

  it('skips malformed lines', () => {
    const gpus = parseGpuStats('bad line\n0, NVIDIA L40S, 12, 8589934592, 48318382080, 42');
    expect(gpus).toHaveLength(1);
    expect(gpus[0].index).toBe(0);
  });

  it('handles GPU with special chars in name', () => {
    const gpus = parseGpuStats('0, NVIDIA A100-SXM4-80GB, 95, 40000000000, 81504000000, 72');
    expect(gpus).toHaveLength(1);
    expect(gpus[0].name).toBe('NVIDIA A100-SXM4-80GB');
  });
});

describe('parseCpuStat', () => {
  it('parses a vmstat line', () => {
    // Standard vmstat output: r b swpd free buff cache si so bi bo in cs us sy id wa st
    const stdout = '0 0 0 8589934592 1073741824 16106127360 0 0 1024 2048 1500 3000 25 10 60 3 2';
    const cpu = parseCpuStat(stdout);
    expect(cpu).not.toBeNull();
    expect(cpu!.utilization).toBe(40); // 100 - 60(idle)
  });

  it('handles multi-line vmstat output', () => {
    const stdout = [
      'procs -----------memory---------- ---swap-- -----io---- -system-- ------cpu-----',
      ' r  b   swpd   free   buff  cache   si   so    bi    bo   in   cs us sy id wa st',
      ' 0 0 0 8000000000 1000000000 15000000000 0 0 512 1024 1000 2000 20 5 70 3 2',
      ' 1 0 0 7500000000 1000000000 15000000000 0 0 1024 2048 1500 3000 30 15 50 3 2',
    ].join('\n');
    const cpu = parseCpuStat(stdout);
    expect(cpu).not.toBeNull();
    expect(cpu!.utilization).toBe(50); // 100 - 50(idle)
  });

  it('returns null for empty input', () => {
    expect(parseCpuStat('')).toBeNull();
  });

  it('returns null for short lines', () => {
    expect(parseCpuStat('0 0 0')).toBeNull();
  });
});

describe('parseMemoryStat', () => {
  it('parses a free -b line', () => {
    const stdout = [
      '               total        used        free      shared  buff/cache   available',
      'Mem:   33020416512   4294967296  12884901888   536870912  15840547328  26424576512',
      'Swap:   17179869184          0  17179869184',
    ].join('\n');
    const mem = parseMemoryStat(stdout);
    expect(mem).not.toBeNull();
    expect(mem!).toEqual<MemoryStat>({
      total: 33020416512,
      used: 4294967296,
    });
  });

  it('returns null for empty input', () => {
    expect(parseMemoryStat('')).toBeNull();
  });

  it('returns null when Mem line is missing', () => {
    expect(parseMemoryStat('Swap:   17179869184          0  17179869184')).toBeNull();
  });
});

describe('buildTokenStats', () => {
  const VLLM_SCRAPE = [
    'vllm:num_requests_running{model_name="m"} 2.0',
    'vllm:num_requests_waiting{model_name="m"} 1.0',
    'vllm:prompt_tokens_total{model_name="m"} 1000.0',
    'vllm:generation_tokens_total{model_name="m"} 500.0',
    'vllm:request_success_total{model_name="m",finished_reason="stop"} 10.0',
  ].join('\n');

  const LLAMACPP_SCRAPE = [
    'llamacpp:requests_processing 1',
    'llamacpp:requests_deferred 0',
    'llamacpp:prompt_tokens_total 2000',
    'llamacpp:tokens_predicted_total 800',
    'llamacpp:n_decode_total 400',
  ].join('\n');

  it('builds vllm token stats', () => {
    const metrics = parseMetrics(VLLM_SCRAPE, 'vllm');
    const stats = buildTokenStats(metrics, VLLM_SCRAPE, 'vllm');
    expect(stats).not.toBeNull();
    expect(stats!.running).toBe(3); // 2 running + 1 waiting
    expect(stats!.promptTokens).toBe(1000);
    expect(stats!.generationTokens).toBe(500);
    expect(stats!.requests).toBe(10);
  });

  it('builds llamacpp token stats', () => {
    const metrics = parseMetrics(LLAMACPP_SCRAPE, 'llamacpp');
    const stats = buildTokenStats(metrics, LLAMACPP_SCRAPE, 'llamacpp');
    expect(stats).not.toBeNull();
    expect(stats!.running).toBe(1);
    expect(stats!.promptTokens).toBe(2000);
    expect(stats!.generationTokens).toBe(800);
    expect(stats!.requests).toBe(0); // llamacpp has no request_success_total
  });

  it('returns null for failed metrics', () => {
    const stats = buildTokenStats({ ok: false }, '', 'vllm');
    expect(stats).toBeNull();
  });
});

describe('metricsCurlCommand', () => {
  it('reads API key from file for llamacpp', () => {
    const cmd = metricsCurlCommand('llamacpp', 8000);
    expect(cmd).toContain('-H "Authorization: Bearer $(cat /etc/llm/api-key)"');
    expect(cmd).toContain('localhost:8000/metrics');
    expect(cmd).toContain('llamacpp:');
  });

  it('omits auth header for vllm', () => {
    const cmd = metricsCurlCommand('vllm', 8000);
    expect(cmd).not.toContain('Authorization');
    expect(cmd).toContain('localhost:8000/metrics');
    expect(cmd).toContain('vllm:');
  });
});

describe('commands', () => {
  it('has the expected nvidia-smi command', () => {
    expect(NVIDIA_SMI_CMD).toContain('--query-gpu=');
    expect(NVIDIA_SMI_CMD).toContain('utilization.gpu');
    expect(NVIDIA_SMI_CMD).toContain('--format=csv,noheader,nounits');
  });

  it('has the expected vmstat command', () => {
    expect(VMSTAT_CMD).toBe('vmstat 1 2 | tail -1');
  });

  it('has the expected free command', () => {
    expect(FREE_CMD).toBe('free -b');
  });
});
