# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`outfit` is a Go CLI that configures a coding agent — a **harness** — to use a model provider, by deep-merging provider settings into that harness's config. Two harnesses are supported: [opencode](https://opencode.ai) (config under `${XDG_CONFIG_HOME:-$HOME/.config}/opencode`) and [Pi](https://github.com/earendil-works/pi) (`~/.pi/agent/models.json`). The harness is chosen at runtime — never baked into an Outfit file — so the same selection applies to either.

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

- `cmd/outfit/main.go` — CLI: command dispatch (`add`/`remove`/`list`/`show`/`apply`/`unapply`/`serve`/`export`/`harness`/`init-providers`), flag parsing (`parseSelection` registers both long and short flags against the same vars, and returns the chosen harness name separately so it never leaks into a `Selection`), and user-facing output. `add` and `apply` share `applySelection`, the core that writes one provider selection; `remove` and `unapply` share `removeSelection`, its inverse. Both resolve the active harness (`harness.Resolve`) and route through it. `show` reports the active harness's current config via `harness.State` (the same read `export` reconstructs an Outfit from). `serve` is harness-agnostic (it launches `llama-server`, not a config write). `harness` launches the active harness (forwarding trailing args), or manages the stored default with `--get`/`--set`. Tests: `main_test.go` (dispatch/add/remove/list), `apply_test.go` (`apply`/`unapply`/`export`), `harness_test.go` (the `harness` command, `show`, and Pi-routed add/export/remove), `serve_test.go`.
- `internal/outfit` — the `Outfit` file format and the shared `Selection` type: a flat, Dockerfile-style description of one provider selection (`PROVIDER`/`FAMILY`/`MODEL`/`ALIAS`/`CONTEXT`/`OUTPUT`/`BASEURL`/`PRESET`). `Parse` reads it into a `Selection` (keywords case-insensitive via `canonicalKeyword`, UPPERCASE canonical, `#` comments); `Format` renders one back out for `export`. `apply` defaults to `./Outfit` (`outfit.DefaultFile`). The harness is deliberately **not** a keyword — it is a runtime choice, so an Outfit stays portable.
- `internal/harness` — the harness abstraction. The `Harness` interface (`Name`/`Command`/`ConfigPath`/`Apply`/`Remove`/`State`), a `registry` of the opencode and pi adapters (`adapters.go`, which wrap `catalog` + `opencode`/`pi` + `contextsize`), runtime selection via `Resolve` with precedence **`--harness`/`-H` flag > `OUTFIT_HARNESS` env > stored preference > opencode**, and preference load/save (`LoadPreference`/`SavePreference`) in `${XDG_CONFIG_HOME:-~/.config}/outfit/config.json`. **Start here when adding a third harness:** implement the interface and register it.
- `internal/catalog` — the embedded provider catalogue (`//go:embed providers.yaml`), its types, and the two block builders: `BuildProviderBlock` turns a provider+family+model selection into an opencode provider block; `BuildPiProvider` turns the same into a Pi provider entry. `MatchFamily` does the reverse for `export` (configured models -> family name). The catalogue can be overridden at runtime via the `--providers` flag or `OUTFIT_PROVIDERS` env var (flag > env > embedded; see `ResolveCatalogPath`/`LoadFrom`). `providers.yaml` lives here.
- `internal/opencode` — opencode config IO: JSONC read/merge/write, env/key resolution, JSON-Pointer helpers. `LoadConfigState` reads the config back (configured providers, their model keys, the default model, context/output limits) for `export`.
- `internal/pi` — Pi config IO: resolves `~/.pi/agent/models.json` (note: `~/.pi/agent/`, **not** XDG), deep-merges one managed provider entry into it preserving everything else (other providers, unknown fields), `Remove`, and reads provider state back for `export`. Written `0600`.
- `internal/contextsize` — parses human-friendly sizes (`128k`, `1.5m`, `200000`); `Apply` sets `limit.context` and `limit.output` on opencode model blocks; `DefaultOutput` returns a quarter of the context (the fallback when `OUTPUT` is unset).
- `internal/preset` — parses llama.cpp [preset `.ini`](https://github.com/ggml-org/llama.cpp/blob/master/docs/preset.md) files into named sections of flags, and renders a `llama-server` command for `serve` (`Parse`/`Flags`/`FormatCommand`).
- `internal/catalog/providers.yaml` — externalised provider/model-family data (URLs, model ids, key env vars, and a `pi:` block per Pi-capable provider). **Add providers/models here, not in Go.** Embedded at build time but kept external for maintenance.
- `examples/` — runnable guides, each a directory with a README and an `Outfit`.

## Architecture notes (the important part)

**Harness routing.** `cmd` does the harness-neutral work (load the catalogue, validate the family/model, parse sizes, pick the provider) and then hands a `Selection` to the harness resolved by `harness.Resolve`. Each adapter owns its config format end-to-end. The import graph is one-way: `harness` imports `catalog`/`opencode`/`pi`/`contextsize`/`outfit`; none of those import `harness`, so there are no cycles. A model is keyed by its `ALIAS` when given, otherwise the provider-native `MODEL` (`modelKey` in `adapters.go`).

**In-place JSONC merge, never overwrite (opencode).** This is the core invariant of the opencode adapter. Understand it before touching `internal/opencode`:

- Targets the existing `opencode.json` or `opencode.jsonc` (preferring whichever exists), falling back to creating `opencode.json`.
- Parses the config as **JSONC** via `github.com/tailscale/hujson` (tolerates comments and trailing commas).
- Edits are applied as an **RFC 6902 JSON Patch** on the hujson AST, which preserves comments and formatting *outside* the managed provider block. Parent objects (`/provider`) are only created with an `add` op when absent, so sibling providers are never clobbered. Path segments are escaped with `jsonPointerEscape` (model ids contain `/`).
- The `openrouter`-style block is deep-merged (`deepMerge`) over any existing one so user extras survive.
- `remove` guards every `remove` op with `Find` (RFC 6902 errors on missing paths) and clears the top-level default `model` when it pointed at something removed.
- Output is written `0600` (and `chmod`ed, since `WriteFile` won't change perms on an existing file) because it may hold an API key.

**Pi config (`internal/pi`).** Pi's `models.json` is plain JSON of the form `{"providers": {"<id>": {baseUrl, api, apiKey, models: [...]}}}`. The whole file is read into a generic map so unknown top-level keys, sibling providers, and unknown provider fields all round-trip untouched; only the managed provider is merged in (models unioned by `id`). `apiKey` is written as a `$ENV_VAR` interpolation (never the resolved secret), and **keyless local providers get a dummy literal placeholder** (`piPlaceholderAPIKey`) because Pi hides a provider's models from `/model` until *some* auth is configured. Pi has no top-level default-model setting, so `export` relies on the provider selection alone and `add` tells the user which model to pick.

**Key/option resolution.** `catalog.BuildProviderBlock` injects `options.apiKey` (opencode) from the provider's `apiKeyEnv` (resolved from `.env` next to the tool, then the environment), validating `apiKeyPrefix` and erroring when `apiKeyRequired`. `optionsFromEnv` injects other options (e.g. `baseURL`, `region`) when their env vars are set. The opencode top-level `model` is `<providerId>/<modelKey>`. Base-URL precedence for both builders: `--base-url`/`-u` flag > `OUTFIT_BASE_URL` > the catalogue's `pi.baseUrl`/`options.baseURL`.

**`serve`.** `outfit serve` launches `llama-server` for an Outfit without touching any harness config. With a `PRESET` it flattens the matching `.ini` section (plus `[*]` defaults) into flags via `internal/preset`; otherwise it derives a command from `MODEL`/`ALIAS`/`CONTEXT`/`BASEURL`. Anything the Outfit states overrides the preset. `--dry-run`/`-n` prints the command without running it.

Invariants to keep: opencode merges stay idempotent and never drop unrelated config or comments; Pi merges preserve sibling providers and unknown fields; output stays `0600`; the harness never appears in an Outfit; and every family's `defaultModel` must be one of its `models` (enforced by `TestCatalogIntegrity`).

## Reference

- opencode config schema: https://opencode.ai/docs/config/. The catalogue follows it: `amazon-bedrock` is the Bedrock provider id, custom providers (`ollama`, `llamacpp`, `openai-compatible`) carry an `npm` package plus `options.baseURL`. opencode also supports `{env:VAR}` substitution in the config, but this tool deliberately embeds the resolved key instead (a global config can't rely on a project-local `.env`).
- Pi custom-models schema: https://github.com/earendil-works/pi (`packages/coding-agent/docs/models.md`). `api` is one of `openai-completions`/`openai-responses`/`anthropic-messages`/`google-generative-ai`; `apiKey` supports `$ENV_VAR` interpolation. Not every provider maps to Pi — those without a `pi:` block (e.g. `amazon-bedrock`) error under the pi harness.
