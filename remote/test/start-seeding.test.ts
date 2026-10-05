import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Context, LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import type { InstanceInfo } from '../lambda/shared/aws';
import { DAEMON_STATUS_CMD } from '../lambda/shared/daemon';

// The weights gate of the start Lambda: a wake must not launch an instance
// whose weights are still seeding, and a wake with absent weights starts the
// seed rather than booting on a partial prefix. All AWS calls are stubbed.

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
const findManagedInstances = vi.fn();
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
const weightsPresent = vi.fn();
const launchSeedInstance = vi.fn();
const buildSeedJob = vi.fn();
const startInstance = vi.fn();

vi.mock('../lambda/shared/aws', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lambda/shared/aws')>()),
  findManagedInstance: (...args: unknown[]) => findManagedInstance(...args),
  findManagedInstances: (...args: unknown[]) => findManagedInstances(...args),
  getInstance: (...args: unknown[]) => getInstance(...args),
  startInstance: (...args: unknown[]) => startInstance(...args),
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

vi.mock('../lambda/shared/seed', () => ({
  weightsPresent: (...args: unknown[]) => weightsPresent(...args),
}));

vi.mock('../lambda/shared/seed/launch', () => ({
  seedInfraFromEnv: () => ({ bucket: 'test-bucket' }),
  buildSeedJob: (...args: unknown[]) => buildSeedJob(...args),
  launchSeedInstance: (...args: unknown[]) => launchSeedInstance(...args),
}));

// These tests are about the weights gate, so the environment's lock is always free.
vi.mock('../lambda/shared/wake-lock', () => ({
  acquireWakeLock: async () => true,
  releaseWakeLock: async () => undefined,
  wakeLockHeld: async () => false,
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

const CONFIG = {
  runner: 'llamacpp',
  modelId: 'org/model',
  quant: 'Q4_K_M',
  weightsPrefix: 'llamacpp/org/model/Q4_K_M',
  contextSize: 32768,
  servedModelName: 'friendly',
  serveArgs: [],
  companions: {},
};

// seedIdFor('llamacpp', 'org/model', 'Q4_K_M').
const SEED_ID = 'llamacpp--org-model--Q4_K_M';

/** A seed instance carrying its own id tag. */
function seedInstance(id: string, state: string, seedId: string = SEED_ID): InstanceInfo {
  return { instanceId: id, state, tags: { 'cloud-vm-llm:seed-id': seedId } };
}

// The seed instances DescribeInstances would return, and which of them answer
// the gate's questions. The filter check mirrors findSeedInstances: a seed-id
// tag filter narrows, and no filter means every seed instance.
let seeds: InstanceInfo[] = [];
function seedLookup(
  _key: string,
  _value: string,
  filters: { Name: string; Values: string[] }[] = [],
): InstanceInfo[] {
  const idFilter = filters.find((f) => f.Name === 'tag:cloud-vm-llm:seed-id');
  return idFilter ? seeds.filter((i) => i.tags?.['cloud-vm-llm:seed-id'] === idFilter.Values[0]) : seeds;
}

beforeEach(() => {
  vi.clearAllMocks();
  seeds = [];
  // A parsed config, whole: the boot script iterates it and the start's body
  // renders it, on the path where the weights are present.
  readDeployConfig.mockResolvedValue(CONFIG);
  weightsPresent.mockResolvedValue(true);
  findSeedMock();
  findEnvEip.mockResolvedValue({ publicIp: '198.51.100.7', allocationId: 'eipalloc-test' });
  findEnvSecurityGroup.mockResolvedValue('sg-test');
  readEnvApiKey.mockResolvedValue('sk-test');
  isSsmAgentOnline.mockResolvedValue(true);
  runShellCommand.mockImplementation((_instanceId: string, command: string) =>
    command === DAEMON_STATUS_CMD
      ? Promise.resolve({ status: 'Success', stdout: JSON.stringify({ state: 'stopped' }) })
      : Promise.resolve({ status: 'Success', stdout: '200' }),
  );
  startInstance.mockResolvedValue(undefined);
  startEngineDaemon.mockResolvedValue(true);
  findManagedInstance.mockResolvedValue(null);
  getInstance.mockResolvedValue({ instanceId: 'i-new', state: 'running', launchTime: new Date() });
  runInstance.mockResolvedValue('i-new');
  findLatestAmi.mockResolvedValue({ imageId: 'ami-test1', rootVolumeSizeGb: 80 });
  launchSeedInstance.mockResolvedValue({ seedId: SEED_ID, instanceId: 'i-seed', started: true });
  buildSeedJob.mockImplementation(
    (cfg: unknown) => ({ seedId: SEED_ID, cfg }),
  );
});

function findSeedMock() {
  findManagedInstances.mockImplementation((_k: string, _v: string, f?: { Name: string; Values: string[] }[]) =>
    Promise.resolve(seedLookup(_k, _v, f ?? [])),
  );
}

describe('the weights gate', () => {
  it('proceeds to launch when the weights are present', async () => {
    const result = await handler(wakeEvent, context);

    expect(structured(result).statusCode).toBe(200);
    expect(runInstance).toHaveBeenCalled();
    expect(launchSeedInstance).not.toHaveBeenCalled();
  });

  it('joins a running seed without launching an instance', async () => {
    weightsPresent.mockResolvedValue(false);
    seeds = [seedInstance('i-seed', 'running')];

    const result = await handler(wakeEvent, context);

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(503);
    expect(reply.state).toBe('seeding');
    expect(reply.seedId).toBe(SEED_ID);
    expect(reply.retry_after_seconds).toBe(60);
    expect(reply.message).toContain(`spinloop remote seed status ${SEED_ID}`);
    expect(runInstance).not.toHaveBeenCalled();
    expect(launchSeedInstance).not.toHaveBeenCalled();
  });

  it('reports a manifest read failure as a 502, not a retryable seeding state', async () => {
    // Read the failure as absent and a transient glitch pays for a full
    // re-seed; read it as present and the wake boots on unverified weights.
    // The gate says the check failed instead, and stops there.
    weightsPresent.mockRejectedValue(new Error('AccessDenied'));
    seeds = [seedInstance('i-seed', 'running')];
    const result = await handler(wakeEvent, context);
    const body = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(502);
    expect(body.state).toBeUndefined();
    expect(body.error).toContain('could not check whether the weights are present');
    expect(body.error).toContain('AccessDenied');
    expect(findManagedInstances).not.toHaveBeenCalled();
    expect(launchSeedInstance).not.toHaveBeenCalled();
    expect(runInstance).not.toHaveBeenCalled();
  });

  it('holds the re-wake too, not just the launch', async () => {
    weightsPresent.mockResolvedValue(false);
    seeds = [seedInstance('i-seed', 'running')];
    // The environment's instance exists and is stopped: absent the gate this
    // is the re-wake path.
    findManagedInstance.mockResolvedValue({ instanceId: 'i-old', state: 'stopped' });
    const result = await handler(wakeEvent, context);
    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(503);
    expect(reply.state).toBe('seeding');
    expect(reply.seedId).toBe(SEED_ID);
    // The gate answers before the re-wake: the stopped instance is not
    // started and no fresh one is launched.
    expect(startInstance).not.toHaveBeenCalled();
    expect(runInstance).not.toHaveBeenCalled();
  });

  it('counts a pending seed as in flight', async () => {
    weightsPresent.mockResolvedValue(false);
    seeds = [seedInstance('i-seed', 'pending')];

    const result = await handler(wakeEvent, context);

    expect(JSON.parse(structured(result).body).state).toBe('seeding');
    expect(launchSeedInstance).not.toHaveBeenCalled();
    expect(runInstance).not.toHaveBeenCalled();
  });

  it('starts a fresh seed when a stopped one is all that remains', async () => {
    // A stopped seed instance is a dead seed: its joined state is already
    // failed, and waiting on it would wedge every start for these weights.
    weightsPresent.mockResolvedValue(false);
    seeds = [seedInstance('i-seed', 'stopped')];

    const result = await handler(wakeEvent, context);

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(503);
    expect(reply.state).toBe('seeding');
    expect(reply.seedId).toBe(SEED_ID);
    expect(launchSeedInstance).toHaveBeenCalledTimes(1);
    expect(runInstance).not.toHaveBeenCalled();
  });

  it('starts the seed from the environment deploy-config, companions and all', async () => {
    weightsPresent.mockResolvedValue(false);
    const withDrafter = { ...CONFIG, companions: { draft: 'dflash-kquant.gguf' } };
    readDeployConfig.mockResolvedValue(withDrafter);

    const result = await handler(wakeEvent, context);

    expect(JSON.parse(structured(result).body).state).toBe('seeding');
    expect(buildSeedJob).toHaveBeenCalledWith(withDrafter, expect.anything(), '');
    expect(launchSeedInstance).toHaveBeenCalledTimes(1);
  });

  it('waits for a slot rather than exceeding the seed cap', async () => {
    weightsPresent.mockResolvedValue(false);
    seeds = [
      seedInstance('i-a', 'running', 'llamacpp--other-a'),
      seedInstance('i-b', 'running', 'llamacpp--other-b'),
    ];

    const result = await handler(wakeEvent, context);

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(503);
    expect(reply.state).toBe('seeding');
    expect(reply.seedId).toBe(SEED_ID);
    expect(reply.message).toMatch(/2 seeds are running \(cap 2\)/);
    expect(launchSeedInstance).not.toHaveBeenCalled();
    expect(runInstance).not.toHaveBeenCalled();
  });

  it('does not count stopped seeds against the cap', async () => {
    weightsPresent.mockResolvedValue(false);
    seeds = [
      seedInstance('i-a', 'stopped', 'llamacpp--other-a'),
      seedInstance('i-b', 'running', 'llamacpp--other-b'),
    ];

    await handler(wakeEvent, context);

    expect(launchSeedInstance).toHaveBeenCalledTimes(1);
  });

  it('reports a launch failure as retryable seeding, not a fatal error', async () => {
    weightsPresent.mockResolvedValue(false);
    launchSeedInstance.mockRejectedValue(new Error('no capacity in the seed zone'));

    const result = await handler(wakeEvent, context);

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(503);
    expect(reply.state).toBe('seeding');
    expect(reply.seedId).toBe(SEED_ID);
    expect(reply.message).toContain('no capacity in the seed zone');
    expect(reply.message).toContain(`spinloop remote seed status ${SEED_ID}`);
    expect(runInstance).not.toHaveBeenCalled();
  });
});
