## Context

The remote endpoint's API key is stored in AWS Secrets Manager, not in the local `remote.json`. The start Lambda returns `base_url` and `api_key` in its response, but calling it also boots the instance — a potentially minutes-long operation. The `Status` Lambda returns `base_url` but not `api_key`. There is no lightweight way to just "get the env vars" without also triggering a start.

Currently, `outfit remote start` prints export lines to stdout always — there's no way to suppress them. And `outfit harness` has no concept of a remote endpoint — it only knows about the Outfit file's instructions and the local environment.

## Goals / Non-Goals

**Goals:**
- Provide a dedicated `outfit remote env` command that returns the remote endpoint's env vars, suitable for `eval "$(outfit remote env)"`
- The env command must NOT start the endpoint — it should fail if the instance is stopped (the user should run `outfit remote start` first)
- Make the export output on `outfit remote start` opt-in via `-e/--env`, so the default behaviour is clean progress-only output
- Automatically inject the remote endpoint's env vars when `outfit harness` launches with a remote Outfit, so the user doesn't need to manually eval anything
- Keep the API key off disk — it flows through the Lambda response → process env → child harness env

**Non-Goals:**
- Persisting the API key anywhere on disk (it's not needed; the Lambda returns it on every call)
- Adding remote awareness to `outfit apply` (that command only writes config; it doesn't launch anything)
- Supporting non-OpenAI-compatible remote providers (remote deploy only supports `llamacpp` and `vllm`, both using `OPENAI_API_KEY`)
- Starting a stopped endpoint from `remote env` (that is `remote start`'s job)

## Decisions

**Decision 1: New "env" Lambda, separate from Start**

A new Lambda (`remote/lambda/env/index.ts`) that only returns env vars without starting the instance. It reuses `readEnvApiKey()` and `baseUrlFor()` from `remote/lambda/shared/environments.ts` — the same functions the start Lambda uses. If the instance is stopped, it returns a 503 with a message telling the user to run `outfit remote start` first. This keeps the env call fast (just Secrets Manager + EC2 DescribeAddress) and separates concerns: start boots, env reads.

The new Lambda needs minimal IAM permissions: `secretsmanager:GetSecretValue` (already granted to start) and `ec2:DescribeAddresses` (to find the EIP). No EC2 run/terminate, no SSM access.

**Decision 2: `harness` calls `remote.Env()` directly, not via subprocess**

The `harness` command already imports `internal/remote` (for `remoteBaseURL`). It can call `remote.Env()` directly to fetch the response and extract `BaseURL` and `APIKey`, rather than spawning a subprocess. This avoids the overhead of a second process and the fragility of parsing its stdout.

**Decision 3: `start` default changes to no-export**

Currently `start` always prints export lines. This is the only visible behaviour change: users who rely on `eval "$(outfit remote start)"` will need to add `-e`. The `env` command exists as the dedicated path for this use case. This is **BREAKING** for scripts, but the migration is straightforward and the new behaviour is cleaner.

**Decision 4: Remote env vars are hardcoded to `OPENAI_BASE_URL` / `OPENAI_API_KEY`**

Both `llamacpp` and `vllm` (the only providers that can be remote-deployed) use `OPENAI_API_KEY` as their `apiKeyEnv`. The base URL is injected as `OPENAI_BASE_URL` (the `optionsFromEnv` variable for the `openai-compatible` provider). This matches the catalogue and means the harness's existing env-var resolution (`{env:OPENAI_API_KEY}` in opencode, `$OPENAI_API_KEY` in Pi) works without any config changes.

**Decision 5: `harness` only injects env vars when the Outfit has `REMOTE`**

The `REMOTE` instruction is the signal that this Outfit targets a remote endpoint. Without it, the harness behaves as before. The `sel.Remote` field is available from the parsed Outfit selection, which `applyBeforeLaunch` already reads.

**Decision 6: Env Lambda uses a separate Function URL**

The CDK stack adds the env Lambda with its own Function URL (`env_url`), following the same pattern as `start_url`, `stop_url`, and `deploy_url`. The Go `Config` struct gets an `EnvURL` field. The stack output JSON (`OutfitRemoteConfig`) includes `env_url` alongside the others. Existing configs without `env_url` still work for start/stop/deploy.

## Risks / Trade-offs

[Risk] `harness` fails if the endpoint is stopped (env Lambda returns 503). → **Mitigation**: The error message tells the user to run `outfit remote start` first. This is the desired behaviour — the user should explicitly decide when to boot a cold GPU instance.

[Risk] AWS credentials aren't available in the harness launch environment. → **Mitigation**: `harness` runs in the user's shell, which already has AWS credentials for `outfit remote start/stop` to work. If credentials fail, the error is surfaced immediately before any harness launch.

[Risk] The `start --no-export` default is a breaking change for existing scripts. → **Mitigation**: The fix is one flag (`-e`), and `remote env` is the preferred path going forward. The usage message in `cmdRemote` documents the change.

[Risk] New Lambda increases deployment cost/surface area. → **Mitigation**: The Lambda is minimal (~50 lines, no dependencies beyond shared modules), and runs infrequently (only when `outfit remote env` or `outfit harness` is called). Cost is negligible at this scale.

## Open Questions

- Should `harness` warn the user when it detects a REMOTE Outfit and calls the env Lambda? (Yes — a single stderr line like "Fetching remote endpoint env vars..." keeps the user informed.)
