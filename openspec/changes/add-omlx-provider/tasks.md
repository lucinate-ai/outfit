## 1. Catalogue

- [x] 1.1 Add an `omlx` provider to `internal/catalog/providers.yaml`: `npm` `@ai-sdk/openai-compatible`, `options.baseURL` `http://localhost:8000/v1`, `optionsFromEnv.baseURL` `OMLX_BASE_URL`, `apiKeyEnv` `OPENAI_API_KEY` with `apiKeyOptional: true`, and a `pi` block using `openai-completions`.

## 2. Preset dialects

- [x] 2.1 Add `Dialect{Aliases, Boolean}` to `internal/preset`, with `LlamaCpp` wrapping the existing tables and `OMLX` as the zero value (long-form keys pass through).
- [x] 2.2 Move `canonicalName`/`flagFor`/`Flags` onto `Dialect`; keep package-level `Flags`, `CanonicalKey`, `Preset.Args` and `Preset.Command` as `LlamaCpp` wrappers so `remote.go`'s `isCloudOwned` is unchanged.
- [x] 2.3 Add `Preset.CommandIn(dialect, binary, subcommand, sec, overrides...)` for engines that need a subcommand.
- [x] 2.4 Update the package doc; fix `Select`'s error text, which told the user to set `MODEL` when `serve` selects a section by `ALIAS`.

## 3. Serve

- [x] 3.1 Extract the serve command from `cmd/outfit/main.go` into `cmd/outfit/serve.go` (required: `TestCompletionCoversDispatch` scans `main.go` for `case "…":` at one tab, so `engineFor`'s switch there reads as new commands).
- [x] 3.2 Add `serveEngine` (binary, subcommand, dialect, params, needsModel, installHint) and `engineFor`, the local twin of `runnerFor`; error for any provider that is not a local engine.
- [x] 3.3 Add `resolveOMLXBinary`: the `omlxBinary` override, then `PATH`, then `/Applications/oMLX.app/Contents/MacOS/omlx-cli`.
- [x] 3.4 Split `outfitServeParams` into `llamacppServeParams` and `omlxServeParams` over a shared `bindAddressParams`; oMLX maps only `BASEURL`.
- [x] 3.5 Route `cmdServe` through the engine: per-engine install hint, subcommand in the argv, and `needsModel` gating the "needs a PRESET or a MODEL" error.

## 4. Tests

- [x] 4.1 `internal/preset`: dialect isolation (`OMLX` does not alias `m`/`c`, has no bare booleans), the LlamaCpp wrappers still render `--hf-repo`/`--mmap` and `CanonicalKey`, and `CommandIn` places the subcommand.
- [x] 4.2 `internal/catalog`: `omlx` keyless on localhost, an env reference when the key is set, and the Pi placeholder/reference pair.
- [x] 4.3 `cmd/outfit`: generalise the binary stub, then cover the oMLX argv, no-model start, preset pass-through, Outfit-over-preset, never-passes-a-key, execution, the oMLX install hint, and the now-rejected non-engine provider.
- [x] 4.4 `go test ./... -race -cover` green, every package ≥ 80%.

## 5. Docs

- [x] 5.1 Rewrite `docs/commands/serve.md` around per-engine selection; document the oMLX flags, the binary lookup, and that presets are not portable between engines.
- [x] 5.2 Update `docs/README.md` (provider list, base-URL env row, keyless-local sentence), `docs/outfit-file.md` (`PRESET` is engine-specific), and `README.md` (provider list, serve section, env vars, guides).
- [x] 5.3 Update the CLI usage text in `cmd/outfit/main.go`.
- [x] 5.4 Update `AGENTS.md`: the layout (new `serve.go`), the `internal/preset` and `serve` notes, and a new trap for cross-dialect presets.
- [x] 5.5 Add `examples/omlx/qwen3.6/` (README, Outfit, preset.ini) and link it from `README.md`.
- [x] 5.6 Add a CHANGELOG entry, including the breaking `serve` change.

## 6. Verify

- [x] 6.1 `gofmt -l` clean, `go vet ./...`, `go test ./... -race -cover` green.
- [x] 6.2 `outfit serve --dry-run` on the oMLX example, on a bare `PROVIDER omlx`, and on a non-engine provider (which must now error).
- [x] 6.3 Regression: `outfit serve --dry-run examples/llamacpp/qwen3.6/Outfit` produces the same command as before the change.
- [x] 6.4 `outfit add -p omlx` under both harnesses: keyless on localhost, `{env:…}`/`$VAR` when remote or keyed.
- [ ] 6.5 `openspec validate add-omlx-provider` — the CLI is not installed in this environment; artifacts were hand-written to the archived change's shape.
