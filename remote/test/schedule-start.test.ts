import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Context, LambdaFunctionURLResult } from 'aws-lambda';
import { DAEMON_STATUS_CMD } from '../lambda/shared/daemon';
import type { ScheduledRunEvent } from '../lambda/shared/schedules';

// A `start` schedule firing reaches the same wake as an on-demand start: a
// stopped instance is re-woken, a running one is left alone. All AWS calls are
// stubbed.

const LAMBDA_ENV = {
  TAG_KEY: 'cloud-vm-llm:managed',
  TAG_VALUE: 'true',
  ENGINE_PORT: '8000',
  AMI_ROLE_TAG_KEY: 'cloud-vm-llm:role',
  AMI_ROLE_TAG_VALUE: 'runtime-ami',
  AMI_RUNNER_TAG_KEY: 'cloud-vm-llm:runner',
  INSTANCE_TYPE: 'g6e.xlarge',
  SUBNET_IDS: 'subnet-test',
  INSTANCE_PROFILE_ARN: 'arn:aws:iam::0:instance-profile/test',
  WEIGHTS_BUCKET: 'test-bucket',
  MAX_CONCURRENT_SEEDS: '2',
  AWS_REGION: 'us-east-1',
  BOOT_LOG_GROUP: '/test/boot',
  LLAMACPP_LOG_GROUP: '/test/llamacpp',
  VLLM_LOG_GROUP: '/test/vllm',
};

const findManagedInstance = vi.fn();
const getInstance = vi.fn();
const startEngineDaemon = vi.fn();
const startInstance = vi.fn();
const runInstance = vi.fn();
const tagInstance = vi.fn();
const isSsmAgentOnline = vi.fn();
const runShellCommand = vi.fn();
const readDeployConfig = vi.fn();
const findEnvEip = vi.fn();
const findEnvSecurityGroup = vi.fn();
const readEnvApiKey = vi.fn();

vi.mock('../lambda/shared/aws', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lambda/shared/aws')>()),
  findManagedInstance: (...args: unknown[]) => findManagedInstance(...args),
  getInstance: (...args: unknown[]) => getInstance(...args),
  startEngineDaemon: (...args: unknown[]) => startEngineDaemon(...args),
  startInstance: (...args: unknown[]) => startInstance(...args),
  runInstance: (...args: unknown[]) => runInstance(...args),
  tagInstance: (...args: unknown[]) => tagInstance(...args),
  isSsmAgentOnline: (...args: unknown[]) => isSsmAgentOnline(...args),
  runShellCommand: (...args: unknown[]) => runShellCommand(...args),
  readDeployConfig: (...args: unknown[]) => readDeployConfig(...args),
}));

vi.mock('../lambda/shared/environments', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lambda/shared/environments')>()),
  findEnvEip: (...args: unknown[]) => findEnvEip(...args),
  findEnvSecurityGroup: (...args: unknown[]) => findEnvSecurityGroup(...args),
  readEnvApiKey: (...args: unknown[]) => readEnvApiKey(...args),
}));

vi.mock('../lambda/shared/seed', () => ({ weightsPresent: async () => true }));

// A scheduled start takes the environment's start lock like any other start;
// here the lock is a stub so each test can say whether another start holds it.
const acquireWakeLock = vi.fn();
const releaseWakeLock = vi.fn();
vi.mock('../lambda/shared/wake-lock', () => ({
  acquireWakeLock: (...args: unknown[]) => acquireWakeLock(...args),
  releaseWakeLock: (...args: unknown[]) => releaseWakeLock(...args),
  wakeLockHeld: async () => false,
}));

let handler: (event: ScheduledRunEvent, context: Context) => Promise<LambdaFunctionURLResult>;

beforeAll(async () => {
  Object.assign(process.env, LAMBDA_ENV);
  ({ handler } = await import('../lambda/start/index'));
});

const context = { getRemainingTimeInMillis: () => 600_000 } as unknown as Context;

function runEvent(action: 'start' | 'stop'): ScheduledRunEvent {
  return { source: 'spinloop.schedule', action, environment: 'dev' };
}

function structured(result: LambdaFunctionURLResult): { statusCode: number; body: string } {
  return result as { statusCode: number; body: string };
}

beforeEach(() => {
  vi.clearAllMocks();
  acquireWakeLock.mockResolvedValue(true);
  releaseWakeLock.mockResolvedValue(undefined);
  readDeployConfig.mockResolvedValue({
    runner: 'llamacpp',
    modelId: 'org/model',
    quant: 'Q4_K_M',
    weightsPrefix: 'llamacpp/org/model/Q4_K_M',
    contextSize: 32768,
    servedModelName: 'friendly',
    serveArgs: [],
    companions: {},
  });
  findEnvEip.mockResolvedValue({ publicIp: '198.51.100.7', allocationId: 'eipalloc-test' });
  findEnvSecurityGroup.mockResolvedValue('sg-test');
  readEnvApiKey.mockResolvedValue('sk-test');
  isSsmAgentOnline.mockResolvedValue(true);
  runShellCommand.mockImplementation((_instanceId: string, command: string) =>
    command === DAEMON_STATUS_CMD
      ? Promise.resolve({ status: 'Success', stdout: JSON.stringify({ state: 'stopped' }) })
      : Promise.resolve({ status: 'Success', stdout: '200' }),
  );
  startEngineDaemon.mockResolvedValue(true);
});

describe('a scheduled start', () => {
  it('re-wakes a stopped instance, with no retention deadline', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-off', state: 'stopped' });
    getInstance.mockResolvedValue({ instanceId: 'i-off', state: 'running', launchTime: new Date() });

    const result = await handler(runEvent('start'), context);
    expect(structured(result).statusCode).toBe(200);
    expect(JSON.parse(structured(result).body).state).toBe('ready');
    expect(startInstance).toHaveBeenCalledWith('i-off');
    expect(tagInstance).not.toHaveBeenCalledWith('i-off', 'Retain-Until', expect.anything());
  });

  it('leaves a running instance alone', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });
    getInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running', launchTime: new Date() });

    const result = await handler(runEvent('start'), context);
    expect(structured(result).statusCode).toBe(200);
    expect(startInstance).not.toHaveBeenCalled();
    expect(runInstance).not.toHaveBeenCalled();
  });

  it('reports an unconfigured environment without launching anything', async () => {
    readDeployConfig.mockRejectedValue(new Error('unconfigured'));
    findManagedInstance.mockResolvedValue(undefined);

    const result = await handler(runEvent('start'), context);
    expect(structured(result).statusCode).toBe(503);
    expect(runInstance).not.toHaveBeenCalled();
    expect(startInstance).not.toHaveBeenCalled();
  });

  it('takes the environment’s start lock and releases it afterwards', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });
    getInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running', launchTime: new Date() });

    await handler(runEvent('start'), context);

    expect(acquireWakeLock).toHaveBeenCalledTimes(1);
    expect(acquireWakeLock.mock.calls[0][0]).toBe('dev');
    expect(releaseWakeLock).toHaveBeenCalledTimes(1);
    expect(releaseWakeLock.mock.calls[0][0]).toBe('dev');
  });

  it('launches nothing while a manual start holds the lock', async () => {
    acquireWakeLock.mockResolvedValue(false);
    findManagedInstance.mockResolvedValue(null);

    const result = await handler(runEvent('start'), context);

    expect(structured(result).statusCode).toBe(503);
    expect(JSON.parse(structured(result).body).state).toBe('starting');
    expect(findManagedInstance).not.toHaveBeenCalled();
    expect(runInstance).not.toHaveBeenCalled();
    expect(startInstance).not.toHaveBeenCalled();
    expect(releaseWakeLock).not.toHaveBeenCalled();
  });

  it('ignores an event whose action is not start', async () => {
    const result = await handler(runEvent('stop'), context);
    expect(JSON.parse(structured(result).body).state).toBe('ignored');
    expect(findManagedInstance).not.toHaveBeenCalled();
  });
});
