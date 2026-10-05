import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import type { InstanceInfo } from '../lambda/shared/aws';
import type { SeedStatus } from '../lambda/shared/seed/status';

// The seed control surface: start, status, list, stop. The join and the cap
// judge which seeds are in flight through the same discovery the start
// Lambda's weights gate uses, so a stopped seed's compute counts as ceased
// here too — joining it would wedge every request for its weights, and
// counting it would be a cap a dead body keeps filled. All AWS calls are
// stubbed.

const LAMBDA_ENV = {
  TAG_KEY: 'cloud-vm-llm:managed',
  MAX_CONCURRENT_SEEDS: '2',
};

const findManagedInstances = vi.fn();
const terminateInstance = vi.fn();
const weightsPresent = vi.fn();
const launchSeedInstance = vi.fn();
const buildSeedJob = vi.fn();
const readSeedStatus = vi.fn();
const writeTerminalRecord = vi.fn();

vi.mock('../lambda/shared/aws', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lambda/shared/aws')>()),
  findManagedInstances: (...args: unknown[]) => findManagedInstances(...args),
  terminateInstance: (...args: unknown[]) => terminateInstance(...args),
}));

vi.mock('../lambda/shared/seed', () => ({
  weightsPresent: (...args: unknown[]) => weightsPresent(...args),
}));

vi.mock('../lambda/shared/seed/launch', () => ({
  seedInfraFromEnv: () => ({ bucket: 'test-bucket' }),
  buildSeedJob: (...args: unknown[]) => buildSeedJob(...args),
  launchSeedInstance: (...args: unknown[]) => launchSeedInstance(...args),
}));

vi.mock('../lambda/shared/seed/status', () => ({
  readSeedStatus: (...args: unknown[]) => readSeedStatus(...args),
  writeTerminalRecord: (...args: unknown[]) => writeTerminalRecord(...args),
}));

let handler: (event: LambdaFunctionURLEvent) => Promise<LambdaFunctionURLResult>;

beforeAll(async () => {
  Object.assign(process.env, LAMBDA_ENV);
  ({ handler } = await import('../lambda/seed/index'));
});

function event(method: string, opts: { id?: string; body?: string } = {}): LambdaFunctionURLEvent {
  return {
    requestContext: { http: { method } },
    queryStringParameters: opts.id ? { id: opts.id } : undefined,
    body: opts.body,
  } as unknown as LambdaFunctionURLEvent;
}

function structured(result: LambdaFunctionURLResult): { statusCode: number; body: string } {
  return result as { statusCode: number; body: string };
}

const BODY = JSON.stringify({ runner: 'llamacpp', modelId: 'org/model', quant: 'Q4_K_M' });

// seedIdFor('llamacpp', 'org/model', 'Q4_K_M').
const SEED_ID = 'llamacpp--org-model--Q4_K_M';

/** A seed instance carrying its id and model tags. */
function seedInstance(id: string, state: string, seedId: string = SEED_ID): InstanceInfo {
  return { instanceId: id, state, tags: { 'cloud-vm-llm:seed-id': seedId } };
}

// The seed instances DescribeInstances would return. The filter check mirrors
// findSeedInstances: a seed-id tag filter narrows, and no filter means every
// seed instance.
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
  findManagedInstances.mockImplementation((_k: string, _v: string, f?: { Name: string; Values: string[] }[]) =>
    Promise.resolve(seedLookup(_k, _v, f ?? [])),
  );
  weightsPresent.mockResolvedValue(false);
  buildSeedJob.mockImplementation((cfg: unknown) => ({ seedId: SEED_ID, cfg }));
  launchSeedInstance.mockResolvedValue({ seedId: SEED_ID, instanceId: 'i-seed', started: true });
  readSeedStatus.mockResolvedValue({ seedId: SEED_ID, state: 'transferring' } as SeedStatus);
  terminateInstance.mockResolvedValue(undefined);
  writeTerminalRecord.mockResolvedValue(undefined);
});

describe('starting a seed', () => {
  it('starts a fresh seed and derives the weights prefix the deploy would', async () => {
    const result = await handler(event('POST', { body: BODY }));

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(200);
    expect(reply.started).toBe(true);
    expect(reply.seedId).toBe(SEED_ID);
    expect(reply.weightsPrefix).toBe('models/llamacpp/org/model/Q4_K_M/');
    expect(reply.message).toContain(`spinloop cloud seed status ${SEED_ID}`);
    // The prefix is derived, not carried by the caller: the same inputs the
    // deploy path uses decide where the weights go.
    expect(buildSeedJob.mock.calls[0][0]).toMatchObject({
      runner: 'llamacpp',
      modelId: 'org/model',
      quant: 'Q4_K_M',
      weightsPrefix: 'models/llamacpp/org/model/Q4_K_M/',
    });
    expect(launchSeedInstance).toHaveBeenCalledTimes(1);
  });

  it('joins a running seed without starting a second one', async () => {
    seeds = [seedInstance('i-seed', 'running')];

    const result = await handler(event('POST', { body: BODY }));

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(200);
    expect(reply.started).toBe(false);
    expect(reply.joined).toBe(true);
    expect(reply.instanceId).toBe('i-seed');
    expect(launchSeedInstance).not.toHaveBeenCalled();
    // The join is by identity: the lookup carries the seed-id filter.
    expect(findManagedInstances).toHaveBeenCalledWith('cloud-vm-llm:managed', 'seed', [
      { Name: 'tag:cloud-vm-llm:seed-id', Values: [SEED_ID] },
    ]);
  });

  it('does not join a stopped seed: its compute has ceased', async () => {
    // A stopped instance is not in flight, the way the start's gate treats
    // it: waiting on it would wedge every request for these weights.
    seeds = [seedInstance('i-seed', 'stopped')];

    const result = await handler(event('POST', { body: BODY }));

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(200);
    expect(reply.started).toBe(true);
    expect(reply.joined).toBe(false);
    expect(launchSeedInstance).toHaveBeenCalledTimes(1);
  });

  it('refuses a start that would exceed the cap on running seeds', async () => {
    seeds = [
      seedInstance('i-a', 'running', 'llamacpp--other-a'),
      seedInstance('i-b', 'running', 'llamacpp--other-b'),
    ];

    const result = await handler(event('POST', { body: BODY }));

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(429);
    expect(reply.error).toContain('2 seeds are already running (cap 2)');
    expect(launchSeedInstance).not.toHaveBeenCalled();
  });

  it('does not count a stopped seed against the cap', async () => {
    // A dead body holds no compute: the slot it appears to fill is free.
    seeds = [
      seedInstance('i-a', 'running', 'llamacpp--other-a'),
      seedInstance('i-b', 'stopped', 'llamacpp--other-b'),
    ];

    const result = await handler(event('POST', { body: BODY }));

    expect(structured(result).statusCode).toBe(200);
    expect(JSON.parse(structured(result).body).started).toBe(true);
    expect(launchSeedInstance).toHaveBeenCalledTimes(1);
  });

  it('does nothing when the weights are already in S3, unless asked to', async () => {
    weightsPresent.mockResolvedValue(true);

    const result = await handler(event('POST', { body: BODY }));

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(200);
    expect(reply.started).toBe(false);
    expect(reply.alreadySeeded).toBe(true);
    expect(launchSeedInstance).not.toHaveBeenCalled();
  });

  it('re-seeds present weights when force is set', async () => {
    weightsPresent.mockResolvedValue(true);

    const result = await handler(
      event('POST', { body: JSON.stringify({ runner: 'llamacpp', modelId: 'org/model', quant: 'Q4_K_M', force: true }) }),
    );

    expect(structured(result).statusCode).toBe(200);
    expect(JSON.parse(structured(result).body).started).toBe(true);
    expect(launchSeedInstance).toHaveBeenCalledWith(expect.anything(), expect.anything(), { force: true });
  });

  it('rejects a request it cannot seed', async () => {
    const noRunner = await handler(event('POST', { body: JSON.stringify({ modelId: 'org/model' }) }));
    expect(structured(noRunner).statusCode).toBe(400);
    expect(JSON.parse(structured(noRunner).body).error).toContain('runner');

    const noModel = await handler(event('POST', { body: JSON.stringify({ runner: 'llamacpp' }) }));
    expect(structured(noModel).statusCode).toBe(400);
    expect(JSON.parse(structured(noModel).body).error).toContain('modelId');

    expect(launchSeedInstance).not.toHaveBeenCalled();
  });
});

describe('reading seed state', () => {
  it('reports a seed nobody has run as unknown, not failed', async () => {
    readSeedStatus.mockResolvedValue({ seedId: SEED_ID, state: 'failed' } as SeedStatus);

    const result = await handler(event('GET', { id: SEED_ID }));

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(404);
    expect(reply.state).toBe('unknown');
  });

  it('reports a known seed from its records joined with its instance', async () => {
    seeds = [seedInstance('i-seed', 'running')];
    const status = { seedId: SEED_ID, state: 'transferring', progressPercent: 41 } as SeedStatus;
    readSeedStatus.mockResolvedValue(status);

    const result = await handler(event('GET', { id: SEED_ID }));

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(200);
    expect(reply.state).toBe('transferring');
    expect(reply.progressPercent).toBe(41);
    expect(readSeedStatus).toHaveBeenCalledWith(SEED_ID, expect.objectContaining({ instanceId: 'i-seed' }));
  });

  it('lists the seeds with the model their instances carry', async () => {
    seeds = [
      { ...seedInstance('i-a', 'running', 'llamacpp--other-a'), tags: { 'cloud-vm-llm:seed-id': 'llamacpp--other-a', 'cloud-vm-llm:seed-model': 'other/a' } },
      { ...seedInstance('i-seed', 'running'), tags: { 'cloud-vm-llm:seed-id': SEED_ID, 'cloud-vm-llm:seed-model': 'org/model' } },
    ];
    readSeedStatus.mockImplementation(async (seedId: string): Promise<SeedStatus> =>
      ({ seedId, state: 'starting' }) as SeedStatus,
    );

    const result = await handler(event('GET'));

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(200);
    expect(reply.count).toBe(2);
    const ours = reply.seeds.find((s: { seedId: string }) => s.seedId === SEED_ID);
    expect(ours.modelId).toBe('org/model');
    expect(ours.state).toBe('starting');
  });
});

describe('stopping a seed', () => {
  it('terminates the seed and records the stop on its behalf', async () => {
    seeds = [seedInstance('i-seed', 'running')];

    const result = await handler(event('DELETE', { id: SEED_ID }));

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(200);
    expect(reply.stopped).toBe(true);
    expect(terminateInstance).toHaveBeenCalledWith('i-seed');
    // The instance cannot say the last word once it is gone; the stop records
    // the outcome as stopped rather than leaving it to read as a crash.
    expect(writeTerminalRecord).toHaveBeenCalledWith(SEED_ID, 'i-seed', 'stopped', 'stopped by request');
  });

  it('treats a stop with nothing running as satisfied, not an error', async () => {
    const result = await handler(event('DELETE', { id: SEED_ID }));

    const reply = JSON.parse(structured(result).body);
    expect(structured(result).statusCode).toBe(200);
    expect(reply.stopped).toBe(false);
    expect(terminateInstance).not.toHaveBeenCalled();
  });

  it('refuses to stop a seed it cannot name', async () => {
    const result = await handler(event('DELETE'));

    expect(structured(result).statusCode).toBe(400);
    expect(terminateInstance).not.toHaveBeenCalled();
  });
});

describe('an error the surface cannot answer', () => {
  it('reports an AWS failure as a 502 naming it, not a crash', async () => {
    findManagedInstances.mockRejectedValue(Object.assign(new Error('EC2 unavailable'), { name: 'TimeoutError' }));

    const result = await handler(event('POST', { body: BODY }));

    expect(structured(result).statusCode).toBe(502);
    expect(JSON.parse(structured(result).body).error).toContain('EC2 unavailable');
    expect(launchSeedInstance).not.toHaveBeenCalled();
  });
});
