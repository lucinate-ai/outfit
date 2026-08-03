## Why

When an Outfit names a remote endpoint with `REMOTE`, applying it still keys the
harness provider config on the `PROVIDER` value (the engine, e.g. `llamacpp`), so
every environment built from the same engine collapses to the same provider key:
a second environment's `apply` overwrites the first's block, and the model
reference (`llamacpp/qwen`) can't tell environments apart. The remote workflow is
multi-environment by design, so the harness provider should be named per
environment.

## What Changes

- When an applied Outfit has a `REMOTE`, the harness (opencode/pi) provider is
  keyed on the **remote environment name** rather than the `PROVIDER` value, and
  the default model reads as `<env>/<model>` (e.g. `REMOTE dev-1` + `PROVIDER
  llamacpp` + `ALIAS qwen` → provider `dev-1`, model `dev-1/qwen`).
- The `PROVIDER` entry still supplies the engine configuration (npm, options,
  API-key env, base URL). Only the harness-facing name changes.
- Both `REMOTE` forms are covered: a bare name uses the value itself; a path form
  reads the `environment` field from the named `remote.json`, falling back to the
  `PROVIDER` value when that field is absent.
- With no `REMOTE`, the `PROVIDER` value remains the provider name (unchanged).
- Unapply removes the same provider that apply wrote, keeping the two symmetric.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `remote-environments`: add a requirement that a `REMOTE` sets the harness
  provider name (and the `<env>/<model>` default model) when an Outfit is applied,
  and that unapply removes that same provider.

## Impact

- `cmd/outfit/remote.go`: a tolerant remote-config reader and an env-name helper
  (bare name, else the config's `environment` field).
- `cmd/outfit/main.go`: `applySelection` overrides the harness provider name after
  the catalog lookup; `removeSelection` applies the same override (and gains the
  Outfit directory) so unapply stays symmetric.
- No changes to `internal/harness`, `internal/catalog`, `internal/opencode`, or
  `internal/pi` — they already key on whatever provider id they are handed.
- `docs/outfit-file.md`: note that `REMOTE` sets the harness provider name.
- Not solved here: `outfit export` of a remote-applied Outfit stays lossy (the
  harness config does not record `REMOTE`), as it already is today.
