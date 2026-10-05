/**
 * The per-environment lock a start holds while it works on an environment.
 *
 * The start Lambda decides whether to launch by looking the instance up by tag,
 * which is eventually consistent, so two starts close together can each miss the
 * other's new instance and each launch one. The lock is an SSM parameter created
 * only if absent, so exactly one of two simultaneous starts creates it.
 *
 * The value records who holds the lock and when it expires. The expiry is the
 * time the holder could run until, so a start that was killed before it could
 * release the lock blocks its environment for no longer than that.
 */

import {
  DeleteParameterCommand,
  GetParameterCommand,
  PutParameterCommand,
  SSMClient,
} from '@aws-sdk/client-ssm';
import { errorName } from './aws';

const ssm = new SSMClient({});

/** SSM parameter holding an environment's start lock. */
export function wakeLockParam(env: string): string {
  return `/cloud-vm-llm/${env}/wake-lock`;
}

interface LockValue {
  owner: string;
  expiresAt: string;
}

/** The lock a stored value describes, or null when it cannot be read as one. */
function parseLock(raw: string | undefined): LockValue | null {
  if (!raw) {
    return null;
  }
  try {
    const parsed = JSON.parse(raw) as Partial<LockValue>;
    if (typeof parsed.owner === 'string' && typeof parsed.expiresAt === 'string') {
      return { owner: parsed.owner, expiresAt: parsed.expiresAt };
    }
  } catch {
    // Fall through: a value that is not JSON is not a lock anyone could release.
  }
  return null;
}

/** Create the lock if no parameter exists. True when this call created it. */
async function createIfAbsent(env: string, value: string): Promise<boolean> {
  try {
    await ssm.send(
      new PutParameterCommand({
        Name: wakeLockParam(env),
        Value: value,
        Type: 'String',
        Overwrite: false,
      }),
    );
    return true;
  } catch (err) {
    if (errorName(err) === 'ParameterAlreadyExists') {
      return false;
    }
    throw err;
  }
}

async function readRaw(env: string): Promise<string | null> {
  try {
    const out = await ssm.send(new GetParameterCommand({ Name: wakeLockParam(env) }));
    return out.Parameter?.Value ?? '';
  } catch (err) {
    if (errorName(err) === 'ParameterNotFound') {
      return null;
    }
    throw err;
  }
}

async function remove(env: string): Promise<void> {
  try {
    await ssm.send(new DeleteParameterCommand({ Name: wakeLockParam(env) }));
  } catch (err) {
    if (errorName(err) !== 'ParameterNotFound') {
      throw err;
    }
  }
}

/**
 * Take the environment's lock for `owner`, expiring at `expiresAt`. Returns
 * true when this call holds the lock, false when another start does. An expired
 * lock, or one whose value cannot be read, is taken over. Any other failure to
 * read or write the lock is thrown, so the caller never proceeds unlocked.
 *
 * SSM has no compare-and-set, so two starts that both find the same expired
 * lock within milliseconds can both pass the re-read below; the create that
 * follows lets only one of them win unless a third start deletes in between.
 */
export async function acquireWakeLock(
  env: string,
  owner: string,
  expiresAt: Date,
  now: Date = new Date(),
): Promise<boolean> {
  const value = JSON.stringify({ owner, expiresAt: expiresAt.toISOString() } satisfies LockValue);
  if (await createIfAbsent(env, value)) {
    return true;
  }
  const current = await readRaw(env);
  if (current === null) {
    // Released between the failed create and the read.
    return createIfAbsent(env, value);
  }
  const lock = parseLock(current);
  if (lock && new Date(lock.expiresAt).getTime() > now.getTime()) {
    return false;
  }
  // Expired, or not a lock at all. Confirm it is still the same value, so a
  // lock another start has just taken over is not deleted from under it.
  if ((await readRaw(env)) !== current) {
    return false;
  }
  await remove(env);
  return createIfAbsent(env, value);
}

/**
 * Whether a start currently holds the environment's lock: the parameter exists,
 * reads as a lock, and has not expired. Writes nothing. A failure to read it is
 * thrown, and the caller decides what that means for what it is reporting.
 */
export async function wakeLockHeld(env: string, now: Date = new Date()): Promise<boolean> {
  const lock = parseLock((await readRaw(env)) ?? undefined);
  return lock !== null && new Date(lock.expiresAt).getTime() > now.getTime();
}

/** Release the lock if `owner` still holds it. A lock someone else holds is left alone. */
export async function releaseWakeLock(env: string, owner: string): Promise<void> {
  const current = await readRaw(env);
  if (current === null) {
    return;
  }
  if (parseLock(current)?.owner !== owner) {
    return;
  }
  await remove(env);
}
