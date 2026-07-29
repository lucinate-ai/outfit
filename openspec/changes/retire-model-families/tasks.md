## 1. Catalogue data and API

- [ ] 1.1 Rewrite `internal/catalog/providers.yaml`: drop every `families:` block (models, defaultModel); keep only provider plumbing (description, name, npm, apiKeyEnv + required/optional/prefix, options, optionsFromEnv, pi). Update the file header comment to match.
- [ ] 1.2 In `internal/catalog/catalog.go`, remove the `Family` type, `Provider.Families`, `MatchFamily`, `SortedFamilyNames`, and `Family.ModelKeys`.
- [ ] 1.3 Drop the `familyName` parameter from `BuildProviderBlock` and `BuildPiProvider`; the model comes solely from `modelOverride`. Keep default-model resolution and alias-keying behaviour identical.

## 2. Outfit format and selection

- [ ] 2.1 In `internal/outfit/outfit.go`, remove `kwFamily`, `Selection.Family`, the parse case, the `canonicalKeyword` entry, and the `FAMILY` line in `Format`.
- [ ] 2.2 In `cmd/outfit/main.go`, remove the `--model-family`/`-f` flag binding and any use of `sel.Family`.

## 3. Commands

- [ ] 3.1 Simplify `cmdList` (`cmd/outfit/main.go`) to print providers + description, api-key requirement, and supported harnesses — no family lines.
- [ ] 3.2 Simplify `cmdExport` to name the configured model with a `MODEL` line (drop the `MatchFamily` branch; keep the `ModelKeys[0]` fallback as the sole path).
- [ ] 3.3 Update the remove path so `-p` alone removes the whole provider and a model/alias removes one key; delete the family-expansion branch.
- [ ] 3.4 Update `internal/harness/adapters.go` call sites to the new builder signatures.

## 4. Completion

- [ ] 4.1 In `cmd/outfit/complete.go`, remove `kindFamily`, family completion, and the `--model-family` scoping in `modelNames`; complete models by `--provider` only (or drop model completion if no static source remains).

## 5. Tests

- [ ] 5.1 Remove `TestMatchFamily`; update `catalog_test.go` for the new builder signatures.
- [ ] 5.2 Update `outfit_test.go` (remove `FAMILY` parse/format cases; add a `MODEL`-based minimal-Outfit case).
- [ ] 5.3 Update `main_test.go` (`TestCmdList` no longer asserts `family`/`default:`; export/remove tests use `-m`/`ALIAS`).
- [ ] 5.4 Update `complete_test.go` to drop family-completion assertions.
- [ ] 5.5 Run `go test ./... -cover`; confirm every package stays ≥ 80% and add a targeted single-model export/remove test if any dips.

## 6. Docs

- [ ] 6.1 Update `docs/outfit-file.md` (remove `FAMILY` from the keyword table, rules, and examples; update the "at least one of" rule to `MODEL`/`ALIAS`).
- [ ] 6.2 Update `docs/commands/list.md`, `docs/commands/export.md`, `docs/commands/add.md`, and `docs/commands/remove.md` where `FAMILY`/`--model-family`/`-f` appear.
- [ ] 6.3 Update `AGENTS.md` if it documents families or the catalogue's model lists.

## 7. Verify

- [ ] 7.1 `go build ./...`, `gofmt -l` clean, `go test ./... -cover` green.
- [ ] 7.2 Round-trip check: apply `examples/llamacpp/qwen3.6/Outfit`, run `outfit export`, confirm it reproduces the selection with no `FAMILY`.
- [ ] 7.3 `openspec validate retire-model-families` passes.
