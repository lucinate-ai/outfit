## Why

Standing up the remote GPU endpoint today means leaving the `outfit` CLI
entirely: the user has to obtain the `remote/` TypeScript CDK project and run a
multi-step `pnpm`/`cdk` sequence by hand (documented only in `remote/README.md`),
in the right order, with two easy-to-miss gotchas. The `outfit` binary ships
none of that and does none of the orchestration, so the gap between "installed
`outfit`" and "have a remote endpoint" is a manual, error-prone detour. A single
`outfit remote bootstrap` command should close that gap — while making the AWS
resources and cost it is about to create unmistakably clear before anything is
deployed.

## What Changes

- Add a `bootstrap` subcommand to the `outfit remote` command group. It obtains
  the `remote/` CDK project and drives the deploy sequence end to end:
  `pnpm install` → (once) `cdk bootstrap` → `pnpm deploy:image` → `pnpm bake
  <runner>` → `pnpm run deploy` (which generates `remote.json`) →
  `outfit remote deploy`. The endpoint `Outfit` is the committed, hand-written
  `remote/Outfit` — nothing generates it; it states no `BASEURL` and takes the
  endpoint address from `remote.json`'s `base_url`.
- Register the deployed instance as a **named environment** (depends on
  `add-remote-environments`): a `--env <name>` flag (default `default`) selects
  where the generated `remote.json` is written —
  `~/.config/outfit/remotes/<env>/remote.json` — so provisioning a second
  instance never clobbers the first, and an Outfit points at it with
  `REMOTE <env>`.
- Obtain the CDK sources by **downloading** a version-matched snapshot of
  `remote/` from the GitHub repository (the tag matching the running binary),
  with a `--ref` override and a fallback for `dev` builds. The sources are not
  embedded in the binary: `node_modules` cannot ship in a Go binary and a
  `pnpm install` is needed at runtime regardless.
- Gate every AWS-mutating action behind an explicit **consent step**. Before
  deploying, bootstrap prints the target AWS account and region, the resources
  it will create (VPC, Elastic IP, S3 weights bucket, secrets, SSM parameters,
  three Lambdas with Function URLs, an EventBridge idle rule, EC2 Image Builder
  pipelines, and a GPU-adjacent AMI bake), the rough cost, and the exact
  commands it will run — then requires confirmation. `--dry-run` prints this plan
  and stops; `--yes` skips the interactive prompt for non-interactive use.
- Run preflight checks first (Node/`pnpm` present, AWS credentials resolvable,
  whether CDK is already bootstrapped) and surface the GPU vCPU quota as a
  warning it cannot auto-raise.
- Collect the two required, no-default settings — `allowedCidr` (the user's
  public IP as a `/32`) and `runner` (`llamacpp` default, or `vllm`) — via flags
  or detection, and write them where the CDK reads them (`remote/.env` and
  `cdk.json` context) so `pnpm run deploy` picks them up.
- Default to doing the fast, deterministic steps and kicking the slow ~20-40 min
  AMI bake off asynchronously, handing off the bake wait and quota increase with
  clear instructions; `--wait` blocks on the bake.
- Bootstrap is re-runnable: it skips `pnpm install` when dependencies are
  present and `cdk bootstrap` when already done, and does not redeploy stacks
  needlessly.
- Update the top-level and `remote` usage/help text and the shell completion
  scripts to list `bootstrap`.

This does **not** rewrite the CDK in Go or embed the infrastructure in the
binary. `pnpm`/`cdk`/Node remain runtime prerequisites for the bootstrap path;
a self-contained (no-Node) provisioner would require replacing CDK with raw
CloudFormation and Go Lambdas and is explicitly out of scope here.

## Capabilities

### New Capabilities

- `endpoint-provisioning`: standing up the remote endpoint's AWS infrastructure
  from the CLI — obtaining the version-matched CDK sources, preflight checks, the
  consent gate and its dry-run, collecting required settings, and orchestrating
  the (resumable) `pnpm`/`cdk` deploy sequence through to a usable `remote.json`.

### Modified Capabilities

- `remote-endpoint`: the "Remote command group" requirement gains `bootstrap`
  as a recognised subcommand alongside `start`, `stop`, `status`, `deploy` and
  `ls` (the last added by `add-remote-environments`, which this change is
  sequenced after).

## Impact

- **Depends on `add-remote-environments`**: bootstrap writes the deployed
  instance into that per-user environment registry (via `EnvConfigPath(--env)`)
  instead of a single shared file. This change is sequenced after it.
- **New code**: `cmd/outfit/remote_bootstrap.go` (the handler), plus a small
  internal helper package if warranted (source download/extract, external-command
  runner, consent-plan rendering). New `case "bootstrap"` in `cmdRemote`
  (`cmd/outfit/remote.go`).
- **Touched**: usage/help text in `cmd/outfit/main.go`; the embedded completion
  scripts `cmd/outfit/completion.{bash,zsh,ps1}`.
- **Dependencies**: reuses `aws-sdk-go-v2` (already present) for credential
  resolution; optionally adds the STS client to name the account/region in the
  consent plan. No new Node/CDK code — it drives the existing `remote/` project.
- **Runtime prerequisites** (documented, checked by preflight): Node 22, `pnpm`,
  AWS credentials, a one-time `cdk bootstrap`, and GPU vCPU quota for a later
  `start`.
- **Docs**: `remote/README.md` and the `docs/commands/remote.md` reference gain
  the bootstrap flow; the manual sequence stays documented as the under-the-hood
  detail.
