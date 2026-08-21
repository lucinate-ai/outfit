# remote — the cloud GPU deployment

The deployment [`outfit remote`](../docs/commands/remote.md) drives:
scale-to-zero, self-hosted LLM endpoints on AWS, each exposing an
OpenAI-compatible API for use as a coding-agent backend. It is split into two
layers. A **control plane** — the lifecycle Lambdas, the weights bucket, the
VPC and the AMI bake pipelines — is deployed **once per account** by
`outfit remote bootstrap`. **Environments** — one per endpoint, each with its
own Elastic IP, API key and allowed CIDR — are created on it by
`outfit remote deploy`, as many as you need side by side. An environment's GPU
instance exists only while you are actually using it: the start Lambda
launches it on demand (and re-wakes it when it is merely stopped), and the
stop Lambda's idle sweep stops it after a period of idleness, then terminates
it after a further retention period.

The inference engine is **pluggable** — [llama.cpp](https://github.com/ggml-org/llama.cpp)
or [vLLM](https://docs.vllm.ai). The deployed default is llama.cpp, serving
Unsloth's `Qwen3.6-27B-MTP-GGUF` at **128k context** with a q8 KV cache and
**multi-token prediction** (~0.8 draft acceptance, so decode is roughly twice
what it would be without). Which engine you get is decided by one line in an
`Outfit` file, not by a redeploy.

The instance is **stateless**, and responsibilities are split cleanly:

- A slim **AMI per engine** (baked by the image stack via EC2 Image Builder)
  carries only the NVIDIA driver and that engine — a `uv` venv for vLLM, a
  prebuilt CUDA `llama-server` for llama.cpp. No Docker. Both are
  model-agnostic and rarely change.
- The **model weights** live in an **S3 bucket**, put there by a disposable
  seed job that downloads from Hugging Face entirely within AWS. You do not run
  it by hand: deploying a model whose weights are missing starts it for you.
- At boot the instance **syncs the weights from S3** onto its disk (~2–4 min)
  and starts the engine pointed at them.

Because the AMI is a regional artifact, the start Lambda can launch in **any**
availability zone — it tries each g6e zone in turn until one has capacity.

The image stack defines an Image Builder **pipeline**, not a build, so
deploying it never runs (or fails on) a bake. You trigger bakes out-of-band
with `pnpm bake <runner>`; each successful bake **tags** its AMI with its
engine, and the start Lambda launches the **newest AMI matching the engine it
was told to run**. A failed bake produces no new AMI and changes nothing.

```
outfit remote bootstrap ─▶ control-plane stack (Lambdas, S3, VPC, roles) + bake pipelines
     pnpm bake llamacpp ─▶ Image Builder pipeline ─(async)─▶ AMI (driver + engine), tagged
outfit remote deploy ─▶ deploy Lambda ─▶ creates env <name>: EIP, SG (your CIDR),
                                      │  API key, deploy-config (what to serve)
                                      └─ seeds weights ─▶ S3 weights bucket (shared)
                                         newest AMI by tag + weights ◀─┐ (at launch)
outfit remote start ─SigV4▶ start Lambda ─ RunInstances (try each AZ) ─▶ EC2 g6e.xlarge
outfit remote status ──?env▶ (Function URL,  + the env's EIP, SSM)      │ L40S 48GB
outfit remote stop ───────▶ stop Lambda        AWS_IAM auth             │ s3 sync weights
outfit remote pause ──────▶  (stop, not terminate)                      │ engine on :8000
                                  ▲
EventBridge rate(5 min) ─────────┘ (idle sweep: stop, then terminate)  ▼
coding agent ── OPENAI_BASE_URL=http://<env EIP>:8000/v1 + api key ──▶ direct HTTP
```

Inference traffic goes directly from your machine to the instance; the Lambdas
only orchestrate. They live outside the VPC (no NAT gateway cost) and observe
the engine by running `curl localhost` on the instance via SSM Run Command, so
nothing is exposed beyond the API port itself, and that only to your IP. A
stable Elastic IP is re-associated with each launch so the endpoint URL never
changes.

For how the pieces fit together — the stacks, the deploy-config control plane,
and the wake/idle lifecycle — see [docs/architecture.md](docs/architecture.md).

## Prerequisites

- An AWS account with admin (or equivalent) credentials configured locally
- Node.js 22+ and [pnpm](https://pnpm.io)
- The [`outfit`](https://github.com/lucinate-ai/outfit) CLI, which drives the
  endpoint
- AWS CLI v2, plus the [Session Manager plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)
  for shell access (there is no SSH)

> **⚠️ GPU quota**: new AWS accounts have a vCPU quota of **0** for G-series
> instances, which makes the very first `start` fail. Check before deploying:
>
> ```sh
> aws service-quotas get-service-quota --service-code ec2 \
>   --quota-code L-DB2E81BA --region us-east-1 \
>   --query 'Quota.Value'
> ```
>
> A `g6e.xlarge` needs 4 vCPUs. If the value is below 4, request an increase
> for "Running On-Demand G and VT instances" and expect approval to take up to
> a day or two. (The quota is per region — a grant in one region does not carry
> to another.)

**Changing region?** g6e coverage is patchy: the region must offer the type at
all (none of eu-west-1/eu-west-2 do), and within a region it exists in only
some AZs (in us-east-1, not us-east-1a). Set `availabilityZones` to the g6e
zones — the start Lambda tries them in order. List what your account sees:

```sh
aws ec2 describe-instance-type-offerings --location-type availability-zone \
  --region <region> --filters Name=instance-type,Values=g6e.xlarge \
  --query 'InstanceTypeOfferings[].Location'
```

## Deploy

The one-time account setup is
[`outfit remote bootstrap`](../docs/commands/remote.md#bootstrapping-the-account),
which drives this directory for you — download, consent plan, then the shared
deploy and the AMI bakes. Endpoints come after it, one `outfit remote deploy`
per environment:

```sh
outfit remote bootstrap   # once per account: control-plane stack + pipelines + bakes
outfit remote deploy      # creates the Outfit's REMOTE environment and says
                          # what it serves; seeds the weights if missing
```

Under the hood, bootstrap runs this directory's own commands — usable by hand
too:

```sh
pnpm install
pnpm cdk bootstrap     # once per account/region
pnpm deploy:image      # creates the bake pipelines — instant, no build yet
pnpm bake llamacpp     # bakes that engine's AMI — ~15-25 min, in the background
pnpm run deploy        # deploys the control-plane stack (Lambdas, VPC, S3 bucket)
```

- `pnpm deploy:image` only creates the pipelines, so it deploys in seconds and
  can never fail because of a bake.
- `pnpm bake <vllm|llamacpp>` builds that engine's AMI and returns immediately;
  the build runs asynchronously — check it with the `aws imagebuilder get-image`
  command the script prints. Re-bake only when the engine version or the driver
  changes; the model is **not** baked in.
- `pnpm run deploy` deploys the **control-plane stack** — VPC, the lifecycle Lambdas,
  the S3 weights bucket, roles — and publishes its outputs for discovery. It
  creates no Elastic IP and no environment.
- `outfit remote deploy` reads the [`Outfit`](Outfit) and its
  [`preset.ini`](preset.ini), creates the environment the Outfit's `REMOTE`
  names (its EIP, API key, ingress scoped to your `--allowed-cidr`, defaulting
  to your public IP), registers it under `~/.config/outfit/remotes/<env>/`, and
  tells it what to serve. If those weights are not in S3 it starts the seed job
  itself (~15–20 min, all within AWS) and says so; wait for it before the first
  `start`.

> Use `pnpm run deploy`, not `pnpm deploy` — the latter is pnpm's own built-in
> `deploy` command. And don't pass `-c` flags through it: pnpm appends extra
> arguments to the **last** command in the script chain, not to `cdk deploy`.
> Put context in `cdk.json` instead.

The bake and the weight seed are independent and can run in parallel.

Everything per-environment — the model, the engine, the context window, the
allowed CIDR — is given to `outfit remote deploy`, not to the stack. `.env` can
hold `HF_TOKEN` for gated model repos (used only when seeding). The shared
layer's own settings all have defaults, overridable in `cdk.json`:

| Context key | Default | Notes |
|---|---|---|
| `region` | `us-east-1` | g6e is not offered everywhere (absent from all of eu-west-1/2) |
| `availabilityZones` | `us-east-1b,c,d,e` | g6e zones the start Lambda tries, in order |
| `hfToken` | *(empty)* | Only for gated repos; used only when seeding |
| `instanceType` | `g6e.xlarge` | Runtime GPU type, 1× L40S 48 GB, ~$1.86/hr |
| `builderInstanceType` | `m5.xlarge` | Cheap non-GPU type used to bake the AMI and to seed |
| `imageVolumeGb` | `80` | AMI root — fits the OS + engine + the model synced at boot |
| `llamacppRelease` | `b10435` | Pinned ai-dock/llama.cpp-cuda build baked into the llama.cpp AMI |
| `vllmVersion` | `0.26.0` | vLLM version installed into that AMI's venv (`uv pip install`) |
| `nvidiaDriverPackage` | `nvidia-driver-570-server-open` | Driver installed in both AMIs |
| `idleThresholdMinutes` | `15` | Stop after this long without requests |
| `stopRetentionMinutes` | `60` | Keep a stopped instance (re-wakeable) this long before terminating it |
| `gracePeriodMinutes` | `30` | Never stop this soon after boot (covers the cold load) |
| `maxRuntimeMinutes` | `240` | Hard stop this long after boot, even if busy |

The **model, quant, context window and engine flags are not in this table** —
they come from the `Outfit` and its preset via `outfit remote deploy`, so
changing model is a command, not a redeploy.

What needs what:
- Change **model, quant, context or engine flags** → edit the `Outfit`/preset,
  then `outfit remote deploy --overwrite`. No bake, no redeploy.
- Change an environment's **allowed CIDR** →
  `outfit remote deploy --overwrite --allowed-cidr <ip>/32`.
- Change **`llamacppRelease`/`vllmVersion`/`nvidiaDriverPackage`** → bump the
  recipe `version` in `lib/image-stack.ts`, then `pnpm deploy:image` +
  `pnpm bake <runner>`.
- Change a shared setting (idle timers, instance type) → `pnpm run deploy`.

**On the model choice**: BF16 weights for a 27B model are ~54 GB and do not fit
the L40S's 48 GB, so a quantised checkpoint is mandatory. The default Q6_K_XL
GGUF is ~22.5 GB, leaving room for a 128k q8 KV cache (~16 GB) inside 48 GB.
For vLLM, FP8 is hardware-native on the L40S (Ada generation).

### Switching engine, or model

The `Outfit` is the control surface. `PROVIDER` names the engine, so the same
file that runs a model locally under `outfit serve` deploys it to the cloud
under `outfit remote deploy`:

```sh
outfit remote deploy                 # what ./Outfit describes
outfit remote deploy path/to/Outfit  # something else
outfit remote deploy --dry-run       # print the config without sending it
```

Cutting back to vLLM means an Outfit with `PROVIDER vllm` and the FP8 repo as
its `MODEL` — both AMIs stay baked, so it is a deploy, not a rebuild. The start
Lambda launches the AMI matching the engine the environment's deploy-config
names, so nothing else has to agree.

#### Companion weights

A model published with extra files beside its weights — a speculative-decoding
drafter, a perception encoder — can carry them too. The deploy-config names
them by **role**, and `outfit remote deploy` fills that in from the preset keys
that already drive a local serve:

| Preset key | Role | Synced to | Engine flag |
|---|---|---|---|
| `spec-draft-model` (`md`) | `draft` | `draft.gguf` | `--spec-draft-model` |
| `mmproj` (`mm`) | `mmproj` | `mmproj.gguf` | `--mmproj` |

Only the **filename** travels: a companion ships in the model's own repo, so
the preset's local path is dropped and the seed fetches that name from Hugging
Face. The deployment then names the file at its own synced path, so nothing
depends on where you keep it locally.

How the engine *uses* a companion is still yours — `--spec-type draft-dflash`
stays in the preset and passes through untouched.

Adding a companion to a model already in S3 re-seeds it: presence is judged
over the whole expected set, so the instance can never start pointing at a
companion that was never synced.

### First boot

A wake is an instance launch, an **S3 sync of the weights** (~2–4 min,
EBS-write-bound), then loading them into VRAM and warm-up — so roughly
**8–10 minutes**, every time, with no Hugging Face dependency. The first
request after a cold start also pays a one-off warm-up (~30 s); steady-state
decode is around 28 tokens/s. Watch a wake:

```sh
pnpm console                                 # SSM shell onto the running instance
tail -f /var/lib/outfit/daemon/engine.log    # engine logs (both runners)
tail -f /var/log/cloud-init-output.log       # boot: s3 sync progress
```

## Daily use

The endpoint is driven by the `outfit` CLI, using this directory's `Outfit`;
its `REMOTE` names the environment `deploy` registered under
`~/.config/outfit/remotes/<env>/`. Run these from this directory (`outfit`
reads `./Outfit`):

```sh
outfit remote start    # boots (or re-wakes a stopped) the instance, blocks
                        # until it is serving, prints OPENAI_BASE_URL + OPENAI_API_KEY exports
outfit apply           # points your coding agent at the endpoint
outfit remote status   # instance state + endpoint health
outfit remote pause    # stop now (no terminate); a later start re-wakes it
outfit remote stop     # terminate now instead of waiting for the idle timer
```

`outfit apply` writes the endpoint's base URL and API key into your harness
config, so export the key that `outfit remote start` prints first. The base URL
comes from the environment's `remote.json` (`base_url`), since the Outfit
states none; a `BASEURL` in the Outfit would override it. The model
name to request is the Outfit's `ALIAS` (`qwen3.6-27b`) — the same value the
server is started under, so the two cannot drift:

```sh
eval "$(outfit remote start)"   # sets OPENAI_BASE_URL + OPENAI_API_KEY
curl "$OPENAI_BASE_URL/models" -H "Authorization: Bearer $OPENAI_API_KEY"
curl "$OPENAI_BASE_URL/chat/completions" \
  -H "Authorization: Bearer $OPENAI_API_KEY" -H 'Content-Type: application/json' \
  -d '{"model":"qwen3.6-27b","messages":[{"role":"user","content":"Say hi"}]}'
```

> Qwen3.6 is a reasoning model and will happily spend a small token budget
> entirely on thinking, returning empty `content` with the text in
> `reasoning_content`. Give requests generous `max_tokens`.

### Idle behaviour

The on-instance outfit daemon reads its engine's request/token counters every
15 seconds and keeps track of when the engine was last doing work, which it
reports as `lastActiveAt` and `idleSeconds` on `/v1/status`. Every 5 minutes
the stop Lambda asks for that (via SSM) and **stops** the instance once
`idleSeconds` passes `idleThresholdMinutes`. With defaults, expect that
**15–20 minutes** after the last request.

Stopping (rather than terminating) keeps the boot disk and the weights the
boot synced onto it, so the next `outfit remote start` **re-wakes** the
instance — a boot without a fresh launch and a no-op S3 sync — instead of
launching one from the AMI. The stopped instance is billed for its volume only,
not compute, and the sweep **terminates** it once it has been stopped longer
than `stopRetentionMinutes` (default 1 h): after that, the next start is a
fresh launch again. `outfit remote pause` does the same stop on purpose.

Sampling on the box is what makes this reliable: a busy endpoint that happens
to have nothing in flight at the moment a 5-minute sweep lands used to read as
idle, because that single sample was the entire signal. Sixty samples between
sweeps cannot miss traffic the way one can.

If the daemon cannot be reached, or answers without a last-active time, the
instance is treated as showing no activity and is still stopped at the
threshold — deliberately, so a wedged box does not run up GPU-hours unnoticed.
That second case is also why the **runtime AMIs must be re-baked before the
control plane is deployed**: an outfit older than daemon-owned idle detection
reports no last-active time, and there is no fallback to counter scraping.

There is also a hard cap: `maxRuntimeMinutes` (default 4 hours) stops the
instance that long after its session started **even if requests are still
flowing**, as a backstop against a runaway session. A session begins at launch
or re-wake (the control plane records both on the instance), so it caps one
running session, not anything cumulative — if you hit it mid-work, `outfit
remote start` brings the endpoint back for another 4 hours. Like the idle
stop, it lands on the next 5-minute tick.

**Pinning an instance up**: tag it `Retain-Until` with a UTC ISO-8601 time and
neither the idle timer nor the hard cap will touch it until then — handy while
debugging on the box. A manual `outfit remote stop` still works.

```sh
aws ec2 create-tags --resources <instance id> \
  --tags Key=Retain-Until,Value=2026-07-25T22:45:00Z
```

## Costs

At rest (nothing running) the endpoint costs only the **Elastic IP, the S3
weights (~$0.60/mo for the 26 GB GGUF), the AMI snapshots, and Secrets
Manager — roughly $6–7/month**. Between the idle stop and the instance's
termination after `stopRetentionMinutes`, its stopped root volume adds a small
EBS charge; long-term idle costs the same as today,
because the sweep does terminate eventually. While running it is
**$1.86/hour in us-east-1**; ~2 h of
coding a day lands around $90/month. Full breakdown in
[docs/costs.md](docs/costs.md).

## Operations

- **Logs**: `pnpm console` (an SSM shell onto the running instance) then
  `tail -f /var/lib/outfit/daemon/engine.log` (the engine, both runners) or
  `tail -f /var/log/cloud-init-output.log` (boot / S3 sync). The engine log and
  the boot log are also shipped to CloudWatch — groups `/cloud-vm-llm/<engine>`
  and `/cloud-vm-llm/boot`, stream `<env>/<instance-id>` — so they survive the
  instance's termination. Lambda decisions (launch AZ, idle/terminate, deploys)
  are in the three Lambdas' CloudWatch log groups.
- **Changing the model**: edit the `Outfit`/preset and run `outfit remote
  deploy`. It seeds the new weights if needed. No bake, no redeploy.
- **Changing the engine version or the driver**: update `llamacppRelease` /
  `vllmVersion` / `nvidiaDriverPackage`, **bump the recipe (and component)
  `version` in `lib/image-stack.ts`** (Image Builder versions are immutable),
  then `pnpm deploy:image` + `pnpm bake <runner>`.
- **Your home IP changed**: `outfit remote deploy --overwrite --allowed-cidr
  <ip>/32`. Ingress is per environment, and an existing environment keeps its
  ingress unless a CIDR is passed explicitly — auto-detection applies only to a
  first deploy.
- **Force a fresh AMI** (same config): just `pnpm bake <runner>` — the runtime
  launches the newest tagged AMI.
- **Force a re-seed** of weights already in S3:
  `outfit remote deploy --reseed` (the deploy Lambda otherwise seeds only what
  is missing). It re-fetches the weights the Outfit names and costs a
  ~20-minute seed instance, so it is never the default.

### Diagnostics

`pnpm console` drops you onto the running instance over SSM (needs
`session-manager-plugin`). Once there:

| Want to know | Command |
|---|---|
| Engine state (idle/running/stopped/crashed) | `curl -s 127.0.0.1:4242/v1/status` |
| Engine + host metrics | `curl -s 127.0.0.1:4242/v1/metrics` |
| Follow the engine's logs | `tail -f /var/lib/outfit/daemon/engine.log` |
| Why it won't start | `tail -50 /var/lib/outfit/daemon/engine.log` (or the boot log below for a pre-engine failure) |
| Is it up? | `systemctl is-active outfit-daemon` · `ss -ltn \| grep :8000` |
| Is MTP actually working | `grep 'draft acceptance' /var/lib/outfit/daemon/engine.log` |
| Boot / S3-sync progress | `tail -f /var/log/cloud-init-output.log` |
| Weights pulled so far | `du -sh /opt/llm/model` |
| GPU + driver | `nvidia-smi` |
| RAM + swap | `free -h` |

(Both runners run under the same `outfit-daemon` unit: the baked `outfit`
binary supervises the engine and serves its control API on loopback `:4242`.)

From your own machine, no shell needed (the EIP is `<endpoint>`):

```sh
curl -s -o /dev/null -w '%{http_code}\n' http://<endpoint>:8000/health   # 200 once serving
eval "$(outfit remote start)"                                            # base URL + key
curl "$OPENAI_BASE_URL/models" -H "Authorization: Bearer $OPENAI_API_KEY"
```

The Lambdas log every decision to CloudWatch. In the **stop** Lambda's log
group, each 5-minute tick prints a JSON line — grep `"mode":"idle"` to see why
it kept or terminated the instance (e.g. `"decision":"stop","reason":"idle for
32.9 min"`, or `"reason":"retained until …"` when a `Retain-Until` tag is set).
The **start** Lambda logs the launch AZ and each wake phase.

## Security notes

- The API port is only reachable from `allowedCidr`, and the engine itself
  requires the generated API key (stored in Secrets Manager) as a second layer.
- Traffic is plain HTTP, so the API key is visible in transit; the /32
  restriction is what makes this acceptable for solo use. The planned fix —
  which also ends the allowed-CIDR juggling when your home IP changes — is
  joining the instance to a tailnet: see
  [docs/tailscale-plan.md](docs/tailscale-plan.md).
- No SSH ingress; shell access is via SSM Session Manager. IMDSv2 is enforced.
- The Function URLs require SigV4-signed requests (`lambda:InvokeFunctionUrl`),
  so `outfit` needs no AWS permissions beyond invoking them.

## Teardown

```sh
pnpm cdk destroy cloud-vm-llm cloud-vm-llm-image
```

Removes both stacks. If an instance is currently running, terminate it first
(`outfit remote stop`) — it is not owned by CloudFormation. The **S3 weights
bucket is retained** on destroy (so you don't lose the seeded weights); baked
AMIs and their snapshots are not owned by the stacks either. Delete the bucket,
deregister the AMIs, and delete their snapshots by hand to reclaim that storage.

## Troubleshooting

- **`start` returns `unconfigured`**: nothing has been deployed yet. Run
  `outfit remote deploy`.
- **`start` returns `no-ami`**: no AMI is tagged for the engine you asked for.
  Run `pnpm bake <runner>` and wait for it to reach `AVAILABLE`.
- **`start` returns `no-capacity`**: every configured AZ was out of g6e
  capacity at that moment. The start Lambda already tried them all; wait a few
  minutes and retry, or widen/adjust `availabilityZones`.
- **A bake fails**: it does **not** touch the stack or the previous AMI. Check
  it with `aws imagebuilder get-image --image-build-version-arn <arn>` (the arn
  `pnpm bake` printed) and the CloudWatch log group for the recipe, fix, and
  `pnpm bake` again. The driver install is the most likely failure — adjust
  `nvidiaDriverPackage`.
- **`deploy:image` fails with "recipe/component version already exists"**: you
  changed a baked-in setting without bumping the `version` on the recipe (or
  component) in `lib/image-stack.ts`. Bump and redeploy.
- **`start` reaches `running` but never `ready`, or the model is empty**: the
  weights aren't in S3 yet, or a seed is still running. Check
  `aws s3 ls s3://<bucket>/models/<runner>/<model>/`.
- **Quota errors on launch**: see the GPU quota warning above.
- **`start` times out repeatedly**: `pnpm console` onto the instance and read
  `tail -50 /var/lib/outfit/daemon/engine.log` (or `/cloud-vm-llm/llamacpp` in
  CloudWatch if the instance is already gone). Known startup
  crashes, all handled for the defaults but reachable after a bump:
  - `libcudart.so.12: cannot open shared object` — the prebuilt llama.cpp
    tarball bundles only its own libraries, not the CUDA runtime; the AMI
    installs `cuda-cudart-12-8`, `libcublas-12-8` and `libnccl2` for it.
  - `Python.h: No such file or directory` (vLLM) — a model needs Triton's
    runtime compile; the AMI installs `python3.12-dev` for it.
  - `Could not find nvcc` (vLLM) — the FlashInfer sampler wants the CUDA
    toolkit, which the slim AMI omits. The user-data sets
    `VLLM_USE_FLASHINFER_SAMPLER=0` (native sampler) to avoid it.
  - Engine start OOM — lower `CONTEXT` in the Outfit, or use a smaller quant;
    the driver failing to load shows up as a bad `nvidia-smi`.
- **The coding agent reports `the model ... does not exist`**: the `model` id it
  sends must equal what the server serves. Under llama.cpp that is the Outfit's
  `ALIAS`, which is also what the server is started with, so keep the two the
  same — and if you deploy with a different `ALIAS`, re-run `outfit apply`.
  Under vLLM there is no alias, so the id is the Hugging Face repo. Either way,
  `curl "$OPENAI_BASE_URL/models"` shows the truth.
