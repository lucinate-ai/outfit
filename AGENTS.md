# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`outfit` is a Go CLI that configures the current user's global [opencode](https://opencode.ai) installation to use a model provider, by deep-merging provider settings into the opencode config under `${XDG_CONFIG_HOME:-$HOME/.config}/opencode`.

## Commands

```sh
go test ./...                  # run the suite
go test ./... -cover           # with coverage (keep total >= 80%)
go vet ./...                   # vet
go build -o outfit .        # build the CLI binary
gofmt -w *.go                  # format
```

Run a single test: `go test -run TestWriteConfig_Idempotent ./...`

## Layout

- `main.go` — CLI: command dispatch (`add`/`remove`/`list`/`apply`/`export`), flag parsing (`parseSelection` registers both long and short flags against the same vars), and user-facing output. `add` and `apply` share `applySelection`, the core that writes one provider selection.
- `outfit.go` — the `Outfit` file format: a flat, Dockerfile-style description of one provider selection (`PROVIDER`/`FAMILY`/`MODEL`/`CONTEXT`/`BASEURL`, the last two mapping to `--context`/`--base-url`). `parseOutfit` reads it into a `selection` (keywords case-insensitive via `canonicalKeyword`, UPPERCASE canonical, `#` comments); `formatOutfit` renders one back out for `export`. `apply` defaults to `./Outfit` (`DefaultOutfitFile`).
- `catalog.go` — the embedded provider catalogue (`//go:embed providers.yaml`), its types, and `buildProviderBlock`, which turns a provider+family+model selection into an opencode provider block. `matchFamily` does the reverse for `export` (configured models -> family name). The catalogue can be overridden at runtime via the `--providers` flag or `OUTFIT_PROVIDERS` env var (flag > env > embedded; see `resolveCatalogPath`/`loadCatalogFrom`).
- `config.go` — opencode config IO: JSONC read/merge/write, env/key resolution, JSON-Pointer helpers. `loadConfigState` reads the config back (configured providers, their model keys, the default model) for `export`.
- `providers.yaml` — externalised provider/model-family data (URLs, model ids, key env vars). **Add providers/models here, not in Go.** Embedded at build time but kept external for maintenance.
- `examples/` — runnable guides, each a directory with a README and an `Outfit`.
- `*_test.go` — `catalog_test.go` (catalogue integrity + `buildProviderBlock`), `config_test.go` (merge/remove/IO), `main_test.go` (CLI layer), `outfit_test.go` (Outfit parse/format + `apply`/`export`).

## Architecture notes (the important part)

**In-place JSONC merge, never overwrite.** This is the core invariant. Understand it before touching `config.go`:

- Targets the existing `opencode.json` or `opencode.jsonc` (preferring whichever exists), falling back to creating `opencode.json`.
- Parses the config as **JSONC** via `github.com/tailscale/hujson` (tolerates comments and trailing commas).
- Edits are applied as an **RFC 6902 JSON Patch** on the hujson AST, which preserves comments and formatting *outside* the managed provider block. Parent objects (`/provider`) are only created with an `add` op when absent, so sibling providers are never clobbered. Path segments are escaped with `jsonPointerEscape` (model ids contain `/`).
- The `openrouter`-style block is deep-merged (`deepMerge`) over any existing one so user extras survive.
- `remove` guards every `remove` op with `Find` (RFC 6902 errors on missing paths) and clears the top-level default `model` when it pointed at something removed.
- Output is written `0600` (and `chmod`ed, since `WriteFile` won't change perms on an existing file) because it may hold an API key.

**Key/option resolution.** `buildProviderBlock` injects `options.apiKey` from the provider's `apiKeyEnv` (resolved from `.env` next to the tool, then the environment), validating `apiKeyPrefix` and erroring when `apiKeyRequired`. `optionsFromEnv` injects other options (e.g. `baseURL`, `region`) when their env vars are set. The opencode top-level `model` is `<providerId>/<modelKey>`.

Invariants to keep: merges stay idempotent, never drop unrelated config or comments, output stays `0600`, and every family's `defaultModel` must be one of its `models` (enforced by `TestCatalogIntegrity`).

## Reference

The opencode config schema is documented at https://opencode.ai/docs/config/. The catalogue follows it: `amazon-bedrock` is the Bedrock provider id, custom providers (`ollama`, `llamacpp`, `openai-compatible`) carry an `npm` package plus `options.baseURL`. Note opencode also supports `{env:VAR}` substitution in the config, but this tool deliberately embeds the resolved key instead (a global config can't rely on a project-local `.env`).
