## Why

Seeding a model's weights into S3 is currently the least supervised thing the
control plane does. It is a bash script built as a TypeScript string, launched
fire-and-forget by the deploy Lambda, with a near-identical copy in
`scripts/seed-model.mjs`. Nothing reports back, and six failures follow from
that shape:

1. **A failed seed never stops billing.** The script runs under `set -e`, so a
   download that fails aborts it *before* the closing `shutdown -h now`. The
   instance then runs until somebody notices.
2. **Nothing can be asked about a seed in flight.** There is no status, no
   listing, no stop. The documented way to watch one is to open an SSM session
   and `tail` a file on a box that is about to delete itself.
3. **A second invocation launches a second instance.** Presence is judged by a
   sentinel object that only appears at the end, so two deploys inside the
   fetch window both miss it and both launch.
4. **Nothing records which weights are in the bucket.** The fetch takes
   whatever `main` points at and writes no provenance, so two seeds a month
   apart can leave different weights under the same prefix with no way to tell.
5. **Completeness is a guess.** The "these weights are done" marker is a
   per-runner file assumed to be written last by `aws s3 sync`. A truncated
   sync that happens to land that file reads as complete for ever.
6. **The Hugging Face token is written to the boot log.** The script fetches it
   into a shell variable under `set -x`, and bash's xtrace expands assignments
   from command substitution, so the token's value is traced into
   `/var/log/cloud-init-output.log` and the EC2 console output.

The job also moves ~30 GB twice — Hugging Face to EBS, then EBS to S3 — on an
instance whose EBS bandwidth is the narrowest pipe in the system, which is where
most of the quoted 15–20 minutes goes. And it can only run at all by borrowing
the vLLM GPU image, because that image happens to carry a Python virtualenv with
`huggingface_hub` in it; a `seedTooling` flag exists purely to encode that
coincidence.

## What Changes

Seeding becomes a first-class, observable, self-terminating job with its own
control surface.

- **A real seeder program**, `remote/seeder/`, written in TypeScript against the
  official `@huggingface/hub` SDK, bundled at synth time and fetched from S3 by
  the instance. The boot script shrinks to about fifteen lines and stops being
  the place where logic lives.
- **Streaming transfer.** Bytes go Hugging Face → memory → S3 multipart, with no
  disk staging, in ranged parts so a failed part is retried alone rather than
  restarting a file. A file that keeps failing falls back to disk staging, so
  streaming never becomes a way to get stuck.
- **No baked image.** The job runs on the stock Amazon Linux 2023 image, which
  already carries the SSM agent and the AWS CLI; Node and the CloudWatch agent
  come from the distribution's own in-region repositories. `seedTooling` and the
  borrowed GPU image both go away, and seeding stops depending on a bake.
- **Progress and outcome in CloudWatch, as Embedded Metric Format.** The seeder
  writes structured records that the CloudWatch agent ships; CloudWatch extracts
  the metrics from them automatically. One mechanism gives both alarm-able
  metrics and a readable per-seed history.
- **An idempotent seed Lambda** with start, status, list and stop, behind an IAM
  Function URL like the others. Identity is derived from what is being seeded,
  so repeated requests for the same weights converge on one instance and
  different requests get their own.
- **Self-termination in three independent layers** — in-process, an on-box
  wall-clock cap, and the existing periodic sweep — so no single failure mode
  leaves an instance running, and a seed whose instance died is reported as
  failed rather than as permanently in progress.
- **A `_seed.json` manifest** written last, replacing the per-runner sentinel.
  It records the resolved revision, the file list with checksums, and what
  produced it, so what is in the bucket is identifiable rather than inferred.
- **A CLI surface**, `outfit remote seed start|status|ls|stop`, selecting its
  environment by the same rules as the other remote subcommands.
  `scripts/seed-model.mjs` is removed in favour of it.

Deploying still seeds automatically when the weights are absent, but now returns
a seed handle the CLI can follow, rather than an instance id and an instruction
to wait twenty minutes.

## Capabilities

### New Capabilities

- `weight-seeding`: The seed job as a supervised unit of work — how a seed
  request is identified, how repeated requests converge, what the job guarantees
  about finishing and about stopping, how it reports progress and outcome, how
  its state is determined, and what it leaves behind.
- `remote-seed`: The operator's CLI surface for seeds — starting one, asking
  after one, listing those in flight, and stopping one.

### Modified Capabilities

- `model-weights`: Completeness is judged by a manifest written as the final
  step rather than by a per-runner sentinel guessed to be written last; the
  revision fetched is recorded and may be pinned on the request; and the reply
  to a deployment that starts a fetch identifies that fetch so its progress can
  be followed.

## Impact

- **New**: `remote/seeder/` (the on-instance program and its tests);
  `remote/lambda/seed/`; `remote/lambda/shared/seed/` (identity, launch, status,
  and the job-spec/manifest contract shared with the seeder); a
  `/cloud-vm-llm/seed` log group with its own retention; a seed Function URL and
  the `SeedUrl` stack output.
- **Changed**: `remote/lambda/shared/seed.ts` is replaced; `RunnerSpec` loses
  `seedDownload` (a bash fragment) and `weightsSentinel`, and gains a
  declarative file-selection rule; `StopFn`'s sweep gains a seed pass that
  judges liveness from the seed's own records rather than from an inference
  daemon that seed instances do not run; `DeployFn`'s reply carries a seed
  handle; `lib/llm-stack.ts` gains the Lambda, the log group, the bundle asset
  and the IAM for them; `lib/config.ts` gains the seed instance type, the
  maximum seed lifetime, the stall threshold, the concurrency cap and the seed
  log retention.
- **Removed**: `remote/scripts/seed-model.mjs`, and the documentation that
  points at it.
- **Go client**: `internal/remote` gains the seed calls and an optional
  `seed_url` in the remote config, degrading like `env_url` does for configs
  written before it existed; `cmd/outfit` gains the `remote seed` subcommand.
- **Dependencies**: `@huggingface/hub` and `@aws-sdk/client-cloudwatch-logs` in
  `remote/`. No new language toolchain, no new runtime service, no new data
  store: seed state is EC2 tags, CloudWatch records and the S3 manifest.
- **Security**: the Hugging Face token is read in-process by the seeder rather
  than into a traced shell variable, so it stops appearing in boot logs.
