## 1. New env Lambda (TypeScript)

- [ ] 1.1 Create `remote/lambda/env/index.ts`: handler calls `readEnvApiKey(env)` and `baseUrlFor(eip, port)`, returns JSON with `base_url` and `api_key`. Returns 503 if the instance is stopped (use `findManagedInstance` to check).
- [ ] 1.2 Add env Lambda to `remote/lib/llm-stack.ts`: `EnvFn` with Function URL (IAM auth), minimal IAM perms (`secretsmanager:GetSecretValue`, `ec2:DescribeAddresses`).
- [ ] 1.3 Add `EnvUrl` CDK output and include `env_url` in `OutfitRemoteConfig` JSON output.

## 2. Go remote package: new `Env()` function

- [ ] 2.1 Add `EnvURL string` field to `remote.Config` struct
- [ ] 2.2 Add `OUTFIT_REMOTE_ENV_URL` env override in `finishConfig`
- [ ] 2.3 Implement `func Env(ctx context.Context, cfg Config) (*Response, error)` — calls `EnvURL` via `call()`, returns error if instance is stopped
- [ ] 2.4 Add `remote_test.go` tests for `Env` (happy path, stopped instance error)

## 3. Shared export printing helper

- [ ] 3.1 Extract `printRemoteEnv(*remote.Response)` function in `cmd/outfit/remote.go` that prints `export OPENAI_BASE_URL=...` and `export OPENAI_API_KEY=...` to stdout
- [ ] 3.2 Update `cmdRemoteStart` to use `printRemoteEnv` (no behavioural change yet)

## 4. New `outfit remote env` subcommand

- [ ] 4.1 Add `env` case to `cmdRemote` dispatch switch
- [ ] 4.2 Implement `cmdRemoteEnv` function: resolves remote config, calls `remote.Env`, prints exports to stdout; prints error to stderr if instance is stopped
- [ ] 4.3 Update usage message in `cmdRemote` to include `env`
- [ ] 4.4 Add `env` to `remote` subcommands in `cmd/outfit/complete.go`

## 5. `outfit remote start` `-e/--env` flag

- [ ] 5.1 Add `-e`/`--env` bool flags to `cmdRemoteStart`
- [ ] 5.2 Gate `printRemoteEnv` call behind the flag — default (no flag) produces no export output
- [ ] 5.3 Add `--env`/`-e` to the `remote` command's flags in `complete.go`

## 6. Harness remote env injection

- [ ] 6.1 Add `remoteEnvVars(cfg remote.Config, baseEnv []string) ([]string, error)` in `cmd/outfit/main.go`: calls `remote.Env`, injects `OPENAI_BASE_URL` and `OPENAI_API_KEY` (skipping if already set)
- [ ] 6.2 Change `applyBeforeLaunch` to return the parsed `outfit.Selection` alongside the envDir (new `applyResult` struct)
- [ ] 6.3 Update `cmdHarness` to check `result.sel.Remote` after apply, resolve remote config, and call `remoteEnvVars` to build the child process env
- [ ] 6.4 Print "Fetching remote endpoint env vars..." to stderr when calling env Lambda from `harness`

## 7. Tests

- [ ] 7.1 `TestRemoteEnv_PrintsExports` — new `env` subcommand prints exports via httptest server
- [ ] 7.2 `TestRemoteEnv_FailsWhenStopped` — env subcommand fails with helpful message when instance is stopped
- [ ] 7.3 `TestRemoteStart_NoEnvByDefault` — start without flag produces no stdout exports
- [ ] 7.4 `TestRemoteStart_EnvFlagPrintsExports` — start with `-e` prints exports
- [ ] 7.5 `TestHarness_RemoteEnvInjected` — harness with remote Outfit injects env vars
- [ ] 7.6 `TestHarness_RemoteEnvDoesNotOverrideExisting` — existing env vars are preserved
- [ ] 7.7 `TestHarness_NoRemoteUnaffected` — harness without REMOTE works as before
- [ ] 7.8 `TestCompletion_RemoteSubcommands` — verify `env` is completable

## 8. Verify

- [ ] 8.1 Run `go test ./... -cover` — all tests pass, coverage >= 80%
- [ ] 8.2 Run `go vet ./...` and `gofmt -w ./...`
