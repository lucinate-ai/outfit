import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Context, LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import type { DeployConfig } from '../lambda/shared/deploy-config';
import { DAEMON_STATUS_CMD } from '../lambda/shared/daemon';

// A boot that dies leaves a running instance with no daemon on it. Left to
// itself the wake can only report "still starting" until its deadline, so the
// GPU bills for a box that will never serve — the failure this covers. The
// boot records why it died; the wake reads that and ends.

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
  // The weights gate asks this on every wake; these tests are about the wake
  // itself, so the weights are always present.
  weightsPresent: async () => true,
}));

// These tests are about the wake itself, so the environment's lock is always free.
vi.mock('../lambda/shared/wake-lock', () => ({
  acquireWakeLock: async () => true,
  releaseWakeLock: async () => undefined,
  wakeLockHeld: async () => false,
}));

let handler: (event: LambdaFunctionURLEvent, context: Context) => Promise<LambdaFunctionURLResult>;
let buildInferenceUserData: (env: string, cfg: DeployConfig) => string;
let BOOT_FAILED_MARKER: string;

beforeAll(async () => {
  Object.assign(process.env, LAMBDA_ENV);
  ({ handler, buildInferenceUserData, BOOT_FAILED_MARKER } = await import('../lambda/start/index'));
});

const wakeEvent = {
  queryStringParameters: { env: 'dev' },
  requestContext: { http: { method: 'POST' } },
} as unknown as LambdaFunctionURLEvent;

const context = { getRemainingTimeInMillis: () => 600_000 } as unknown as Context;

function structured(result: LambdaFunctionURLResult): { statusCode: number; body: string } {
  return result as { statusCode: number; body: string };
}

const LLAMACPP: DeployConfig = {
  runner: 'llamacpp',
  modelId: 'org/model',
  quant: 'Q4_K_M',
  weightsPrefix: 'llamacpp/org/model/Q4_K_M',
  contextSize: 32768,
  servedModelName: 'friendly',
  serveArgs: [],
  companions: {},
  spinloopVersion: 'latest',
};

// The reason a real boot recorded: the incident that motivated this — a
// renamed repository whose release no longer carries the old asset name.
const RECORDED_FAILURE =
  'SPINLOOP_BOOT_FAILED: line 71 exited 22: curl -fsSL "$SPINLOOP_URL/spinloop_linux_amd64.tar.gz" -o /tmp/spinloop-dl/spinloop_linux_amd64.tar.gz';

/** A daemon that never answers, with the marker holding whatever the boot left. */
function instanceWith(markerContents: string) {
  runShellCommand.mockImplementation((_id: string, command: string) => {
    if (command === DAEMON_STATUS_CMD) {
      return Promise.resolve({ status: 'Success', stdout: '' });
    }
    if (command.includes(BOOT_FAILED_MARKER)) {
      return Promise.resolve({ status: 'Success', stdout: markerContents });
    }
    return Promise.resolve({ status: 'Success', stdout: '200' });
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  readDeployConfig.mockResolvedValue(LLAMACPP);
  findEnvEip.mockResolvedValue({ publicIp: '198.51.100.7', allocationId: 'eipalloc-test' });
  findEnvSecurityGroup.mockResolvedValue('sg-test');
  readEnvApiKey.mockResolvedValue('sk-test');
  isSsmAgentOnline.mockResolvedValue(true);
  startEngineDaemon.mockResolvedValue(true);
  findManagedInstance.mockResolvedValue({ instanceId: 'i-broken', state: 'running' });
  getInstance.mockResolvedValue({ instanceId: 'i-broken', state: 'running', launchTime: new Date() });
});

describe('a boot that died is reported, not waited out', () => {
  it('answers with the recorded reason instead of a generic timeout', async () => {
    instanceWith(RECORDED_FAILURE);

    const result = await handler(wakeEvent, context);

    expect(structured(result).statusCode).toBe(500);
    const body = JSON.parse(structured(result).body);
    expect(body.state).toBe('boot-failed');
    expect(body.instance_id).toBe('i-broken');
    // The failing command travels back to the caller: the whole point is that
    // the reason reaches whoever ran the start.
    expect(body.message).toContain('spinloop_linux_amd64.tar.gz');
    // Not "starting" — a retry cannot fix a boot that already ran, since user
    // data does not re-run on a stop/start cycle.
    expect(body.retry_after_seconds).toBeUndefined();
  });

  it('gives up rather than asking a daemon that was never installed to start', async () => {
    instanceWith(RECORDED_FAILURE);

    await handler(wakeEvent, context);

    expect(startEngineDaemon).not.toHaveBeenCalled();
  });

  it('keeps waiting when the marker is absent, so a slow boot still wins', async () => {
    // No marker, and the daemon answers on the second look: the ordinary
    // slow-boot path must not be cut short by the new check.
    let daemonLooks = 0;
    runShellCommand.mockImplementation((_id: string, command: string) => {
      if (command === DAEMON_STATUS_CMD) {
        daemonLooks += 1;
        return Promise.resolve({
          status: 'Success',
          stdout: daemonLooks > 1 ? JSON.stringify({ state: 'stopped' }) : '',
        });
      }
      if (command.includes(BOOT_FAILED_MARKER)) {
        return Promise.resolve({ status: 'Success', stdout: '' });
      }
      return Promise.resolve({ status: 'Success', stdout: '200' });
    });

    const result = await handler(wakeEvent, context);

    expect(structured(result).statusCode).toBe(200);
    expect(JSON.parse(structured(result).body).state).toBe('ready');
    expect(startEngineDaemon).toHaveBeenCalled();
  });

  it('treats an unreadable marker as no failure', async () => {
    // An SSM command that did not succeed says nothing about the boot; only
    // the marker's own text ends a wake. The daemon answers on the second
    // look, so the unreadable marker is read exactly once, in between.
    let daemonLooks = 0;
    runShellCommand.mockImplementation((_id: string, command: string) => {
      if (command === DAEMON_STATUS_CMD) {
        daemonLooks += 1;
        return Promise.resolve({
          status: 'Success',
          stdout: daemonLooks > 1 ? JSON.stringify({ state: 'stopped' }) : '',
        });
      }
      if (command.includes(BOOT_FAILED_MARKER)) {
        return Promise.resolve({ status: 'Failed', stdout: '' });
      }
      return Promise.resolve({ status: 'Success', stdout: '200' });
    });

    const result = await handler(wakeEvent, context);

    expect(structured(result).statusCode).toBe(200);
  });
});

describe('the boot script records why it died', () => {
  const script = () => buildInferenceUserData('dev-1', LLAMACPP);

  it('arms an ERR trap that writes the failing command to the marker', () => {
    expect(script()).toContain(`echo "$msg" >${BOOT_FAILED_MARKER}`);
    expect(script()).toMatch(/trap '.*BASH_COMMAND.*' ERR/);
  });

  it('inherits the trap into subshells, so a failed $(...) still records', () => {
    // Without -E the trap does not fire inside command substitutions, which
    // is where the version lookup runs.
    expect(script()).toContain('set -Eeuxo pipefail');
  });

  it('clears a stale marker before doing anything that could write one', () => {
    const text = script();
    expect(text).toContain(`rm -f ${BOOT_FAILED_MARKER}`);
    expect(text.indexOf(`rm -f ${BOOT_FAILED_MARKER}`)).toBeLessThan(text.indexOf('nvidia-smi'));
  });

  it('names the missing asset when a release does not carry it', () => {
    // A rename resolves a version happily and then 404s the download, so the
    // bare curl failure would point at the wrong thing.
    expect(script()).toContain('the release may not publish that asset name');
  });
});
