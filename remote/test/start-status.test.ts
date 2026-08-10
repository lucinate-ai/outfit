import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Context, LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import { DAEMON_STATUS_CMD, DAEMON_UNREACHABLE } from '../lambda/shared/daemon';

// The status branch of the start Lambda is the only place the control plane
// asks the daemon "when did this engine last do work?". These tests drive that
// branch with the AWS calls stubbed, covering what it reports, what it does
// when the daemon cannot answer, and that it does not serialise the two SSM
// calls it makes.

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
const isSsmAgentOnline = vi.fn();
const runShellCommand = vi.fn();
const findEnvEip = vi.fn();

vi.mock('../lambda/shared/aws', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lambda/shared/aws')>()),
  findManagedInstance: (...args: unknown[]) => findManagedInstance(...args),
  isSsmAgentOnline: (...args: unknown[]) => isSsmAgentOnline(...args),
  runShellCommand: (...args: unknown[]) => runShellCommand(...args),
}));

vi.mock('../lambda/shared/environments', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lambda/shared/environments')>()),
  findEnvEip: (...args: unknown[]) => findEnvEip(...args),
}));

let handler: (event: LambdaFunctionURLEvent, context: Context) => Promise<LambdaFunctionURLResult>;

beforeAll(async () => {
  Object.assign(process.env, LAMBDA_ENV);
  ({ handler } = await import('../lambda/start/index'));
});

/** A GET on the start URL is what `outfit remote status` sends. */
const statusEvent = {
  queryStringParameters: { env: 'dev' },
  requestContext: { http: { method: 'GET' } },
} as unknown as LambdaFunctionURLEvent;

/** Narrow the result union to the structured reply these handlers return. */
function structured(result: LambdaFunctionURLResult): { statusCode: number; body: string } {
  return result as { statusCode: number; body: string };
}

function bodyOf(result: LambdaFunctionURLResult): Record<string, unknown> {
  return JSON.parse(structured(result).body);
}

/** A healthy engine: the health curl answers 200. */
const HEALTHY = { status: 'Success', stdout: '200' };

const daemonReply = (fields: Record<string, unknown>) => ({
  status: 'Success',
  stdout: JSON.stringify({ state: 'running', ...fields }),
});

beforeEach(() => {
  vi.clearAllMocks();
  findEnvEip.mockResolvedValue({ publicIp: '198.51.100.7' });
  findManagedInstance.mockResolvedValue({ instanceId: 'i-abc', state: 'running' });
  isSsmAgentOnline.mockResolvedValue(true);
});

/** Route each SSM invocation by the command it was given. */
function ssmRouter(daemon: unknown, health: unknown = HEALTHY) {
  return (_id: string, command: string) =>
    Promise.resolve(command === DAEMON_STATUS_CMD ? daemon : health);
}

describe('remote status activity reporting', () => {
  it('reports the daemon’s last-active time for a running endpoint', async () => {
    runShellCommand.mockImplementation(
      ssmRouter(daemonReply({ lastActiveAt: '2026-08-09T12:00:00Z', idleSeconds: 42 })),
    );

    const body = bodyOf(await handler(statusEvent, {} as Context));
    expect(body.state).toBe('running');
    expect(body.healthy).toBe(true);
    expect(body.lastActiveAt).toBe('2026-08-09T12:00:00Z');
    expect(body.idleSeconds).toBe(42);
  });

  it('treats an omitted idleSeconds as zero, not as absent', async () => {
    // The daemon omits idleSeconds rather than sending 0 while the engine is
    // working right now. The timestamp is the gate, so this must survive.
    runShellCommand.mockImplementation(
      ssmRouter(daemonReply({ lastActiveAt: '2026-08-09T12:00:00Z' })),
    );

    const body = bodyOf(await handler(statusEvent, {} as Context));
    expect(body.lastActiveAt).toBe('2026-08-09T12:00:00Z');
    expect(body.idleSeconds).toBe(0);
  });

  it('asks the daemon and the health endpoint concurrently, not in sequence', async () => {
    const inFlight = new Set<string>();
    let seenTogether = false;
    runShellCommand.mockImplementation(async (_id: string, command: string) => {
      inFlight.add(command);
      // Yield so the other call can start if it was issued concurrently.
      await new Promise((resolve) => setTimeout(resolve, 5));
      if (inFlight.size > 1) {
        seenTogether = true;
      }
      inFlight.delete(command);
      return command === DAEMON_STATUS_CMD
        ? daemonReply({ lastActiveAt: '2026-08-09T12:00:00Z', idleSeconds: 1 })
        : HEALTHY;
    });

    await handler(statusEvent, {} as Context);
    expect(runShellCommand).toHaveBeenCalledTimes(2);
    expect(seenTogether).toBe(true);
  });

  it('omits the fields when the engine has done no work yet', async () => {
    runShellCommand.mockImplementation(ssmRouter(daemonReply({ state: 'idle' })));

    const body = bodyOf(await handler(statusEvent, {} as Context));
    expect(body.healthy).toBe(true);
    expect(body).not.toHaveProperty('lastActiveAt');
    expect(body).not.toHaveProperty('idleSeconds');
  });

  it.each([
    ['unreachable', { status: 'Success', stdout: `${DAEMON_UNREACHABLE}\n` }],
    ['unparseable', { status: 'Success', stdout: 'curl: (7) Failed to connect' }],
    ['a failed SSM command', { status: 'Failed', stdout: '' }],
  ])('degrades quietly when the daemon reply is %s', async (_name, daemon) => {
    runShellCommand.mockImplementation(ssmRouter(daemon));

    const result = await handler(statusEvent, {} as Context);
    expect(structured(result).statusCode).toBe(200);
    const body = bodyOf(result);
    // The rest of the report is untouched: this must never be able to turn a
    // working status into a failing one.
    expect(body.state).toBe('running');
    expect(body.healthy).toBe(true);
    expect(body.base_url).toContain('198.51.100.7');
    expect(body).not.toHaveProperty('lastActiveAt');
  });

  it('survives an SSM error without failing the report', async () => {
    runShellCommand.mockImplementation((_id: string, command: string) =>
      command === DAEMON_STATUS_CMD
        ? Promise.reject(new Error('InvalidInstanceId'))
        : Promise.resolve(HEALTHY),
    );

    const result = await handler(statusEvent, {} as Context);
    expect(structured(result).statusCode).toBe(200);
    const body = bodyOf(result);
    expect(body.healthy).toBe(true);
    expect(body).not.toHaveProperty('lastActiveAt');
  });

  it('makes no SSM call at all for a stopped instance', async () => {
    findManagedInstance.mockResolvedValue({ instanceId: 'i-abc', state: 'stopped' });

    const body = bodyOf(await handler(statusEvent, {} as Context));
    expect(body.state).toBe('stopped');
    expect(body.healthy).toBe(false);
    expect(body).not.toHaveProperty('lastActiveAt');
    // Reaching the daemon needs a running box, so the branch returns before
    // spending an SSM round trip to learn nothing.
    expect(runShellCommand).not.toHaveBeenCalled();
    expect(isSsmAgentOnline).not.toHaveBeenCalled();
  });

  it('reports nothing when the instance is undeployed', async () => {
    findManagedInstance.mockResolvedValue(null);
    findEnvEip.mockResolvedValue(null);

    const body = bodyOf(await handler(statusEvent, {} as Context));
    expect(body.state).toBe('undeployed');
    expect(body).not.toHaveProperty('lastActiveAt');
    expect(runShellCommand).not.toHaveBeenCalled();
  });

  it('skips the daemon when the SSM agent is offline', async () => {
    isSsmAgentOnline.mockResolvedValue(false);

    const body = bodyOf(await handler(statusEvent, {} as Context));
    expect(body.state).toBe('running');
    expect(body.healthy).toBe(false);
    expect(body).not.toHaveProperty('lastActiveAt');
    expect(runShellCommand).not.toHaveBeenCalled();
  });
});
