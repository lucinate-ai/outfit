## 1. Shared-layer discovery and config (`internal/remote`)

- [ ] 1.1 Add a `SharedLayer` struct and `DiscoverSharedLayer(ctx, cfg, stackName string) (SharedLayer, error)` reading the bootstrap stack's CloudFormation outputs (lifecycle Lambda URLs, weights bucket, shared roles, region) via `DescribeStacks`; return a helpful "run `outfit remote bootstrap` first" error when the stack is absent. Reuse the `service/cloudformation` client the bootstrap change adds.
- [ ] 1.2 Add an `Environment string` field (`json:"environment"`) to `Config`, and send it with each `start`/`stop`/`status`/`deploy` request so the shared Lambdas select the right instance.
- [ ] 1.3 Tests: discovery parses outputs and errors on an absent stack (behind a seam, no AWS); `Config` round-trips `environment`.

## 2. Deploy creates an environment (`cmd/outfit/remote.go`)

- [ ] 2.1 `cmdRemoteDeploy` takes a required environment name (positional) plus flags: `--allowed-cidr`, `--overwrite`, and the existing `--dry-run`.
- [ ] 2.2 Discover the shared layer via `DiscoverSharedLayer`; on absence, fail telling the user to bootstrap first.
- [ ] 2.3 Overwrite detection: the environment is already registered (`remote.EnvConfigPath(env)` exists) or its instance is live (a `status` call via the shared Lambda). If so, warn and require `--overwrite` — not implied by `--yes`; a fresh environment needs neither.
- [ ] 2.4 Auto-detect the allowed CIDR (GET `https://checkip.amazonaws.com`, `/32`) when `--allowed-cidr` is empty; validate it.
- [ ] 2.5 Derive what to serve from the Outfit + preset with the existing `deployConfigFor` logic, and send it to the shared deploy Lambda with the environment name and CIDR; receive the environment's base URL (EIP) and identifier.
- [ ] 2.6 Register the environment via `remote.SaveEnvironment(env, …)` (owner-only): the shared Lambda URLs, region, base URL, and the `environment` identifier.
- [ ] 2.7 Print the report: environment created and registered; whether weights are still being fetched; not started (use `outfit remote start`).

## 3. Environment-aware Lambdas and per-env resources (`remote/`, TypeScript CDK)

- [x] 3.1 Split the per-environment resources out of the shared stack; the shared **deploy** Lambda provisions, keyed by environment name, an Elastic IP, an API-key secret, an allowed-CIDR security-group rule, and SSM deploy-config/idle-state, returning the base URL and identifier.
- [x] 3.2 Make the **start**/**stop**/**status** Lambdas take an environment identifier: launch that environment's instance, associate its EIP, and act only on it (by tag + per-env SSM state).
- [x] 3.3 Make the scheduled idle sweep iterate every environment's instance, judging and terminating each on its own activity.
- [x] 3.4 Have the shared stack publish the discovery outputs (Lambda URLs, bucket, roles, region) under the well-known stack name.
- [x] 3.5 Update the CDK/Lambda vitest tests for per-environment behaviour (per-env SSM keys, env-scoped start/stop, the multi-environment idle sweep).

## 4. Help, completion, docs

- [ ] 4.1 Update `deploy` usage/help (`cmd/outfit/main.go`) to show the environment argument and `--allowed-cidr`/`--overwrite`.
- [ ] 4.2 Completion (`cmd/outfit/complete.go`): `deploy`'s positional is an environment name; add its flags.
- [ ] 4.3 Docs: `docs/commands/remote.md` and `remote/README.md` — the `bootstrap` → `deploy <env>` → `start` flow, per-environment CIDR, and the overwrite guard.

## 5. Tests (Go, hermetic — no AWS, no network)

- [ ] 5.1 Not-bootstrapped: `deploy` with the discovery seam returning absent → fails telling the user to bootstrap, creates nothing.
- [ ] 5.2 Overwrite guard: an already-registered env (or the live-instance seam true) aborts without `--overwrite` (even under `--yes`) and creates nothing; `--overwrite` proceeds; a fresh env proceeds.
- [ ] 5.3 Registration: a successful deploy writes `remotes/<env>/remote.json` owner-only with the base URL and `environment` identifier, and `outfit remote ls` shows it.
- [ ] 5.4 CIDR: default is the detected `/32`; an invalid `--allowed-cidr` is rejected.

## 6. Verification

- [ ] 6.1 `go test ./... -cover` (keep ≥ 80%), `gofmt`; `pnpm test` for the CDK/Lambda changes in `remote/`.
- [ ] 6.2 End-to-end on a throwaway account: `outfit remote bootstrap` → `outfit remote deploy <env>` → `outfit remote ls` shows it → `outfit remote start` boots that environment's instance at its EIP.
