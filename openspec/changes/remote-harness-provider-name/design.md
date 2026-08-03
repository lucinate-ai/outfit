## Context

`applySelection` (`cmd/outfit/main.go`) is the shared core of `add`, `apply`, and
`harness <outfit>`. It resolves the catalogue provider `p` from `sel.Provider`,
then hands `sel` to the active harness, which keys its config on `sel.Provider`
and constructs the default model as `sel.Provider + "/" + modelKey` (in
`internal/catalog`). The `REMOTE` value is currently consulted only to fill a
missing base URL (`remoteBaseURL`).

The catalogue builders (`BuildProviderBlock`, `BuildPiProvider`) use the id they
are given only as a label and as the model-key prefix — `RequireOptions`/
`ResolveOptions` never use it as a map key — so the id passed to the harness can
differ from the catalogue key the engine was looked up under.

## Goals / Non-Goals

**Goals:**
- Key the harness provider on the remote environment name when an applied Outfit
  has a `REMOTE`, producing `<env>/<model>`.
- Support both `REMOTE` forms (bare name; path with an `environment` field).
- Keep `PROVIDER` as the engine definition.
- Keep apply and unapply symmetric.

**Non-Goals:**
- Round-tripping a remote Outfit through `outfit export` (already lossy — the
  harness config does not record `REMOTE`).
- Any change to the on-disk `remote.json` format or the deploy/registry flow.
- Changing behaviour for Outfits without a `REMOTE`.

## Decisions

**Override `sel.Provider` after the catalogue lookup, not inside the harness.**
`sel` is passed by value, so `applySelection` can set `sel.Provider` to the env
name once `p` is resolved from the real `PROVIDER`; every downstream consumer (the
harness block, the model key, the "Configured provider …" line) then follows with
no signature changes to the harness interface. Alternative — threading a separate
`providerName` argument through `harness.Apply` and both catalogue builders — was
rejected as more churn across packages for the same effect.

**Resolve the env name in `cmd/outfit`, next to the existing remote helpers.** Add
a tolerant `remoteConfig(remoteValue, outfitDir)` that returns the whole
`remote.Config` (a missing file yields the zero value, matching how the base-URL
lookup already tolerates an Outfit naming a remote before deploy writes it), and
`remoteEnvName` on top of it: the bare name when `remote.IsEnvName` is true, else
`cfg.Environment`. `remoteBaseURL` is refactored onto `remoteConfig` so the two
never diverge. The bare-name path needs no file read.

**Apply the same override in `removeSelection` for symmetry.** `removeSelection`
removes by `sel.Provider`, so it gains the Outfit directory (`cmdUnapply` passes
`filepath.Dir(outfitPath)`; `cmdRemove` passes `""`) and applies the same
`remoteEnvName` override, so unapply removes exactly what apply wrote.

## Risks / Trade-offs

- **A path-form `REMOTE` whose `remote.json` is deleted between apply and unapply
  loses the env name, so unapply falls back to `PROVIDER` and leaves the block
  behind.** → The canonical workflow uses the bare-name form, which needs no file
  and is unaffected; the path form is the older usage and the config normally
  persists.
- **`outfit export` of a remote-applied Outfit emits `PROVIDER <env>`, which does
  not re-apply cleanly.** → Export was already lossy for remote Outfits (it
  dropped `REMOTE` and emitted `PROVIDER <engine>` + `BASEURL`); this is not a
  regression and is called out as out of scope.
- **Two environments from the same engine now write two harness blocks instead of
  overwriting one.** → This is the intended fix; existing single-environment and
  no-REMOTE behaviour is unchanged.
