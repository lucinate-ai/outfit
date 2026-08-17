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
  STOP_RETENTION_MINUTES: '720',
};

const findManagedInstance = vi.fn();
const stopInstance = vi.fn();
const startInstance = vi.fn();
const terminateInstance = vi.fn();
const tagInstance = vi.fn();

vi.mock('../lambda/shared/aws', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lambda/shared/aws')>()),
  findManagedInstance: (...args: unknown[]) => findManagedInstance(...args),
  stopInstance: (...args: unknown[]) => stopInstance(...args),
  startInstance: (...args: unknown[]) => startInstance(...args),
  terminateInstance: (...args: unknown[]) => terminateInstance(...args),
  tagInstance: (...args: unknown[]) => tagInstance(...args),
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
function stopEvent(action?: string, method: 'GET' | 'POST' = 'POST') {
  const query: Record<string, string> = { env: 'dev' };
  if (action) {
    query.action = action;
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
  it('terminates a running instance', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });

    const body = bodyOf(await handler(stopEvent(), {} as Context));
    expect(body.state).toBe('terminating');
    expect(terminateInstance).toHaveBeenCalledWith('i-run');
    expect(stopInstance).not.toHaveBeenCalled();
    expect(tagInstance).not.toHaveBeenCalled();
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
  it('records the stop time and stops a running instance', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });

    const body = bodyOf(await handler(stopEvent('pause'), {} as Context));
    expect(body.state).toBe('stopping');
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

    const body = bodyOf(await handler(stopEvent('pause'), {} as Context));
    expect(body.state).toBe('stopped');
    expect(stopInstance).not.toHaveBeenCalled();
    expect(terminateInstance).not.toHaveBeenCalled();
    expect(tagInstance).not.toHaveBeenCalled();
  });

  it('self-heals the stop time of an already-stopped instance that lacks it', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-off', state: 'stopped' });

    const body = bodyOf(await handler(stopEvent('pause'), {} as Context));
    expect(body.state).toBe('stopped');
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
    expect(stopInstance).not.toHaveBeenCalled();
    expect(terminateInstance).not.toHaveBeenCalled();
  });
});

describe('manual status (GET)', () => {
  it('reports the stopped state of a re-wakeable instance', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-off', state: 'stopped' });

    const body = bodyOf(await handler(stopEvent(undefined, 'GET'), {} as Context));
    expect(body.state).toBe('stopped');
  });
});
