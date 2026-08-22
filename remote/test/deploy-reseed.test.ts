import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LambdaFunctionURLEvent } from 'aws-lambda';

// Controlled per test.
let weightsAreThere = true;
const launched: unknown[] = [];
const persisted: unknown[] = [];

vi.mock('../lambda/shared/aws', () => ({
  requireEnv: (name: string) => (name === 'ENGINE_PORT' ? '8000' : `stub-${name}`),
  readDeployConfig: vi.fn(),
  writeDeployConfig: vi.fn(async (_param: string, cfg: unknown) => {
    persisted.push(cfg);
  }),
  errorName: (err: unknown) => (err instanceof Error ? err.name : String(err)),
}));

vi.mock('../lambda/shared/environments', () => ({
  environmentFrom: (_q: unknown, body: unknown) => (body as string) ?? 'default',
  deployConfigParam: (env: string) => `/cloud-vm-llm/${env}/deploy-config`,
  baseUrlFor: (ip: string, port: number) => `http://${ip}:${port}/v1`,
  ensureEnvEip: async () => ({ publicIp: '198.51.100.7' }),
  ensureEnvSecurityGroup: async () => undefined,
  findEnvSecurityGroup: async () => 'sg-1',
  ensureEnvApiKey: async () => 'key',
}));

vi.mock('../lambda/shared/seed', () => ({
  weightsPresent: async () => weightsAreThere,
}));

// Launching moved to its own module with the seed rework; the deploy path now
// builds a job and asks for a seed id back rather than an instance id.
vi.mock('../lambda/shared/seed/launch', () => ({
  seedInfraFromEnv: () => ({ bucket: 'weights' }),
  buildSeedJob: (cfg: unknown) => ({ seedId: 'llamacpp--m', cfg }),
  launchSeedInstance: async (job: { seedId: string }, _infra: unknown, opts?: { force?: boolean }) => {
    launched.push({ job, force: opts?.force ?? false });
    return { seedId: job.seedId, instanceId: 'i-seed', started: true };
  },
}));

let handler: (event: LambdaFunctionURLEvent) => Promise<{ statusCode: number; body: string }>;

beforeAll(async () => {
  ({ handler } = (await import('../lambda/deploy/index')) as never);
});

const CONFIG = {
  environment: 'glimmer',
  runner: 'llamacpp',
  modelId: 'meta-models/Muse-Glimmer-30B-GGUF',
  quant: 'kquant-dynamic',
  contextSize: 524288,
  servedModelName: 'muse-glimmer-30b',
  serveArgs: [],
  allowedCidr: '203.0.113.7/32',
};

function post(body: Record<string, unknown>): LambdaFunctionURLEvent {
  return {
    requestContext: { http: { method: 'POST' } },
    body: JSON.stringify(body),
    isBase64Encoded: false,
  } as unknown as LambdaFunctionURLEvent;
}

beforeEach(() => {
  launched.length = 0;
  persisted.length = 0;
  weightsAreThere = true;
});

describe('deploy --reseed', () => {
  it('seeds stored weights when a re-seed is requested', async () => {
    const res = await handler(post({ ...CONFIG, reseed: true }));
    expect(res.statusCode).toBe(200);
    expect(launched).toHaveLength(1);
    const reply = JSON.parse(res.body);
    expect(reply.seeding).toBe(true);
    // The stable seed id, so the operator can follow it.
    expect(reply.seedId).toBe('llamacpp--m');
  });

  it('takes the same force path as `outfit remote seed start --force`', async () => {
    // One behaviour, one implementation: --reseed must escape the launch
    // idempotency token exactly as a forced seed does, or it would be handed
    // back the attempt it is replacing.
    await handler(post({ ...CONFIG, reseed: true }));
    expect(launched[0]).toMatchObject({ force: true });
  });

  it('does not force a seed that is merely filling in absent weights', async () => {
    weightsAreThere = false;
    await handler(post(CONFIG));
    expect(launched[0]).toMatchObject({ force: false });
  });

  it('leaves stored weights alone without the request', async () => {
    const res = await handler(post(CONFIG));
    expect(res.statusCode).toBe(200);
    expect(launched).toHaveLength(0);
    expect(JSON.parse(res.body).seeding).toBe(false);
  });

  it('starts exactly one seed when the weights are also absent', async () => {
    weightsAreThere = false;
    await handler(post({ ...CONFIG, reseed: true }));
    expect(launched).toHaveLength(1);
  });

  it('still seeds absent weights with no request', async () => {
    weightsAreThere = false;
    await handler(post(CONFIG));
    expect(launched).toHaveLength(1);
  });

  it('rejects a non-boolean reseed', async () => {
    const res = await handler(post({ ...CONFIG, reseed: 'yes' }));
    expect(res.statusCode).toBe(400);
    expect(JSON.parse(res.body).error).toMatch(/reseed must be a boolean/);
  });

  it('never persists reseed into the stored deploy-config', async () => {
    // Stored, it would re-seed on every wake that read the config back.
    await handler(post({ ...CONFIG, reseed: true }));
    expect(persisted).toHaveLength(1);
    expect(persisted[0]).not.toHaveProperty('reseed');
  });
});
