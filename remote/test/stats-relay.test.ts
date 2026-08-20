import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import { DAEMON_STATUS_CMD, DAEMON_UNREACHABLE } from '../lambda/shared/daemon';

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

// The handler reaches the daemon twice — metrics and status — so the stub
// answers each command separately, the way the start-status tests do.
function stubDaemon(metrics: string, status = JSON.stringify({ state: 'running' })) {
  runShellCommand.mockImplementation((_instanceId, command) =>
    Promise.resolve({
      status: 'Success',
      stdout: command === DAEMON_STATUS_CMD ? status : metrics,
    }),
  );
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
    stubDaemon(
      JSON.stringify({
        state: 'running',
        tokens: { running: 1, counter: 10, promptTokens: 5, generationTokens: 5, requests: 2 },
        lastActiveAt: '2026-08-09T12:00:00Z',
        idleSeconds: 42,
      }),
    );

    return handler(statsEvent).then((result) => {
      const body = bodyOf(result);
      expect(body.lastActiveAt).toBe('2026-08-09T12:00:00Z');
      expect(body.idleSeconds).toBe(42);
      expect(body.tokens).toBeDefined();
    });
  });

  it('leaves them absent when the engine has done no work', async () => {
    stubDaemon(JSON.stringify({ state: 'running', cpu: { utilization: 5 } }));

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
    expect(body).not.toHaveProperty('version');
    // Reaching the daemon needs a running box.
    expect(runShellCommand).not.toHaveBeenCalled();
  });

  it('reports the error when the metrics scrape throws', async () => {
    runShellCommand.mockImplementation((_instanceId, command) =>
      command === DAEMON_STATUS_CMD
        ? Promise.resolve({ status: 'Success', stdout: JSON.stringify({ state: 'idle' }) })
        : Promise.reject(new Error('ssm: access denied')),
    );

    const result = await handler(statsEvent);
    expect(structured(result).statusCode).toBe(200);
    const body = bodyOf(result);
    expect(body.errors).toContain('daemon: Error');
    expect(body).not.toHaveProperty('version');
  });
});

describe('stats relays the daemon’s version', () => {
  it('carries it through unchanged', async () => {
    stubDaemon(
      JSON.stringify({ state: 'running', cpu: { utilization: 5 } }),
      JSON.stringify({ state: 'running', version: '1.18.0' }),
    );

    const body = bodyOf(await handler(statsEvent));
    expect(body.version).toBe('1.18.0');
    expect(body.cpu).toBeDefined();
  });

  it('leaves it absent when the daemon reports none', async () => {
    stubDaemon(JSON.stringify({ state: 'running', cpu: { utilization: 5 } }));

    const body = bodyOf(await handler(statsEvent));
    expect(body).not.toHaveProperty('version');
    expect(body.cpu).toBeDefined();
  });

  it('leaves it absent without an error when only the status scrape fails', async () => {
    runShellCommand.mockImplementation((_instanceId, command) =>
      Promise.resolve({
        status: 'Success',
        stdout:
          command === DAEMON_STATUS_CMD
            ? `${DAEMON_UNREACHABLE}\n`
            : JSON.stringify({ state: 'running', cpu: { utilization: 5 } }),
      }),
    );

    const result = await handler(statsEvent);
    expect(structured(result).statusCode).toBe(200);
    const body = bodyOf(result);
    expect(body).not.toHaveProperty('version');
    expect(body.cpu).toBeDefined();
    // The version is an add-on, not a health signal — the metrics reply is
    // still fully reported.
    expect(body.errors).toBeUndefined();
  });

  it('leaves it absent when the daemon is unreachable', async () => {
    runShellCommand.mockResolvedValue({ status: 'Success', stdout: `${DAEMON_UNREACHABLE}\n` });

    const body = bodyOf(await handler(statsEvent));
    expect(body).not.toHaveProperty('version');
    // Both scrapes failing is already covered by the metrics error entry.
    expect(body.errors).toContain('daemon: unreachable or unrecognisable metrics reply');
  });

  it('leaves it absent when the status scrape ends in failure', async () => {
    runShellCommand.mockImplementation((_instanceId, command) =>
      Promise.resolve({
        status: command === DAEMON_STATUS_CMD ? 'Failed' : 'Success',
        stdout: command === DAEMON_STATUS_CMD ? '' : JSON.stringify({ state: 'running', cpu: { utilization: 5 } }),
      }),
    );

    const result = await handler(statsEvent);
    expect(structured(result).statusCode).toBe(200);
    const body = bodyOf(result);
    expect(body).not.toHaveProperty('version');
    expect(body.cpu).toBeDefined();
    expect(body.errors).toBeUndefined();
  });

  it('leaves it absent without an error when the status scrape throws', async () => {
    const log = vi.spyOn(console, 'log').mockImplementation(() => {});
    runShellCommand.mockImplementation((_instanceId, command) =>
      command === DAEMON_STATUS_CMD
        ? Promise.reject(new Error('ssm timeout'))
        : Promise.resolve({ status: 'Success', stdout: JSON.stringify({ state: 'running', cpu: { utilization: 5 } }) }),
    );

    try {
      const result = await handler(statsEvent);
      expect(structured(result).statusCode).toBe(200);
      const body = bodyOf(result);
      expect(body).not.toHaveProperty('version');
      expect(body.cpu).toBeDefined();
      expect(body.errors).toBeUndefined();
      // The throw is logged for the operator, never turned into a reply error.
      expect(log).toHaveBeenCalledWith(
        expect.stringContaining('"phase":"daemon-version"'),
      );
    } finally {
      log.mockRestore();
    }
  });

  it('leaves it absent when the daemon reports an empty version', async () => {
    stubDaemon(
      JSON.stringify({ state: 'running', cpu: { utilization: 5 } }),
      JSON.stringify({ state: 'running', version: '' }),
    );

    const body = bodyOf(await handler(statsEvent));
    expect(body).not.toHaveProperty('version');
    expect(body.cpu).toBeDefined();
  });
});
