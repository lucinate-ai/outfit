import { describe, expect, it } from 'vitest';
import {
  DAEMON_METRICS_CMD,
  DAEMON_UNREACHABLE,
  parseDaemonMetrics,
} from '../lambda/shared/daemon';

// A representative /v1/metrics reply from the on-instance outfit daemon —
// the Go side's metrics.Stats shape.
const daemonReply = JSON.stringify({
  state: 'running',
  runner: 'llamacpp',
  modelId: '/opt/llm/model/model.gguf',
  uptimeSeconds: 123,
  tokens: { running: 2, counter: 6020, promptTokens: 4096, generationTokens: 1024, requests: 17 },
  gpus: [
    {
      index: 0,
      name: 'NVIDIA L40S',
      utilization: 12,
      memoryUsed: 8589934592,
      memoryTotal: 48318382080,
      temperature: 42,
    },
  ],
  cpu: { utilization: 30 },
  memory: { total: 33020416512, used: 4294967296 },
});

describe('parseDaemonMetrics', () => {
  it('parses a daemon metrics reply', () => {
    const parsed = parseDaemonMetrics(daemonReply);
    expect(parsed).not.toBeNull();
    expect(parsed!.state).toBe('running');
    expect(parsed!.tokens).toEqual({
      running: 2,
      counter: 6020,
      promptTokens: 4096,
      generationTokens: 1024,
      requests: 17,
    });
    expect(parsed!.gpus).toHaveLength(1);
    expect(parsed!.gpus![0].memoryTotal).toBe(48318382080);
    expect(parsed!.cpu!.utilization).toBe(30);
    expect(parsed!.memory!.used).toBe(4294967296);
  });

  it('parses a reply with omitted stats (absent sources stay absent)', () => {
    const parsed = parseDaemonMetrics(JSON.stringify({ state: 'running' }));
    expect(parsed).not.toBeNull();
    expect(parsed!.tokens).toBeUndefined();
    expect(parsed!.gpus).toBeUndefined();
  });

  it('returns null for the unreachable marker', () => {
    expect(parseDaemonMetrics(`${DAEMON_UNREACHABLE}\n`)).toBeNull();
  });

  it('returns null for empty output', () => {
    expect(parseDaemonMetrics('')).toBeNull();
  });

  it('returns null for non-JSON output', () => {
    expect(parseDaemonMetrics('curl: (7) Failed to connect')).toBeNull();
  });

  it('returns null for JSON that is not a daemon reply', () => {
    expect(parseDaemonMetrics('42')).toBeNull();
    expect(parseDaemonMetrics('{"error":"missing bearer token"}')).toBeNull();
  });
});

describe('DAEMON_METRICS_CMD', () => {
  it('curls the loopback daemon and marks failure', () => {
    expect(DAEMON_METRICS_CMD).toContain('http://127.0.0.1:4242/v1/metrics');
    expect(DAEMON_METRICS_CMD).toContain(DAEMON_UNREACHABLE);
  });
});
