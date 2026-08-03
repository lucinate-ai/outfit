## 1. Remote config helpers (`cmd/outfit/remote.go`)

- [x] 1.1 Add a tolerant `remoteConfig(remoteValue, outfitDir string) (remote.Config, error)` that resolves the path via `resolveRemotePath`, reads the file, returns the zero `remote.Config` when it does not exist, and errors only on a real read/parse failure.
- [x] 1.2 Refactor `remoteBaseURL` to call `remoteConfig` and return `cfg.BaseURL`, preserving its current tolerant behaviour.
- [x] 1.3 Add `remoteEnvName(remoteValue, outfitDir string) (string, error)`: return `""` for an empty value, the value itself when `remote.IsEnvName` is true, else `remoteConfig(...).Environment`.

## 2. Apply path (`cmd/outfit/main.go`)

- [x] 2.1 In `applySelection`, after the catalogue lookup succeeds and before `h.Apply`, when `sel.Remote != ""` set `sel.Provider` to `remoteEnvName(sel.Remote, envDir)` if that returns a non-empty name (propagating any error).
- [x] 2.2 Confirm the existing base-URL-from-remote block still works (it keys off `sel.Remote`/`sel.BaseURL`, not `sel.Provider`).

## 3. Unapply symmetry (`cmd/outfit/main.go`)

- [x] 3.1 Change `removeSelection` to `removeSelection(sel outfit.Selection, h harness.Harness, envDir string)` and apply the same `remoteEnvName` override to `sel.Provider` at the top.
- [x] 3.2 Update `cmdUnapply` to capture the Outfit path and pass `filepath.Dir(outfitPath)` as `envDir`.
- [x] 3.3 Update `cmdRemove` to pass `""` as `envDir` (flag-based, no Outfit file).

## 4. Tests (`cmd/outfit/apply_test.go`)

- [x] 4.1 Add a test: bare-name `REMOTE dev-1` (registered under a temp `XDG_CONFIG_HOME`) applies a provider keyed `dev-1` with default model `dev-1/qwen`, and takes the base URL from the env's `remote.json`.
- [x] 4.2 Add a test: path-form `REMOTE remote.json` with `"environment":"dev-1"` applies a provider keyed `dev-1`.
- [x] 4.3 Add a test: path-form `REMOTE remote.json` with no `environment` field stays keyed `llamacpp` (fallback), and confirm existing base-URL remote tests still pass.
- [x] 4.4 Add a test: apply then unapply a `REMOTE dev-1` Outfit removes the `dev-1` provider block.

## 5. Docs & validation

- [x] 5.1 Note in `docs/outfit-file.md` that `REMOTE` sets the harness provider name (and the `<env>/<model>` default model).
- [x] 5.2 Run `gofmt`, `go build ./...`, and `go test ./... -cover` (keep coverage >= 80%).
- [x] 5.3 Run `openspec validate remote-harness-provider-name --strict` and fix any issues.
