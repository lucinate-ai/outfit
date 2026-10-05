import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Context, LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import { DAEMON_STATUS_CMD } from '../lambda/shared/daemon';
import { wakeLockParam } from '../lambda/shared/wake-lock';

// Starts for one environment take a lock, so two starts close together launch
// one instance, and a start whose instance is stopped under it ends promptly
// and lets the lock go. The lock is the real one, over an in-memory stand-in
// for SSM; every other AWS call is stubbed.

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
  IDLE_THRESHOLD_MINUTES: '15',
  GRACE_PERIOD_MINUTES: '10',
  MAX_RUNTIME_MINUTES: '240',
  STOP_RETENTION_MINUTES: '60',
  MAX_SEED_MINUTES: '60',
  SEED_STALL_MINUTES: '10',
};

const store = new Map<string, string>();
const ssmFailures = new Map<string, Error>();

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
const stopEngineDaemon = vi.fn();
const stopInstance = vi.fn();
const findEnvEip = vi.fn();
const findEnvSecurityGroup = vi.fn();
const readEnvApiKey = vi.fn();

function awsError(name: string): Error {
  return Object.assign(new Error(name), { name });
}

vi.mock('@aws-sdk/client-ssm', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@aws-sdk/client-ssm')>()),
  SSMClient: class {
    async send(cmd: { constructor: { name: string }; input: { Name: string; Value?: string; Overwrite?: boolean } }) {
      const kind = cmd.constructor.name;
      const failure = ssmFailures.get(kind);
      if (failure) {
        throw failure;
      }
      const { Name, Value, Overwrite } = cmd.input;
      if (kind === 'PutParameterCommand') {
        if (!Overwrite && store.has(Name)) {
          throw awsError('ParameterAlreadyExists');
        }
        store.set(Name, Value as string);
        return {};
      }
      if (kind === 'GetParameterCommand') {
        if (!store.has(Name)) {
          throw awsError('ParameterNotFound');
        }
        return { Parameter: { Value: store.get(Name) } };
      }
      if (kind === 'DeleteParameterCommand') {
        if (!store.has(Name)) {
          throw awsError('ParameterNotFound');
        }
        store.delete(Name);
        return {};
      }
      throw new Error(`unexpected ${kind}`);
    }
  },
}));

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
  stopEngineDaemon: (...args: unknown[]) => stopEngineDaemon(...args),
  stopInstance: (...args: unknown[]) => stopInstance(...args),
  // The wake sleeps between polls; these tests step through the polls instead.
  sleep: async () => undefined,
}));

vi.mock('../lambda/shared/environments', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lambda/shared/environments')>()),
  findEnvEip: (...args: unknown[]) => findEnvEip(...args),
  findEnvSecurityGroup: (...args: unknown[]) => findEnvSecurityGroup(...args),
  readEnvApiKey: (...args: unknown[]) => readEnvApiKey(...args),
}));

vi.mock('../lambda/shared/seed', () => ({ weightsPresent: async () => true }));

type Handler = (event: LambdaFunctionURLEvent, context: Context) => Promise<LambdaFunctionURLResult>;
let start: Handler;
let stop: Handler;

beforeAll(async () => {
  Object.assign(process.env, LAMBDA_ENV);
  ({ handler: start } = await import('../lambda/start/index'));
  stop = (await import('../lambda/stop/index')).handler as unknown as Handler;
});

function event(env: string, method = 'POST', query: Record<string, string> = {}): LambdaFunctionURLEvent {
  return {
    queryStringParameters: { env, ...query },
    requestContext: { http: { method } },
  } as unknown as LambdaFunctionURLEvent;
}

function contextOf(requestId: string): Context {
  return { awsRequestId: requestId, getRemainingTimeInMillis: () => 600_000 } as unknown as Context;
}

function structured(result: LambdaFunctionURLResult): { statusCode: number; body: string } {
  return result as { statusCode: number; body: string };
}

function bodyOf(result: LambdaFunctionURLResult): Record<string, any> {
  return JSON.parse(structured(result).body);
}

const FUTURE = new Date(Date.now() + 3_600_000);
const PAST = new Date(Date.now() - 3_600_000);

function lockOf(owner: string, expiresAt: Date): string {
  return JSON.stringify({ owner, expiresAt: expiresAt.toISOString() });
}

const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

beforeEach(() => {
  vi.clearAllMocks();
  store.clear();
  ssmFailures.clear();
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
  runShellCommand.mockImplementation((_id: string, command: string) =>
    command === DAEMON_STATUS_CMD
      ? Promise.resolve({ status: 'Success', stdout: JSON.stringify({ state: 'stopped' }) })
      : Promise.resolve({ status: 'Success', stdout: '200' }),
  );
  startEngineDaemon.mockResolvedValue(true);
  findLatestAmi.mockResolvedValue({ imageId: 'ami-test1', rootVolumeSizeGb: 80 });
  // The lookup the lock exists to protect: it never sees an instance another
  // start is in the middle of launching.
  findManagedInstance.mockResolvedValue(null);
  getInstance.mockResolvedValue({ instanceId: 'i-new', state: 'running', launchTime: new Date() });
  runInstance.mockImplementation(async () => {
    await wait(30);
    return 'i-new';
  });
  stopEngineDaemon.mockResolvedValue(undefined);
});

describe('two starts for one environment', () => {
  it('launch one instance; the other is told a start is in progress', async () => {
    const [a, b] = await Promise.all([
      start(event('dev'), contextOf('req-1')),
      start(event('dev'), contextOf('req-2')),
    ]);

    expect(runInstance).toHaveBeenCalledTimes(1);
    const results = [a, b].map((r) => structured(r).statusCode).sort();
    expect(results).toEqual([200, 503]);
    const refused = bodyOf([a, b].find((r) => structured(r).statusCode === 503)!);
    expect(refused.state).toBe('starting');
    expect(refused.message).toContain('another start');
    expect(refused.retry_after_seconds).toBe(15);
  });

  it('leave the refused start having looked up and launched nothing', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-holder', FUTURE));

    const result = await start(event('dev'), contextOf('req-2'));

    expect(structured(result).statusCode).toBe(503);
    expect(bodyOf(result).message).toContain('another start');
    expect(findManagedInstance).not.toHaveBeenCalled();
    expect(runInstance).not.toHaveBeenCalled();
    expect(startInstance).not.toHaveBeenCalled();
    // The holder's lock is untouched.
    expect(JSON.parse(store.get(wakeLockParam('dev'))!).owner).toBe('req-holder');
  });

  it('are followed by a retry that finds the first start’s instance and launches nothing more', async () => {
    await start(event('dev'), contextOf('req-1'));
    expect(runInstance).toHaveBeenCalledTimes(1);

    findManagedInstance.mockResolvedValue({ instanceId: 'i-new', state: 'running' });
    const retry = await start(event('dev'), contextOf('req-2'));

    expect(structured(retry).statusCode).toBe(200);
    expect(bodyOf(retry).state).toBe('ready');
    expect(runInstance).toHaveBeenCalledTimes(1);
  });
});

describe('different environments', () => {
  it('do not wait for each other', async () => {
    const [a, b] = await Promise.all([
      start(event('a'), contextOf('req-1')),
      start(event('b'), contextOf('req-2')),
    ]);
    expect(structured(a).statusCode).toBe(200);
    expect(structured(b).statusCode).toBe(200);
    expect(runInstance).toHaveBeenCalledTimes(2);
  });
});

describe('the lock is released on every ending', () => {
  it('after ready', async () => {
    await start(event('dev'), contextOf('req-1'));
    expect(store.has(wakeLockParam('dev'))).toBe(false);
  });

  it('after a retryable refusal (no AMI baked)', async () => {
    findLatestAmi.mockResolvedValue(null);
    const result = await start(event('dev'), contextOf('req-1'));
    expect(bodyOf(result).state).toBe('no-ami');
    expect(store.has(wakeLockParam('dev'))).toBe(false);
  });

  it('after an instance goes terminal', async () => {
    getInstance.mockResolvedValue({ instanceId: 'i-new', state: 'terminated' });
    const result = await start(event('dev'), contextOf('req-1'));
    expect(structured(result).statusCode).toBe(503);
    expect(store.has(wakeLockParam('dev'))).toBe(false);
  });

  it('after an unexpected error', async () => {
    runInstance.mockRejectedValue(new Error('boom'));
    await expect(start(event('dev'), contextOf('req-1'))).rejects.toThrow('boom');
    expect(store.has(wakeLockParam('dev'))).toBe(false);
  });

  it('so the next start is not refused', async () => {
    runInstance.mockRejectedValueOnce(new Error('boom'));
    await expect(start(event('dev'), contextOf('req-1'))).rejects.toThrow('boom');
    const next = await start(event('dev'), contextOf('req-2'));
    expect(structured(next).statusCode).toBe(200);
  });

  it('only when this start still holds it', async () => {
    findLatestAmi.mockImplementation(async () => {
      // Another start took the lock over while this one was working.
      store.set(wakeLockParam('dev'), lockOf('req-other', FUTURE));
      return null;
    });
    await start(event('dev'), contextOf('req-1'));
    expect(JSON.parse(store.get(wakeLockParam('dev'))!).owner).toBe('req-other');
  });
});

describe('an abandoned lock', () => {
  it('is taken over once it has expired', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-killed', PAST));
    const result = await start(event('dev'), contextOf('req-2'));
    expect(structured(result).statusCode).toBe(200);
    expect(runInstance).toHaveBeenCalledTimes(1);
    expect(store.has(wakeLockParam('dev'))).toBe(false);
  });

  it('is not taken over while it is still valid', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-running', FUTURE));
    const result = await start(event('dev'), contextOf('req-2'));
    expect(structured(result).statusCode).toBe(503);
    expect(runInstance).not.toHaveBeenCalled();
  });

  it('is given an expiry just past this invocation’s own time limit', async () => {
    let written: { owner: string; expiresAt: string } | undefined;
    findLatestAmi.mockImplementation(async () => {
      written = JSON.parse(store.get(wakeLockParam('dev'))!);
      return null;
    });
    const before = Date.now();
    await start(event('dev'), contextOf('req-1'));
    const margin = Date.parse(written!.expiresAt) - before - 600_000;
    expect(written!.owner).toBe('req-1');
    expect(margin).toBeGreaterThanOrEqual(29_000);
    expect(margin).toBeLessThan(35_000);
  });
});

describe('a lock that cannot be taken', () => {
  it('stops the start before it launches anything, and says to retry', async () => {
    ssmFailures.set('PutParameterCommand', awsError('AccessDeniedException'));
    const result = await start(event('dev'), contextOf('req-1'));
    expect(structured(result).statusCode).toBe(503);
    expect(bodyOf(result).state).toBe('starting');
    expect(findManagedInstance).not.toHaveBeenCalled();
    expect(runInstance).not.toHaveBeenCalled();
  });
});

describe('status while a start is in progress', () => {
  const read = async () => bodyOf(await start(event('dev', 'GET'), contextOf('req-read')));

  it('shows a second client the start another client began', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-holder', FUTURE));
    // The launch is not visible to the lookup yet, which is the window the flag covers.
    findManagedInstance.mockResolvedValue(null);

    const body = await read();

    expect(body.state).toBe('starting');
    expect(body.start_in_progress).toBe(true);
  });

  it.each(['stopped', 'pending'])('reports %s as starting while a start holds the lock', async (state) => {
    store.set(wakeLockParam('dev'), lockOf('req-holder', FUTURE));
    findManagedInstance.mockResolvedValue({ instanceId: 'i-x', state });

    const body = await read();

    expect(body.state).toBe('starting');
    expect(body.start_in_progress).toBe(true);
  });

  it('keeps a running instance running, and flags the start while its model loads', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-holder', FUTURE));
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });
    isSsmAgentOnline.mockResolvedValue(false);

    const body = await read();

    expect(body.state).toBe('running');
    expect(body.healthy).toBe(false);
    expect(body.start_in_progress).toBe(true);
  });

  it('flags a start on a fully reporting running instance too', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-holder', FUTURE));
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });

    const body = await read();

    expect(body.state).toBe('running');
    expect(body.start_in_progress).toBe(true);
  });

  it('reports no start when none holds the lock', async () => {
    findManagedInstance.mockResolvedValue(null);
    const body = await read();
    expect(body.state).toBe('stopped');
    expect(body.start_in_progress).toBe(false);
  });

  it('ignores a lock that has expired', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-killed', PAST));
    findManagedInstance.mockResolvedValue(null);

    const body = await read();

    expect(body.state).toBe('stopped');
    expect(body.start_in_progress).toBe(false);
  });

  it('still answers when the lock cannot be read', async () => {
    ssmFailures.set('GetParameterCommand', awsError('ThrottlingException'));
    findManagedInstance.mockResolvedValue(null);

    const body = await read();

    expect(body.state).toBe('stopped');
    expect(body.start_in_progress).toBe(false);
  });

  it('shows the start from the moment it takes the lock, and not after it ends', async () => {
    let during: Record<string, any> | undefined;
    findLatestAmi.mockImplementation(async () => {
      during = bodyOf(await start(event('dev', 'GET'), contextOf('req-read')));
      return null;
    });

    await start(event('dev'), contextOf('req-1'));
    const after = await read();

    expect(during?.state).toBe('starting');
    expect(during?.start_in_progress).toBe(true);
    expect(after.start_in_progress).toBe(false);
  });

  it('does not show a start that is only waiting to retry for capacity', async () => {
    findLatestAmi.mockResolvedValue({ imageId: 'ami-test1', rootVolumeSizeGb: 80 });
    runInstance.mockRejectedValue(awsError('InsufficientInstanceCapacity'));

    const refused = await start(event('dev'), contextOf('req-1'));
    expect(bodyOf(refused).state).toBe('no-capacity');

    const body = await read();
    expect(body.state).toBe('stopped');
    expect(body.start_in_progress).toBe(false);
  });

  it('leaves the lock as the holder had it', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-holder', FUTURE));
    findManagedInstance.mockResolvedValue(null);

    await read();

    expect(JSON.parse(store.get(wakeLockParam('dev'))!).owner).toBe('req-holder');
  });
});

describe('reads and stops', () => {
  it('a status read does not take, or wait for, the lock', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-holder', FUTURE));
    const result = await start(event('dev', 'GET'), contextOf('req-2'));
    expect(structured(result).statusCode).toBe(200);
    expect(JSON.parse(store.get(wakeLockParam('dev'))!).owner).toBe('req-holder');
  });

  it('a pause is carried out while a start holds the lock', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-holder', FUTURE));
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });
    const result = await stop(event('dev', 'POST', { action: 'pause' }), contextOf('req-stop'));
    expect(structured(result).statusCode).toBe(200);
    expect(stopInstance).toHaveBeenCalledWith('i-run');
  });
});

describe('a stop during a start', () => {
  it('after the start command ends the start, and releases the lock', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-off', state: 'stopped' });
    getInstance.mockResolvedValue({ instanceId: 'i-off', state: 'stopped' });

    const result = await start(event('dev'), contextOf('req-1'));

    expect(startInstance).toHaveBeenCalledWith('i-off');
    expect(structured(result).statusCode).toBe(503);
    const body = bodyOf(result);
    expect(body.state).toBe('stopped');
    expect(body.message).toContain('while starting');
    expect(body.retry_after_seconds).toBe(15);
    expect(startEngineDaemon).not.toHaveBeenCalled();
    expect(store.has(wakeLockParam('dev'))).toBe(false);
  });

  it('right after a fresh launch ends the start, rather than re-waking the instance it just launched', async () => {
    getInstance.mockResolvedValue({ instanceId: 'i-new', state: 'stopping' });

    const result = await start(event('dev'), contextOf('req-1'));

    expect(structured(result).statusCode).toBe(503);
    expect(bodyOf(result).message).toContain('stopped while starting');
    expect(startInstance).not.toHaveBeenCalled();
    expect(store.has(wakeLockParam('dev'))).toBe(false);
  });

  it('while waiting for the agent ends the start', async () => {
    isSsmAgentOnline.mockResolvedValue(false);
    getInstance
      .mockResolvedValueOnce({ instanceId: 'i-new', state: 'running' }) // first loop
      .mockResolvedValueOnce({ instanceId: 'i-new', state: 'stopped' }); // agent loop

    const result = await start(event('dev'), contextOf('req-1'));

    expect(structured(result).statusCode).toBe(503);
    expect(bodyOf(result).state).toBe('stopped');
    expect(store.has(wakeLockParam('dev'))).toBe(false);
  });

  it('terminated while the engine loads ends the start', async () => {
    runShellCommand.mockImplementation((_id: string, command: string) =>
      command === DAEMON_STATUS_CMD
        ? Promise.resolve({ status: 'Success', stdout: JSON.stringify({ state: 'stopped' }) })
        : Promise.resolve({ status: 'Success', stdout: '503' }),
    );
    getInstance
      .mockResolvedValueOnce({ instanceId: 'i-new', state: 'running' }) // first loop
      .mockResolvedValueOnce({ instanceId: 'i-new', state: 'running' }) // agent loop
      .mockResolvedValueOnce({ instanceId: 'i-new', state: 'running' }) // daemon loop
      .mockResolvedValueOnce({ instanceId: 'i-new', state: 'terminated' }); // health loop

    const result = await start(event('dev'), contextOf('req-1'));

    expect(structured(result).statusCode).toBe(503);
    expect(bodyOf(result).state).toBe('terminated');
    expect(store.has(wakeLockParam('dev'))).toBe(false);
  });

  it('is not mistaken for a stop when the instance cannot be looked up yet', async () => {
    getInstance
      .mockResolvedValueOnce({ instanceId: 'i-new', state: 'running' }) // first loop
      .mockRejectedValueOnce(new Error('not visible yet')) // agent loop
      .mockResolvedValue({ instanceId: 'i-new', state: 'running' });

    const result = await start(event('dev'), contextOf('req-1'));

    expect(structured(result).statusCode).toBe(200);
  });

  it('still re-wakes an instance that was stopped before this start issued its command', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });
    getInstance
      .mockResolvedValueOnce({ instanceId: 'i-run', state: 'stopped' }) // first loop: the stop raced discovery
      .mockResolvedValue({ instanceId: 'i-run', state: 'running' });

    const result = await start(event('dev'), contextOf('req-1'));

    expect(startInstance).toHaveBeenCalledWith('i-run');
    expect(structured(result).statusCode).toBe(200);
  });
});
