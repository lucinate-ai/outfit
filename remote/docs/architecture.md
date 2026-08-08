# Architecture

Maintainer-facing notes on how this deployment is put together. For *using* it, see
the [README](../README.md); for the pound-and-pence, [costs.md](costs.md).

## What it is

A **scale-to-zero, self-hosted OpenAI-compatible LLM endpoint** on AWS. A GPU
instance exists only while you are actually serving requests: a start Lambda
launches one on demand, and a stop Lambda terminates it after idle. Nothing runs
(and almost nothing is billed) at rest.

Three ideas hold it together:

- **The instance is stateless.** No fixed EC2 instance, no persistent EBS. The
  start Lambda launches one from a baked AMI, the stop Lambda terminates it. The
  model weights live in S3 and are synced onto the instance at boot.
- **The runner is pluggable.** The inference server (currently vLLM; llama.cpp
  landing) is chosen per deployment, not hard-wired. A runner-neutral
  *deploy-config* says what to serve; the start Lambda builds the right command.
- **Deployment config is data, not a redeploy.** What to serve (runner, model,
  context, serve args) lives in an SSM parameter written by a deploy Lambda, so
  switching model or runner is a parameter write — no `cdk deploy`.

## The pieces

```mermaid
flowchart TB
  subgraph client["Your machine"]
    agent["coding agent<br/>(OpenAI client)"]
    outfit["outfit CLI"]
  end

  subgraph image["Image stack (cloud-vm-llm-image)"]
    pipeline["EC2 Image Builder<br/>pipeline (per runner)"]
    ami["slim AMI(s)<br/>tagged by role + runner"]
    pipeline -->|pnpm bake| ami
  end

  subgraph runtime["Runtime stack (cloud-vm-llm)"]
    start["StartFn"]
    stop["StopFn"]
    deploy["DeployFn"]
    dcfg[("SSM: deploy-config")]
    idle[("SSM: idle-state")]
    eip["Elastic IP"]
    s3[("S3: weights")]
    sg["Security group<br/>:port from your /32"]
    sched["EventBridge<br/>rate(5 min)"]
  end

  inst["EC2 g6e (L40S)<br/>runner + weights"]

  outfit -->|SigV4 start, status| start
  outfit -->|SigV4 stop| stop
  outfit -->|SigV4 deploy| deploy
  deploy -->|write| dcfg
  start -->|read at wake| dcfg
  start -->|RunInstances<br/>+ associate| eip
  start -.->|launch newest AMI by tag| ami
  inst -->|sync at boot| s3
  eip --- inst
  agent -->|http + api key| inst
  sched --> stop
  stop -->|SSM scrape, terminate| inst
  start -->|SSM health| inst
```

The Lambdas live **outside the VPC** (no NAT cost) and reach the instance over
**SSM Run Command** — a `curl` to the on-instance **outfit daemon**'s
loopback control API (`127.0.0.1:4242`), which supervises the engine and
collects its metrics — so nothing is exposed beyond the vLLM port, and that
only to your `/32`. A stable **Elastic IP** is re-associated on each launch so
the base URL never changes.

## The deploy-config control plane

The seam between "what infra exists" (CDK's job, provisioned once) and "what to
serve" (per deployment). `outfit remote deploy` reads an Outfit file and POSTs a
DeployConfig to the deploy Lambda; the Lambda validates it and writes the
`/cloud-vm-llm/deploy-config` SSM parameter. The next wake reads it.

```mermaid
flowchart LR
  outfitfile["Outfit file<br/>(runner, MODEL, CONTEXT, preset)"]
  outfit["outfit remote deploy"]
  deploy["DeployFn<br/>(validate)"]
  param[("deploy-config<br/>SSM param")]
  seed["seed instance<br/>(if model changed)"]
  start["StartFn (next wake)"]

  outfitfile --> outfit -->|SigV4 POST| deploy
  deploy -->|PutParameter| param
  deploy -.->|RunInstances| seed --> s3[("S3 weights")]
  start -->|read| param
  start -->|render daemon deploy-config| unit["outfit daemon"]
```

The DeployConfig contract (`lambda/shared/deploy-config.ts`):

```
{ runner: "vllm" | "llamacpp",   // required; no default
  modelId, quant, contextSize, servedModelName,
  serveArgs: string[] }          // runner-specific flags, pre-tokenised
```

`weightsPrefix` is **not** part of the wire contract — the Lambda derives it as
`models/<runner>/<modelId>[/<quant>]/` and stores it in the parameter, so callers
never encode the S3 layout (and a prefix sent in the body is ignored). If those
weights are not in the bucket, the Lambda launches the seed instance itself and
replies `{seeding: true, seedInstanceId}`; a wake before it finishes would sync
an incomplete prefix, so wait for it.

At boot, `buildInferenceUserData()` renders it into the on-instance outfit
daemon's own deploy config — the model as the synced local path, the bind
address and per-runner key delivery resolved into the serve args — and the
daemon builds the engine command from there (`vllm serve …` or
`llama-server …`). There is **no default runner**: an unset or invalid config
fails the wake loudly rather than guessing.

The parameter is **outfit/manual-owned**. CDK creates it with a constant
`unconfigured` placeholder — deliberately *not* the cfg-derived config — so a
later `cdk deploy` can never clobber what `outfit remote deploy` (or a manual
edit) put there. `pnpm deploy`'s seed step (`scripts/seed-deploy-config.mjs`)
writes a cfg-derived initial config over the placeholder *only* while it is still
unconfigured, and only when CDK knows the full serve config (vLLM); llama.cpp's
serve args come from an Outfit, so its config is left for `outfit remote deploy`
to set.

## Wake lifecycle

`outfit remote start` (or any POST to the start Function URL) blocks until the
server is answering, so the caller gets one "ready" with the base URL + key.

```mermaid
sequenceDiagram
  participant O as outfit remote start
  participant S as StartFn
  participant P as deploy-config (SSM)
  participant E as EC2
  participant I as instance
  O->>S: POST (SigV4)
  S->>P: read deploy-config
  alt unconfigured
    S-->>O: 503 "run outfit remote deploy"
  end
  S->>E: RunInstances (try each g6e AZ until capacity)
  S->>E: associate Elastic IP
  loop until healthy or deadline
    S->>I: SSM curl localhost/health
  end
  Note over I: boot → swap → S3 sync weights → serve
  S-->>O: 200 ready {base_url, api_key}
```

Boot user-data (built by the start Lambda from the deploy-config): log
`nvidia-smi`, add a swapfile, `aws s3 sync` the weights, fetch the API key, then
write the daemon's deploy config, start `outfit daemon` (loopback `:4242`), and
request the engine's first start over its control API. The health check hits
`/health` on the port — portable across runners.

## Idle / stop

```mermaid
flowchart TB
  tick["EventBridge tick<br/>(every 5 min)"] --> stop["StopFn idleCheck"]
  stop --> retain{"Retain-Until<br/>in the future?"}
  retain -->|yes| wait1["wait"]
  retain -->|no| cap{"past max runtime?"}
  cap -->|yes| term["TerminateInstances"]
  cap -->|no| grace{"within grace<br/>of launch?"}
  grace -->|yes| wait2["wait"]
  grace -->|no| scrape["SSM: scrape metrics"]
  scrape --> active{"activity since<br/>the idle anchor?"}
  active -->|yes| upd["record, wait"]
  active -->|no| idle{"idle > threshold?"}
  idle -->|yes| term
  idle -->|no| wait3["wait"]
```

A failed scrape counts as "no activity", so a wedged box is still terminated at
the threshold rather than burning GPU-hours. A `Retain-Until` instance tag (UTC
ISO-8601) overrides both the idle timer and the max-runtime cap; a manual
`outfit remote stop` still terminates immediately.

## Image stack

`cloud-vm-llm-image` defines an **Image Builder pipeline**, not an image — so
`cdk deploy` is instant and a bad bake can never fail the stack. `pnpm bake`
triggers a build out-of-band; each successful bake tags its AMI (role, and — as
the second runner lands — runner), and the start Lambda launches the **newest
AMI matching the tags**. A slim AMI carries only the driver + the runner
(vLLM as a `uv` venv; llama.cpp as a prebuilt CUDA `llama-server`) — no Docker.

## Key files

| Concern | File |
|---|---|
| Config + validation | `lib/config.ts` |
| Runtime stack (lambdas, EIP, S3, params) | `lib/llm-stack.ts` |
| Image Builder pipeline | `lib/image-stack.ts` |
| Deploy-config contract | `lambda/shared/deploy-config.ts` |
| Runner registry (one spec per runner: boot, weights, seed) | `lambda/runners/` |
| The on-instance daemon's API (SSM curl targets) | `lambda/shared/daemon.ts` |
| Wake / launch / user-data (`buildInferenceUserData`) | `lambda/start/index.ts` |
| Idle / manual stop | `lambda/stop/index.ts`, `lambda/shared/idle.ts` |
| Set the deploy-config | `lambda/deploy/index.ts` |
| Shared AWS + SSM helpers | `lambda/shared/aws.ts` |
| Seed weights to S3 | `scripts/seed-model.mjs` |
| Seed the initial deploy-config (once, over the placeholder) | `scripts/seed-deploy-config.mjs` |
