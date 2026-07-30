## 1. optionsRequired schema

- [ ] 1.1 Add `OptionsRequired []string` (yaml `optionsRequired`) to `Provider` in `internal/catalog/catalog.go`
- [ ] 1.2 In `BuildProviderBlock`, after merging options + optionsFromEnv, fail when any required option is missing/empty, naming the option and (when mapped) its env var
- [ ] 1.3 Document `optionsRequired` in the `providers.yaml` header comment

## 2. Vertex providers

- [ ] 2.1 Add `google-vertex` provider (Gemini): no npm, no apiKey, `location: global`, `optionsRequired: [project]`, optionsFromEnv `project`/`location`, no pi block
- [ ] 2.2 Add `google-vertex-anthropic` provider (Claude): same shape as 2.1
- [ ] 2.3 Spot-check option key (`location` vs `region`) and model-id forms against a current opencode build

## 3. Tests

- [ ] 3.1 `catalog` test: required option unset → error names option + env var; set → injected into block
- [ ] 3.2 `catalog` test: both Vertex providers build with no apiKey and expected options; `BuildPiProvider` rejects them
- [ ] 3.3 `outfit list` test/golden: both providers appear, opencode-only
- [ ] 3.4 `go test ./... -cover` stays >= 80%; run gofmt

## 4. Docs

- [ ] 4.1 Add a Vertex example to `docs/commands/add.md` beside the Bedrock one
- [ ] 4.2 Note ADC/`gcloud auth application-default login` and `GOOGLE_VERTEX_PROJECT` in the relevant docs
