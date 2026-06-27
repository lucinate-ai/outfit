# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`outfit` is a Go CLI that configures the current user's global [opencode](https://opencode.ai) installation to use a model provider, by deep-merging provider settings into the opencode config under `${XDG_CONFIG_HOME:-$HOME/.config}/opencode`.

## Commands

```sh
go test ./...                  # run the suite
go test ./... -cover           # with coverage (keep total >= 80%)
go vet ./...                   # vet
go build -o outfit ./cmd/outfit   # build the CLI binary
gofmt -w ./...                 # format
```

Run a single test: `go test -run TestWriteConfig_Idempotent ./...`

## Layout

The binary lives under `cmd/`; the domain logic is split into `internal/` packages so each concern is isolated and independently testable.

- `cmd/outfit/main.go` — CLI: command dispatch (`add`/`remove`/`list`/`apply`/`unapply`/`export`), flag parsing (`parseSelection` registers both long and short flags against the same vars), and user-facing output. `add` and `apply` share `applySelection`, the core that writes one provider selection; `remove` and `unapply` share `removeSelection`, its inverse. Tests: `main_test.go` (dispatch/add/remove/list), `apply_test.go` (`apply`/`unapply`/`export` end-to-end).
- `internal/outfit` — the `Outfit` file format and the shared `Selection` type: a flat, Dockerfile-style description of one provider selection (`PROVIDER`/`FAMILY`/`MODEL`/`CONTEXT`/`BASEURL`, the last two mapping to `--context`/`--base-url`). `Parse` reads it into a `Selection` (keywords case-insensitive via `canonicalKeyword`, UPPERCASE canonical, `#` comments); `Format` renders one back out for `export`. `apply` defaults to `./Outfit` (`outfit.DefaultFile`).
- `internal/catalog` — the embedded provider catalogue (`//go:embed providers.yaml`), its types, and `BuildProviderBlock`, which turns a provider+family+model selection into an opencode provider block. `MatchFamily` does the reverse for `export` (configured models -> family name). The catalogue can be overridden at runtime via the `--providers` flag or `OUTFIT_PROVIDERS` env var (flag > env > embedded; see `ResolveCatalogPath`/`LoadFrom`). `providers.yaml` lives here.
- `internal/opencode` — opencode config IO: JSONC read/merge/write, env/key resolution, JSON-Pointer helpers. `LoadConfigState` reads the config back (configured providers, their model keys, the default model) for `export`.
- `internal/contextsize` — parses human-friendly context window sizes (`128k`, `1.5m`, `200000`) and applies `limit.context` to model blocks.
- `internal/catalog/providers.yaml` — externalised provider/model-family data (URLs, model ids, key env vars). **Add providers/models here, not in Go.** Embedded at build time but kept external for maintenance.
- `examples/` — runnable guides, each a directory with a README and an `Outfit`.

## Architecture notes (the important part)

**In-place JSONC merge, never overwrite.** This is the core invariant. Understand it before touching `internal/opencode`:

- Targets the existing `opencode.json` or `opencode.jsonc` (preferring whichever exists), falling back to creating `opencode.json`.
- Parses the config as **JSONC** via `github.com/tailscale/hujson` (tolerates comments and trailing commas).
- Edits are applied as an **RFC 6902 JSON Patch** on the hujson AST, which preserves comments and formatting *outside* the managed provider block. Parent objects (`/provider`) are only created with an `add` op when absent, so sibling providers are never clobbered. Path segments are escaped with `jsonPointerEscape` (model ids contain `/`).
- The `openrouter`-style block is deep-merged (`deepMerge`) over any existing one so user extras survive.
- `remove` guards every `remove` op with `Find` (RFC 6902 errors on missing paths) and clears the top-level default `model` when it pointed at something removed.
- Output is written `0600` (and `chmod`ed, since `WriteFile` won't change perms on an existing file) because it may hold an API key.

**Key/option resolution.** `catalog.BuildProviderBlock` injects `options.apiKey` from the provider's `apiKeyEnv` (resolved from `.env` next to the tool, then the environment), validating `apiKeyPrefix` and erroring when `apiKeyRequired`. `optionsFromEnv` injects other options (e.g. `baseURL`, `region`) when their env vars are set. The opencode top-level `model` is `<providerId>/<modelKey>`.

Invariants to keep: merges stay idempotent, never drop unrelated config or comments, output stays `0600`, and every family's `defaultModel` must be one of its `models` (enforced by `TestCatalogIntegrity`).

## Reference

The opencode config schema is documented at https://opencode.ai/docs/config/. The catalogue follows it: `amazon-bedrock` is the Bedrock provider id, custom providers (`ollama`, `llamacpp`, `openai-compatible`) carry an `npm` package plus `options.baseURL`. Note opencode also supports `{env:VAR}` substitution in the config, but this tool deliberately embeds the resolved key instead (a global config can't rely on a project-local `.env`).
