## Why

The remote path assumes a model is exactly one file. The seed globs
`*<quant>*`, drops anything matching `mmproj`, sorts what is left and takes the
first, normalising it to `model.gguf`; the llama.cpp runner serves that single
file. Any companion weight published beside the model — a speculative-decoding
drafter, a perception encoder — cannot reach the instance.

Muse-Glimmer-30B makes the cost concrete. Meta ships
`dflash-kquant.gguf` (1.63 GB) beside the weights and quotes a 3.1x decode
speedup from it on an RTX 5090. The local example in
`examples/llamacpp/muse-glimmer-30b/` uses it; the cloud deployment silently
cannot, so the GPU instance we pay by the hour for is the one configuration
that runs without the speedup.

The existing `mmproj` exclusion is the same gap already met once and papered
over with a filter, rather than modelled.

## What Changes

- A deployment MAY name **companion weights** beside its main weights: extra
  files from the same Hugging Face repo, each with a role (`draft`, `mmproj`).
  Companions are optional; a deployment naming none behaves exactly as today.
- The seed fetches each named companion alongside the main weights and
  normalises it to a fixed name per role, so the runtime never guesses
  filenames. The blanket `mmproj` exclusion is replaced by explicit selection —
  `mmproj` stops being a special case and becomes a companion role.
- Presence-of-weights is judged across the whole expected set rather than one
  sentinel, so adding a companion to an existing deployment re-seeds instead of
  silently reusing weights that lack it.
- The engine's command gains the companion path flags
  (`--spec-draft-model`, `--mmproj`), pointing at the synced local paths. These
  join the deployment-owned settings, so a preset's local path for a drafter is
  dropped rather than leaking onto the instance where it does not exist.
- `outfit remote deploy` reads the companion filenames from the same preset
  keys that drive a local `outfit serve`, keeping the existing "one preset
  works locally and remotely without edits" property.

Not breaking: every field is optional and absent companions reproduce today's
behaviour byte for byte.

## Capabilities

### New Capabilities

None. This extends two existing capabilities rather than introducing a new one.

### Modified Capabilities

- `model-weights`: weights become a main artefact plus zero or more named
  companions. Where they live is still derived; what counts as "present" must
  now cover the companions, not just a single marker.
- `inference-runners`: the deployment-owned part of the engine command grows to
  include companion locations on disk, so a caller-supplied path for one is
  overridden rather than passed through.

## Impact

- `remote/lambda/shared/deploy-config.ts` — the `DeployConfig` contract and its
  parser/validator.
- `remote/lambda/runners/spec.ts`, `llamacpp.ts`, `vllm.ts` — the per-runner
  seed download, synced paths, and command building.
- `remote/lambda/shared/seed.ts` — the presence check.
- `remote/scripts/seed-model.mjs` — the manual re-seed path, which duplicates
  the download shape.
- `cmd/outfit/remote.go` — `deployConfigFor`, and the cloud-owned flag set.
- `internal/preset/preset.go` — alias coverage for the drafter flag spellings,
  so a cloud-owned flag is recognised however it is written.
- `examples/llamacpp/muse-glimmer-30b/` — the motivating case; its README
  currently documents the cloud path as drafter-less.
- No infrastructure change: no new bucket, role, or AMI. Seeded objects gain
  siblings under the existing prefix.
