## Why

"Report the node's outfit version in `outfit remote status`" (issue #56) shipped only halfway. The archived change `2026-08-15-remote-status-version` completed the daemon (`/v1/status` carries `version`), the CLI (status and metrics print it when present), and the fleet client, but left its section 6 unchecked: the stats Lambda only scrapes the daemon's `/v1/metrics`, so on any deployed environment the reply never contains a `version`, and `outfit remote status` / `outfit remote metrics` silently omit the line they were built to show.

## What Changes

- The stats Lambda (`remote/lambda/stats/index.ts`) reads the daemon's `version` from its `/v1/status` reply and includes it in its reply, scraping status in parallel with the existing `/v1/metrics` SSM call (the pattern the start Lambda already uses for its daemon probe).
- The Lambda-side types gain the field: `StatsResult` and `DaemonStatus` (the latter mirroring the Go `StatusResponse`, which already carries it).
- Absent version (daemon unreachable, or an old daemon without the field) is omitted from the reply without adding an error entry — same convention as `lastActiveAt`/`idleSeconds`.

No Go changes: the client (`StatsResponse.Version`), the printing in `remote status`/`remote metrics`, and the tests already exist and start receiving values once the Lambda is updated.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

(none — the required behaviour is already specified in `remote-version-reporting` and `remote-stats`; this change makes the control plane honour it. The change opts out of spec deltas via `skip_specs: true`.)

## Impact

- `remote/lambda/stats/index.ts` — parallel status scrape, version relay.
- `remote/lambda/shared/stats.ts` — `StatsResult.version`.
- `remote/lambda/shared/daemon.ts` — `DaemonStatus.version`.
- `remote/test/stats-relay.test.ts`, `remote/test/stats.test.ts` — command-aware SSM mocks and relay assertions.
- Live environments report a version only after the control plane is redeployed (`pnpm deploy` in `remote/`, manual); that follow-up is noted here rather than tracked as a task of this change.
