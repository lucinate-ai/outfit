## Context

The archived `remote-harness-provider-name` change made `applySelection`
(`cmd/outfit/main.go`) override `sel.Provider` with the resolved environment name
for a remote Outfit, so the opencode provider is keyed `dev-2` and the model reads
`dev-2/qwen`. But the provider block's display name is still set from the
catalogue engine in `catalog.BuildProviderBlock`:

```go
if p.Name != "" { block["name"] = p.Name }   // e.g. "llama.cpp"
```

opencode's model picker lists providers by that display name, not the key, so a
local `llamacpp` provider and a remote `dev-2` provider both render as `llama.cpp`
and are indistinguishable. Pi is unaffected: its provider entry has no separate
display name — it is keyed directly on the provider id — so this concerns the
opencode harness only.

## Goals / Non-Goals

**Goals:**
- A remote provider's opencode display name reflects its environment, so it is
  told apart from a local engine of the same kind (e.g. `llama.cpp (dev-2)`).
- Compose the label only where the environment name is known, so the path-form
  `REMOTE`-without-environment case keeps the plain engine label.
- Keep the provider key, model reference, options, key variable, and base URL
  exactly as the existing remote-naming behaviour leaves them.

**Non-Goals:**
- Changing anything for Pi (no provider-level display name to collide).
- Changing the display name of a non-remote provider.
- Reworking how the environment name itself is resolved (unchanged from
  `remoteEnvName`).

## Decisions

### Decision: Carry the label on the Selection, set in applySelection

`applySelection` is the one place that holds both the catalogue engine (`p`, so
`p.Name`) and the resolved environment name (`env`), and it already knows whether
a rename happened — it only overrides `sel.Provider` when `env != ""`. So it is
also the right place to compose the label. A new runtime field
`Selection.DisplayName` carries it to the harness, set in the same `if env != ""`
block that renames the provider:

```go
if env != "" {
    sel.Provider = env
    sel.DisplayName = catalog.RemoteProviderLabel(p.Name, env)
}
```

`DisplayName` stays empty in every other path — including a path-form `REMOTE`
whose config names no environment — so those keep the engine label with no
special-casing.

**Alternative considered — compose in the opencode adapter from `p.Name` and
`sel.Provider`:** rejected. By the time the adapter runs, `sel.Provider` has
already been overwritten, so the adapter cannot tell a renamed provider (`dev-2`)
from an un-renamed one (`llamacpp`, the no-environment path form), and would
mislabel the latter as `llama.cpp (llamacpp)`. The rename decision lives in
`applySelection`; the label decision belongs with it.

**Alternative considered — new positional param on `BuildProviderBlock`:**
rejected as the weaker of two workable options. `BuildProviderBlock` already
takes four positional strings; a fifth is easy to misorder. Overriding
`block["name"]` in the opencode adapter keeps the display-name concern in the
harness that has it and leaves the catalogue signature alone.

### Decision: A small pure helper for the label text

`catalog.RemoteProviderLabel(engine, env string) string` returns
`fmt.Sprintf("%s (%s)", engine, env)` when `engine != ""`, otherwise `env`. A
named, pure function is trivial to unit-test and keeps the format in one place.

### Decision: The opencode adapter applies the override

`opencodeHarness.Apply` sets `block["name"] = sel.DisplayName` when
`sel.DisplayName != ""`, after `BuildProviderBlock` returns. `WriteConfig`
deep-merges the block, so re-applying updates the label in place. `removeSelection`
needs no label — removal is by provider key and model key.

## Risks / Trade-offs

- **A runtime-only field on `Selection` mixes parsed and derived state.** →
  `Selection` already carries derived runtime values (`BaseURL` filled from the
  remote config, `Provider` overwritten with the environment name), so a
  documented `DisplayName` follows the established pattern rather than setting a
  new one.
- **A user who already applied a remote Outfit keeps the old bare label until
  they re-apply.** → Acceptable: `outfit apply` is the natural point to pick up
  the new label, and the deep-merge overwrites the stale `name` cleanly.
