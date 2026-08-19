import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Context, LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';

// The wake branch of the start Lambda: a previously stopped instance is
// re-woken (started, not replaced), a dying instance fails retryably, and a
// running one is left alone. All AWS calls are stubbed.

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
const findLatestAmi = vi.fn();
const tagInstance = vi.fn();
const associateEip = vi.fn();
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
  findLatestAmi: (...args: unknown[]) => findLatestAmi(...args),
  tagInstance: (...args: unknown[]) => tagInstance(...args),
  associateEip: (...args: unknown[]) => associateEip(...args),
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

let handler: (event: LambdaFunctionURLEvent, context: Context) => Promise<LambdaFunctionURLResult>;

beforeAll(async () => {
  Object.assign(process.env, LAMBDA_ENV);
  ({ handler } = await import('../lambda/start/index'));
});

const wakeEvent = {
  queryStringParameters: { env: 'dev' },
  requestContext: { http: { method: 'POST' } },
} as unknown as LambdaFunctionURLEvent;

// The start Lambda blocks until the model serves; give it a wide-enough
// remaining time that the happy path never meets the deadline.
const context = { getRemainingTimeInMillis: () => 600_000 } as unknown as Context;

function structured(result: LambdaFunctionURLResult): { statusCode: number; body: string } {
  return result as { statusCode: number; body: string };
}

const HEALTHY = { status: 'Success', stdout: '200' };

beforeEach(() => {
  vi.clearAllMocks();
  readDeployConfig.mockResolvedValue({ runner: 'llamacpp' });
  findEnvEip.mockResolvedValue({ publicIp: '198.51.100.7', allocationId: 'eipalloc-test' });
  findEnvSecurityGroup.mockResolvedValue('sg-test');
  readEnvApiKey.mockResolvedValue('sk-test');
  isSsmAgentOnline.mockResolvedValue(true);
  runShellCommand.mockResolvedValue(HEALTHY);
  startEngineDaemon.mockResolvedValue(true);
});

describe('re-waking a stopped instance', () => {
  it('starts the existing instance instead of launching a new one', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-off', state: 'stopped' });
    getInstance.mockResolvedValue({ instanceId: 'i-off', state: 'running', launchTime: new Date() });

    const result = await handler(wakeEvent, context);
    expect(structured(result).statusCode).toBe(200);
    expect(JSON.parse(structured(result).body).state).toBe('ready');

    expect(startInstance).toHaveBeenCalledWith('i-off');
    // The engine start is the control plane's ask, not user data's: a
    // re-wake must not bet on the boot script re-running.
    expect(startEngineDaemon).toHaveBeenCalledWith('i-off');
    // The session start is recorded, so the max-runtime cap measures this
    // session rather than first boot.
    expect(tagInstance).toHaveBeenCalledWith(
      'i-off',
      'Started-At',
      expect.stringMatching(/^\d{4}-\d{2}-\d{2}T/),
    );
    expect(findLatestAmi).not.toHaveBeenCalled();
    expect(runInstance).not.toHaveBeenCalled();
  });

  it('leaves a running instance alone', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });
    getInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running', launchTime: new Date() });

    const result = await handler(wakeEvent, context);
    expect(structured(result).statusCode).toBe(200);
    expect(startInstance).not.toHaveBeenCalled();
    expect(tagInstance).not.toHaveBeenCalled();
    expect(runInstance).not.toHaveBeenCalled();
  });
});

describe('a dying instance is not adopted', () => {
  it.each(['shutting-down', 'terminated'] as const)(
    'fails retryably when it is %s',
    async (state) => {
      findManagedInstance.mockResolvedValue({ instanceId: 'i-dying', state });
      getInstance.mockResolvedValue({ instanceId: 'i-dying', state, launchTime: new Date() });

      const result = await handler(wakeEvent, context);
      expect(structured(result).statusCode).toBe(503);
      const body = JSON.parse(structured(result).body);
      expect(body.state).toBe(state);
      expect(body.retry_after_seconds).toBeGreaterThan(0);
      expect(startInstance).not.toHaveBeenCalled();
    },
  );
});
