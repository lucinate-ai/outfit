## 1. The contract

- [ ] 1.1 Add `COMPANION_ROLES = ['draft', 'mmproj'] as const`, the
  `CompanionRole` type and an `isCompanionRole` guard to
  `remote/lambda/shared/deploy-config.ts`, alongside the existing `RUNNERS`
  pattern
- [ ] 1.2 Add `companions: Partial<Record<CompanionRole, string>>` to the
  `DeployConfig` interface, documenting that values are filenames within the
  model's own repo
- [ ] 1.3 Parse and validate `companions` in `parseDeployConfig`: absent →
  `{}`, non-object → error, unknown role → error naming the role and the
  supported set, empty/non-string filename → error, filename containing a path
  separator → error (a companion is a name in the repo, not a path)
- [ ] 1.4 Add a `companionFileName(role)` helper returning the fixed on-disk
  name (`draft.gguf`, `mmproj.gguf`) so the seed and the runner spec cannot
  disagree about it
- [ ] 1.5 Unit-test the parser: no companions, one companion, both, unknown
  role, non-string value, path separator in value, and that a config written
  before this change still parses

## 2. Runner spec seam

- [ ] 2.1 Add `companionArgs(cfg: DeployConfig, modelDir: string): string[]` to
  the `RunnerSpec` interface in `remote/lambda/runners/spec.ts`, with a doc
  comment stating a role a runner does not understand is ignored, not fatal
- [ ] 2.2 Implement `companionArgs` in `llamacpp.ts`: `draft` →
  `--spec-draft-model <modelDir>/draft.gguf`, `mmproj` →
  `--mmproj <modelDir>/mmproj.gguf`, in a stable order
- [ ] 2.3 Implement `companionArgs` in `vllm.ts` returning `[]`
- [ ] 2.4 Pass the companion args through the llama.cpp `daemonBoot` into
  `daemonDeployConfig`'s `extraServeArgs`, after `--api-key-file` and before
  the passed-through `serveArgs`
- [ ] 2.5 Test that a config with no companions produces a command identical to
  the pre-change one, and that a drafter config adds exactly the one flag

## 3. Seeding

- [ ] 3.1 Extend `llamacpp.seedDownload` to add each companion's exact filename
  to `allow_patterns` (the existing `*<quant>*` glob does not match a drafter
  such as `dflash-kquant.gguf`)
- [ ] 3.2 After the main GGUF is normalised to `model.gguf`, copy each
  companion to its `companionFileName(role)`, failing the seed loudly if a
  named companion is not found in the download
- [ ] 3.3 Keep the main-GGUF selection from matching a companion: exclude the
  companions' filenames from the `find` that picks `model.gguf`, replacing the
  hard-coded `! -iname '*mmproj*'` exclusion
- [ ] 3.4 Change `remote/scripts/seed-model.mjs` to import and use the runner
  spec's `seedDownload` instead of restating the download shell, so the manual
  re-seed and the automatic seed cannot drift
- [ ] 3.5 Unit-test `buildSeedUserData` for the drafter case: shell quoting of
  the filename, the companion appearing in `allow_patterns`, and the copy to
  `draft.gguf`

## 4. Presence checking

- [ ] 4.1 Replace `RunnerSpec.weightsSentinel(prefix)` with
  `weightsKeys(cfg, prefix): string[]` — the existing sentinel plus one key per
  named companion — and update both runner specs
- [ ] 4.2 Update `weightsPresent` in `remote/lambda/shared/seed.ts` to HEAD
  every key and return false if any is missing, preserving the existing
  NotFound/NoSuchKey/404 handling
- [ ] 4.3 Test that a model seeded without a companion is judged **absent**
  once a companion is added — the regression that would otherwise boot an
  instance pointing at a file that was never synced

## 5. The `outfit` side

- [ ] 5.1 Add `spec-draft-model`/`md`/`model-draft` aliases to
  `internal/preset`'s canonical map so every spelling resolves to one name
  (`mm` → `mmproj` already exists), with tests
- [ ] 5.2 Add `spec-draft-model` and `mmproj` to `cloudOwnedFlags` in
  `cmd/outfit/remote.go`, so a locally-meaningful path is dropped from
  `serveArgs`
- [ ] 5.3 In `deployConfigFor`, read those preset keys and populate
  `dc.Companions` with `filepath.Base` of each value, before the cloud-owned
  drop is applied
- [ ] 5.4 Add the `Companions` field to the Go-side `remote.DeployConfig` and
  include it in the deploy request body
- [ ] 5.5 Print named companions in `outfit remote deploy`'s summary output
  (beside runner/model/context), so the user can see the drafter was picked up
- [ ] 5.6 Test `deployConfigFor`: a preset with a drafter yields the companion
  and no `--spec-draft-model` in `serveArgs`; a preset without one yields an
  empty map; a preset using `-md` is treated the same as `--spec-draft-model`

## 6. Docs and the motivating example

- [ ] 6.1 Update `examples/llamacpp/muse-glimmer-30b/README.md`: remove the
  "cloud path loses the drafter" limitation and document that the drafter is
  carried, noting `--spec-type draft-dflash` must still be in the preset
- [ ] 6.2 Note in the example that a companion's filename is taken from the
  basename of the preset value, so it must match the name in the HF repo
- [ ] 6.3 Update `remote/README.md`'s deploy-config description to cover
  companions
- [ ] 6.4 Run the full test suite plus `pnpm -C remote test`, and verify
  `outfit remote deploy --dry-run` against the example shows the drafter

## 7. End-to-end verification

- [ ] 7.1 Bump `llamacppRelease` to a build carrying the Muse Glimmer merge and
  re-bake the llama.cpp AMI (the pin predates it, so the drafter cannot be
  exercised remotely until this is done)
- [ ] 7.2 Deploy the example remotely, confirm the seed syncs both
  `model.gguf` and `draft.gguf`, and confirm the engine log shows the drafter
  loading
- [ ] 7.3 Confirm a deployment that names no companion still starts unchanged
