## Context

See proposal.md — Why. The shape of the existing code matters to the approach:

- `DeployConfig` (`remote/lambda/shared/deploy-config.ts`) is the whole
  runner-neutral contract, validated by `parseDeployConfig`, stored in SSM. It
  already derives `weightsPrefix` rather than trusting the caller.
- `RunnerSpec` (`remote/lambda/runners/spec.ts`) is the established seam for
  everything runner-specific: `seedDownload`, `syncedModelPath`,
  `weightsSentinel`, `daemonBoot`. The registry is a `Record<Runner, …>`, so a
  new member fails to compile until every runner implements it.
- `cloudOwnedFlags` (`cmd/outfit/remote.go`) already exists precisely to stop a
  locally-meaningful preset value reaching the instance. A drafter path is the
  same problem it was built for.
- `remote/scripts/seed-model.mjs` duplicates the seed's download shell for
  manual re-seeds, so the two drift unless the fragment has one source.

The constraint that shapes everything below: the seed's `allow_patterns` is
`['*<quant>*']`. For the motivating case `quant` is `kquant-dynamic`, and the
drafter is `dflash-kquant.gguf` — which does **not** match. Companions cannot
ride along on the existing glob; they need to be named.

## Goals / Non-Goals

**Goals:**

- One mechanism that covers the drafter and the encoder, rather than a bespoke
  field per companion.
- Keep the "one preset serves local and cloud" property: no new Outfit
  keyword, no second file to maintain.
- Absent companions produce byte-identical behaviour to today.

**Non-Goals:**

- Companions from a *different* repo to the main weights. Meta publishes both
  in one repo; cross-repo would add a second credential scope for no current
  case.
- Split/sharded GGUFs. The existing "took the first, warned" behaviour is
  untouched.
- Making vLLM use companions. The field is runner-neutral, but only the
  llama.cpp spec maps roles to flags for now.

## Decisions

### A role-keyed map, not one field per companion

`DeployConfig` gains `companions?: Record<CompanionRole, string>` where the key
is a validated role (`draft`, `mmproj`) and the value is the filename in the
model's repo:

```json
{ "companions": { "draft": "dflash-kquant.gguf" } }
```

*Alternative — `draftFile` / `mmprojFile` as separate optional strings.*
Simpler to read, and honest about there being only two. Rejected because every
stage (validate, seed, sync, path, flag) would grow a parallel branch per
companion, which is exactly the duplication that produced the hard-coded
`mmproj` exclusion. With a map, each stage iterates one collection.

*Alternative — an untyped `extraFiles: string[]`.* Rejected: the runtime has to
know what a file is *for* to pick its flag. A bare list pushes that back to
guessing by filename, which is the failure mode being removed.

The role set is a closed union validated in `parseDeployConfig`, so a typo is
rejected at deploy time rather than producing a silently flagless instance.

### Roles map to flags in the runner spec, not the shared layer

`RunnerSpec` gains `companionArgs(cfg, modelDir): string[]`. The llama.cpp spec
maps `draft` → `--spec-draft-model <modelDir>/draft.gguf` and `mmproj` →
`--mmproj <modelDir>/mmproj.gguf`; vLLM returns `[]`.

This keeps the existing architecture honest: `spec.ts`'s comment already states
that runner-specific behaviour lives in the spec so there are "no scattered
binary conditions to hunt down". Role→flag is exactly that kind of knowledge.
It also means a role a runner does not understand is inert rather than fatal.

The returned args join the existing `extraServeArgs` of `daemonDeployConfig`,
which is already how `--api-key-file` and `--gpu-memory-utilization` are
injected — so companion flags land in the same deployment-owned position, ahead
of the passed-through `serveArgs`.

### Fixed on-disk names, assigned by role

The seed normalises each companion to `<role>.gguf` (`draft.gguf`,
`mmproj.gguf`), mirroring what it already does for `model.gguf`. The runtime
then never discovers filenames, and the S3 layout stays legible.

### Presence is checked over the expected set

`weightsSentinel(prefix): string` becomes `weightsKeys(cfg, prefix): string[]`
— the main sentinel plus one key per named companion — and `weightsPresent`
HEADs all of them, returning false if any is missing.

This is the subtle correctness point. `weightsPrefixFor` derives from
`(runner, modelId, quant)`, and a companion changes none of those. Without this
change, adding a drafter to an already-seeded model finds `model.gguf` present,
skips the seed, and boots an instance whose `--spec-draft-model` points at a
file that was never synced — a start failure minutes later, with nothing in the
deploy output to explain it.

*Alternative — fold companions into the prefix.* Rejected: it re-downloads the
20 GB main weights to add a 1.6 GB drafter, and orphans the old prefix.

### `outfit` reads companions from the preset it already reads

`deployConfigFor` maps preset keys to roles: `spec-draft-model` → `draft`,
`mmproj` → `mmproj`, taking `filepath.Base` of the value. The user writes the
drafter once, for `outfit serve`, and deploy derives the repo filename from it.

This holds because a companion lives in the model's own repo, so its basename
*is* its repo filename — true for the motivating case and the documented way to
obtain the file. Where it does not hold, deploy fails loudly at seed time with
a missing file rather than silently omitting the drafter.

Both keys join `cloudOwnedFlags`, so the local path is dropped from `serveArgs`
and replaced by `companionArgs`. `internal/preset` gains aliases so `md`,
`model-draft` and `mm` all canonicalise to the same names the drop-set checks —
without them, a preset written with `-md` would evade the drop and leak a local
path onto the instance.

### The projector exclusion stays (revised during implementation)

The proposal said naming companions explicitly would *replace* the blanket
`! -iname '*mmproj*'`. Implementing it showed that removes a protection worth
keeping: where the quant glob happens to match a projector — quant `Q4_K_M`
matching `mmproj-Q4_K_M.gguf` — the projector sorts *before* the real weights
(`mm` < `mo`) and would be copied to `model.gguf` and served as the model.

So both exclusions apply: the named companions, and `*mmproj*` regardless.
`mmproj` is still no longer a *special case* in the sense that mattered —
keeping a projector is now done by naming it as a companion rather than being
impossible — but not selecting one as the main model stays unconditional.

### A charset, not multi-layer quoting (added during implementation)

A companion filename is interpolated into generated shell *and* into a Python
literal inside it. Rather than quote correctly through both layers,
`COMPANION_FILENAME` (`/^[A-Za-z0-9._-]+$/`) rejects anything that could escape
either, at deploy time where the error is visible. Real GGUF filenames fit it.
This subsumes the path-separator check the proposal called for.

### One download fragment, two callers

`seedDownload` keeps producing the shell, extended to fetch each companion by
exact filename (a second `allow_patterns` entry) and copy it to its role name.
The manual script is changed to import the same fragment rather than restating
it, removing the existing drift risk between the automatic and manual paths.

Implementation note: importing the runner spec means the script has to be
TypeScript, so `scripts/seed-model.mjs` became `scripts/seed-model.mts`, run
with the `tsx` already in devDependencies (`pnpm seed-model` unchanged). It
also now validates through `parseDeployConfig`, so the manual path cannot
accept a config the automatic one would reject.

## Risks / Trade-offs

- **A companion's basename is assumed to be its repo filename** → Wrong only if
  the user renamed the file locally. Fails at seed with a clear "file not found
  in repo" rather than serving silently without the drafter; documented in the
  example README.
- **Re-seed cost when adding a companion** → Adding a 1.6 GB drafter re-fetches
  the ~20 GB main weights, because presence is all-or-nothing per prefix. A
  per-file sync would avoid it; not worth the complexity for a one-off ~20 min
  job that the deploy output already announces.
- **`--spec-type` remains the user's** → The cloud sets only the drafter
  *path*; selecting `draft-dflash` stays in the preset's passed-through args. A
  user who names a drafter but omits `--spec-type` gets llama.cpp's default
  draft path, as they would locally. Consistent, but worth a README note since
  Meta's own card omits the flag.
- **vLLM ignores companions** → A config could name one for a vLLM deployment
  and see it seeded but unused. Acceptable: inert rather than fatal, and the
  alternative (rejecting per-runner) puts runner knowledge back in the shared
  validator.

## Migration Plan

No migration. Every field is optional; `parseDeployConfig` treats an absent
`companions` as `{}`, and stored configs written before this change parse
unchanged. `weightsKeys` for a config with no companions returns exactly the
single sentinel checked today, so already-seeded models stay "present" and are
not re-fetched.

Rollback is a Lambda redeploy: older code ignores the extra SSM field, and the
extra objects in S3 are inert.

Ship order: contract and validation, then seed, then presence, then the
runner's flags, then the `outfit` side — each step leaves the tree working,
with the `outfit` change last because it is what starts emitting the new field.
