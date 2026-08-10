## 1. Daemon status response

- [x] 1.1 Add `Version` field to `daemon.StatusResponse` struct in `internal/daemon/daemon.go`
- [x] 1.2 Add `Version` field to `daemon.Daemon` struct in `internal/daemon/daemon.go`
- [x] 1.3 Populate `resp.Version` from `d.Version` in `daemon.Status()` method
- [x] 1.4 Update `docs/openapi.yaml` — add `version` field to `StatusResponse` schema
- [x] 1.5 Pass `version` from `cmd/outfit` when constructing `daemon.Daemon` in `serve_daemon.go` (`cmdDaemon` and `cmdServe`)
- [x] 1.6 Update `internal/daemon/openapi_test.go` to verify the new field

## 2. Remote stats response

- [x] 2.1 Add `Version` field to `remote.StatsResponse` struct in `internal/remote/remote.go`

## 3. CLI remote output

- [x] 3.1 Print `version` in `cmdRemoteStatus` (`cmd/outfit/remote.go`) when present in stats response
- [x] 3.2 Print `version` in `formatMetricsTable` (`cmd/outfit/remote.go`)
- [x] 3.3 Include `version` in `formatMetricsJSON` (`cmd/outfit/remote.go`)
- [x] 3.4 Optionally include version in `formatMetricsBar` header

## 4. CLI fleet output

- [x] 4.1 Update `fleetRow` in `cmd/outfit/fleet.go` to render `r.Status.Version` per node
- [x] 4.2 Add version column or inline it into the fleet status table output
- [x] 4.3 Update fleet status tests in `cmd/outfit/fleet_test.go` for version output

## 5. Tests

- [x] 5.1 Add daemon test: `Status()` returns version field
- [x] 5.2 Update `cmdRemoteStatus` tests in `cmd/outfit/remote_test.go` for version output
- [x] 5.3 Update remote test stubs to include version in stats response
- [x] 5.4 Run full test suite: `go test ./... -cover`

## 6. Remote Lambda (TypeScript, `remote/`)

- [ ] 6.1 Update stats Lambda to read version from daemon `/v1/status` and include it in the reply
- [ ] 6.2 Deploy updated Lambda (manual step — not part of CI)
