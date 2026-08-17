/**
 * The seed sweep and the inference sweep act on disjoint populations and judge
 * them by different signals. This is the regression guard for the specific
 * mistake of letting the seed pass reach the daemon scrape: a seed instance
 * runs no outfit daemon, so that scrape would fail against it and yield nothing
 * usable — the sweep would then reap or spare seeds for the wrong reasons.
 */

import { beforeEach, describe, expect, it, vi } from 'vitest';

const findManagedInstances = vi.fn();
const terminateInstance = vi.fn();
const runShellCommand = vi.fn();
const isSsmAgentOnline = vi.fn();
const latestStream = vi.fn();
const writeTerminalRecord = vi.fn();

vi.mock('../lambda/shared/aws', async () => {
  const actual = await vi.importActual<typeof import('../lambda/shared/aws')>(
    '../lambda/shared/aws',
  );
  return {
    ...actual,
    findManagedInstances: (...args: unknown[]) => findManagedInstances(...args),
    findManagedInstance: vi.fn().mockResolvedValue(null),
    terminateInstance: (...args: unknown[]) => terminateInstance(...args),
    runShellCommand: (...args: unknown[]) => runShellCommand(...args),
    isSsmAgentOnline: (...args: unknown[]) => isSsmAgentOnline(...args),
    readDeployConfig: vi.fn().mockResolvedValue({}),
  };
});

vi.mock('../lambda/shared/seed/status', () => ({
  latestStream: (...args: unknown[]) => latestStream(...args),
  writeTerminalRecord: (...args: unknown[]) => writeTerminalRecord(...args),
}));

process.env.TAG_KEY = 'cloud-vm-llm';
process.env.TAG_VALUE = 'endpoint';
process.env.IDLE_THRESHOLD_MINUTES = '15';
process.env.GRACE_PERIOD_MINUTES = '30';
process.env.MAX_RUNTIME_MINUTES = '240';
process.env.MAX_SEED_MINUTES = '60';
process.env.SEED_STALL_MINUTES = '10';

const minutesAgo = (n: number) => new Date(Date.now() - n * 60_000);

describe('the seed sweep', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    latestStream.mockResolvedValue(null);
  });

  it('asks only for seed-tagged instances, never endpoint-tagged ones', async () => {
    findManagedInstances.mockResolvedValue([]);
    const { seedSweep } = await import('../lambda/stop/index');
    await seedSweep();
    expect(findManagedInstances).toHaveBeenCalledWith('cloud-vm-llm', 'seed');
  });

  it('never runs the daemon scrape a seed instance cannot answer', async () => {
    findManagedInstances.mockResolvedValue([
      {
        instanceId: 'i-seed',
        state: 'running',
        launchTime: minutesAgo(5),
        tags: { 'cloud-vm-llm:seed-id': 'vllm--org-m' },
      },
    ]);
    latestStream.mockResolvedValue({ streamName: 's', lastEventTimestamp: Date.now() });

    const { seedSweep } = await import('../lambda/stop/index');
    await seedSweep();

    expect(runShellCommand).not.toHaveBeenCalled();
    expect(isSsmAgentOnline).not.toHaveBeenCalled();
  });

  it('judges liveness from the seed records, not from SSM', async () => {
    findManagedInstances.mockResolvedValue([
      {
        instanceId: 'i-seed',
        state: 'running',
        launchTime: minutesAgo(30),
        tags: { 'cloud-vm-llm:seed-id': 'vllm--org-m' },
      },
    ]);
    latestStream.mockResolvedValue({ streamName: 's', lastEventTimestamp: Date.now() });

    const { seedSweep } = await import('../lambda/stop/index');
    await seedSweep();

    expect(latestStream).toHaveBeenCalledWith('vllm--org-m');
    expect(terminateInstance).not.toHaveBeenCalled();
  });

  it('reaps a stalled seed and records why, so status never says "in progress"', async () => {
    findManagedInstances.mockResolvedValue([
      {
        instanceId: 'i-seed',
        state: 'running',
        launchTime: minutesAgo(30),
        tags: { 'cloud-vm-llm:seed-id': 'vllm--org-m' },
      },
    ]);
    latestStream.mockResolvedValue({
      streamName: 's',
      lastEventTimestamp: minutesAgo(20).getTime(),
    });

    const { seedSweep } = await import('../lambda/stop/index');
    await seedSweep();

    expect(terminateInstance).toHaveBeenCalledWith('i-seed');
    expect(writeTerminalRecord).toHaveBeenCalledWith(
      'vllm--org-m',
      'i-seed',
      'failed',
      expect.stringMatching(/no progress reported/),
    );
  });

  it('reaps a seed past the hard cap', async () => {
    findManagedInstances.mockResolvedValue([
      {
        instanceId: 'i-seed',
        state: 'running',
        launchTime: minutesAgo(90),
        tags: { 'cloud-vm-llm:seed-id': 'vllm--org-m' },
      },
    ]);
    latestStream.mockResolvedValue({ streamName: 's', lastEventTimestamp: Date.now() });

    const { seedSweep } = await import('../lambda/stop/index');
    await seedSweep();

    expect(terminateInstance).toHaveBeenCalledWith('i-seed');
    expect(writeTerminalRecord).toHaveBeenCalledWith(
      'vllm--org-m',
      'i-seed',
      'failed',
      expect.stringMatching(/past the 60 min cap/),
    );
  });

  it('carries on after one seed errors, rather than abandoning the sweep', async () => {
    findManagedInstances.mockResolvedValue([
      { instanceId: 'i-a', state: 'running', launchTime: minutesAgo(90), tags: { 'cloud-vm-llm:seed-id': 'a' } },
      { instanceId: 'i-b', state: 'running', launchTime: minutesAgo(90), tags: { 'cloud-vm-llm:seed-id': 'b' } },
    ]);
    latestStream.mockRejectedValueOnce(new Error('CloudWatch is unhappy'));
    latestStream.mockResolvedValue({ streamName: 's', lastEventTimestamp: Date.now() });

    const { seedSweep } = await import('../lambda/stop/index');
    await seedSweep();

    // The first threw; the second was still judged and reaped.
    expect(terminateInstance).toHaveBeenCalledWith('i-b');
  });
});
