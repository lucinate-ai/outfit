## 1. Catalogue data and API

- [x] 1.1 Rewrite `internal/catalog/providers.yaml`: drop every `families:` block (models, defaultModel); keep only provider plumbing (description, name, npm, apiKeyEnv + required/optional/prefix, options, optionsFromEnv, pi). Update the file header comment to match.
- [x] 1.2 In `internal/catalog/catalog.go`, remove the `Family` type, `Provider.Families`, `MatchFamily`, `SortedFamilyNames`, and `Family.ModelKeys`.
- [x] 1.3 Drop the `familyName` parameter from `BuildProviderBlock` and `BuildPiProvider`; the model comes solely from `modelOverride`. Keep default-model resolution and alias-keying behaviour identical.

## 2. Outfit format and selection

- [x] 2.1 In `internal/outfit/outfit.go`, remove `kwFamily`, `Selection.Family`, the parse case, the `canonicalKeyword` entry, and the `FAMILY` line in `Format`.
- [x] 2.2 In `cmd/outfit/main.go`, remove the `--model-family`/`-f` flag binding and any use of `sel.Family`.

## 3. Commands

- [x] 3.1 Simplify `cmdList` (`cmd/outfit/main.go`) to print providers + description, api-key requirement, and supported harnesses — no family lines.
- [x] 3.2 Simplify `cmdExport` to name the configured model with a `MODEL` line (drop the `MatchFamily` branch; keep the `ModelKeys[0]` fallback as the sole path).
- [x] 3.3 Update the remove path so `-p` alone removes the whole provider and a model/alias removes one key; delete the family-expansion branch.
- [x] 3.4 Update `internal/harness/adapters.go` call sites to the new builder signatures.

## 4. Completion

- [x] 4.1 In `cmd/outfit/complete.go`, remove `kindFamily`, family completion, and the `--model-family` scoping in `modelNames`; complete models by `--provider` only (or drop model completion if no static source remains).

## 5. Tests

- [x] 5.1 Remove `TestMatchFamily`; update `catalog_test.go` for the new builder signatures.
- [x] 5.2 Update `outfit_test.go` (remove `FAMILY` parse/format cases; add a `MODEL`-based minimal-Outfit case).
- [x] 5.3 Update `main_test.go` (`TestCmdList` no longer asserts `family`/`default:`; export/remove tests use `-m`/`ALIAS`).
- [x] 5.4 Update `complete_test.go` to drop family-completion assertions.
- [x] 5.5 Run `go test ./... -cover`; confirm every package stays ≥ 80% and add a targeted single-model export/remove test if any dips.

## 6. Docs

- [x] 6.1 Update `docs/outfit-file.md` (remove `FAMILY` from the keyword table, rules, and examples; update the "at least one of" rule to `MODEL`/`ALIAS`).
- [x] 6.2 Update `docs/commands/list.md`, `docs/commands/export.md`, `docs/commands/add.md`, and `docs/commands/remove.md` where `FAMILY`/`--model-family`/`-f` appear.
- [x] 6.3 Update `AGENTS.md` if it documents families or the catalogue's model lists.

## 7. Verify

- [x] 7.1 `go build ./...`, `gofmt -l` clean, `go test ./... -cover` green.
- [x] 7.2 Round-trip check: apply `examples/llamacpp/qwen3.6/Outfit`, run `outfit export`, confirm it reproduces the selection with no `FAMILY`.
- [x] 7.3 `openspec validate retire-model-families` passes.
