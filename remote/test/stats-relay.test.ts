import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import { DAEMON_UNREACHABLE } from '../lambda/shared/daemon';

// The stats Lambda is a relay: everything measured comes from the instance's
// daemon, and the control plane adds only what it alone knows. These tests
// cover the activity pair making that hop intact, and staying absent when the
// daemon has nothing to say.

const LAMBDA_ENV = {
  TAG_KEY: 'cloud-vm-llm:managed',
  TAG_VALUE: 'true',
  AWS_REGION: 'us-east-1',
};

const findManagedInstance = vi.fn();
const runShellCommand = vi.fn();
const readDeployConfig = vi.fn();

vi.mock('../lambda/shared/aws', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lambda/shared/aws')>()),
  findManagedInstance: (...args: unknown[]) => findManagedInstance(...args),
  runShellCommand: (...args: unknown[]) => runShellCommand(...args),
  readDeployConfig: (...args: unknown[]) => readDeployConfig(...args),
}));

let handler: (event: LambdaFunctionURLEvent) => Promise<LambdaFunctionURLResult>;

beforeAll(async () => {
  Object.assign(process.env, LAMBDA_ENV);
  ({ handler } = await import('../lambda/stats/index'));
});

const statsEvent = { queryStringParameters: { env: 'dev' } } as unknown as LambdaFunctionURLEvent;

/** Narrow the result union to the structured reply these handlers return. */
function structured(result: LambdaFunctionURLResult): { statusCode: number; body: string } {
  return result as { statusCode: number; body: string };
}

function bodyOf(result: LambdaFunctionURLResult): Record<string, unknown> {
  return JSON.parse(structured(result).body);
}

beforeEach(() => {
  vi.clearAllMocks();
  readDeployConfig.mockResolvedValue({ runner: 'llamacpp', modelId: 'org/model' });
  findManagedInstance.mockResolvedValue({
    instanceId: 'i-abc',
    state: 'running',
    instanceType: 'g6e.xlarge',
    launchTime: new Date(),
  });
});

describe('stats relays the daemon’s activity record', () => {
  it('carries lastActiveAt and idleSeconds through unchanged', () => {
    runShellCommand.mockResolvedValue({
      status: 'Success',
      stdout: JSON.stringify({
        state: 'running',
        tokens: { running: 1, counter: 10, promptTokens: 5, generationTokens: 5, requests: 2 },
        lastActiveAt: '2026-08-09T12:00:00Z',
        idleSeconds: 42,
      }),
    });

    return handler(statsEvent).then((result) => {
      const body = bodyOf(result);
      expect(body.lastActiveAt).toBe('2026-08-09T12:00:00Z');
      expect(body.idleSeconds).toBe(42);
      expect(body.tokens).toBeDefined();
    });
  });

  it('leaves them absent when the engine has done no work', async () => {
    runShellCommand.mockResolvedValue({
      status: 'Success',
      stdout: JSON.stringify({ state: 'running', cpu: { utilization: 5 } }),
    });

    const body = bodyOf(await handler(statsEvent));
    expect(body).not.toHaveProperty('lastActiveAt');
    expect(body).not.toHaveProperty('idleSeconds');
    expect(body.cpu).toBeDefined();
  });

  it('leaves them absent when the daemon is unreachable', async () => {
    runShellCommand.mockResolvedValue({ status: 'Success', stdout: `${DAEMON_UNREACHABLE}\n` });

    const result = await handler(statsEvent);
    expect(structured(result).statusCode).toBe(200);
    const body = bodyOf(result);
    expect(body).not.toHaveProperty('lastActiveAt');
    expect(body.errors).toContain('daemon: unreachable or unrecognisable metrics reply');
  });

  it('reports nothing measured for a stopped instance', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-abc', state: 'stopped' });

    const body = bodyOf(await handler(statsEvent));
    expect(body.state).toBe('stopped');
    expect(body).not.toHaveProperty('lastActiveAt');
    // Reaching the daemon needs a running box.
    expect(runShellCommand).not.toHaveBeenCalled();
  });
});
