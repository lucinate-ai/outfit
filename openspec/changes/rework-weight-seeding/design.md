## Context

The seed is the only part of the control plane with no feedback path. Everything
else — start, stop, stats, env — is a Lambda behind an IAM Function URL that the
Go client calls with SigV4, and the inference instance reports through an outfit
daemon on loopback that those Lambdas reach over SSM Run Command. The seed has
none of that: it is a bash string (`lambda/shared/seed.ts`), duplicated in
`scripts/seed-model.mjs`, launched and forgotten. See proposal.md for what that
costs.

Constraints that shape the approach:

- **`remote/` is a single TypeScript package** — pnpm, `tsc --noEmit`, vitest,
  esbuild via CDK's `NodejsFunction`, Lambdas on `ARM_64`. Whatever is added
  should build and test in that one lane rather than introduce a second.
- **Lambdas live outside the VPC** (no NAT), so they reach instances only over
  SSM. A seed instance runs no daemon, so the control plane cannot interrogate it
  the way it interrogates an inference box — the seed must push its state out.
- **The weights bucket is account-wide, not per-environment.** One model seeded
  once serves every environment naming it, so a seed has no environment.
- **The Go client discovers the control plane from stack outputs**, and its
  `remote.Config` already carries optional URLs (`env_url`, `base_url`) that
  degrade gracefully for configs written before they existed.
- Instances self-terminate today via `InstanceInitiatedShutdownBehavior:
  terminate`, and an EventBridge rule already runs `StopFn` every five minutes.
  Both are reused rather than replaced.

## Goals / Non-Goals

**Goals:**

- Make the seed a job with an identity, a lifecycle and a control surface —
  start, status, list, stop — matching the existing Lambda-per-verb pattern.
- Make the seed's state knowable from outside it, durably, after it is gone.
- Make it structurally impossible for a seed to run indefinitely.
- Move the seed's logic out of a boot script into a program that is built and
  tested with the rest of `remote/`.
- Remove seeding's dependency on a baked AMI and on Python.

**Non-Goals:**

- Changing where weights live or how their location is derived — `models/<runner>/
  <modelId>[/<quant>]/` is unchanged, and `weightsPrefixFor` stays as it is.
- Making the inference path aware of seeds beyond what it already knows: the boot
  still syncs a prefix from S3 and does not consult a manifest.
- Replacing the sentinel concept with a general-purpose job framework. Seed state
  is EC2 tags, CloudWatch records and one S3 object; no queue, no state machine
  service, no table.
- Parallelising a single seed across instances, or caching Hugging Face
  repositories between seeds.
- Changing the AMI bake pipeline, other than deleting the `seedTooling` flag that
  exists only to mark which runner image the seed borrows.

## Decisions

### The seeder is a TypeScript program in `remote/seeder/`, not a bash string

The alternative was Go — `outfit` is Go, the binary is already baked into the AMI
and already plays a server-side role as `outfit daemon`, and a static binary needs
no runtime on the box. It was rejected because Hugging Face publishes no Go SDK,
so the repository-listing, revision-resolution and gated-auth paths would all be
hand-rolled against an API that changes, and "don't make the pull brittle" is the
requirement that argues loudest here. `@huggingface/hub` is the official client
and is TypeScript.

Placing it inside `remote/` rather than as a top-level project is deliberate: the
Lambda that writes the job spec and the program that reads it share
`lambda/shared/seed/contract.ts` by import, so the wire contract between them
cannot drift. A sibling top-level package would have to duplicate or publish those
types. `remote/seeder/` still has its own entry point, its own bundle and its own
tests — it is a project, not a folder of helpers — but it shares one `tsconfig`,
one vitest run and one dependency tree.

### The transfer is ranged parts into S3 multipart, with a disk-staging fallback

The requirement is to stream if possible without making the pull brittle. Naive
streaming — one HTTP body per file into one upload — is brittle: a blip 25 GB into
a file restarts the file. The shape chosen instead:

```
for each selected file:
    { size, etag(sha256), downloadLink } ← fileDownloadInfo(repo, path, revision)
    CreateMultipartUpload(bucket, prefix + path)
    for each 64 MiB window, 8 in flight:
        GET downloadLink  with  Range: bytes=<lo>-<hi>
            └─▶ UploadPart(n)          ← retried alone on failure
    CompleteMultipartUpload
    verify sha256 against the source etag
```

Properties this buys: no disk proportional to the model; a failure blast radius of
one part rather than one file (finer-grained than `snapshot_download`'s own
resume); exact progress, because the sizes are known from the metadata pass before
any bytes move; and integrity verified against the checksum Hugging Face
publishes, which is what makes the manifest's guarantee real rather than nominal.

The fallback matters as much as the streaming: a part that exhausts its retries
causes that *one file* to be re-done by staging it to `/tmp` and uploading it,
then unlinking. Bounded disk, streaming by default, never stuck. This is the
concrete answer to "don't make the pull brittle" — robustness is a fallback path,
not an argument against streaming.

**Spiked and confirmed** (task 1.1/1.3, against `openai-community/gpt2`, which is
Xet-backed, so the indirection that was the main risk is covered):

- A ranged `GET` on the `resolve` endpoint returns `206` with a correct
  `content-range: bytes 0-99/548105171`, and repeated requests for the same
  window return byte-identical data. `accept-ranges: bytes` is present on both
  the `resolve` response and the CDN target it redirects to.
- `x-linked-etag` on the `resolve` response carries the file's **sha256**, and
  `x-linked-size` its true size — so the integrity check has a source of truth.
  Note that the *CDN* response's own `etag` is the Xet content hash, **not** the
  sha256; the checksum must be taken from the `resolve` response, not from the
  redirect target.
- `x-repo-commit` on the same response carries the resolved commit sha, which is
  what the manifest records as the revision. No separate resolve call is needed.

**The spike also changed a decision.** The redirect target is a signed URL with an
`Expires` roughly one hour out — the same order as `maxSeedMinutes`, so a cached
link could expire mid-transfer on a slow seed. Rather than track expiry and
re-sign, each part's `GET` is issued **against the `resolve` endpoint and allowed
to redirect**, so every part gets a freshly signed URL. The cost is one extra
302 (982 bytes) per 64 MiB part, which is nothing; the benefit is that URL expiry
stops being a failure mode the code has to model at all. A `403` on a part is
still retried, which covers the case where a signature expires between the
redirect and the read.

### Status is Embedded Metric Format, read as logs and as metrics

Three CloudWatch primitives were on the table:

| | Read path | Carries a message | Cost shape |
|---|---|---|---|
| `PutMetricData` | `GetMetricData` | no — numbers only | per unique metric **and dimension set** |
| EventBridge `PutEvents` | none — a bus, not a store; needs a rule and a target before anything can be read back | yes | per event |
| **EMF** (structured JSON via the agent) | `GetLogEvents` on a known stream | yes | log ingestion, metrics extracted free |

EMF wins because it satisfies both halves of the requirement with one write: the
seeder appends JSON to a file, the CloudWatch agent ships it, CloudWatch extracts
the declared metrics automatically. Metrics exist for alarms; the records exist for
diagnosis. Plain `PutMetricData` would need a parallel channel for the error
message; EventBridge would need a rule and a storage target before status could be
read at all.

The cardinality decision is the important one. `SeedId` is a **property** of the
record, not a metric dimension:

```jsonc
{ "_aws": { "CloudWatchMetrics": [{
      "Namespace": "cloud-vm-llm/seed",
      "Dimensions": [["Runner"]],          // low cardinality, bounded by RUNNERS
      "Metrics": [ {"Name":"BytesTransferred","Unit":"Bytes"},
                   {"Name":"Succeeded","Unit":"Count"}, … ] }] },
  "Runner": "vllm",
  "SeedId": "vllm--Qwen-Qwen3-32B",        // property: filterable, not billed
  "Phase": "uploading", "Percent": 62.4,
  "BytesTotal": 32212254720, "CurrentFile": "model-00009-of-00017.safetensors" }
```

Dimensioning on `SeedId` would mint a new billed custom metric for every model
ever seeded, for ever. Dimensioning on `Runner` keeps the metric count bounded by
`RUNNERS.length` while per-seed detail stays in the record, which is where the
status read looks anyway.

`Phase` is deliberately *not* a metric. An enum encoded as a number is unreadable
on a graph and meaningless when averaged.

Stream naming is `<seedId>/<instanceId>`, which gives the spec's requirement that
a re-seed not interleave with the attempt before it, and makes the status read two
cheap calls:

```
DescribeLogStreams(prefix=<seedId>, orderBy=LastEventTime, desc, limit=1)
GetLogEvents(that stream, startFromHead=false, limit≈20)  → newest parseable record
```

The first call also returns `lastEventTimestamp`, which is the stall signal — so
the sweep gets every seed's liveness without reading a single log event.

### Identity is a slug of the weights prefix; convergence is EC2's `ClientToken`

`weightsPrefixFor(runner, modelId, quant)` already uniquely determines what a seed
produces, so the seed id is a slug of it — `vllm--Qwen-Qwen3-32B` — rather than a
digest. A digest would be equally deterministic and strictly worse to live with: it
has to be typed into `seed stop`, read in `seed ls`, and recognised in the console.
Slugs are valid as EC2 tag values (≤256 chars) and as log stream names (≤512, no
`:` or `*`); a short hash suffix is appended only if a model id is long enough to
threaten those limits.

Convergence uses the mechanism EC2 already provides:

```ts
ClientToken: `seed-${seedId}-${generation}`     // was: randomUUID()
```

`RunInstances` treats a repeated `ClientToken` within 24 hours as the same call and
returns the *same* instance. A 20-minute job inside a 24-hour window means two
concurrent Lambda invocations collapse to one instance with no lock, no
conditional write, and no describe-then-launch race — the race the current code
loses. A tag filter (`cloud-vm-llm:seed-id`) is checked first for the common case
and to answer `seed ls`, but correctness under concurrency rests on the token, not
the check.

`generation` is what makes a deliberate re-seed possible inside the dedupe window.
**Implementation refined this**: `generation` for an ordinary start must be a
*constant*, not a timestamp. A bucketed timestamp fails precisely where the
mechanism is needed — two calls a second apart either side of a bucket boundary
get different tokens and launch two instances, which is the race the token exists
to close.

A constant then raises the converse problem the risk list already named: EC2's
24-hour window would also deduplicate a legitimately new attempt, handing back a
terminated instance to a seed retried four hours after it failed. That is resolved
by *detecting* rather than predicting it — `launchSeedInstance` inspects the state
of whatever instance the call returns and, if it is not alive, retries once with a
fresh generation. Detection is reliable; prediction is not. A deliberate re-seed
skips straight to a fresh generation.

Rejected: a DynamoDB table for seed jobs. It would be the conventional answer and
it is genuinely unnecessary — EC2 tags say what is running, CloudWatch says what it
is doing, the manifest says what finished. Adding a table would add a resource, a
consistency question and a cleanup story for no capability.

### The instance is stock AL2023 on `c7g.large`, with nothing baked

The base image is the public Amazon Linux 2023 arm64 AMI, resolved at synth from
`/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-arm64`. It already
carries the SSM agent and AWS CLI v2. Node and the CloudWatch agent come from
AL2023's own repositories, which are S3-backed in-region:

```bash
dnf install -y nodejs24 amazon-cloudwatch-agent
```

Pinned, with no version-probing fallback chain: if the package is absent the seed
fails at boot in seconds with a legible dnf error, which is a far better failure
than a silent drop to an older runtime and a confusing error ten minutes in. The
seeder additionally asserts its own minimum version at startup and records the
version it ran on in the manifest.

**Spiked and confirmed** (task 1.2, reading the AL2023 `core` aarch64 repository
metadata directly): `nodejs24` is published at 24.11.0, alongside `nodejs22`
(22.14.0) and `nodejs20` (20.10.0). `amazon-cloudwatch-agent` (1.247358.0) is in
the same repository. So `nodejs24` is what gets pinned.

The spike also justifies the pin rather than weakening it: the *unversioned*
`nodejs` package in that repository is **18.12.1**. Installing `nodejs` — the
obvious thing to write — would silently land two LTS lines behind the version the
seeder is developed against.

The seeder bundle rides as a CDK S3 asset, esbuild'd at synth time in the stack
code — the same thing `NodejsFunction` does internally, so there is no ordering
hazard between `pnpm build` and `cdk deploy`, and the bundle is versioned with the
stack. The job spec goes inline in user-data as a heredoc; it is under 2 KB against
a 16 KB limit, so a second S3 object would be gratuitous.

User-data therefore reduces to roughly:

```bash
#!/bin/bash
shutdown -h +${maxSeedMinutes}          # layer 2, before anything can fail
trap 'sleep 10; shutdown -h now' EXIT   # the sleep lets the agent flush
dnf install -y nodejs24 amazon-cloudwatch-agent
<cw agent config: /var/log/seed/emf.jsonl → /cloud-vm-llm/seed, <seedId>/<instance>>
aws s3 cp s3://<cdk-assets>/<hash>/seed.mjs /opt/
cat >/opt/job.json <<'JOB' … JOB
node /opt/seed.mjs /opt/job.json
```

Note the absence of `set -euxo pipefail`. It is what breaks termination today — an
aborted script never reaches its `shutdown` — and `set -x` is what traces the
Hugging Face token into the boot log. The trap replaces `set -e`'s role, and the
seeder reads the token itself through the SDK, so neither is needed.

**On instance size**: `c7g.large` (2 vCPU, 4 GiB, arm64) replaces `m5.xlarge`, a
62% hourly reduction, and the memory is sized for 8 concurrent 64 MiB parts with
headroom. The honest note is that this barely matters — a 20-minute seed costs
$0.024 on `c7g.large` against $0.064 on `m5.xlarge`, and a faster box that
finishes sooner costs *less*, so duration dominates price. The t-family is
avoided deliberately: `t4g.small` looks like the smaller answer but meters both
CPU and network burst credits, and a sustained multi-gigabit pull with TLS and
sha256 over 30 GB exhausts both, turning a six-minute job into a throttled
40-minute one. `c7g` is the smallest family without a credit cliff. The type is a
config knob either way.

Graviton is safe: `@huggingface/hub` and the AWS SDK v3 are pure JavaScript with no
native modules, and the Lambdas are already `ARM_64`.

### Termination is three independent layers, and the sweep looks in the right place

```
Layer 1  in-process        seeder's top-level catch + exit handler → terminal
                           record; user-data's trap EXIT → shutdown
         catches success, thrown errors, nonzero exit
         misses  SIGKILL, the OOM killer, a kernel panic

Layer 2  on-box clock      shutdown -h +60, the first line of user-data
         catches any process death, any hang
         misses a boot where user-data never ran

Layer 3  StopFn sweep      every 5 min, on the existing EventBridge rule
         terminate a seed-tagged instance when
             now − launchTime > maxSeedMinutes  (60)
          or now − lastEventTimestamp > stallMinutes  (10)
         then PutLogEvents a synthetic terminal record on its behalf
```

Layers 2 and 3 read `maxSeedMinutes` from the same `lib/config.ts` value — layer 2
by rendering it into user-data — so they cannot drift apart.

Layer 3 is the one that needs care about *where it looks*. `StopFn` currently
judges an inference instance by scraping its outfit daemon over SSM
(`DAEMON_STATUS_CMD`). A seed instance runs no daemon, so that check would fail
against it and yield nothing usable. The seed pass instead judges liveness from
`DescribeLogStreams`'s `lastEventTimestamp` — the seed's own progress reports, per
the spec — and is a separate pass keyed on a distinct tag value
(`cloud-vm-llm: seed` rather than `endpoint`), so seed instances stay invisible to
the inference sweep and vice versa. The existing `Retain-Until` tag is honoured, so
a stuck seed can be pinned for inspection.

The synthetic terminal record is what closes the "instance gone, last word was
41%" hole: without it, status would read that seed as in progress for ever.
`PutLogEvents` no longer requires sequence tokens, so this is a single call.

### `_seed.json` replaces the per-runner sentinel

Written last, after every file has completed and verified:

```jsonc
{ "modelId": "Qwen/Qwen3-32B", "revision": "a3f9c21…",   // resolved commit sha
  "runner": "vllm", "quant": "", "seededAt": "…",
  "seederVersion": "…", "seederNodeVersion": "…",
  "files": [ { "path": "…", "size": 0, "sha256": "…" } ] }
```

`weightsPresent()` becomes "does `_seed.json` exist and parse", which removes both
the per-runner branch and the assumption that `aws s3 sync` writes a particular
file last — an assumption that made a truncated sync look complete for ever.
`RunnerSpec.weightsSentinel` is deleted.

Recording the revision fixes the current non-reproducibility: today's
`snapshot_download(MODEL_ID)` takes whatever `main` points at and records nothing.
A request may now pin a revision; absent a pin the resolved sha is recorded either
way, so two prefixes seeded months apart are distinguishable.

### `RunnerSpec` stops emitting shell

`seedDownload(cfg): string` — a bash fragment — becomes a declarative selection:

```ts
seedSelection: (cfg) => ({ include: ['*'] })                        // vllm
seedSelection: (cfg) => ({ include: [`*${cfg.quant}*`],
                           exclude: ['*mmproj*'],
                           expectSingle: 'model.gguf' })            // llamacpp
```

`expectSingle` fails the seed when the selection matches more than one file. That
is a deliberate behaviour change: today llama.cpp prints a `WARNING`, takes the
first GGUF and ships a broken split-quant model. Failing loudly is the point.

`seedTooling` is deleted along with the borrowed vLLM image.

### Deploy keeps auto-seeding, but hands back a followable handle

`DeployFn` still launches a seed when the weights are absent — refusing and making
the operator run a second command would be worse ergonomics for no gain — but its
reply carries `seedId` instead of `seedInstanceId`. The instance id is an
implementation detail that changes on a re-launch; the seed id is stable and is
what `seed status` takes.

The CLI then queries `SeedFn` directly rather than reading progress through
`DeployFn`. Routing status through deploy would mean a progress check re-entering
a function whose job is to mutate environment state — the wrong shape for a poll.

`SeedURL` joins `remote.Config` as an optional field with an
`OUTFIT_REMOTE_SEED_URL` override and a `SeedUrl` stack output added to
`OutfitRemoteConfig`, following exactly how `EnvURL` degrades: a configuration
written before the seed endpoint existed keeps working for every other subcommand
and simply cannot print the tracking hint.

### Layout

```
remote/
├── seeder/src/{index,hf,transfer,emf,manifest}.ts   the on-instance program
├── seeder/test/
├── lambda/seed/index.ts                             POST/GET/GET-list/DELETE
├── lambda/shared/seed/
│   ├── contract.ts       job spec + EMF record + manifest — imported by BOTH
│   ├── identity.ts       seedId slug, client token, tags
│   ├── launch.ts         AMI resolve, user-data, RunInstances
│   └── status.ts         the EC2 ⋈ CloudWatch join
└── lib/llm-stack.ts      SeedFn, seed log group, bundle asset, IAM
```

New config in `lib/config.ts`: `seedInstanceType` (`c7g.large`),
`maxSeedMinutes` (60), `seedStallMinutes` (10), `maxConcurrentSeeds` (3),
`seedLogRetentionDays` (3).

Seed log retention is 3 days rather than the inference groups' 1: these records are
the account's account of what is in its bucket and why, they are kilobytes per
seed, and a seed failure is often noticed the day after.

## Risks / Trade-offs

- ~~`Range` on the Hugging Face CDN is load-bearing and unverified.~~
  **Resolved by spike**: `206` with correct `content-range`, on a Xet-backed repo,
  with the sha256 available from `x-linked-etag`. The residual risk is that this is
  undocumented behaviour of a third party's CDN rather than a contract, so it could
  change; the disk-staging fallback is what bounds that, and a wholesale
  withdrawal of range support would degrade every part to its fallback rather than
  break the seed.
- ~~`nodejs24` may not exist in AL2023's repositories.~~ **Resolved by spike**:
  published at 24.11.0. Residual risk is that a future AL2023 release retires it
  while the pin remains, which fails the seed at boot with a legible dnf error —
  the intended failure mode, not a silent one.
- **Hand-rolled byte movement replaces a well-worn library path.** Using
  `fileDownloadInfo` for metadata but moving bytes with our own ranged `fetch`
  means `huggingface_hub`'s retry and resume logic is ours to own. This is the
  deliberate cost of streaming; the disk-staging fallback is what bounds the
  downside, and the per-part retry is arguably finer-grained than what is being
  given up.
- **The 24-hour `ClientToken` window cuts both ways.** It is what makes
  convergence free, and it also means a token must vary for a deliberate re-seed —
  hence `generation`. Getting that wrong shows up as a re-seed that silently
  returns the old, already-terminated instance. Worth a direct test.
- **EMF ties status to log ingestion.** If the CloudWatch agent fails to start,
  the seed still transfers correctly but reports nothing, and layer 3 will reap it
  as stalled after 10 minutes even though it was working. Mitigated by treating an
  agent that fails to start as a boot failure — better a seed that fails fast than
  one that runs invisibly — and by the seeder also writing its records to stdout,
  so the boot log retains a copy.
- **A ten-minute stall threshold is a guess.** The metadata pass before any bytes
  move is the quiet window, and a very large repository could conceivably exceed
  ten minutes of listing and HEADing. Mitigated by emitting a progress record
  during the metadata pass, not only once transfer starts.
- **Verifying sha256 over ~30 GB costs CPU** on a 2-vCPU instance, concurrently
  with TLS. If it proves to be the bottleneck rather than the network, the answer
  is a larger instance, not skipping verification — the manifest's guarantee
  depends on it.
- **Two write paths to the weights prefix now exist during migration**: prefixes
  seeded by the old script have no `_seed.json` and will read as absent, causing
  one re-seed per already-seeded model. That is the correct behaviour under the new
  spec (a prefix whose provenance is unknown is not proven complete), but it is a
  real one-off cost in time and transfer for anyone with a populated bucket. A task
  covers documenting it, and a manifest-backfill helper is deliberately *not*
  offered: writing a manifest for files nobody verified would be asserting a
  guarantee that was never checked.
