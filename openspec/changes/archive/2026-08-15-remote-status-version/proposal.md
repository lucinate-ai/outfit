## Why

There's no way to see which outfit release a remote instance or fleet node is running. Version drift between what you deployed and what's actually on the box has caused real debugging time — an empty or mismatched `outfitVersion` in the AMI bake only fails deep in a bake or at runtime. Once the instance is up, `outfit remote status` reports EC2 state and health but not the outfit version on the node, so "is this node on the release I expect?" is unanswerable without SSHing in. The same gap exists for fleet nodes — `outfit fleet status` shows state and serving but not the daemon's version, making a partially-upgraded fleet hard to spot.

## What Changes

- Add a `version` field to the daemon's `/v1/status` response, populated from the build-time `main.version` variable.
- Include the daemon's version in the stats Lambda's reply, so it is available through the existing SSM-scrape path that `remote metrics` already uses.
- Print the outfit version in `outfit remote status` output when the instance is running (read from the stats Lambda); omit when stopped.
- Print the outfit version in `outfit remote metrics` output alongside the existing state/runner/model information.
- Print the outfit version per node in `outfit fleet status` output, read directly from the daemon's `/v1/status` response.

## Capabilities

### New Capabilities
- `remote-version-reporting`: Reports the outfit version running on a remote instance through `outfit remote status` and `outfit remote metrics`, and per-node through `outfit fleet status`, sourced from the daemon's status endpoint.

### Modified Capabilities
- `daemon-api`: The daemon's `/v1/status` response gains a `version` field.
- `remote-stats`: The stats Lambda reply gains a `version` field; `remote metrics` output includes it.
- `fleet-client`: `outfit fleet status` renders the outfit version per node alongside state and serving.

## Impact

- `internal/daemon/daemon.go` — `StatusResponse` struct gains `Version` field; daemon constructor or status method populates it.
- `internal/daemon/api.go` — no handler change needed (it serializes `StatusResponse`), but OpenAPI spec must include the new field.
- `internal/remote/remote.go` — `StatsResponse` struct gains `Version` field.
- `cmd/outfit/remote.go` — `cmdRemoteStatus` prints `version`; `formatMetricsTable` prints version line.
- `cmd/outfit/fleet.go` — `renderFleetStatus` and `fleetRow` include version per node.
- `docs/openapi.yaml` — `StatusResponse` schema gains `version`.
- Remote TypeScript Lambda code (`remote/`) — stats Lambda reads version from daemon `/v1/status` and includes it in its reply.
- Tests: daemon status tests, remote test stubs, CLI remote test assertions, and fleet status rendering tests updated for the new field.
