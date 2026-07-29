## 1. Discovery package

- [ ] 1.1 Create `internal/discovery` with a short-timeout `*http.Client` (seconds, not the remote package's 10 minutes) and a `Discover(provider, resolve) ([]string, error)` entry point.
- [ ] 1.2 Implement the OpenAI-compatible adapter: `GET {baseURL}/models`, parse `data[].id`; send `Authorization: Bearer <resolved key>` when a key resolves.
- [ ] 1.3 Implement the Ollama adapter: `GET {baseURL}/api/tags`, parse `models[].name`.
- [ ] 1.4 Select the adapter by provider kind (derive from provider id / `pi.api`; add an optional `discovery:` catalogue field only if the mapping is ambiguous — decide here).
- [ ] 1.5 Resolve base URL with selection precedence (`--base-url`, `OUTFIT_BASE_URL`, catalogue) and the same `resolve` closure the apply path uses; never write the key anywhere.

## 2. Caching and failure semantics

- [ ] 2.1 Add an in-process TTL cache keyed by resolved endpoint; second lookup within TTL returns cached models with no network call.
- [ ] 2.2 Ensure every failure path (network error, non-2xx, timeout, missing key, bad JSON) returns an empty set, never a fatal error.

## 3. Surfacing

- [ ] 3.1 Add a `--models` flag to `outfit list`; when set, call discovery per provider (or the named provider) and print discovered ids under each provider's plumbing, with a "no models found" note on empty.
- [ ] 3.2 Keep plain `outfit list` (no `--models`) network-free and unchanged.
- [ ] 3.3 Source model completion in `cmd/outfit/complete.go` from discovery for a supported provider, scoped to the typed `--provider`; emit nothing on failure.

## 4. Tests

- [ ] 4.1 Table tests for each adapter against recorded JSON fixtures (OpenAI-compatible, Ollama).
- [ ] 4.2 Cache test: two lookups, one served endpoint hit (use a counting stub server).
- [ ] 4.3 Failure tests: unreachable endpoint, 500 status, malformed body, missing key — each yields no models and no error.
- [ ] 4.4 `outfit list --models` command test against a stub server; assert models printed and that plain `list` makes no request.
- [ ] 4.5 Completion test: discovered ids offered; endpoint error yields no candidates and no stderr. Keep coverage ≥ 80%.

## 5. Docs

- [ ] 5.1 Document `outfit list --models` in `docs/commands/list.md`.
- [ ] 5.2 Note in `docs/outfit-file.md` that `outfit list --models <provider>` shows model ids to put in `MODEL`.

## 6. Verify

- [ ] 6.1 `go build ./...`, `gofmt -l` clean, `go test ./... -cover` green.
- [ ] 6.2 Manual check against a local Ollama or llama.cpp server (or a stub): `outfit list --models <provider>` prints live models; the same offline prints plumbing + "no models found" and exits 0.
- [ ] 6.3 `openspec validate live-model-discovery` passes.
