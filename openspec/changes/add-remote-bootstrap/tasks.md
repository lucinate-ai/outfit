## 1. Source download helper (`internal/remote/source.go`)

- [ ] 1.1 Add `ResolveRef(version, override string) string`: `--ref` wins; a clean tag is used verbatim; `dev`/`-dirty`/`-g<sha>` describe-suffixes fall back to `main`.
- [ ] 1.2 Add `SourceDir(ref string) string` mirroring `ConfigPath()`'s XDG base → `$XDG_CONFIG_HOME/outfit/cdk/<ref>` (default `~/.config/outfit/cdk/<ref>`; named `cdk/` to avoid collision with the `remotes/` environment registry); add `SourceRoot() string` returning the `cdk` parent for pruning.
- [ ] 1.3 Add `ExtractRemote(r io.Reader, destDir string) error`: gunzip+untar, keep only `remote/*` (strip the leading `outfit-<ref>/` segment), reject `..` path traversal, skip `node_modules`/`cdk.out`/gitignored generated files. Split extraction from download so it takes an `io.Reader` (hermetic tests).
- [ ] 1.4 Add `DownloadRemote(ctx, ref, destDir string) error`: GET `codeload.github.com/lucinate-ai/outfit/tar.gz/<ref>`, stream into `ExtractRemote`; skip re-download when `<destDir>/package.json` exists (the ref is encoded in `destDir`, so its presence means same ref).
- [ ] 1.5 Add `PruneSources(root, keepRef string) error`: after a successful bootstrap, remove every `<root>/<ref>` sibling except `keepRef`; a no-op when `--dir` was given (the user owns that path — no namespacing, no pruning).
- [ ] 1.6 Add `internal/remote/source_test.go`: table-driven `ResolveRef`; in-memory gzip-tar extraction test (only `remote/*` lands, README skipped, traversal rejected); `PruneSources` removes stale ref dirs and keeps `keepRef`.

## 2. Shared AWS-config helper (`internal/remote/remote.go`)

- [ ] 2.1 Extract `LoadAWSConfig(ctx, region string) (aws.Config, error)` from the existing `sign()` credential-load path; have `sign()` call it (no behaviour change).
- [ ] 2.2 Add a `CallerIdentity(ctx, cfg)` helper wrapping `sts.GetCallerIdentity` to return the account id; promote the already-indirect `service/sts` to a direct require (`go mod tidy`).

## 3. Bootstrap command skeleton (`cmd/outfit/remote_bootstrap.go`)

- [ ] 3.1 Add `cmdRemoteBootstrap(args []string) error` with the FlagSet: `--env` (default `default`; the environment name to register), `--allowed-cidr`, `--runner` (default `llamacpp`), `--hf-token`, `--ref`, `--dir`, `--region`, `--dry-run`/`-n`, `--yes`/`-y`, `--wait`, `--force-bake`.
- [ ] 3.2 Validate `--runner` early via the existing `runnerFor`; auto-detect the public IP (GET `https://checkip.amazonaws.com`, append `/32`) when `--allowed-cidr` is empty; validate the CIDR against the CDK's IPv4-CIDR regex.
- [ ] 3.3 Add the `case "bootstrap": return cmdRemoteBootstrap(rest)` to `cmdRemote` (`cmd/outfit/remote.go`) and widen its usage string and unknown-subcommand error to include `bootstrap`.

## 4. Preflight checks

- [ ] 4.1 Check `pnpm` and `node` on PATH via `exec.LookPath`; parse `node --version` and require major ≥ 22; fail early naming any missing prerequisite.
- [ ] 4.2 Resolve AWS credentials via `LoadAWSConfig` + `Credentials.Retrieve`; on failure outside `--dry-run`, fail with the existing "configure env/profile/SSO" guidance.
- [ ] 4.3 Resolve the account via `CallerIdentity` for the plan; degrade to "unknown" when offline/`--dry-run`.
- [ ] 4.4 Surface the GPU vCPU quota as a warning (no query, no auto-raise).

## 5. Settings written into the downloaded sources

- [ ] 5.1 Upsert `ALLOWED_CIDR` (and `HF_TOKEN` when given) in `<remoteDir>/.env`, writing mode `0o600`.
- [ ] 5.2 Set `context.runner` in `<remoteDir>/cdk.json` as an additive JSON edit (no `-c`).

## 6. Consent gate

- [ ] 6.1 Render the provisioning plan to stderr: account, region, runner, CIDR, the target environment name and its `remotes/<env>/remote.json` write path, source ref/dir, the resource bullets, a qualitative cost caveat (an ongoing at-rest cost plus a per-hour GPU cost while running; point at the CDK cost docs rather than embedding figures), the quota caveat, and the exact command list (`pnpm run deploy`, not `pnpm deploy`).
- [ ] 6.2 Gate logic: `--dry-run` prints the plan and returns before the prompt (no download, no commands); `--yes` skips the prompt; otherwise read stdin and accept only `y`/`yes`, aborting cleanly on anything else.

## 7. Orchestration

- [ ] 7.1 Add the `runStep` exec helper (behind a `stepRunner` seam) using `exec.CommandContext`, `cmd.Dir = <remoteDir>`, streamed stdio, `signal.NotifyContext` for Ctrl-C; return an error naming the failed step (no `os.Exit`).
- [ ] 7.2 Run the sequence in order: `pnpm install` (skip if `node_modules` present) → `pnpm cdk bootstrap` → `pnpm deploy:image` → `pnpm bake <runner>` (async, skip on re-run unless `--force-bake`) → `pnpm run deploy`.
- [ ] 7.3 After `run deploy`, register `<cdkDir>/remote.json` (now carrying `base_url` + control URLs) into the environment registry at `remote.EnvConfigPath(--env)` (from `add-remote-environments`), creating `~/.config/outfit/remotes/<env>/`; print the next steps (apply an Outfit with `REMOTE <env>`; wait for the bake, then `outfit remote deploy`/`start`). Nothing generates the `Outfit`.
- [ ] 7.4 Implement `--wait`: after `run deploy`, poll the Image Builder pipeline until the AMI is available before finishing; without it, print the async hand-off and exit 0.
- [ ] 7.5 On success (default location only), call `PruneSources(SourceRoot(), ref)` so stale-version source dirs don't accumulate; skip when `--dir` was given.

## 8. Help text and completion

- [ ] 8.1 Update `usage()` and the package doc comment in `cmd/outfit/main.go` to list `bootstrap` and describe the consent gate / `--dry-run` / `--yes`.
- [ ] 8.2 Add `bootstrap` and its flags to the `remote` entry in `cmd/outfit/complete.go` (optionally a `kindRunner` for `--runner`); confirm `TestCompletionCoversDispatch` passes (embedded shell scripts need no edits).

## 9. Tests (hermetic — no AWS, no network)

- [ ] 9.1 Add `stepRunner`, downloader, and caller-identity seams as package vars so tests inject recorders/stubs.
- [ ] 9.2 `.env` upsert + `cdk.json` runner write test (replace existing CIDR, append token, `context.runner` set, mode `0o600`).
- [ ] 9.3 Consent-plan output test via captured stderr: assert account/region/resources, that a cost caveat is present (without asserting specific figures), the command list, and `pnpm run deploy` (not `pnpm deploy`).
- [ ] 9.4 `--dry-run` records zero commands; confirmation `n` aborts with nothing run; `y`/`--yes` records the five commands in order with `cmd.Dir == <remoteDir>` and `bake <runner>`.
- [ ] 9.5 Preflight failures: empty `PATH` → error names pnpm/node and does not download; `--runner=bogus` → error, no download.

## 10. Docs and verification

- [ ] 10.1 Add the bootstrap flow to `docs/commands/remote.md` and `remote/README.md`, keeping the manual sequence documented as the under-the-hood detail.
- [ ] 10.2 Run `go test ./... -cover` (keep ≥ 80%), `gofmt`, and `outfit remote bootstrap --dry-run` to confirm the plan renders and nothing runs.
- [ ] 10.3 End-to-end on a throwaway AWS account: `outfit remote bootstrap --env test` → confirm → `~/.config/outfit/remotes/test/remote.json` registered, `outfit remote ls` shows it → `outfit remote start` boots the endpoint.
