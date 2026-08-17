## 1. Spikes that gate the design

- [ ] 1.1 Confirm `@huggingface/hub`'s `fileDownloadInfo` (or equivalent) returns a
  resolvable download link, for a public repo and a gated one, and that a ranged
  `GET` against that link returns `206` with the requested bytes. Try a
  Xet-backed repo as well as a classic LFS one. If ranged requests are not
  honoured, switch the transfer to whole-file streams with per-file retry and note
  it in design.md — no spec changes either way.
- [ ] 1.2 Confirm which `nodejs*` packages Amazon Linux 2023 publishes
  (`dnf list --available 'nodejs*'` on a stock AL2023 arm64 instance). Pin
  `nodejs24` if present, else `nodejs22`. Record the decision in design.md.
- [ ] 1.3 Confirm the source's published checksum is retrievable per file (the
  `X-Linked-Etag`/LFS sha256) for both LFS and Xet repos, since the manifest's
  integrity guarantee depends on it. If it is unavailable for some file class,
  decide and document whether those files are verified by size alone.

## 2. Shared contract

- [ ] 2.1 Add `lambda/shared/seed/contract.ts`: the job-spec type the Lambda writes
  and the seeder reads, the EMF record shape, and the `_seed.json` manifest type,
  with the namespace, metric names and phase values as constants.
- [ ] 2.2 Add `lambda/shared/seed/identity.ts`: `seedIdFor(runner, modelId, quant)`
  producing the slug, the hash-suffix path for over-long ids, the deterministic
  `ClientToken` including `generation`, and the seed tag keys/values. Unit test the
  slug against ids containing `/`, `.`, `:` and non-ASCII, and against the EC2 tag
  and log-stream length limits.
- [ ] 2.3 Add `seedSelection` to `RunnerSpec` (include/exclude globs,
  `expectSingle`), implement it for vllm and llamacpp, and delete `seedDownload`
  and `weightsSentinel` from the interface and both specs.
- [ ] 2.4 Delete `seedTooling` from `RunnerSpec` and both runner specs, and remove
  the seed-runner AMI lookup it fed.

## 3. The seeder program

- [ ] 3.1 Scaffold `remote/seeder/` — entry point taking a job-spec path, a
  `build:seeder` script, and vitest coverage in the existing run. Add
  `@huggingface/hub` to `remote/package.json`.
- [ ] 3.2 `seeder/src/emf.ts`: append EMF records to the log file and mirror them to
  stdout; `Runner` as the only dimension, `SeedId` and the rest as properties;
  `Phase` carried as a property and not as a metric.
- [ ] 3.3 `seeder/src/hf.ts`: resolve the revision (honouring a pin), list the
  repository, apply the selection rule, fail on an ambiguous `expectSingle` match,
  and return the per-file size, checksum and link. Read the Hugging Face token from
  Secrets Manager in-process — never via a shell.
- [ ] 3.4 `seeder/src/transfer.ts`: ranged-part fetch into S3 multipart, bounded
  concurrency, per-part retry with backoff, `AbortMultipartUpload` on give-up, and
  per-file sha256 verification against the source checksum.
- [ ] 3.5 Add the disk-staging fallback for a file whose parts exhaust their
  retries: stage to `/tmp`, upload, unlink. Assert disk use stays bounded to one
  file regardless of model size.
- [ ] 3.6 `seeder/src/manifest.ts`: write `_seed.json` as the final step, only after
  every file has completed and verified, recording model, resolved revision, runner,
  quant, timestamp, seeder and Node versions, and the file list with sizes and
  checksums.
- [ ] 3.7 Emit a progress record during the metadata pass, before any bytes move, so
  a large repository's listing phase cannot look like a stall.
- [ ] 3.8 Assert the Node major version at startup; on a version below the floor,
  emit a terminal failure record naming the version found and exit nonzero.
- [ ] 3.9 Terminal reporting: a top-level catch and an exit handler that emit
  exactly one terminal record (succeeded/failed with a message) and cannot emit two.
- [ ] 3.10 Tests: selection rules including the ambiguous-GGUF failure; a part
  failure retried alone; a part exhausting retries falling back to staging; a
  checksum mismatch failing the seed with no manifest written; the manifest written
  only after every file completes.

## 4. Launch path

- [ ] 4.1 `lambda/shared/seed/launch.ts`: resolve the stock AL2023 arm64 AMI from
  the public SSM parameter, render the user-data, and `RunInstances` with the
  deterministic `ClientToken`, the seed tags and terminate-on-shutdown.
- [ ] 4.2 Render the user-data: `shutdown -h +${maxSeedMinutes}` first, `trap …
  EXIT` with the agent-flush sleep, the pinned `dnf install`, the CloudWatch agent
  config writing to `/cloud-vm-llm/seed` on stream `<seedId>/<instanceId>`, the
  bundle fetch, the inline job spec, and `node`. No `set -euxo pipefail`. Unit test
  the rendered script — shell-quoting bugs here surface as a silent failure twenty
  minutes later.
- [ ] 4.3 Treat a CloudWatch agent that fails to start as a boot failure rather
  than proceeding to transfer invisibly.
- [ ] 4.4 Replace `lambda/shared/seed.ts`: keep `weightsPresent` but judge presence
  by `_seed.json` parsing, and delete `buildSeedUserData` and `launchSeedInstance`
  in favour of the new module.

## 5. Seed Lambda and status

- [ ] 5.1 `lambda/shared/seed/status.ts`: `DescribeLogStreams` by seed-id prefix
  ordered by last event time for the newest attempt, then `GetLogEvents` for the
  newest parseable record; join with EC2 state so a vanished instance with a
  non-terminal last record reports failed and a live instance with no record reports
  starting. Unit test every cell of that join.
- [ ] 5.2 `lambda/seed/index.ts`: `POST` start (idempotent, honouring `force` and an
  optional `revision` pin, refusing over the concurrency cap with a 429), `GET ?id=`
  status, `GET` list, `DELETE ?id=` stop.
- [ ] 5.3 Stop: terminate the instance and `PutLogEvents` a `stopped` terminal
  record; stopping a seed that is not running succeeds and says so.
- [ ] 5.4 List: enumerate seed-tagged instances with identity, what they are
  seeding, age and phase; state plainly when there are none.

## 6. Sweep and termination

- [ ] 6.1 Add a seed pass to `StopFn`, keyed on the seed tag value so it is separate
  from the inference sweep: terminate past `maxSeedMinutes` from launch, or past
  `seedStallMinutes` since the last event timestamp, honouring `Retain-Until`.
- [ ] 6.2 Have the sweep `PutLogEvents` a synthetic terminal record for any seed it
  reaps, so status never reports a reaped seed as in progress.
- [ ] 6.3 Test that the seed pass never issues the daemon SSM scrape used for
  inference instances, and that the inference sweep never sees seed instances.

## 7. Stack wiring

- [ ] 7.1 esbuild the seeder in the stack at synth time and publish it as an S3
  asset; grant the seed role read on it.
- [ ] 7.2 Add the `/cloud-vm-llm/seed` log group with `seedLogRetentionDays`.
- [ ] 7.3 Add `SeedFn` with an IAM Function URL, plus the `SeedUrl` output and its
  entry in `OutfitRemoteConfig`.
- [ ] 7.4 IAM: seed role gets bucket read/write under `models/*`, the HF secret,
  the bundle asset, and `logs:CreateLogStream`/`PutLogEvents` on the seed group
  only. `SeedFn` gets `RunInstances`/`CreateTags`/`TerminateInstances` (seed
  tag-scoped), `DescribeInstances`, `PassRole` to the seed role only,
  `DescribeLogStreams`/`GetLogEvents`/`PutLogEvents` on the seed group, and read on
  the manifest. `StopFn` gets the seed-scoped terminate and the log calls.
- [ ] 7.5 Add `seedInstanceType`, `maxSeedMinutes`, `seedStallMinutes`,
  `maxConcurrentSeeds` and `seedLogRetentionDays` to `lib/config.ts`, and have the
  user-data render `maxSeedMinutes` from the same value the sweep reads.
- [ ] 7.6 Update `test/stack.test.ts` for the new function, log group, asset and
  policies; update or replace `test/seed.test.ts` for the new user-data and launch.

## 8. Deploy handoff

- [ ] 8.1 Change `DeployFn`'s reply to carry `seedId` in place of
  `seedInstanceId`, keeping auto-seed on a missing-weights deploy.
- [ ] 8.2 Update the Go `DeployResponse` accordingly and have `outfit remote deploy`
  print the follow-up command rather than a wait estimate.

## 9. Go client and CLI

- [ ] 9.1 Add `SeedURL` to `remote.Config` with an `OUTFIT_REMOTE_SEED_URL`
  override, optional in the same way as `EnvURL`, and a clear error naming the
  value to add when a seed command runs without it.
- [ ] 9.2 Add the seed calls to `internal/remote`: start (with force and revision),
  status, list, stop.
- [ ] 9.3 Add `outfit remote seed <start|status|ls|stop>` to the dispatch in
  `cmd/outfit/remote.go`, resolving what to seed from the Outfit the same way
  `deploy` does, and update the `remote` usage string and the unknown-subcommand
  error.
- [ ] 9.4 Output: `start` says whether it started or joined; `status` prints phase,
  progress and outcome; `ls` states plainly when nothing is in flight; `stop` is
  safe twice.
- [ ] 9.5 Tests for each subcommand including the not-configured, unknown-seed and
  cap-reached paths. Keep coverage at or above the 80% bar.

## 10. Removal and documentation

- [ ] 10.1 Delete `remote/scripts/seed-model.mjs` and its `seed-model` package
  script.
- [ ] 10.2 Update `remote/README.md` and `remote/docs/architecture.md`: the seed
  lifecycle and its control surface, `outfit remote seed` in place of
  `pnpm seed-model`, the manifest in place of the sentinel, the seed log group, and
  the new config knobs. Refresh the architecture diagrams that show the seed as a
  one-way arrow.
- [ ] 10.3 Document the migration: prefixes seeded before this change carry no
  `_seed.json` and will be re-seeded once. State why no backfill helper is offered.
- [ ] 10.4 Note the fixed token disclosure in the changelog entry, since anyone who
  ran the old seed has a token traced into a boot log and may want to rotate it.

## 11. Verification

- [ ] 11.1 `pnpm build`, `pnpm test`, `pnpm synth` in `remote/`; `go test ./...
  -cover` and `gofmt` at the repo root.
- [ ] 11.2 End-to-end in a real account: seed a vLLM checkpoint and a llama.cpp
  GGUF; confirm the manifest, the instance's self-termination, and status
  progressing through phases to succeeded.
- [ ] 11.3 Force a failure (a nonexistent model, then a revoked token) and confirm
  the instance terminates, status reports failed with a message, and the records
  outlive the instance.
- [ ] 11.4 Fire two simultaneous starts for the same weights and confirm exactly one
  instance exists; then two for different models and confirm two.
- [ ] 11.5 Confirm a deliberate re-seed inside the 24-hour dedupe window launches a
  new instance rather than returning the terminated one.
- [ ] 11.6 Kill the seeder process with `SIGKILL` and confirm the sweep reaps the
  instance and status reports failed rather than in progress.
- [ ] 11.7 Confirm no boot log or console output contains the Hugging Face token.
