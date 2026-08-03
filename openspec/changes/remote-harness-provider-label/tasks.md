## 1. Label helper

- [x] 1.1 Add `RemoteProviderLabel(engine, env string) string` to
  `internal/catalog/catalog.go`: `"engine (env)"` when engine is non-empty,
  otherwise `env`.
- [x] 1.2 Unit-test the helper: engine + env, empty engine, (defensively) empty
  env.

## 2. Carry the label to the harness

- [x] 2.1 Add a documented runtime field `DisplayName string` to
  `outfit.Selection`, noting it is derived at apply time (like `BaseURL`), not
  parsed from an Outfit.
- [x] 2.2 In `applySelection` (`cmd/outfit/main.go`), set
  `sel.DisplayName = catalog.RemoteProviderLabel(p.Name, env)` inside the existing
  `if env != ""` block that overrides `sel.Provider`.

## 3. Apply the override in the opencode harness

- [x] 3.1 In `opencodeHarness.Apply` (`internal/harness/adapters.go`), after
  `BuildProviderBlock`, set `block["name"] = sel.DisplayName` when
  `sel.DisplayName != ""`.

## 4. Tests and docs

- [x] 4.1 Add/extend a test in `cmd/outfit/apply_test.go` (or the harness test)
  proving a remote Outfit writes a provider whose display `name` is
  `llama.cpp (dev-2)` while a local `llamacpp` provider keeps `llama.cpp`.
- [x] 4.2 Add a test for the path-form-`REMOTE`-without-environment case: display
  name stays the plain engine name.
- [x] 4.3 Note in `docs/outfit-file.md` that a remote provider is labelled per
  environment so it reads distinctly in a harness model picker.
- [x] 4.4 Run `go test ./...` and `openspec validate remote-harness-provider-label`.
