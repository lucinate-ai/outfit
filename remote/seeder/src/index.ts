/**
 * The seeder: fetch a model's weights from Hugging Face straight into S3, say
 * what it is doing while it does it, and always report an outcome.
 *
 * Run on a disposable instance as `node seed.mjs <job.json>`. Everything it
 * needs is in the job spec except the Hugging Face token, which it reads from
 * Secrets Manager itself — never through a shell, which is how the old boot
 * script traced the token into the boot log.
 */

import { readFileSync } from 'node:fs';
import { mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import { S3Client } from '@aws-sdk/client-s3';
import { GetSecretValueCommand, SecretsManagerClient } from '@aws-sdk/client-secrets-manager';
import type { SeedJob } from '../../lambda/shared/seed/contract';
import { SEED_METRICS } from '../../lambda/shared/seed/contract';
import { Reporter } from './emf';
import { planTransfer } from './hf';
import { buildManifest, writeManifest } from './manifest';
import { transferFile, type TransferredFile } from './transfer';

/**
 * The runtime the seeder is developed and tested against. AL2023 publishes
 * nodejs24; the unversioned `nodejs` package there is 18, so a boot script that
 * installed the wrong one would otherwise fail somewhere deep in the transfer
 * rather than here.
 */
export const MIN_NODE_MAJOR = 20;

export function nodeMajor(version: string = process.version): number {
  return Number.parseInt(version.replace(/^v/, '').split('.')[0] ?? '0', 10);
}

/** How often progress is reported while transferring. */
const PROGRESS_INTERVAL_MS = 10_000;

export function readJob(path: string): SeedJob {
  const job = JSON.parse(readFileSync(path, 'utf8')) as SeedJob;
  for (const key of ['seedId', 'runner', 'modelId', 'bucket', 'prefix'] as const) {
    if (!job[key]) {
      throw new Error(`job spec is missing ${key}`);
    }
  }
  return job;
}

async function readToken(job: SeedJob): Promise<string> {
  if (!job.hfSecretArn) {
    return '';
  }
  const client = new SecretsManagerClient({ region: job.region });
  const secret = await client.send(new GetSecretValueCommand({ SecretId: job.hfSecretArn }));
  return secret.SecretString ?? '';
}

/** The instance's own id, for the log stream and the records. */
async function instanceId(): Promise<string> {
  try {
    const tokenResponse = await fetch('http://169.254.169.254/latest/api/token', {
      method: 'PUT',
      headers: { 'x-aws-ec2-metadata-token-ttl-seconds': '60' },
      signal: AbortSignal.timeout(2000),
    });
    const token = await tokenResponse.text();
    const idResponse = await fetch('http://169.254.169.254/latest/meta-data/instance-id', {
      headers: { 'x-aws-ec2-metadata-token': token },
      signal: AbortSignal.timeout(2000),
    });
    return (await idResponse.text()).trim() || 'unknown';
  } catch {
    return 'unknown';
  }
}

/**
 * Seams for the tests: the network-facing halves of the run, so the
 * orchestration — what is reported, in what order, and when the manifest is
 * written — can be exercised without Hugging Face or S3.
 */
export interface RunDeps {
  planTransfer?: typeof planTransfer;
  transferFile?: typeof transferFile;
  writeManifest?: typeof writeManifest;
  readToken?: (job: SeedJob) => Promise<string>;
}

export async function runSeed(
  job: SeedJob,
  reporter: Reporter,
  s3: S3Client,
  deps: RunDeps = {},
): Promise<void> {
  const plan_ = deps.planTransfer ?? planTransfer;
  const transfer_ = deps.transferFile ?? transferFile;
  const writeManifest_ = deps.writeManifest ?? writeManifest;

  reporter.emit('resolving', { Message: `resolving ${job.modelId}` }, { [SEED_METRICS.started]: 1 });

  const token = await (deps.readToken ?? readToken)(job);

  // The metadata pass can take a while on a large repository, and silence here
  // would look like a stall to the sweep — so it reports before any bytes move.
  const plan = await plan_(job.modelId, job.revision, job.selection, token);
  reporter.emit('resolving', {
    Message: `${plan.files.length} file(s), ${plan.totalBytes} bytes at ${plan.revision}`,
    Revision: plan.revision,
    FilesTotal: plan.files.length,
    BytesTotal: plan.totalBytes,
  });

  let bytesDone = 0;
  let filesDone = 0;
  let stagedFiles = 0;
  let lastReport = 0;
  const transferred: TransferredFile[] = [];

  for (const file of plan.files) {
    const report = (force = false) => {
      const now = Date.now();
      if (!force && now - lastReport < PROGRESS_INTERVAL_MS) {
        return;
      }
      lastReport = now;
      reporter.progress(
        {
          Revision: plan.revision,
          CurrentFile: file.storeAs,
          BytesTotal: plan.totalBytes,
          BytesDone: bytesDone,
          FilesTotal: plan.files.length,
          FilesDone: filesDone,
          ...(stagedFiles ? { StagedFiles: stagedFiles } : {}),
        },
        {
          [SEED_METRICS.progressPercent]:
            plan.totalBytes > 0 ? Math.round((bytesDone / plan.totalBytes) * 1000) / 10 : 0,
        },
      );
    };
    report(true);

    const result = await transfer_(
      file,
      {
        bucket: job.bucket,
        prefix: job.prefix,
        modelId: job.modelId,
        revision: plan.revision,
        token,
        partSizeBytes: job.partSizeBytes,
        partConcurrency: job.partConcurrency,
        partAttempts: job.partAttempts,
        onBytes: (delta) => {
          bytesDone += delta;
          report();
        },
        onStaged: (path, reason) => {
          stagedFiles += 1;
          reporter.emit('transferring', {
            Message: `streaming ${path} failed (${reason}); staging it on disk instead`,
            CurrentFile: path,
          });
        },
      },
      { s3 },
    );
    transferred.push(result);
    filesDone += 1;
    reporter.progress(
      { CurrentFile: file.storeAs, FilesDone: filesDone, FilesTotal: plan.files.length },
      { [SEED_METRICS.filesCompleted]: 1, [SEED_METRICS.bytesTransferred]: result.size },
    );
  }

  // Only now, with every file transferred and verified, is the manifest written.
  reporter.emit('finalising', { Message: 'writing the manifest', Revision: plan.revision });
  await writeManifest_(s3, job, buildManifest(job, plan.revision, transferred));

  reporter.terminal('succeeded', {
    Revision: plan.revision,
    FilesTotal: plan.files.length,
    FilesDone: filesDone,
    BytesTotal: plan.totalBytes,
    BytesDone: bytesDone,
    ...(stagedFiles ? { StagedFiles: stagedFiles } : {}),
    Message: `seeded ${plan.files.length} file(s) to s3://${job.bucket}/${job.prefix}`,
  });
}

export async function main(argv: string[]): Promise<number> {
  const jobPath = argv[2];
  if (!jobPath) {
    process.stderr.write('usage: seed.mjs <job.json>\n');
    return 2;
  }

  let job: SeedJob;
  try {
    job = readJob(jobPath);
  } catch (err) {
    // Before a job is parsed there is no seed id to report under, so this can
    // only go to the boot log. The sweep still reaps the instance.
    process.stderr.write(`seed: cannot read job spec: ${(err as Error).message}\n`);
    return 1;
  }

  mkdirSync(dirname(job.recordPath), { recursive: true });
  const reporter = new Reporter({
    seedId: job.seedId,
    runner: job.runner,
    modelId: job.modelId,
    instanceId: await instanceId(),
    recordPath: job.recordPath,
  });

  // A runtime below the floor is reported as a seed failure like any other,
  // rather than surfacing as an obscure syntax or API error mid-transfer.
  const major = nodeMajor();
  if (major < MIN_NODE_MAJOR) {
    reporter.terminal('failed', {
      Error: `node ${process.version} is below the minimum v${MIN_NODE_MAJOR} this seeder requires`,
    });
    return 1;
  }

  // Exactly one terminal record: Reporter.terminal ignores the second call, so
  // the specific failure below always beats this generic backstop.
  process.on('exit', () => {
    reporter.terminal('failed', { Error: 'the seeder exited without reporting an outcome' });
  });

  try {
    await runSeed(job, reporter, new S3Client({ region: job.region }));
    return 0;
  } catch (err) {
    reporter.terminal('failed', { Error: (err as Error).message });
    return 1;
  }
}

// Only run when executed, not when imported by a test.
if (process.argv[1] && /seed(\.mjs|er)?$|index\.(ts|js|mjs)$/.test(process.argv[1])) {
  void main(process.argv).then((code) => {
    process.exitCode = code;
  });
}
