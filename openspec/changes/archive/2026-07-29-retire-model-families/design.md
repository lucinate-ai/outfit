## Context

The catalogue's `Family` layer (`internal/catalog/catalog.go`) groups a provider's
example models under a name, with a `defaultModel`. It touches five call sites: the two
block builders (`BuildProviderBlock`, `BuildPiProvider`), the Outfit format
(`Selection.Family`, `FAMILY` keyword), the CLI flag `--model-family`/`-f`, `outfit list`
/ `outfit export` / `outfit remove`, and shell completion. The behaviour that must survive
the removal is: a selection still configures one model (from `MODEL`/`ALIAS`), that model
becomes the harness default, and an `ALIAS` still keys the model. `BuildProviderBlock`
already synthesises a model entry from `modelOverride` when it is not otherwise present,
so the single-model path already exists and is exercised by every shipped example.

## Goals / Non-Goals

**Goals:**
- Remove families and per-family default models from the catalogue data and API.
- Remove the `FAMILY` keyword and `--model-family`/`-f` flag.
- Preserve single-model selection, default-model resolution, and alias-keying unchanged.
- Keep the public builder functions coherent (drop the now-unused `familyName` parameter).
- Keep test coverage ≥ 80% and all docs consistent.

**Non-Goals:**
- Any live/dynamic model discovery source (separate change).
- Changing context/output limit handling, API-key resolution, base-URL precedence, or the
  opencode/Pi config-merge behaviour.
- A deprecation shim for `FAMILY` — it is removed outright (no shipped Outfit uses it).

## Decisions

**Drop `familyName` from the builder signatures** rather than pass `""`. Leaving a dead
parameter invites confusion; the builders become
`BuildProviderBlock(id, p, modelOverride, baseURLOverride, resolve)` and the Pi analogue.
Callers in `internal/harness/adapters.go` pass `modelKey(sel)` as before.

**Remove `MatchFamily` and let `export` name the model directly.** Export already falls
back to `st.ModelKeys[0]` when no family matches (`cmd/outfit/main.go`); with families
gone, that fallback becomes the only path. A config with several models under one provider
still exports the default model (or the first) as `MODEL` — export represents one model per
Outfit, which is the format's existing constraint.

**`outfit remove -p <provider>` with no model still removes the whole provider**; naming a
model or alias removes that one key. The family-expansion branch is deleted.

**`outfit list` shows providers + plumbing only.** Losing the model hint is accepted here
and addressed by the separate live-discovery change; the test assertions for `family`/
`default:` lines are removed.

Alternatives considered: keeping single-model families as thin wrappers (rejected — still
curation and still a `families:` block to maintain); keeping `FAMILY` as a deprecated
no-op alias for `MODEL` (rejected — no users to protect, and it would keep the parser and
completion complexity).

## Risks / Trade-offs

- [Breaking change: an Outfit or script using `FAMILY`/`-f` stops working] → The proposal
  and docs state the migration (`FAMILY x` → `MODEL x` or `ALIAS x`); no shipped example
  uses it; the parser error already names the accepted keywords, so the failure is legible.
- [`outfit list` loses model suggestions] → Intentional; the follow-up live-discovery
  change restores a browsable, self-updating list without curation.
- [Coverage dip from deleting family tests] → Net code shrinks too; verify with
  `go test ./... -cover` and add a small `export`/`remove` single-model test if a package
  dips below 80%.

## Migration Plan

Single atomic change, no runtime state. Ship removes the keyword/flag and the family data
together so the parser, builders, and catalogue stay consistent. Rollback is a revert of
the change; no data migration is involved.
