## Context

Remote inference is split into two layers. The **shared** layer — Image Builder
+ AMIs, the lifecycle Lambdas, the weights bucket, shared roles and a VPC — is
deployed once per account, analogous to `cdk bootstrap`. **Environments** — each
with its own Elastic IP and EC2 instance — are created later, on demand, by
`outfit remote deploy` (the sibling `add-remote-deploy-environments` change),
which the shared Lambdas then drive across the account.

This change is the shared layer. It keeps the pragmatic approach decided earlier
— wrap the existing TypeScript CDK by downloading it and shelling out to
`pnpm`/`cdk`, not rewriting it in Go — and the hard consent requirement. What
changed is scope: bootstrap no longer creates an endpoint (no EIP, no instance,
no environment registration); it stands up only what every environment reuses,
and publishes it for discovery.

## Goals / Non-Goals

**Goals:**

- One command for the once-per-account shared setup: Image Builder + AMIs, the
  environment-aware lifecycle Lambdas + IAM, the shared bucket/roles/VPC.
- Discoverable afterwards: `outfit remote deploy` finds the shared layer from
  CloudFormation stack outputs, so nothing per-environment is baked in here.
- A hard consent gate over the shared resources and their cost; `--dry-run` and
  `--yes`.
- Idempotent and safe to re-run (it never touches a live instance).
- Reuse existing patterns and add no dependency not already in the module graph.

**Non-Goals:**

- Creating an environment (EIP + instance), registering it, or seeding weights —
  that is `outfit remote deploy` in `add-remote-deploy-environments`.
- The per-environment API key (per-env, created at deploy) and the
  overwrite-a-live-instance guard (belongs to deploy).
- Rewriting the CDK in Go or embedding infrastructure in the binary.

## Decisions

### Wrap the TS CDK by downloading sources; do not embed

Bootstrap downloads the `remote/` subtree from
`codeload.github.com/lucinate-ai/outfit/tar.gz/<ref>` (stdlib only) and extracts
`remote/*` into a work dir. Embedding was rejected: `node_modules` can't ship in
a Go binary and a `pnpm install` runs at runtime anyway.

### Ref matches the binary; sources in a ref-keyed, pruned cache

`version` (`-ldflags -X main.version`) drives the ref: a clean tag verbatim; a
`dev`/dirty build falls back to `main`; `--ref` overrides. The default work dir
reuses `internal/remote`'s `configHome()` → `<configHome>/cdk/<ref>/` (named
`cdk/` to avoid colliding with the `remotes/` environment registry), overridable
with `--dir`. Keying by ref means a re-run at the same version reuses sources
(and `node_modules`); a new version downloads fresh and prunes the old on success.

### Shared settings go into the files the CDK reads

- Which runner AMIs to bake → `cdk.json` `context.runners` (`--runners`, default
  both). The engine is a per-environment choice at `deploy`, so no single
  `runner` here; bootstrap just ensures the AMIs exist.
- An optional `--hf-token` → `.env` `HF_TOKEN`, `0o600`, to populate the shared
  HfToken secret used when seeding gated weights.

The allowed ingress CIDR is **not** a bootstrap setting: it is per-environment,
so it lives on `outfit remote deploy` (each environment scopes who can reach its
instance).

### Consent gate

Before any AWS-mutating command, print (to stderr) a plan: account (via
`sts:GetCallerIdentity`, degrading to "unknown" offline), region, the shared
resource list, a qualitative cost caveat (ongoing at-rest + per-hour-while-running
figures live in the CDK cost docs, not the binary), and the exact command list.
Then require confirmation. `--dry-run` returns before the prompt and runs nothing;
`--yes` skips the prompt; any non-`y`/`yes` answer aborts.

### Discovery via CloudFormation stack outputs

The shared stack publishes its Lambda URLs, weights bucket, roles and region as
CloudFormation outputs under a well-known stack name. `outfit remote deploy`
reads them with `DescribeStacks` (the deploy change), so no local account file
can go stale and it works from any machine with account access. Bootstrap adds the
`aws-sdk-go-v2/service/cloudformation` client, also used in preflight to detect
whether the account is already bootstrapped.

### Idempotent re-run, no overwrite guard here

Re-running updates the shared stack (CloudFormation no-ops the unchanged parts),
skips `pnpm install` when `node_modules` is present, and re-bakes only on
`--force-bake`. Because bootstrap touches only shared infra and never a live
instance, it needs no `--overwrite`; the overwrite-a-live-instance guard lives in
`deploy`. Preflight notes when the account is already bootstrapped.

### Reuse the exec idiom; orchestration

A small `runStep` helper (behind a `stepRunner` seam) mirrors `serve.go`'s
`exec.Command` streaming, with `cmd.Dir = <cdkDir>` and `signal.NotifyContext`
for Ctrl-C, returning errors (naming the failed step) rather than `os.Exit`. The
default run: preflight → download → write settings → consent → `pnpm install`
(skip if `node_modules`) → `cdk bootstrap` → `pnpm deploy:image` → `pnpm bake
<runners>` (async) → deploy the shared stack. `--wait` blocks on the bake;
otherwise the slow bake is handed off.

## Risks / Trade-offs

- **Node/pnpm/cdk remain prerequisites** → preflight checks them and AWS creds,
  failing early before anything downloads.
- **Slow, capacity-gated bake and GPU quota** → bake is async by default; the
  quota is a prominent warning (can't auto-raise).
- **Accidental cost** → the consent gate and `--dry-run` make the shared
  resources and cost explicit; nothing runs on a non-`yes` answer.
- **Depends on the `remote/` CDK restructure** (shared stack split, env-aware
  Lambdas) landing in the TS project → tracked with these two changes; bootstrap
  drives whatever the downloaded sources define.

## Open Questions

- Bootstrap bakes both runner AMIs by default; if baking is expensive enough to
  want opt-in, `--runners` already allows narrowing.
