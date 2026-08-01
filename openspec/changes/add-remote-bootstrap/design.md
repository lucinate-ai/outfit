## Context

The remote endpoint's infrastructure lives in `remote/` as a standalone
TypeScript AWS-CDK project (`pnpm`). Deploying it today is a manual, ordered
`pnpm`/`cdk` sequence documented only in `remote/README.md`. The `outfit` binary
carries none of it. `outfit remote {start,stop,status,deploy}` (in
`cmd/outfit/remote.go`, backed by `internal/remote`) drive an endpoint that
already exists; nothing in the CLI creates one.

This change adds `outfit remote bootstrap` to close that gap by orchestrating the
existing CDK project — not reimplementing it. The design is constrained by three
things: the CDK is TypeScript (so `pnpm`/`cdk`/Node stay runtime prerequisites);
the CDK reads its two no-default settings from specific files (`.env` and
`cdk.json` context); and the `remote/README.md` records two easy-to-miss gotchas
(`pnpm run deploy`, never `pnpm deploy`; never pass `-c` context through
`pnpm run`). The direction (wrap the TS CDK, download the sources) and the hard
consent requirement were decided with the user up front.

This change **depends on `add-remote-environments`**: bootstrap registers the
deployed instance in that per-user environment registry
(`~/.config/outfit/remotes/<env>/remote.json`) rather than a single shared file,
so provisioning more than one instance does not clobber.

## Goals / Non-Goals

**Goals:**

- One command from "installed `outfit`" to "a usable `remote.json`", driving the
  README sequence: `pnpm install` → `cdk bootstrap` → `deploy:image` → `bake` →
  `run deploy` → the initial `outfit remote deploy`.
- A hard consent gate: nothing that touches AWS runs until the user has seen the
  target account/region, the resources, the cost, and the exact commands, and
  confirmed. `--dry-run` shows the plan and changes nothing; `--yes` confirms
  non-interactively.
- Version-matched sources: the downloaded `remote/` matches the running binary,
  so the infrastructure matches the CLI driving it.
- Re-runnable: skip work already done; don't accidentally trigger a second slow
  bake or block on the first one.
- Reuse existing patterns (the `exec` streaming idiom, the AWS default-credential
  chain, the XDG config-dir logic) and add no runtime dependency that isn't
  already in the module graph.

**Non-Goals:**

- Rewriting the CDK in Go, embedding the infrastructure in the binary, or a
  no-Node provisioner. That requires replacing CDK with raw CloudFormation + Go
  Lambdas and is a separate, much larger effort. `aws-cdk-go` would not help — it
  still requires the Node `cdk` CLI at runtime.
- Managing the endpoint after provisioning (that is the existing `remote`
  subcommands) or raising the GPU vCPU quota (only warned about; AWS-side).
- First-class Windows support: the CDK's own scripts are bash, so bootstrap is
  macOS/Linux-first like the rest of `remote/`.

## Decisions

### Wrap the TS CDK by downloading sources; do not embed

Bootstrap downloads the `remote/` subtree from
`codeload.github.com/lucinate-ai/outfit/tar.gz/<ref>` (stdlib `net/http` +
`compress/gzip` + `archive/tar`, no new dep) and extracts only `remote/*` into a
work dir, guarding against path traversal. Embedding the TS project was rejected:
`node_modules` can't ship in a Go binary and a `pnpm install` is needed at
runtime anyway, so embedding saves nothing while `go:embed` can't even reach the
repo-root `remote/` from `cmd/outfit`.

### Ref resolution matches the binary, with a dev fallback

`version` (`cmd/outfit/main.go`, set via `-ldflags -X main.version` from the
Makefile/goreleaser) drives the ref: a clean tag (`v0.3.1`) is used verbatim;
`dev` or a `-dirty`/`-g<sha>` describe-suffix falls back to the `main` branch;
`--ref` overrides. This keeps released binaries reproducible and dev builds
working against the tip of `remote/`.

### Sources live under XDG config, in a ref-keyed, pruned cache

Default work dir is `$XDG_CONFIG_HOME/outfit/cdk/<ref>/` (mirroring
`internal/remote.ConfigPath()`'s XDG base), overridable with `--dir`. It is named
`cdk/`, not `remote/`, to avoid confusion with the environments registry at
`remotes/<name>/` (a different thing — deployment state, not CDK sources). Not the
repo's own `remote/` (a `go install`ed binary has no checkout) and not the cwd
(surprising). This keeps the written `.env`, the generated `remote.json` (before
it is registered), and the committed `remote/Outfit` in one predictable place.

The directory is **keyed by the resolved ref** so it doesn't go stale across
upgrades: a same-version re-run reuses `.../cdk/<ref>/` (and its
`node_modules`), while a new binary version resolves a new ref and downloads
fresh into its own dir — the old sources are never silently reused against a
newer binary. "Is the ref unchanged?" is then just "does this ref's dir exist?",
so no separate marker file is needed. The sources are a **disposable cache**: on
a successful bootstrap, other ref dirs under the default `remote/` parent are
**pruned automatically**, keeping only the current one. Nothing the user typed is
trapped there — the CIDR is auto-detected, the HF token is a flag, and the
generated `remote.json` is registered in the environments registry — and a
version change
forces a fresh `pnpm install` anyway (new lockfile), so retaining old
`node_modules` buys nothing. Pruning is scoped to the default location only: an
explicit `--dir` is treated as the user's own and is neither ref-namespaced nor
pruned.

### Settings go into the files the CDK reads

- `allowedCidr` → `.env` `ALLOWED_CIDR` (config reads context ?? `.env`). Written
  in Go with a small upsert, file mode `0o600` (it may sit next to `HF_TOKEN`).
- `runner` → `cdk.json` `context.runner` (config reads it from context only; and
  `-c` can't be passed through `pnpm run deploy`). An additive JSON edit visible
  to every `cdk` invocation.
- `hfToken` (optional) → `.env` `HF_TOKEN`.

### Consent gate

Before any AWS-mutating command, print (to stderr) a plan: account (via
`sts:GetCallerIdentity` — an already-indirect dep, promoted with no new
download; degrades to "unknown" offline), region, runner, CIDR, source ref/dir,
the resource list, a qualitative cost caveat (an ongoing at-rest cost plus a
per-hour GPU cost while running — no hardcoded figures; point at the CDK cost
docs, which are maintained with the infra), the quota caveat, and the exact
command list. Then require confirmation. `--dry-run` returns before the
prompt and runs nothing (and skips the network fetch, staying offline-safe);
`--yes` skips the prompt; any non-`y`/`yes` answer aborts cleanly.

### Reuse the existing exec idiom; no new process package

`serve.go` and `cmdHarness` already stream child stdio via `exec.Command` +
`os.Stdin/Stdout/Stderr`. Bootstrap adds one small unexported `runStep` helper
(behind a `stepRunner` seam for tests) using `exec.CommandContext` with
`cmd.Dir = <remoteDir>` (never `os.Chdir`) and a `signal.NotifyContext` so Ctrl-C
reaches the slow child. Unlike `cmdHarness`, it returns errors (naming the failed
step) rather than `os.Exit`, so orchestration can report where to resume.

### Orchestration: fast steps sync, bake async, gated tail handed off

Default run does preflight → download → write settings → consent → `pnpm install`
(skipped if `node_modules` present) → `cdk bootstrap` (idempotent, always run) →
`deploy:image` → `bake <runner>` (async) → `run deploy` (generates
`remote.json`) → register the generated `remote.json` as the `--env` environment
(`EnvConfigPath(env)`, default `default`) and print next steps. `remote/Outfit`
is committed and hand-written (no `BASEURL`; the address comes from
`remote.json`'s `base_url`), so nothing generates it. The ~20-40 min bake is
started and handed off with instructions; `--wait` blocks until the AMI is
available.
Resumability is filesystem-driven (presence of `node_modules`, sources, CFN
stacks) — no state file. A plain re-run does not re-bake unless `--force-bake`.

### Decisions taken on the flagged questions (recommended defaults)

- **CIDR**: auto-detect the public IP (like `scripts/set-ip`) when
  `--allowed-cidr` is absent, rather than hard-requiring it.
- **`remote.json` handoff**: register the generated file as an environment via
  `EnvConfigPath(--env)` (default `default`), per `add-remote-environments`, so
  the final `outfit remote deploy`/`start` just work and a second instance never
  clobbers the first.
- **`cdk bootstrap`**: rely on its idempotency (always run) rather than adding a
  CloudFormation `DescribeStacks` dependency to detect it.
- **Re-run bake**: skip unless `--force-bake`, to avoid a second 40-min build.
- **STS**: use it directly to name the account (promotes an existing indirect
  dep; no new download).

## Risks / Trade-offs

- **Node/pnpm/cdk remain prerequisites** → preflight checks for them and for AWS
  credentials, failing early with actionable guidance before anything downloads.
- **Slow, capacity-gated bake and GPU quota** → bake is async by default with a
  clear hand-off; the quota is surfaced as a prominent warning (can't auto-raise).
- **Accidental cost** → the consent gate and `--dry-run` make the resources and
  cost explicit before any deploy; nothing runs on a non-`yes` answer.
- **Downloaded sources aren't checksummed by us** → tagged refs over GitHub TLS
  are trusted, but there is no signature verification; documented, not mitigated
  further in this change.
- **Generated `remote.json` lives in `<cdkDir>` but the CLI reads the registry**
  → bootstrap registers it at `EnvConfigPath(--env)` after `run deploy` so the
  other subcommands (and an Outfit's `REMOTE <env>`) find it.
- **Windows** → `pnpm bake` runs bash; bootstrap is macOS/Linux-first, matching
  `remote/`. Documented.

## Open Questions

- None blocking. The flagged decisions above are taken as recommended defaults
  and are cheap to revisit during implementation if the user disagrees.

## Future work

- **`outfit remote destroy` (the inverse of bootstrap).** A later change should
  add a teardown that runs `pnpm cdk destroy` on the deployed stacks (reusing the
  downloaded `cdk/<ref>` sources, re-fetching if pruned) behind the same consent
  gate — destroying is irreversible-ish and must be explicit. Two caveats to
  carry: the CDK retains the S3 weights bucket and the baked AMIs on destroy
  (`RemovalPolicy.RETAIN`), so teardown is not fully cost-free and should say so;
  and it should also deregister the environment (remove `remotes/<env>/`). This
  subsumes the environments change's `outfit remote rm` idea — `rm` removes only
  the local registry entry, `destroy` tears down the AWS infra *and* the entry.
  Out of scope here; noted so bootstrap and destroy stay a matched pair.
