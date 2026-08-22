import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Context, LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';

// The fresh-launch branch of the start Lambda: with no existing instance it
// launches from the newest baked AMI, provisioning the root volume's gp3
// throughput so the weights sync and the daemon's page-cache prewarm run at
// the volume's real ceiling. All AWS calls are stubbed.

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

const context = { getRemainingTimeInMillis: () => 600_000 } as unknown as Context;

function structured(result: LambdaFunctionURLResult): { statusCode: number; body: string } {
  return result as { statusCode: number; body: string };
}

const HEALTHY = { status: 'Success', stdout: '200' };

beforeEach(() => {
  vi.clearAllMocks();
  // serveArgs is iterated when the boot script is built — a minimal config
  // would only survive the re-wake paths, which never build it.
  readDeployConfig.mockResolvedValue({ runner: 'llamacpp', serveArgs: [] });
  findEnvEip.mockResolvedValue({ publicIp: '198.51.100.7', allocationId: 'eipalloc-test' });
  findEnvSecurityGroup.mockResolvedValue('sg-test');
  readEnvApiKey.mockResolvedValue('sk-test');
  isSsmAgentOnline.mockResolvedValue(true);
  runShellCommand.mockResolvedValue(HEALTHY);
  startEngineDaemon.mockResolvedValue(true);
  findManagedInstance.mockResolvedValue(null);
  getInstance.mockResolvedValue({ instanceId: 'i-new', state: 'running', launchTime: new Date() });
  runInstance.mockResolvedValue('i-new');
});

describe('fresh launch', () => {
  it('provisions the root volume throughput, at the AMI root size', async () => {
    findLatestAmi.mockResolvedValue({ imageId: 'ami-test1', rootVolumeSizeGb: 80 });

    const result = await handler(wakeEvent, context);
    expect(structured(result).statusCode).toBe(200);

    expect(runInstance).toHaveBeenCalledWith(
      expect.objectContaining({
        imageId: 'ami-test1',
        rootVolume: { volumeSize: 80, throughput: 1000 },
      }),
    );
  });

  it('launches the AMI root as-is when its size is unreadable', async () => {
    findLatestAmi.mockResolvedValue({ imageId: 'ami-test1', rootVolumeSizeGb: 0 });

    const result = await handler(wakeEvent, context);
    expect(structured(result).statusCode).toBe(200);

    const spec = runInstance.mock.calls[0][0] as { rootVolume?: unknown };
    expect(spec.rootVolume).toBeUndefined();
  });

  it('fails retryably when no AMI has been baked', async () => {
    findLatestAmi.mockResolvedValue(null);

    const result = await handler(wakeEvent, context);
    expect(structured(result).statusCode).toBe(503);
    expect(JSON.parse(structured(result).body).state).toBe('no-ami');
    expect(runInstance).not.toHaveBeenCalled();
  });
});
