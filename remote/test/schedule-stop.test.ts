import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ScheduledRunEvent } from '../lambda/shared/schedules';

// A `stop` schedule firing pauses the running instance, never terminates it,
// and does nothing when there is no running instance or the instance is still
// under a retention deadline. All AWS calls are stubbed.

const LAMBDA_ENV = {
  TAG_KEY: 'cloud-vm-llm:managed',
  TAG_VALUE: 'true',
  IDLE_THRESHOLD_MINUTES: '15',
  GRACE_PERIOD_MINUTES: '10',
  MAX_RUNTIME_MINUTES: '240',
  STOP_RETENTION_MINUTES: '60',
  MAX_SEED_MINUTES: '60',
  SEED_STALL_MINUTES: '10',
};

const findManagedInstance = vi.fn();
const stopEngineDaemon = vi.fn();
const stopInstance = vi.fn();
const terminateInstance = vi.fn();
const tagInstance = vi.fn();

vi.mock('../lambda/shared/aws', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lambda/shared/aws')>()),
  findManagedInstance: (...args: unknown[]) => findManagedInstance(...args),
  stopEngineDaemon: (...args: unknown[]) => stopEngineDaemon(...args),
  stopInstance: (...args: unknown[]) => stopInstance(...args),
  terminateInstance: (...args: unknown[]) => terminateInstance(...args),
  tagInstance: (...args: unknown[]) => tagInstance(...args),
}));

let handler: (event: ScheduledRunEvent) => Promise<unknown>;

beforeAll(async () => {
  Object.assign(process.env, LAMBDA_ENV);
  handler = (await import('../lambda/stop/index')).handler as typeof handler;
});

function runEvent(action: 'start' | 'stop' = 'stop'): ScheduledRunEvent {
  return { source: 'spinloop.schedule', action, environment: 'dev' };
}

beforeEach(() => {
  vi.clearAllMocks();
  stopEngineDaemon.mockResolvedValue(undefined);
});

describe('a scheduled stop', () => {
  it('pauses a running instance without terminating it', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });

    await handler(runEvent());

    expect(findManagedInstance).toHaveBeenCalledWith('cloud-vm-llm:managed', 'true', [
      { Name: 'tag:cloud-vm-llm:env', Values: ['dev'] },
    ]);
    expect(tagInstance).toHaveBeenCalledWith('i-run', 'Stopped-At', expect.any(String));
    expect(stopEngineDaemon).toHaveBeenCalledWith('i-run');
    expect(stopInstance).toHaveBeenCalledWith('i-run');
    expect(terminateInstance).not.toHaveBeenCalled();
  });

  it('pauses an instance whose retention deadline has passed', async () => {
    findManagedInstance.mockResolvedValue({
      instanceId: 'i-run',
      state: 'running',
      retainUntil: new Date(Date.now() - 60_000),
    });

    await handler(runEvent());

    expect(stopInstance).toHaveBeenCalledWith('i-run');
  });

  it('skips an instance that is still retained', async () => {
    findManagedInstance.mockResolvedValue({
      instanceId: 'i-run',
      state: 'running',
      retainUntil: new Date(Date.now() + 3_600_000),
    });

    await handler(runEvent());

    expect(stopInstance).not.toHaveBeenCalled();
    expect(terminateInstance).not.toHaveBeenCalled();
    expect(tagInstance).not.toHaveBeenCalled();
  });

  it('does nothing when there is no instance', async () => {
    findManagedInstance.mockResolvedValue(undefined);

    await expect(handler(runEvent())).resolves.toBeUndefined();

    expect(stopInstance).not.toHaveBeenCalled();
    expect(terminateInstance).not.toHaveBeenCalled();
  });

  it.each(['stopped', 'stopping', 'pending'])('does nothing when the instance is %s', async (state) => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-x', state });

    await handler(runEvent());

    expect(stopInstance).not.toHaveBeenCalled();
    expect(terminateInstance).not.toHaveBeenCalled();
  });

  it('ignores an event whose action is not stop', async () => {
    await handler(runEvent('start'));

    expect(findManagedInstance).not.toHaveBeenCalled();
    expect(stopInstance).not.toHaveBeenCalled();
  });
});
