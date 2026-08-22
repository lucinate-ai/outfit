## 1. Spikes that gate the design

- [x] 1.1 Confirm a ranged `GET` returns `206` with the requested bytes.
  **Confirmed** against `openai-community/gpt2` (Xet-backed): `206`,
  `content-range: bytes 0-99/548105171`, repeated windows byte-identical,
  `accept-ranges: bytes` on both the `resolve` response and its redirect target.
  **Design changed**: the redirect target is signed with a ~1 h `Expires`, so parts
  are fetched against the `resolve` endpoint and allowed to redirect rather than
  caching the signed link. Recorded in design.md.
- [x] 1.2 Confirm which `nodejs*` packages Amazon Linux 2023 publishes.
  **Confirmed** by reading the AL2023 `core` aarch64 repository metadata:
  `nodejs24` 24.11.0, `nodejs22` 22.14.0, `nodejs20` 20.10.0, and
  `amazon-cloudwatch-agent` 1.247358.0 in the same repository. Pinning `nodejs24`.
  Note the unversioned `nodejs` is 18.12.1, which is why the pin matters.
- [x] 1.3 Confirm the source's published checksum is retrievable per file.
  **Confirmed**: `x-linked-etag` on the `resolve` response carries the sha256 and
  `x-linked-size` the true size. Important detail found: the CDN target's own
  `etag` is the Xet content hash, **not** the sha256, so the checksum must be read
  from the `resolve` response. `x-repo-commit` on the same response gives the
  resolved commit sha for the manifest, so no separate resolve call is needed.

## 2. Shared contract

- [x] 2.1 Add `lambda/shared/seed/contract.ts`: the job-spec type the Lambda writes
  and the seeder reads, the EMF record shape, and the `_seed.json` manifest type,
  with the namespace, metric names and phase values as constants.
- [x] 2.2 Add `lambda/shared/seed/identity.ts`: `seedIdFor(runner, modelId, quant)`
  producing the slug, the hash-suffix path for over-long ids, the deterministic
  `ClientToken` including `generation`, and the seed tag keys/values. Unit test the
  slug against ids containing `/`, `.`, `:` and non-ASCII, and against the EC2 tag
  and log-stream length limits.
- [x] 2.3 Add `seedSelection` to `RunnerSpec` (include/exclude globs,
  `expectSingle`), implement it for vllm and llamacpp, and delete `seedDownload`
  and `weightsSentinel` from the interface and both specs.
- [x] 2.4 Delete `seedTooling` from `RunnerSpec` and both runner specs, and remove
  the seed-runner AMI lookup it fed.

## 3. The seeder program

- [x] 3.1 Scaffold `remote/seeder/` — entry point taking a job-spec path, a
  `build:seeder` script, and vitest coverage in the existing run. Add
  `@huggingface/hub` to `remote/package.json`.
- [x] 3.2 `seeder/src/emf.ts`: append EMF records to the log file and mirror them to
  stdout; `Runner` as the only dimension, `SeedId` and the rest as properties;
  `Phase` carried as a property and not as a metric.
- [x] 3.3 `seeder/src/hf.ts`: resolve the revision (honouring a pin), list the
  repository, apply the selection rule, fail on an ambiguous `expectSingle` match,
  and return the per-file size, checksum and link. Read the Hugging Face token from
  Secrets Manager in-process — never via a shell.
- [x] 3.4 `seeder/src/transfer.ts`: ranged-part fetch into S3 multipart, bounded
  concurrency, per-part retry with backoff, `AbortMultipartUpload` on give-up, and
  per-file sha256 verification against the source checksum.
- [x] 3.5 Add the disk-staging fallback for a file whose parts exhaust their
  retries: stage to `/tmp`, upload, unlink. Assert disk use stays bounded to one
  file regardless of model size.
- [x] 3.6 `seeder/src/manifest.ts`: write `_seed.json` as the final step, only after
  every file has completed and verified, recording model, resolved revision, runner,
  quant, timestamp, seeder and Node versions, and the file list with sizes and
  checksums.
- [x] 3.7 Emit a progress record during the metadata pass, before any bytes move, so
  a large repository's listing phase cannot look like a stall.
- [x] 3.8 Assert the Node major version at startup; on a version below the floor,
  emit a terminal failure record naming the version found and exit nonzero.
- [x] 3.9 Terminal reporting: a top-level catch and an exit handler that emit
  exactly one terminal record (succeeded/failed with a message) and cannot emit two.
- [x] 3.10 Tests: selection rules including the ambiguous-GGUF failure; a part
  failure retried alone; a part exhausting retries falling back to staging; a
  checksum mismatch failing the seed with no manifest written; the manifest written
  only after every file completes.

## 4. Launch path

- [x] 4.1 `lambda/shared/seed/launch.ts`: resolve the stock AL2023 arm64 AMI from
  the public SSM parameter, render the user-data, and `RunInstances` with the
  deterministic `ClientToken`, the seed tags and terminate-on-shutdown.
- [x] 4.2 Render the user-data: `shutdown -h +${maxSeedMinutes}` first, `trap …
  EXIT` with the agent-flush sleep, the pinned `dnf install`, the CloudWatch agent
  config writing to `/cloud-vm-llm/seed` on stream `<seedId>/<instanceId>`, the
  bundle fetch, the inline job spec, and `node`. No `set -euxo pipefail`. Unit test
  the rendered script — shell-quoting bugs here surface as a silent failure twenty
  minutes later.
- [x] 4.3 Treat a CloudWatch agent that fails to start as a boot failure rather
  than proceeding to transfer invisibly.
- [x] 4.4 Replace `lambda/shared/seed.ts`: keep `weightsPresent` but judge presence
  by `_seed.json` parsing, and delete `buildSeedUserData` and `launchSeedInstance`
  in favour of the new module.

## 5. Seed Lambda and status

- [x] 5.1 `lambda/shared/seed/status.ts`: `DescribeLogStreams` by seed-id prefix
  ordered by last event time for the newest attempt, then `GetLogEvents` for the
  newest parseable record; join with EC2 state so a vanished instance with a
  non-terminal last record reports failed and a live instance with no record reports
  starting. Unit test every cell of that join.
- [x] 5.2 `lambda/seed/index.ts`: `POST` start (idempotent, honouring `force` and an
  optional `revision` pin, refusing over the concurrency cap with a 429), `GET ?id=`
  status, `GET` list, `DELETE ?id=` stop.
- [x] 5.3 Stop: terminate the instance and `PutLogEvents` a `stopped` terminal
  record; stopping a seed that is not running succeeds and says so.
- [x] 5.4 List: enumerate seed-tagged instances with identity, what they are
  seeding, age and phase; state plainly when there are none.

## 6. Sweep and termination

- [x] 6.1 Add a seed pass to `StopFn`, keyed on the seed tag value so it is separate
  from the inference sweep: terminate past `maxSeedMinutes` from launch, or past
  `seedStallMinutes` since the last event timestamp, honouring `Retain-Until`.
- [x] 6.2 Have the sweep `PutLogEvents` a synthetic terminal record for any seed it
  reaps, so status never reports a reaped seed as in progress.
- [x] 6.3 Test that the seed pass never issues the daemon SSM scrape used for
  inference instances, and that the inference sweep never sees seed instances.

## 7. Stack wiring

- [x] 7.1 esbuild the seeder in the stack at synth time and publish it as an S3
  asset; grant the seed role read on it.
- [x] 7.2 Add the `/cloud-vm-llm/seed` log group with `seedLogRetentionDays`.
- [x] 7.3 Add `SeedFn` with an IAM Function URL, plus the `SeedUrl` output and its
  entry in `OutfitRemoteConfig`.
- [x] 7.4 IAM: seed role gets bucket read/write under `models/*`, the HF secret,
  the bundle asset, and `logs:CreateLogStream`/`PutLogEvents` on the seed group
  only. `SeedFn` gets `RunInstances`/`CreateTags`/`TerminateInstances` (seed
  tag-scoped), `DescribeInstances`, `PassRole` to the seed role only,
  `DescribeLogStreams`/`GetLogEvents`/`PutLogEvents` on the seed group, and read on
  the manifest. `StopFn` gets the seed-scoped terminate and the log calls.
- [x] 7.5 Add `seedInstanceType`, `maxSeedMinutes`, `seedStallMinutes`,
  `maxConcurrentSeeds` and `seedLogRetentionDays` to `lib/config.ts`, and have the
  user-data render `maxSeedMinutes` from the same value the sweep reads.
- [x] 7.6 Update `test/stack.test.ts` for the new function, log group, asset and
  policies; update or replace `test/seed.test.ts` for the new user-data and launch.

## 8. Deploy handoff

- [x] 8.1 Change `DeployFn`'s reply to carry `seedId` in place of
  `seedInstanceId`, keeping auto-seed on a missing-weights deploy.
- [x] 8.2 Update the Go `DeployResponse` accordingly and have `outfit remote deploy`
  print the follow-up command rather than a wait estimate.

## 9. Go client and CLI

- [x] 9.1 Add `SeedURL` to `remote.Config` with an `OUTFIT_REMOTE_SEED_URL`
  override, optional in the same way as `EnvURL`, and a clear error naming the
  value to add when a seed command runs without it.
- [x] 9.2 Add the seed calls to `internal/remote`: start (with force and revision),
  status, list, stop.
- [x] 9.3 Add `outfit remote seed <start|status|ls|stop>` to the dispatch in
  `cmd/outfit/remote.go`, resolving what to seed from the Outfit the same way
  `deploy` does, and update the `remote` usage string and the unknown-subcommand
  error.
- [x] 9.4 Output: `start` says whether it started or joined; `status` prints phase,
  progress and outcome; `ls` states plainly when nothing is in flight; `stop` is
  safe twice.
- [x] 9.5 Tests for each subcommand including the not-configured, unknown-seed and
  cap-reached paths. Keep coverage at or above the 80% bar.

## 10. Removal and documentation

- [x] 10.1 Delete `remote/scripts/seed-model.mjs` and its `seed-model` package
  script.
- [x] 10.2 Update `remote/README.md` and `remote/docs/architecture.md`: the seed
  lifecycle and its control surface, `outfit remote seed` in place of
  `pnpm seed-model`, the manifest in place of the sentinel, the seed log group, and
  the new config knobs. Refresh the architecture diagrams that show the seed as a
  one-way arrow.
- [x] 10.3 Document the migration: prefixes seeded before this change carry no
  `_seed.json` and will be re-seeded once. State why no backfill helper is offered.
- [x] 10.4 Note the fixed token disclosure in the changelog entry, since anyone who
  ran the old seed has a token traced into a boot log and may want to rotate it.

## 11. Verification

- [x] 11.1 `pnpm build`, `pnpm test`, `pnpm synth` in `remote/`; `go test ./...
  -cover` and `gofmt` at the repo root.
> 11.2–11.7 were run against a real AWS account (account 800260117131,
> us-east-1) once the control plane was redeployed with the current code. Two
> real bugs surfaced only by a live seed and are now fixed: the seeder bundle
> was published to S3 as a zipped directory rather than the plain `.mjs` file
> the boot script fetches and runs, so every seed failed at the first `node`
> invocation with a syntax error on the zip's "PK" magic bytes; and the
> launch path treated any `DescribeInstances` "not found" as "brand new, not
> yet visible" without distinguishing it from an idempotency hit on an
> instance from a much earlier session that had aged out of
> `DescribeInstances` entirely, or from a token whose boot-script arguments
> had since changed — both now escape to a fresh generation instead of
> either silently reporting a phantom instance or failing the start outright.

- [x] 11.2 End-to-end in a real account: seed a vLLM checkpoint and a llama.cpp
  GGUF; confirm the manifest, the instance's self-termination, and status
  progressing through phases to succeeded.
- [x] 11.3 Force a failure (a nonexistent model, then a revoked token) and confirm
  the instance terminates, status reports failed with a message, and the records
  outlive the instance.
- [x] 11.4 Fire two simultaneous starts for the same weights and confirm exactly one
  instance exists; then two for different models and confirm two.
- [x] 11.5 Confirm a deliberate re-seed inside the 24-hour dedupe window launches a
  new instance rather than returning the terminated one.
- [x] 11.6 Kill the seeder process with `SIGKILL` and confirm the instance
  terminates and status reports failed rather than in progress. Confirmed via
  the boot script's own EXIT trap (layer 1) firing within 15s of the kill,
  not the 5-minute sweep (layer 3) — the faster of the two defenses fired
  first, as intended.
- [x] 11.7 Confirm no boot log or console output contains the Hugging Face token.
  Confirmed live via an SSM grep of `/var/log/seed-boot.log` and
  `/var/log/cloud-init-output.log` on a running seed instance, in addition to
  the existing unit tests.
