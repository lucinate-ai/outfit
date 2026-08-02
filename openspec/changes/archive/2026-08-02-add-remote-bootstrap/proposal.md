## Why

Standing up remote GPU inference from the `outfit` CLI needs a one-time,
per-account setup — much like `cdk bootstrap` — before any endpoint can run.
Today that setup lives only as a manual `pnpm`/`cdk` sequence in
`remote/README.md`, and it is tangled together with a single endpoint's own
resources. Separating the two makes the model scale: shared, reusable
infrastructure is deployed **once per account**, and individual endpoints
(environments) are created later, on demand.

`outfit remote bootstrap` should deploy only that shared layer — the parts every
environment reuses — and leave the per-environment resources to
[`add-remote-deploy-environments`](../add-remote-deploy-environments/proposal.md),
where `outfit remote deploy` creates an environment's own instance.

## What Changes

- Add a `bootstrap` subcommand to `outfit remote`. It deploys the **shared,
  account-level** infrastructure once, analogous to `cdk bootstrap`:
  - EC2 Image Builder pipelines and the baked AMIs (baking is a shared, common
    thing);
  - the lifecycle Lambdas — start / stop / monitor / deploy — and their IAM.
    These are **environment-aware**: one set drives every environment's instance
    in the account (the per-environment logic lands in the deploy change);
  - the shared S3 weights bucket (per-model prefixes), shared IAM roles, and a
    shared VPC with public subnets.
- **No EIP and no EC2 instance** are created by bootstrap, and **no environment
  is registered** — those belong to `outfit remote deploy`. The per-environment
  **API key** is also per-environment, not shared.
- Make the shared stack **discoverable**: it publishes its Lambda URLs, weights
  bucket, roles, and region as CloudFormation stack outputs under a well-known
  stack name, which `outfit remote deploy` reads (via `DescribeStacks`) to create
  and drive environments.
- Obtain the CDK sources by **downloading** a version-matched snapshot of
  `remote/` from the repository (`--ref` override; `dev` fallback), into a
  ref-keyed, pruned cache. Not embedded — a `pnpm install` runs at runtime.
- Gate the deploy behind an explicit **consent step**: print the target account
  and region, the shared resources it will create, a qualitative cost caveat, and
  the exact commands, then require confirmation. `--dry-run` prints and stops;
  `--yes` confirms non-interactively.
- Run preflight first (Node/`pnpm`, AWS credentials, whether the account is
  already bootstrapped) and surface the GPU vCPU quota as a warning.
- Bootstrap is **idempotent**: re-running updates the shared stack (CloudFormation
  no-ops what is unchanged) and re-bakes only on request. Because it touches only
  shared infra and never a live instance, re-running is safe — the
  overwrite-a-live-instance guard belongs to `deploy`, not here. It notes when
  the account is already bootstrapped.
- Bake AMIs for the runner(s) the account needs (`--runners`, default both
  `llamacpp` and `vllm`) so any environment can pick its engine at deploy time;
  the engine is a per-environment choice, so `runner` is not a bootstrap setting.
- Update the top-level and `remote` usage/help text and the completion scripts.

This does **not** rewrite the CDK in Go or embed infrastructure in the binary;
`pnpm`/`cdk`/Node remain runtime prerequisites for the bootstrap path.

## Capabilities

### New Capabilities

- `endpoint-provisioning`: standing up the **shared, account-level** remote
  infrastructure from the CLI — obtaining the version-matched CDK sources,
  preflight, the consent gate and dry-run, and orchestrating the (idempotent)
  `pnpm`/`cdk` deploy of the shared stack (Image Builder + AMIs, lifecycle
  Lambdas, shared bucket/roles/VPC), published as discoverable stack outputs.

### Modified Capabilities

- `remote-endpoint`: the "Remote command group" requirement gains `bootstrap` as
  a recognised subcommand alongside `start`, `stop`, `status`, `deploy` and `ls`.

## Impact

- **Depends on the `remote/` CDK restructure**: the current single runtime stack
  splits into a shared/bootstrap stack (this change) and per-environment
  resources (the deploy change), and the Lambdas become environment-aware. That
  TS work is a prerequisite carried alongside these two changes.
- **Paired with `add-remote-deploy-environments`**: bootstrap deploys the shared
  layer; that change's `outfit remote deploy` creates per-environment EIP +
  instance, registers the environment, and the shared Lambdas manage it.
- **New code**: `cmd/outfit/remote_bootstrap.go`, `internal/remote/source.go`
  (download/extract/prune), a `case "bootstrap"` in `cmdRemote`.
- **Dependencies**: reuses `aws-sdk-go-v2`; adds the STS client (name the account
  in the plan) and the CloudFormation client (detect whether the shared stack is
  already deployed). No new Node/CDK code — it drives the existing `remote/`.
- **Runtime prerequisites** (checked by preflight): Node 22, `pnpm`, AWS
  credentials, a one-time `cdk bootstrap`, and GPU vCPU quota.
- **Docs**: `remote/README.md` and `docs/commands/remote.md` gain the bootstrap
  flow; the manual sequence stays as the under-the-hood detail.
