## Why

When using a remote endpoint, the API key is only available dynamically — it's stored in AWS Secrets Manager, not in the local `remote.json`. Currently, `outfit remote start` prints `export OPENAI_BASE_URL=...` and `export OPENAI_API_KEY=...` to stdout for `eval`, but this is fragile: users must remember to eval it, and `outfit harness` can't use it automatically.

This change makes the environment variables first-class: a dedicated `outfit remote env` command that calls a new Lambda to fetch env vars from an already-running endpoint, an opt-in flag on `start`, and automatic injection when launching a harness against a remote Outfit.

## What Changes

- **New `outfit remote env` subcommand** — calls a new "env" Lambda that reads the API key from Secrets Manager and returns it with the base URL. Does NOT start the endpoint — it assumes the user has already run `outfit remote start`. Prints shell export lines to stdout.
- **`outfit remote start -e/--env` flag** — opt-in to print export lines after start. Default behaviour (no flag) is to print only progress/status to stderr.
- **`outfit harness` auto-injects remote env vars** — when the applied Outfit has a `REMOTE` instruction, the harness command calls the new env Lambda to fetch `base_url` and `api_key`, then injects `OPENAI_BASE_URL` and `OPENAI_API_KEY` into the launched harness process environment. The harness already resolves `{env:VAR}` / `$VAR` in its config, so the key flows through without being written to disk.

## Capabilities

### New Capabilities

- `remote-env`: The `outfit remote env` command and the `-e/--env` flag on `outfit remote start`, providing a machine-readable way to obtain the remote endpoint's environment variables.
- `harness-remote-env`: Automatic injection of remote endpoint environment variables when `outfit harness` launches with a remote Outfit.

### Modified Capabilities

<!-- None — no existing spec files. -->

## Impact

- `remote/lambda/env/index.ts` — new Lambda that returns `base_url` and `api_key` for a running environment (reads key from Secrets Manager, finds EIP for base URL)
- `remote/lib/llm-stack.ts` — CDK wiring for new env Lambda with Function URL
- `internal/remote/remote.go` — new `Env()` function and `EnvURL` field on `Config`
- `cmd/outfit/remote.go` — new `env` subcommand, refactored export printing, `-e/--env` flag on `start`
- `cmd/outfit/main.go` — `harness` command detects `REMOTE` on the applied Outfit and injects env vars before launching
- `cmd/outfit/complete.go` — completion table updated for new subcommand and flags
- `cmd/outfit/remote_test.go` — tests for new behaviour
