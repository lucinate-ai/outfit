import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Context, LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';

// The Function URL branch of the stop Lambda: GET reports, POST without an
// action terminates, POST with action=pause stops (never terminates). All
// AWS calls are stubbed, so the tests cover the choice of action, not EC2.

const LAMBDA_ENV = {
  TAG_KEY: 'cloud-vm-llm:managed',
  TAG_VALUE: 'true',
  IDLE_THRESHOLD_MINUTES: '15',
  GRACE_PERIOD_MINUTES: '10',
  MAX_RUNTIME_MINUTES: '240',
  STOP_RETENTION_MINUTES: '60',
};

const findManagedInstance = vi.fn();
const findManagedInstances = vi.fn();
const stopEngineDaemon = vi.fn();
const stopInstance = vi.fn();
const startInstance = vi.fn();
const terminateInstance = vi.fn();
const tagInstance = vi.fn();
const isSsmAgentOnline = vi.fn();
const runShellCommand = vi.fn();
const readDeployConfig = vi.fn();

vi.mock('../lambda/shared/aws', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lambda/shared/aws')>()),
  findManagedInstance: (...args: unknown[]) => findManagedInstance(...args),
  findManagedInstances: (...args: unknown[]) => findManagedInstances(...args),
  stopEngineDaemon: (...args: unknown[]) => stopEngineDaemon(...args),
  stopInstance: (...args: unknown[]) => stopInstance(...args),
  startInstance: (...args: unknown[]) => startInstance(...args),
  terminateInstance: (...args: unknown[]) => terminateInstance(...args),
  tagInstance: (...args: unknown[]) => tagInstance(...args),
  isSsmAgentOnline: (...args: unknown[]) => isSsmAgentOnline(...args),
  runShellCommand: (...args: unknown[]) => runShellCommand(...args),
  readDeployConfig: (...args: unknown[]) => readDeployConfig(...args),
}));

let handler: (
  event: LambdaFunctionURLEvent,
  context?: Context,
) => Promise<LambdaFunctionURLResult | void>;

beforeAll(async () => {
  Object.assign(process.env, LAMBDA_ENV);
  ({ handler } = await import('../lambda/stop/index'));
});

/** The stop URL as `outfit remote <verb>` calls it: one environment, an optional mode. */
function stopEvent(action?: string, method: 'GET' | 'POST' = 'POST', force?: boolean) {
  const query: Record<string, string> = { env: 'dev' };
  if (action) {
    query.action = action;
  }
  if (force) {
    query.force = 'true';
  }
  return {
    queryStringParameters: query,
    requestContext: { http: { method } },
  } as unknown as LambdaFunctionURLEvent;
}

function bodyOf(result: LambdaFunctionURLResult | void): Record<string, unknown> {
  return JSON.parse((result as { statusCode: number; body: string }).body);
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('manual stop (terminate)', () => {
  it('stops the engine then terminates the instance', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });
    stopEngineDaemon.mockResolvedValue(true);

    const body = bodyOf(await handler(stopEvent(), {} as Context));
    expect(body.state).toBe('terminating');
    expect(stopEngineDaemon).toHaveBeenCalledWith('i-run');
    expect(terminateInstance).toHaveBeenCalledWith('i-run');
    expect(stopInstance).not.toHaveBeenCalled();
  });

  it('proceeds to terminate even when daemon is unreachable', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });
    stopEngineDaemon.mockResolvedValue(false);

    const body = bodyOf(await handler(stopEvent(), {} as Context));
    expect(body.state).toBe('terminating');
    expect(stopEngineDaemon).toHaveBeenCalledWith('i-run');
    expect(terminateInstance).toHaveBeenCalledWith('i-run');
  });

  it('does nothing when no instance exists', async () => {
    findManagedInstance.mockResolvedValue(null);

    const body = bodyOf(await handler(stopEvent(), {} as Context));
    expect(body.state).toBe('stopped');
    expect(terminateInstance).not.toHaveBeenCalled();
    expect(stopInstance).not.toHaveBeenCalled();
  });
});

describe('manual pause (stop, never terminate)', () => {
  it('records the stop time, stops the engine, then stops the instance', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });
    stopEngineDaemon.mockResolvedValue(true);

    const body = bodyOf(await handler(stopEvent('pause'), {} as Context));
    expect(body.state).toBe('stopping');
    expect(stopEngineDaemon).toHaveBeenCalledWith('i-run');
    expect(stopInstance).toHaveBeenCalledWith('i-run');
    expect(terminateInstance).not.toHaveBeenCalled();
    expect(tagInstance).toHaveBeenCalledWith(
      'i-run',
      'Stopped-At',
      expect.stringMatching(/^\d{4}-\d{2}-\d{2}T/),
    );
  });

  it('proceeds to stop instance even when daemon is unreachable', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });
    stopEngineDaemon.mockResolvedValue(false);

    const body = bodyOf(await handler(stopEvent('pause'), {} as Context));
    expect(body.state).toBe('stopping');
    expect(stopEngineDaemon).toHaveBeenCalledWith('i-run');
    expect(stopInstance).toHaveBeenCalledWith('i-run');
  });

  it('is a noop for an already-stopped instance whose stop time is recorded', async () => {
    findManagedInstance.mockResolvedValue({
      instanceId: 'i-off',
      state: 'stopped',
      stoppedAt: new Date('2026-08-17T10:00:00Z'),
    });

    const body = bodyOf(await handler(stopEvent('pause'), {} as Context));
    expect(body.state).toBe('stopped');
    expect(stopEngineDaemon).not.toHaveBeenCalled();
    expect(stopInstance).not.toHaveBeenCalled();
    expect(terminateInstance).not.toHaveBeenCalled();
    expect(tagInstance).not.toHaveBeenCalled();
  });

  it('self-heals the stop time of an already-stopped instance that lacks it', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-off', state: 'stopped' });

    const body = bodyOf(await handler(stopEvent('pause'), {} as Context));
    expect(body.state).toBe('stopped');
    expect(stopEngineDaemon).not.toHaveBeenCalled();
    expect(stopInstance).not.toHaveBeenCalled();
    expect(terminateInstance).not.toHaveBeenCalled();
    expect(tagInstance).toHaveBeenCalledWith(
      'i-off',
      'Stopped-At',
      expect.stringMatching(/^\d{4}-\d{2}-\d{2}T/),
    );
  });

  it('is a noop when no instance exists', async () => {
    findManagedInstance.mockResolvedValue(null);

    const body = bodyOf(await handler(stopEvent('pause'), {} as Context));
    expect(body.state).toBe('stopped');
    expect(stopEngineDaemon).not.toHaveBeenCalled();
    expect(stopInstance).not.toHaveBeenCalled();
    expect(terminateInstance).not.toHaveBeenCalled();
  });
});

describe('manual stop (forced)', () => {
  it('terminates without stopping the engine first', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });

    const body = bodyOf(await handler(stopEvent(undefined, 'POST', true), {} as Context));
    expect(body.state).toBe('terminating');
    expect(stopEngineDaemon).not.toHaveBeenCalled();
    expect(terminateInstance).toHaveBeenCalledWith('i-run');
    expect(stopInstance).not.toHaveBeenCalled();
  });

  it('records the stop time and stops the instance without stopping the engine', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });

    const body = bodyOf(await handler(stopEvent('pause', 'POST', true), {} as Context));
    expect(body.state).toBe('stopping');
    expect(stopEngineDaemon).not.toHaveBeenCalled();
    expect(stopInstance).toHaveBeenCalledWith('i-run');
    expect(terminateInstance).not.toHaveBeenCalled();
    expect(tagInstance).toHaveBeenCalledWith(
      'i-run',
      'Stopped-At',
      expect.stringMatching(/^\d{4}-\d{2}-\d{2}T/),
    );
  });

  it('is a noop for an already-stopped instance whose stop time is recorded', async () => {
    findManagedInstance.mockResolvedValue({
      instanceId: 'i-off',
      state: 'stopped',
      stoppedAt: new Date('2026-08-17T10:00:00Z'),
    });

    const body = bodyOf(await handler(stopEvent('pause', 'POST', true), {} as Context));
    expect(body.state).toBe('stopped');
    expect(stopEngineDaemon).not.toHaveBeenCalled();
    expect(stopInstance).not.toHaveBeenCalled();
    expect(terminateInstance).not.toHaveBeenCalled();
    expect(tagInstance).not.toHaveBeenCalled();
  });
});

describe('manual status (GET)', () => {
  it('reports the stopped state of a re-wakeable instance', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-off', state: 'stopped' });

    const body = bodyOf(await handler(stopEvent(undefined, 'GET'), {} as Context));
    expect(body.state).toBe('stopped');
  });
});

describe('idle sweep (stop running instance)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    readDeployConfig.mockResolvedValue({ runner: 'llamacpp' } as never);
  });

  it('stops the engine then stops the instance when idle', async () => {
    findManagedInstances.mockResolvedValue([
      {
        instanceId: 'i-idle',
        state: 'running',
        environment: 'dev',
        launchTime: new Date(Date.now() - 30 * 60 * 1000),
      },
    ]);
    isSsmAgentOnline.mockResolvedValue(true);
    runShellCommand.mockResolvedValue({
      status: 'Success',
      stdout: JSON.stringify({ state: 'running', lastActiveAt: new Date(Date.now() - 20 * 60 * 1000).toISOString(), idleSeconds: 1200 }),
    });
    stopEngineDaemon.mockResolvedValue(true);

    // Trigger idle sweep via scheduled event
    await handler({ source: 'aws.events' } as never);

    expect(stopEngineDaemon).toHaveBeenCalledWith('i-idle');
    expect(stopInstance).toHaveBeenCalledWith('i-idle');
    expect(terminateInstance).not.toHaveBeenCalled();
  });

  it('proceeds to stop instance even when daemon is unreachable during idle sweep', async () => {
    findManagedInstances.mockResolvedValue([
      {
        instanceId: 'i-idle',
        state: 'running',
        environment: 'dev',
        launchTime: new Date(Date.now() - 30 * 60 * 1000),
      },
    ]);
    isSsmAgentOnline.mockResolvedValue(true);
    runShellCommand.mockResolvedValue({
      status: 'Success',
      stdout: JSON.stringify({ state: 'running', lastActiveAt: new Date(Date.now() - 20 * 60 * 1000).toISOString(), idleSeconds: 1200 }),
    });
    stopEngineDaemon.mockResolvedValue(false);

    await handler({ source: 'aws.events' } as never);

    expect(stopEngineDaemon).toHaveBeenCalledWith('i-idle');
    expect(stopInstance).toHaveBeenCalledWith('i-idle');
  });
});
