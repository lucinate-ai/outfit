## Context

Currently `outfit remote status` calls the start Lambda's GET endpoint, which returns EC2 instance state and `/health` status but has no path to the daemon running on the box. The daemon already exposes its own status at `GET /v1/status`, including runner/model/uptime — the stats Lambda scrapes that endpoint over SSM for metrics. The binary already tracks `main.version` (set by goreleaser ldflags) and has a `version` command.

The daemon's `StatusResponse` struct currently has no version field. The stats Lambda's `StatsResponse` struct likewise has none. Fleet nodes already read `/v1/status` directly, so they can render version once it exists in the response.

## Goals / Non-Goals

**Goals:**
- Add `version` to the daemon's `StatusResponse` so any caller reading `/v1/status` gets it
- Add `version` to the stats Lambda's reply so `outfit remote metrics` can show it
- Add `version` to `outfit remote status` output, sourced from the stats Lambda (reusing the same SSM path)
- Update `formatMetricsTable` and `formatMetricsJSON` to render the version
- Add version to `outfit fleet status` per-node display, read directly from `/v1/status`

**Non-Goals:**
- Adding version to `outfit remote status` when the instance is stopped — the daemon isn't reachable, so there's nothing to read. Reporting "unavailable" or omitting the field is acceptable.
- Modifying the remote TypeScript Lambda source in this change. The Lambda-side change is necessary but lives in `remote/` (a TypeScript CDK project). The Go-side change is complete on its own; the Lambda change is a one-line field passthrough that the remote deployment owner applies.

## Decisions

**Version source: daemon `/v1/status`, not a separate endpoint.** The daemon already serves status, and the stats Lambda already curls it (for `/v1/metrics`). Adding a field to an existing response is cheaper than a new endpoint and keeps the SSM path the same.

**`remote status` reads via stats Lambda, not directly.** The start Lambda has no daemon access. The stats Lambda does (via SSM). Using the same path as `remote metrics` avoids adding a second SSM call. When the instance is stopped, the stats Lambda returns `state: stopped` with no version — that's the natural "unavailable" state.

**Version in `StatusResponse`, not `Metrics.Stats`.** The version is a property of the daemon binary, not of the running engine's metrics. It belongs in status. The stats Lambda reads status for state/runner/model, so picking up version from the same call is free.

**Fleet reads version directly from the daemon.** Fleet nodes call `/v1/status` directly (no Lambda intermediary), so once the daemon includes `version` in its status response, fleet can render it immediately. No Lambda change needed for fleet.

**Pass-through field, not computed.** The version string is the build-time value (`main.version`). No comparison with a "expected" version — that would require the CLI to know what version was deployed, which it doesn't.

## Risks / Trade-offs

- **Stats Lambda must be redeployed first.** The Go CLI changes are harmless on their own — a stopped instance will simply not show a version. But the version won't appear in `remote metrics` until the remote Lambda is redeployed to include the field. This is acceptable: the CLI renders whatever the Lambda returns, and an empty version is simply omitted.
- **Version field is unverified.** There's no checksum or signature on the version string — it's whatever the daemon reports. This is sufficient for the "which release is running?" question; it doesn't claim cryptographic provenance.
- **Fleet version is only available when the daemon is reachable.** An unreachable node shows its failure outcome without a version — this is consistent with existing fleet status behaviour where unreachable nodes show no metrics or activity data.
