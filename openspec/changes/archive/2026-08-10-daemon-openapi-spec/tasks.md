## 1. One route list

- [x] 1.1 In `internal/daemon/api.go`, replace the inline `mux.HandleFunc`
  patterns with a `Routes() []string` returning the `"METHOD /path"` patterns,
  and have `Handler` register its handlers from that list so the mux and the
  list cannot diverge.
- [x] 1.2 Confirm the existing API tests still pass unchanged — this is a
  refactor, not a behaviour change.

## 2. The spec

- [x] 2.1 Write `docs/openapi.yaml` (OpenAPI 3.1): `GET /v1/status`,
  `POST /v1/start`, `POST /v1/stop`, `GET /v1/metrics`, `PUT /v1/deploy-config`.
- [x] 2.2 Describe bearer auth as a security scheme, applied to every operation,
  with the 401 reply.
- [x] 2.3 Schema `StatusResponse` matching `daemon.StatusResponse`, including
  `lastActiveAt` and `idleSeconds`.
- [x] 2.4 Schema `Stats` matching `metrics.Stats`, with `TokenStats`,
  `GpuStat`, `CpuStat` and `MemoryStat`.
- [x] 2.5 Schema `DeployConfig` matching `remote.DeployConfig`, used as the
  `PUT /v1/deploy-config` body and the optional `POST /v1/start` body.
- [x] 2.6 Schema `Error` (`{error: string}`), and the status codes each
  operation returns — including the 409 on start-while-running.

## 3. The drift test

- [x] 3.1 Add `internal/daemon/openapi_test.go` that parses `docs/openapi.yaml`
  (stdlib only — a minimal YAML read, or convert on the fly; no new dependency).
- [x] 3.2 Compare `Routes()` against the spec's method/path pairs, both
  directions, failing with the offending route named.
- [x] 3.3 Reflect over each response struct's JSON tags and compare against the
  named schema's `properties`, both directions, failing with the field and
  schema named.
- [x] 3.4 Hold the struct→schema mapping in a literal table, with a comment
  saying a new response type needs a line here.
- [x] 3.5 Verify the test actually catches drift: add a field locally, watch it
  fail, remove it.

## 4. Publishing

- [x] 4.1 Add `release.extra_files` to `.goreleaser.yaml` attaching
  `docs/openapi.yaml` to the GitHub release.
- [x] 4.2 Check the config parses (`goreleaser check`, or a release dry run).

## 5. Docs and validation

- [x] 5.1 Link the spec from `docs/http-api.md`, keeping the prose for what a
  schema cannot express.
- [x] 5.2 Note the spec in `AGENTS.md` beside the daemon/API description, so the
  next person changing a route knows the test exists.
- [x] 5.3 `gofmt -w ./...`, `go vet ./...`, `go test ./... -cover`.
- [x] 5.4 `openspec validate daemon-openapi-spec --strict`.
