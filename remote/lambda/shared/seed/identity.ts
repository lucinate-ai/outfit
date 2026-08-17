/**
 * A seed's identity, and the idempotency that follows from it.
 *
 * A seed is fully determined by the weights it would produce, so its id is
 * derived from the same inputs as its S3 prefix — no caller supplies or
 * remembers an identifier, and the same model always resolves to the same seed.
 *
 * The id is a slug rather than a digest on purpose. It is typed into
 * `outfit remote seed stop`, read in `outfit remote seed ls`, and recognised in
 * the CloudWatch console; `vllm--Qwen-Qwen3-32B` serves all three and a sha256
 * serves none of them.
 */

import { createHash } from 'node:crypto';
import type { Runner } from '../deploy-config';

/** Marks a seed instance, distinct from TAG_VALUE 'endpoint' for inference boxes. */
export const SEED_TAG_VALUE = 'seed';

/** Carries the seed id on the instance, so a seed is discoverable by identity. */
export const SEED_ID_TAG_KEY = 'cloud-vm-llm:seed-id';

/** Carries the model id, so `seed ls` can report what is being seeded. */
export const SEED_MODEL_TAG_KEY = 'cloud-vm-llm:seed-model';

/**
 * Upper bound on a seed id. EC2 tag values allow 256 characters and CloudWatch
 * log stream names 512, but the stream is `<seedId>/<instanceId>` and the id
 * also lands in error messages, so it is kept well inside both.
 */
const MAX_SEED_ID = 120;

/** Length of the disambiguating hash appended to an over-long id. */
const HASH_SUFFIX = 8;

/**
 * Slugify one path segment: keep alphanumerics, dot, underscore and hyphen;
 * collapse everything else (including the `/` inside a Hugging Face repo id,
 * which is not legal in a log stream name) to a single hyphen.
 */
function slugSegment(value: string): string {
  return value
    .replace(/[^A-Za-z0-9._-]+/g, '-')
    .replace(/-{2,}/g, '-')
    .replace(/^-|-$/g, '');
}

/**
 * The seed id for a set of weights. Segments are joined with `--` so the
 * boundary between runner, model and quant survives slugification of a model id
 * that itself contains hyphens.
 *
 * An id that would exceed MAX_SEED_ID is truncated and given a hash of the full
 * value, so length is bounded without two long model ids colliding.
 */
export function seedIdFor(runner: Runner, modelId: string, quant: string): string {
  const parts = [runner, slugSegment(modelId)];
  if (quant) {
    parts.push(slugSegment(quant));
  }
  const full = parts.join('--');
  if (full.length <= MAX_SEED_ID) {
    return full;
  }
  const digest = createHash('sha256').update(full).digest('hex').slice(0, HASH_SUFFIX);
  return `${full.slice(0, MAX_SEED_ID - HASH_SUFFIX - 1)}-${digest}`;
}

/**
 * The RunInstances idempotency token. This is what makes concurrent starts
 * converge: EC2 treats a repeated token within 24 hours as the same call and
 * returns the same instance, so two Lambda invocations racing on the same
 * weights produce one instance without a lock or a conditional write.
 *
 * The token is capped at 64 characters, EC2's limit, by hashing when needed
 * rather than truncating — a truncated token could collide with a different
 * seed's, which would silently return the wrong instance.
 */
export function seedClientToken(seedId: string, generation: string): string {
  const token = `seed-${seedId}-${generation}`;
  if (token.length <= 64) {
    return token;
  }
  return `seed-${createHash('sha256').update(token).digest('hex').slice(0, 58)}`;
}

/**
 * The generation an ordinary start uses.
 *
 * It is a CONSTANT, and that is the whole point: two concurrent starts must
 * produce the *same* token or EC2 has nothing to deduplicate. A timestamp — even
 * bucketed to the minute — fails exactly when it matters, because two calls a
 * second apart either side of a bucket boundary would get different tokens and
 * launch two instances, which is the race this mechanism exists to close.
 *
 * The cost of a constant is that EC2's 24-hour window would also deduplicate a
 * *legitimately new* attempt — a seed that failed at 10:00 and is retried at
 * 14:00 would be handed back the terminated instance. That is not handled here
 * but in launchSeedInstance, which checks the state of whatever instance the
 * call returns and retries with a fresh generation if it is not alive. Detecting
 * the stale hit is reliable; predicting it is not.
 */
export const AUTO_GENERATION = 'auto';

/**
 * A generation guaranteed distinct from AUTO_GENERATION and from earlier ones:
 * used for a deliberate re-seed, and by the launch path to escape a stale
 * idempotency hit. Millisecond precision plus a random suffix, because two
 * forced re-seeds in the same millisecond should still be two seeds.
 */
export function freshGeneration(now: Date = new Date()): string {
  const stamp = now.toISOString().replace(/[-:.TZ]/g, '');
  return `${stamp}-${Math.random().toString(36).slice(2, 6)}`;
}
