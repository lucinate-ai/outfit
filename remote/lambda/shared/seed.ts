/**
 * Whether a model's weights are already in the bucket.
 *
 * Presence is judged by the manifest the seeder writes as its final step, once
 * every file has transferred and verified. It replaces the per-runner sentinel
 * this module used to check — a weights file assumed to be written last by
 * `aws s3 sync`. That was an assumption about sync ordering rather than a
 * guarantee, so a truncated sync that happened to land the sentinel read as
 * complete for ever; and it said nothing about *what* was in the prefix.
 *
 * Launching a seed lives in seed/launch.ts; this module is only the question
 * the deploy path asks before deciding whether to launch one.
 */

import { GetObjectCommand, S3Client } from '@aws-sdk/client-s3';
import { errorName } from './aws';
import { companionFileName, type CompanionRole, type DeployConfig } from './deploy-config';
import { manifestKey, type SeedManifest } from './seed/contract';

const s3 = new S3Client({});

function isMissing(err: unknown): boolean {
  const name = errorName(err);
  return name === 'NoSuchKey' || name === 'NotFound' || name === '404';
}

/**
 * Read a prefix's manifest, or null when there is none. A manifest that will
 * not parse is treated as absent rather than as an error: a half-written or
 * hand-edited object should cause a re-seed, not wedge every deploy.
 */
export async function readManifest(
  bucket: string,
  weightsPrefix: string,
): Promise<SeedManifest | null> {
  try {
    const result = await s3.send(
      new GetObjectCommand({ Bucket: bucket, Key: manifestKey(weightsPrefix) }),
    );
    const body = await result.Body?.transformToString();
    if (!body) {
      return null;
    }
    const parsed = JSON.parse(body) as SeedManifest;
    return parsed.modelId && Array.isArray(parsed.files) ? parsed : null;
  } catch (err) {
    if (isMissing(err)) {
      return null;
    }
    throw err;
  }
}

/**
 * Whether the weights for this config are seeded. Weights files present without
 * a manifest count as absent: nothing recorded that they are complete, or which
 * revision they came from.
 *
 * A manifest alone is not enough when companions are named: the weights prefix
 * is derived from (runner, modelId, quant), so adding a companion to an
 * already-seeded model does not change it — the earlier seed's manifest would
 * otherwise read as complete and skip the re-seed a new companion needs.
 */
export async function weightsPresent(bucket: string, cfg: DeployConfig): Promise<boolean> {
  const manifest = await readManifest(bucket, cfg.weightsPrefix);
  if (!manifest) {
    return false;
  }
  const stored = new Set(manifest.files.map((f) => f.path));
  return (Object.keys(cfg.companions ?? {}) as CompanionRole[]).every((role) =>
    stored.has(companionFileName(role)),
  );
}
