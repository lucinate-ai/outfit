## 1. Shared option resolution

- [x] 1.1 Extract `Provider.ResolveOptions(resolve)` — static options with `optionsFromEnv` layered over them, returned as a fresh map so callers can add their own overrides.
- [x] 1.2 Extract `Provider.RequireOptions(id, options)` from the inline `optionsRequired` loop in `BuildProviderBlock`.
- [x] 1.3 Rewire `BuildProviderBlock` onto both helpers; behaviour unchanged.

## 2. Pi builder

- [x] 2.1 Resolve the Pi base URL as: explicit override, then the provider's `optionsFromEnv` variable, then `pi.baseUrl`, then `options.baseURL`.
- [x] 2.2 Call `RequireOptions` in `BuildPiProvider`, so a required option that Pi's schema cannot carry fails rather than being dropped.

## 3. Tests

- [x] 3.1 Regression: a per-provider endpoint variable reaches the Pi entry for `omlx`, `llamacpp`, `vllm` and `ollama`, and a keyed provider then gets `$VAR` rather than the keyless placeholder.
- [x] 3.2 Precedence: explicit override and `OUTFIT_BASE_URL` both beat the provider variable; an unset variable leaves the catalogue value; `openrouter`'s `pi.baseUrl` still applies.
- [x] 3.3 `BuildPiProvider` errors on an unset required option, naming the variable, and builds once it is set.
- [x] 3.4 Integrity: no embedded provider pairs `optionsRequired` with a `pi` block.
- [x] 3.5 Confirm 3.1 and 3.3 fail against the pre-fix builder.

## 4. Docs

- [x] 4.1 Update the `pi-integration` base-URL requirement.
- [x] 4.2 Update the `AGENTS.md` key/option resolution note with the shared helpers and the full precedence.
- [x] 4.3 Update the `providers.yaml` header for the `pi.baseUrl` fallback and the `optionsRequired` restriction.
- [x] 4.4 CHANGELOG `Fixed` entry.

## 5. Verify

- [x] 5.1 `gofmt -l` clean, `go vet ./...`, `go test ./... -race -cover` green.
- [x] 5.2 Manual: `outfit add -H pi` with each per-provider variable pointed at a remote host writes that host and the `$VAR` reference; localhost is unchanged.
