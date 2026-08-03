## Why

When a remote Outfit is applied, the harness provider is keyed on the remote
environment name (e.g. `dev-2`), but its human-readable display name still comes
from the `PROVIDER` catalogue entry (e.g. `llama.cpp`). opencode's model picker
shows the display name, not the provider key, so a remote environment and a local
engine of the same kind render as two identical `llama.cpp` rows — the user
cannot tell which model is the remote one. Naming the provider per environment
was only done for the key; the label needs the same treatment.

## What Changes

- When an applied Outfit has a `REMOTE`, the harness provider's **display name**
  reflects the environment as well as the engine, so it reads distinctly in a
  harness model picker (e.g. `llama.cpp (dev-2)` rather than a bare `llama.cpp`).
- The label is derived from the catalogue engine's display name and the resolved
  environment name; when the engine has no display name, the environment name is
  used on its own.
- With no `REMOTE`, or when no environment name resolves, the display name is the
  catalogue engine name, unchanged.
- Only the display label changes. The provider key, model reference
  (`<env>/<model>`), engine options, API-key variable, and base URL are all
  unchanged from the existing remote-naming behaviour.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `remote-environments`: add a requirement that a `REMOTE` gives the harness
  provider a display name distinct from the local engine of the same kind, so the
  two are told apart in a harness model picker.

## Impact

- `internal/catalog/catalog.go`: `BuildProviderBlock` (and its Pi counterpart)
  need a way to set the display name to something other than the catalogue
  engine name.
- `cmd/outfit/main.go`: `applySelection` passes the environment-derived label
  through when it overrides the provider name for a remote Outfit.
- No change to the provider key, model reference, or engine configuration — this
  builds on the existing `REMOTE`-names-the-provider behaviour.
- `docs/outfit-file.md`: note that a remote provider is labelled per environment.
