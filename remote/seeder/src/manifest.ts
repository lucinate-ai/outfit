/**
 * Writing `_seed.json` — the object whose presence means "these weights are
 * complete".
 *
 * It is written last and only once every file has transferred and verified,
 * which is what makes its presence a sound completeness check. The sentinel it
 * replaces was a weights file assumed to be written last by `aws s3 sync`; that
 * was an assumption about sync ordering rather than a guarantee, so a truncated
 * sync could read as complete for ever.
 */

import { PutObjectCommand, type S3Client } from '@aws-sdk/client-s3';
import {
  manifestKey,
  type SeedJob,
  type SeedManifest,
} from '../../lambda/shared/seed/contract';
import type { TransferredFile } from './transfer';

/** Bumped when the manifest's shape or the transfer's guarantees change. */
export const SEEDER_VERSION = '1.0.0';

export function buildManifest(
  job: SeedJob,
  revision: string,
  files: TransferredFile[],
  now: Date = new Date(),
  nodeVersion: string = process.version,
): SeedManifest {
  return {
    modelId: job.modelId,
    revision,
    runner: job.runner,
    quant: job.quant,
    seededAt: now.toISOString(),
    seedId: job.seedId,
    seederVersion: SEEDER_VERSION,
    seederNodeVersion: nodeVersion,
    files: files
      .map((f) => ({ path: f.storeAs, size: f.size, sha256: f.sha256 }))
      .sort((a, b) => a.path.localeCompare(b.path)),
    totalBytes: files.reduce((sum, f) => sum + f.size, 0),
  };
}

export async function writeManifest(
  s3: S3Client,
  job: SeedJob,
  manifest: SeedManifest,
): Promise<void> {
  await s3.send(
    new PutObjectCommand({
      Bucket: job.bucket,
      Key: manifestKey(job.prefix),
      Body: JSON.stringify(manifest, null, 2),
      ContentType: 'application/json',
    }),
  );
}
