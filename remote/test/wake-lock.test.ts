import { beforeEach, describe, expect, it, vi } from 'vitest';
import { acquireWakeLock, releaseWakeLock, wakeLockHeld, wakeLockParam } from '../lambda/shared/wake-lock';

// The lock is an SSM parameter created only if absent. The SSM client is
// replaced by an in-memory store with the same create/read/delete behaviour,
// so the tests cover the lock's rules, not SSM.

const store = new Map<string, string>();
const failures = new Map<string, Error>();

function awsError(name: string): Error {
  return Object.assign(new Error(name), { name });
}

const sendHook = vi.fn();

vi.mock('@aws-sdk/client-ssm', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@aws-sdk/client-ssm')>()),
  SSMClient: class {
    async send(cmd: { constructor: { name: string }; input: { Name: string; Value?: string; Overwrite?: boolean } }) {
      sendHook(cmd);
      const kind = cmd.constructor.name;
      const failure = failures.get(kind);
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

const NOW = new Date('2026-10-05T12:00:00Z');
const FUTURE = new Date('2026-10-05T12:15:00Z');
const PAST = new Date('2026-10-05T11:00:00Z');

function lockOf(owner: string, expiresAt: Date): string {
  return JSON.stringify({ owner, expiresAt: expiresAt.toISOString() });
}

beforeEach(() => {
  store.clear();
  failures.clear();
  sendHook.mockReset();
});

describe('acquireWakeLock', () => {
  it('takes a free lock and records the owner and expiry', async () => {
    expect(await acquireWakeLock('dev', 'req-1', FUTURE, NOW)).toBe(true);
    expect(JSON.parse(store.get(wakeLockParam('dev'))!)).toEqual({
      owner: 'req-1',
      expiresAt: FUTURE.toISOString(),
    });
  });

  it('names the lock parameter under the environment', () => {
    expect(wakeLockParam('dev')).toBe('/cloud-vm-llm/dev/wake-lock');
  });

  it('refuses a lock another start holds, and leaves it as it was', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-1', FUTURE));
    expect(await acquireWakeLock('dev', 'req-2', FUTURE, NOW)).toBe(false);
    expect(JSON.parse(store.get(wakeLockParam('dev'))!).owner).toBe('req-1');
  });

  it('lets only one of two simultaneous starts take a free lock', async () => {
    const results = await Promise.all([
      acquireWakeLock('dev', 'req-1', FUTURE, NOW),
      acquireWakeLock('dev', 'req-2', FUTURE, NOW),
    ]);
    expect(results.filter(Boolean)).toHaveLength(1);
  });

  it('takes over a lock that has expired', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-old', PAST));
    expect(await acquireWakeLock('dev', 'req-2', FUTURE, NOW)).toBe(true);
    expect(JSON.parse(store.get(wakeLockParam('dev'))!).owner).toBe('req-2');
  });

  it.each([['not json'], ['{"owner":1}'], ['']])('takes over a value that is not a lock: %j', async (raw) => {
    store.set(wakeLockParam('dev'), raw);
    expect(await acquireWakeLock('dev', 'req-2', FUTURE, NOW)).toBe(true);
    expect(JSON.parse(store.get(wakeLockParam('dev'))!).owner).toBe('req-2');
  });

  it('does not delete a lock that changed between its two reads', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-old', PAST));
    // After the first read finds the expired lock, another start replaces it.
    let reads = 0;
    sendHook.mockImplementation((cmd: { constructor: { name: string } }) => {
      if (cmd.constructor.name === 'GetParameterCommand' && ++reads === 1) {
        queueMicrotask(() => store.set(wakeLockParam('dev'), lockOf('req-new', FUTURE)));
      }
    });
    expect(await acquireWakeLock('dev', 'req-2', FUTURE, NOW)).toBe(false);
    expect(JSON.parse(store.get(wakeLockParam('dev'))!).owner).toBe('req-new');
  });

  it('keeps locks of different environments apart', async () => {
    expect(await acquireWakeLock('a', 'req-1', FUTURE, NOW)).toBe(true);
    expect(await acquireWakeLock('b', 'req-2', FUTURE, NOW)).toBe(true);
  });

  it('throws on a failure other than "already exists", rather than proceeding unlocked', async () => {
    failures.set('PutParameterCommand', awsError('AccessDeniedException'));
    await expect(acquireWakeLock('dev', 'req-1', FUTURE, NOW)).rejects.toThrow('AccessDeniedException');
    expect(store.size).toBe(0);
  });

  it('throws when the held lock cannot be read', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-1', FUTURE));
    failures.set('GetParameterCommand', awsError('ThrottlingException'));
    await expect(acquireWakeLock('dev', 'req-2', FUTURE, NOW)).rejects.toThrow('ThrottlingException');
  });
});

describe('wakeLockHeld', () => {
  it('is true for a valid lock and false once it has expired', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-1', FUTURE));
    expect(await wakeLockHeld('dev', NOW)).toBe(true);
    expect(await wakeLockHeld('dev', new Date(FUTURE.getTime() + 1))).toBe(false);
  });

  it('is false with no lock, and for a value that is not a lock', async () => {
    expect(await wakeLockHeld('dev', NOW)).toBe(false);
    store.set(wakeLockParam('dev'), 'not json');
    expect(await wakeLockHeld('dev', NOW)).toBe(false);
  });

  it('writes nothing', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-1', PAST));
    await wakeLockHeld('dev', NOW);
    expect(JSON.parse(store.get(wakeLockParam('dev'))!).owner).toBe('req-1');
  });

  it('throws when the lock cannot be read, leaving the caller to decide', async () => {
    failures.set('GetParameterCommand', awsError('ThrottlingException'));
    await expect(wakeLockHeld('dev', NOW)).rejects.toThrow('ThrottlingException');
  });
});

describe('releaseWakeLock', () => {
  it('removes the lock its owner holds', async () => {
    await acquireWakeLock('dev', 'req-1', FUTURE, NOW);
    await releaseWakeLock('dev', 'req-1');
    expect(store.has(wakeLockParam('dev'))).toBe(false);
  });

  it('leaves a lock another start holds', async () => {
    store.set(wakeLockParam('dev'), lockOf('req-2', FUTURE));
    await releaseWakeLock('dev', 'req-1');
    expect(JSON.parse(store.get(wakeLockParam('dev'))!).owner).toBe('req-2');
  });

  it('does nothing when there is no lock', async () => {
    await expect(releaseWakeLock('dev', 'req-1')).resolves.toBeUndefined();
  });

  it('lets the next start take the lock after a release', async () => {
    await acquireWakeLock('dev', 'req-1', FUTURE, NOW);
    await releaseWakeLock('dev', 'req-1');
    expect(await acquireWakeLock('dev', 'req-2', FUTURE, NOW)).toBe(true);
  });
});
