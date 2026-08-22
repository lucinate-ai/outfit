/**
 * The launch path's two risky pieces: reading the infrastructure out of the
 * environment (where a mistyped variable name is silent until deploy time), and
 * the idempotency escape — EC2's ClientToken window handing back a dead or
 * long-vanished instance to a seed that is legitimately being retried.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const runInstance = vi.fn();
const getInstance = vi.fn();
const getParameterValue = vi.fn();

vi.mock('../lambda/shared/aws', async () => {
  const actual = await vi.importActual<typeof import('../lambda/shared/aws')>('../lambda/shared/aws');
  return {
    ...actual,
    runInstance: (...a: unknown[]) => runInstance(...a),
    getInstance: (...a: unknown[]) => getInstance(...a),
    getParameterValue: (...a: unknown[]) => getParameterValue(...a),
  };
});

const ENV_VARS = {
  AWS_REGION: 'us-east-1',
  WEIGHTS_BUCKET: 'weights',
  SEED_INSTANCE_TYPE: 'c7g.large',
  SEED_SUBNET_ID: 'subnet-1',
  SEED_SECURITY_GROUP_ID: 'sg-1',
  SEED_INSTANCE_PROFILE_ARN: 'arn:aws:iam::1:instance-profile/seed',
  HF_TOKEN_SECRET_ARN: 'arn:secret',
  SEEDER_BUCKET: 'assets',
  SEEDER_KEY: 'hash/seed.mjs',
  MAX_SEED_MINUTES: '60',
};

describe('reading the seed infrastructure from the environment', () => {
  const saved = { ...process.env };
  beforeEach(() => {
    Object.assign(process.env, ENV_VARS);
  });
  afterEach(() => {
    process.env = { ...saved };
  });

  it('reads every value the launch needs', async () => {
    const { seedInfraFromEnv } = await import('../lambda/shared/seed/launch');
    const infra = seedInfraFromEnv();
    // The regression this guards is a variable that is set by the stack but
    // never read (or read under a different name) — silent until deploy time.
    expect(infra).toMatchObject({
      region: 'us-east-1',
      bucket: 'weights',
      instanceType: 'c7g.large',
      subnetId: 'subnet-1',
      securityGroupId: 'sg-1',
      instanceProfileArn: 'arn:aws:iam::1:instance-profile/seed',
      hfSecretArn: 'arn:secret',
      seederBucket: 'assets',
      seederKey: 'hash/seed.mjs',
      maxSeedMinutes: 60,
    });
  });

  it('defaults the transfer tuning so the stack need not set it', async () => {
    const { seedInfraFromEnv } = await import('../lambda/shared/seed/launch');
    const infra = seedInfraFromEnv();
    expect(infra.partSizeBytes).toBe(64 * 1024 * 1024);
    expect(infra.partConcurrency).toBe(8);
    expect(infra.partAttempts).toBe(4);
  });

  it('treats an absent Hugging Face secret as "public repositories only"', async () => {
    delete process.env.HF_TOKEN_SECRET_ARN;
    const { seedInfraFromEnv } = await import('../lambda/shared/seed/launch');
    expect(seedInfraFromEnv().hfSecretArn).toBe('');
  });

  it('fails loudly when a required value is missing', async () => {
    delete process.env.SEEDER_KEY;
    const { seedInfraFromEnv } = await import('../lambda/shared/seed/launch');
    expect(() => seedInfraFromEnv()).toThrow(/SEEDER_KEY/);
  });
});

describe('launching, and the idempotency escape', () => {
  const saved = { ...process.env };
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(process.env, ENV_VARS);
    getParameterValue.mockResolvedValue('ami-al2023');
  });
  afterEach(() => {
    process.env = { ...saved };
  });

  async function launch(force = false) {
    const { buildSeedJob, launchSeedInstance, seedInfraFromEnv } = await import(
      '../lambda/shared/seed/launch'
    );
    const infra = seedInfraFromEnv();
    const job = buildSeedJob(
      {
        runner: 'vllm',
        modelId: 'org/m',
        quant: '',
        weightsPrefix: 'models/vllm/org/m/',
        contextSize: 1,
        servedModelName: 'm',
        serveArgs: [],
        companions: {},
      },
      infra,
      '',
    );
    return launchSeedInstance(job, infra, { force });
  }

  it('launches from the stock Amazon Linux image, not a baked one', async () => {
    runInstance.mockResolvedValue('i-1');
    getInstance.mockResolvedValue({ instanceId: 'i-1', state: 'pending' });

    await launch();

    expect(getParameterValue).toHaveBeenCalledWith(
      '/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-arm64',
    );
    expect(runInstance.mock.calls[0][0]).toMatchObject({ imageId: 'ami-al2023' });
  });

  it('tags the instance so the seed sweep and `seed ls` can find it', async () => {
    runInstance.mockResolvedValue('i-1');
    getInstance.mockResolvedValue({ instanceId: 'i-1', state: 'pending' });

    await launch();

    expect(runInstance.mock.calls[0][0].tags).toMatchObject({
      'cloud-vm-llm': 'seed',
      'cloud-vm-llm:seed-id': 'vllm--org-m',
      'cloud-vm-llm:seed-model': 'org/m',
    });
    // Terminate rather than stop, so a finished seed leaves nothing behind.
    expect(runInstance.mock.calls[0][0].terminateOnShutdown).toBe(true);
  });

  it('uses the same token for two ordinary starts, which is what dedupes them', async () => {
    runInstance.mockResolvedValue('i-1');
    getInstance.mockResolvedValue({ instanceId: 'i-1', state: 'running' });

    await launch();
    await launch();

    const [first, second] = runInstance.mock.calls.map((c) => c[0].clientToken);
    expect(first).toBe(second);
    expect(first).toBe('seed-vllm--org-m-auto');
  });

  it('does not launch twice when the instance comes back alive', async () => {
    runInstance.mockResolvedValue('i-1');
    getInstance.mockResolvedValue({ instanceId: 'i-1', state: 'running' });

    const result = await launch();

    expect(runInstance).toHaveBeenCalledTimes(1);
    expect(result.instanceId).toBe('i-1');
  });

  it('escapes a stale idempotency hit with a fresh generation', async () => {
    // The hazard the design names: a seed retried hours after an earlier
    // attempt terminated would otherwise be handed the dead instance back.
    runInstance.mockResolvedValueOnce('i-old').mockResolvedValueOnce('i-new');
    getInstance
      .mockResolvedValueOnce({ instanceId: 'i-old', state: 'terminated' })
      .mockResolvedValueOnce({ instanceId: 'i-new', state: 'pending' });

    const result = await launch();

    expect(runInstance).toHaveBeenCalledTimes(2);
    expect(result.instanceId).toBe('i-new');
    const [first, second] = runInstance.mock.calls.map((c) => c[0].clientToken);
    expect(first).toBe('seed-vllm--org-m-auto');
    expect(second).not.toBe(first);
  });

  it('treats a shutting-down instance as stale too', async () => {
    runInstance.mockResolvedValueOnce('i-old').mockResolvedValueOnce('i-new');
    getInstance
      .mockResolvedValueOnce({ instanceId: 'i-old', state: 'shutting-down' })
      .mockResolvedValueOnce({ instanceId: 'i-new', state: 'pending' });

    expect((await launch()).instanceId).toBe('i-new');
  });

  it('skips the shared token entirely for a deliberate re-seed', async () => {
    // A force must never be deduplicated onto the attempt it is replacing.
    runInstance.mockResolvedValue('i-forced');
    getInstance.mockResolvedValue({ instanceId: 'i-forced', state: 'pending' });

    await launch(true);

    expect(runInstance).toHaveBeenCalledTimes(1);
    expect(runInstance.mock.calls[0][0].clientToken).not.toBe('seed-vllm--org-m-auto');
  });

  it('escapes an idempotency mismatch when the boot script has changed since the token was last used', async () => {
    // EC2 refuses to honour a repeated ClientToken whose arguments changed —
    // most commonly because a later deploy updated the boot script for the
    // same seed id. That refusal is proof the old request is unrelated, so it
    // is escaped exactly like a stale instance rather than failing the seed.
    const mismatch = new Error('Arguments on this idempotent request are inconsistent');
    mismatch.name = 'IdempotentParameterMismatch';
    runInstance.mockRejectedValueOnce(mismatch).mockResolvedValueOnce('i-new');
    getInstance.mockResolvedValueOnce({ instanceId: 'i-new', state: 'pending' });

    const result = await launch();

    expect(runInstance).toHaveBeenCalledTimes(2);
    expect(result.instanceId).toBe('i-new');
    const [first, second] = runInstance.mock.calls.map((c) => c[0].clientToken);
    expect(first).toBe('seed-vllm--org-m-auto');
    expect(second).not.toBe(first);
  });

  it('recovers from a moment of eventual-consistency lag without relaunching', async () => {
    // DescribeInstances is eventually consistent right after RunInstances;
    // relaunching on the first NotFound would double the instances.
    runInstance.mockResolvedValue('i-1');
    getInstance
      .mockRejectedValueOnce(new Error('InvalidInstanceID.NotFound'))
      .mockResolvedValueOnce({ instanceId: 'i-1', state: 'pending' });

    const result = await launch();

    expect(runInstance).toHaveBeenCalledTimes(1);
    expect(result.instanceId).toBe('i-1');
  });

  it('escapes an idempotency hit whose id never becomes visible, not just a dead one', async () => {
    // The regression this guards: the fixed AUTO_GENERATION token can hit an
    // instance id from a much earlier session that has since aged out of
    // DescribeInstances entirely — indistinguishable from "too new to see
    // yet" by a single check, and previously treated as alive forever.
    runInstance.mockResolvedValueOnce('i-old').mockResolvedValueOnce('i-new');
    getInstance
      .mockRejectedValueOnce(new Error('InvalidInstanceID.NotFound'))
      .mockRejectedValueOnce(new Error('InvalidInstanceID.NotFound'))
      .mockRejectedValueOnce(new Error('InvalidInstanceID.NotFound'))
      .mockRejectedValueOnce(new Error('InvalidInstanceID.NotFound'))
      .mockResolvedValueOnce({ instanceId: 'i-new', state: 'pending' });

    const result = await launch();

    expect(runInstance).toHaveBeenCalledTimes(2);
    expect(result.instanceId).toBe('i-new');
    const [first, second] = runInstance.mock.calls.map((c) => c[0].clientToken);
    expect(first).toBe('seed-vllm--org-m-auto');
    expect(second).not.toBe(first);
  }, 15_000);
});
