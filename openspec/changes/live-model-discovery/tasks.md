## 1. Discovery package

- [x] 1.1 Create `internal/discovery` with a short-timeout `*http.Client` (3s, not the remote package's 10 minutes) and a `Models(provider, baseURLOverride, resolve) ([]string, error)` entry point.
- [x] 1.2 Implement the uniform OpenAI-compatible query: `GET {baseURL}/models`, parse `data[].id`, return sorted; send `Authorization: Bearer <resolved key>` only when a key resolves. (Ollama and llama.cpp both serve `/v1/models`, so one path covers every discoverable provider — no separate Ollama `/api/tags` adapter needed.)
- [x] 1.3 `ResolveBaseURL` / `Discoverable`: a provider with no resolvable base URL (AWS Bedrock) is not discoverable and returns `ErrNotDiscoverable`.
- [x] 1.4 Resolve base URL with selection precedence (`--base-url`, `OUTFIT_BASE_URL`, optionsFromEnv, options.baseURL, then the Pi endpoint) via the same `resolve` closure the apply path uses; never write the key anywhere.

## 2. Caching and failure semantics

- [x] 2.1 In-process TTL cache (60s) keyed by resolved endpoint; a second lookup within the TTL returns cached models with no network call.
- [x] 2.2 Every failure path (network error, non-2xx, timeout, missing endpoint, bad JSON) returns an error the callers treat as "no models", never a fatal error.

## 3. Surfacing

- [x] 3.1 Add a `--models` flag to `outfit list` (plus an optional positional provider filter); when set, call discovery per listed provider and print discovered ids, with a `(none found)` note on empty/failure.
- [x] 3.2 Keep plain `outfit list` (no `--models`) network-free — it resolves no keys and makes no request.
- [x] 3.3 Source model completion in `cmd/outfit/complete.go` from discovery for the typed `--provider`; emit nothing on failure.

## 4. Tests

- [x] 4.1 Discovery package tests against httptest stubs: sorting, auth-header (sent only when resolved), base-URL precedence.
- [x] 4.2 Cache test: three lookups, one served endpoint hit (counting stub server).
- [x] 4.3 Failure tests: unreachable endpoint, 500 status, malformed body, not-discoverable — each yields an error/no models.
- [x] 4.4 `outfit list --models` command test against a stub; assert models printed, plain `list` makes no request, unknown provider filter errors.
- [x] 4.5 Completion test: discovered ids offered from a stub; non-discoverable provider offers nothing. Coverage: discovery 96%, cmd/outfit 90%.

## 5. Docs

- [x] 5.1 Document `outfit list --models` (and the provider positional) in `docs/commands/list.md`.
- [x] 5.2 Note in `docs/outfit-file.md` that `outfit list --models <provider>` shows model ids for `MODEL`.

## 6. Verify

- [x] 6.1 `go build ./...`, `gofmt -l` clean, `go test ./... -cover` green.
- [x] 6.2 Manual check against a local stub: `outfit list --models stub` prints live models; `--models` on a non-discoverable provider prints `(none found)` and exits 0; plain `list` stays offline.
- [x] 6.3 `openspec validate live-model-discovery` passes.
