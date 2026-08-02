## 1. Source download helper (`internal/remote/source.go`)

- [x] 1.1 Add `ResolveRef(version, override string) string`: `--ref` wins; a clean tag verbatim; `dev`/`-dirty`/`-g<sha>` fall back to `main`.
- [x] 1.2 Add `SourceDir(ref string) string` reusing internal/remote's `configHome()` → `<configHome>/cdk/<ref>` (named `cdk/` to avoid collision with the `remotes/` registry); add `SourceRoot() string` returning the `cdk` parent for pruning.
- [x] 1.3 Add `ExtractRemote(r io.Reader, destDir string) error`: gunzip+untar, keep only `remote/*` (strip the leading `outfit-<ref>/`), reject `..` traversal, skip `node_modules`/`cdk.out`/gitignored generated files.
- [x] 1.4 Add `DownloadRemote(ctx, ref, destDir string) error`: GET `codeload.github.com/lucinate-ai/outfit/tar.gz/<ref>`, stream into `ExtractRemote`; skip re-download when `<destDir>/package.json` exists.
- [x] 1.5 Add `PruneSources(root, keepRef string) error`: after a successful bootstrap, remove every `<root>/<ref>` sibling except `keepRef`; no-op when `--dir` was given.
- [x] 1.6 Tests (`internal/remote/source_test.go`): `ResolveRef` table; in-memory gzip-tar extraction (only `remote/*`, README skipped, traversal rejected); `PruneSources` keeps `keepRef`.

## 2. Shared AWS helpers (`internal/remote/remote.go`)

- [x] 2.1 Extract `LoadAWSConfig(ctx, region string) (aws.Config, error)` from `sign()`'s credential path; have `sign()` call it (no behaviour change).
- [x] 2.2 Add `CallerIdentity(ctx, cfg)` (wraps `sts.GetCallerIdentity`) for the plan; promote the already-indirect `service/sts` to a direct require.
- [x] 2.3 Add `SharedStackDeployed(ctx, cfg, stackName string) (bool, error)` (wraps CloudFormation `DescribeStacks`) to detect whether the account is already bootstrapped; add the `aws-sdk-go-v2/service/cloudformation` client.

## 3. Bootstrap command skeleton (`cmd/outfit/remote_bootstrap.go`)

- [x] 3.1 Add `cmdRemoteBootstrap(args []string) error` with the FlagSet: `--runners` (comma-separated, default `llamacpp,vllm`), `--hf-token`, `--ref`, `--dir`, `--region`, `--dry-run`/`-n`, `--yes`/`-y`, `--wait`, `--force-bake`.
- [x] 3.2 Validate `--runners` against the accepted set (reuse `runnerFor` per entry). (No CIDR here — allowed ingress is a per-environment setting on `outfit remote deploy`.)
- [x] 3.3 Add `case "bootstrap": return cmdRemoteBootstrap(rest)` to `cmdRemote` and widen its usage string and unknown-subcommand error to include `bootstrap`.

## 4. Preflight checks

- [x] 4.1 Check `pnpm` and `node` on PATH via `exec.LookPath`; require Node major ≥ 22; fail early naming any missing prerequisite.
- [x] 4.2 Resolve AWS credentials via `LoadAWSConfig` + `Credentials.Retrieve`; fail (outside `--dry-run`) with the existing guidance.
- [x] 4.3 Resolve the account via `CallerIdentity`; degrade to "unknown" offline/`--dry-run`.
- [x] 4.4 Note when already bootstrapped via `SharedStackDeployed` (informational — the deploy is idempotent, not gated).
- [x] 4.5 Surface the GPU vCPU quota as a warning (no query, no auto-raise).

## 5. Shared settings written into the sources

- [x] 5.1 Upsert `HF_TOKEN` (when given) in `<cdkDir>/.env`, mode `0o600`, to populate the shared HfToken secret used by weight seeding.
- [x] 5.2 Set `context.runners` in `<cdkDir>/cdk.json` (additive JSON edit, no `-c`).

## 6. Consent gate

- [x] 6.1 Render the plan to stderr: account, region, runners, source ref/dir, the shared-resource bullets (Image Builder + AMIs; lifecycle Lambdas + IAM; shared bucket/roles/VPC), a qualitative cost caveat (point at the CDK cost docs, no figures), the quota caveat, and the exact command list (`pnpm run deploy`, not `pnpm deploy`).
- [x] 6.2 Gate logic: `--dry-run` prints and returns before the prompt (no download, no commands); `--yes` skips the prompt; otherwise read stdin and accept only `y`/`yes`, aborting cleanly on anything else.

## 7. Orchestration

- [x] 7.1 Add the `runStep` exec helper (behind a `stepRunner` seam): `exec.CommandContext`, `cmd.Dir = <cdkDir>`, streamed stdio, `signal.NotifyContext`; return an error naming the failed step (no `os.Exit`).
- [x] 7.2 Run the sequence: `pnpm install` (skip if `node_modules`) → `pnpm cdk bootstrap` → `pnpm deploy:image` → `pnpm bake <runner>` per selected runner (async, skip on re-run unless `--force-bake`) → deploy the shared stack (`pnpm run deploy`). No environment is created or registered.
- [x] 7.3 Print next steps: the account is bootstrapped; create an endpoint with `outfit remote deploy` (naming an environment), which discovers this shared layer.
- [x] 7.4 Implement `--wait`: after the shared deploy, poll the Image Builder pipeline until the AMI(s) are available before finishing; without it, print the async hand-off and exit 0.
- [x] 7.5 On success (default location only), call `PruneSources(SourceRoot(), ref)`; skip when `--dir` was given.

## 8. Help text and completion

- [x] 8.1 Update `usage()` and the package doc comment in `cmd/outfit/main.go` to list `bootstrap` and describe it as the once-per-account shared setup with a consent gate / `--dry-run` / `--yes`.
- [x] 8.2 Add `bootstrap` and its flags to the `remote` entry in `cmd/outfit/complete.go`; confirm `TestCompletionCoversDispatch` passes.

## 9. Tests (hermetic — no AWS, no network)

- [x] 9.1 Add `stepRunner`, downloader, caller-identity, and shared-stack-detection seams as package vars so tests inject recorders/stubs.
- [x] 9.2 `.env` upsert + `cdk.json` runners write test (append `HF_TOKEN` when given, `context.runners` set, mode `0o600`).
- [x] 9.3 Consent-plan output test via captured stderr: assert account/region/shared-resources, that a cost caveat is present (no specific figures), the command list, and `pnpm run deploy` (not `pnpm deploy`).
- [x] 9.4 `--dry-run` records zero commands; confirmation `n` aborts with nothing run; `y`/`--yes` records the commands in order with `cmd.Dir == <cdkDir>`.
- [x] 9.5 Preflight failures: empty `PATH` → error names pnpm/node and does not download; bad `--runners` entry → error, no download.

## 10. Docs and verification

- [x] 10.1 Add the bootstrap flow (once-per-account shared setup) to `docs/commands/remote.md` and `remote/README.md`, keeping the manual sequence as under-the-hood detail; note that environments come from `outfit remote deploy`.
- [x] 10.2 Run `go test ./... -cover` (keep ≥ 80%), `gofmt`, and `outfit remote bootstrap --dry-run` to confirm the plan renders and nothing runs.
- [ ] 10.3 End-to-end on a throwaway AWS account: `outfit remote bootstrap` → confirm → shared stack deployed, its outputs present, no EIP/instance created.
