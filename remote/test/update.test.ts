import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LambdaFunctionURLEvent } from 'aws-lambda';

// The update Lambda: dispatches on ?cmd=<name>. Currently only set-keep.

const LAMBDA_ENV = {
  TAG_KEY: 'cloud-vm-llm:managed',
  TAG_VALUE: 'true',
};

const findManagedInstance = vi.fn();
const tagInstance = vi.fn();

vi.mock('../lambda/shared/aws', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lambda/shared/aws')>()),
  findManagedInstance: (...args: unknown[]) => findManagedInstance(...args),
  tagInstance: (...args: unknown[]) => tagInstance(...args),
}));

let handler: (event: LambdaFunctionURLEvent) => Promise<unknown>;

beforeAll(async () => {
  Object.assign(process.env, LAMBDA_ENV);
  ({ handler } = await import('../lambda/update/index'));
});

function bodyOf(result: unknown): Record<string, unknown> {
  return JSON.parse((result as { statusCode: number; body: string }).body);
}

function statusOf(result: unknown): number {
  return (result as { statusCode: number }).statusCode;
}

function updateEvent(query: Record<string, string>) {
  return {
    queryStringParameters: query,
  } as unknown as LambdaFunctionURLEvent;
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('set-keep', () => {
  it('tags the instance and returns the deadline', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });
    tagInstance.mockResolvedValue(undefined);

    const result = await handler(
      updateEvent({ env: 'dev', cmd: 'set-keep', retainUntil: '2025-01-01T04:00:00Z' }),
    );
    const body = bodyOf(result);
    expect(statusOf(result)).toBe(200);
    expect(body.retainUntil).toBe('2025-01-01T04:00:00.000Z');
    expect(body.instanceId).toBe('i-run');
    expect(tagInstance).toHaveBeenCalledWith(
      'i-run',
      'Retain-Until',
      '2025-01-01T04:00:00.000Z',
    );
  });

  it('returns 404 when no instance exists', async () => {
    findManagedInstance.mockResolvedValue(null);

    const result = await handler(
      updateEvent({ env: 'dev', cmd: 'set-keep', retainUntil: '2025-01-01T04:00:00Z' }),
    );
    const body = bodyOf(result);
    expect(statusOf(result)).toBe(404);
    expect(body.error).toContain('no running instance');
  });
});

describe('validation', () => {
  it('returns 400 when retainUntil is missing', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });

    const result = await handler(updateEvent({ env: 'dev', cmd: 'set-keep' }));
    const body = bodyOf(result);
    expect(statusOf(result)).toBe(400);
    expect(body.error).toContain('missing retainUntil');
  });

  it('returns 400 when retainUntil is invalid', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-run', state: 'running' });

    const result = await handler(
      updateEvent({ env: 'dev', cmd: 'set-keep', retainUntil: 'not-a-date' }),
    );
    const body = bodyOf(result);
    expect(statusOf(result)).toBe(400);
    expect(body.error).toContain('invalid retainUntil');
  });

  it('returns 400 when cmd is missing', async () => {
    const result = await handler(updateEvent({ env: 'dev' }));
    const body = bodyOf(result);
    expect(statusOf(result)).toBe(400);
    expect(body.error).toContain('missing command');
  });

  it('returns 400 when cmd is unknown', async () => {
    const result = await handler(
      updateEvent({ env: 'dev', cmd: 'frobnicate' }),
    );
    const body = bodyOf(result);
    expect(statusOf(result)).toBe(400);
    expect(body.error).toContain('unknown command');
    expect(body.error).toContain('set-keep');
  });

  it('returns 400 when env is missing', async () => {
    const result = await handler(updateEvent({ cmd: 'set-keep', retainUntil: '2025-01-01T04:00:00Z' }));
    const body = bodyOf(result);
    expect(statusOf(result)).toBe(400);
    expect(body.error).toContain('missing environment');
  });
});
