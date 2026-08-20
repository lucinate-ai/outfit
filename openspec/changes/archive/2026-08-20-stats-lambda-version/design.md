# Design: stats Lambda relays the daemon's version

## Context

Issue #56 wants "is this node on the release I expect?" answerable without SSH. The version data path has three hops:

1. daemon → its `/v1/status` reply: **done** (`StatusResponse.Version`, wired from `main.version`, covered by the OpenAPI contract test).
2. stats Lambda → its reply: **missing** — the handler runs one SSM curl of `/v1/metrics` and parses only metrics; the reply has no `version`.
3. CLI: **done** — `StatsResponse.Version` is parsed from the stats reply; `remote status` (running instances only) and `remote metrics` print it when present; tests stub the values and pass.

This change closes hop 2. The daemon, Go client, CLI, and fleet side are untouched.

## Decisions

### Read the version from `/v1/status`, not `/v1/metrics`

The daemon already reports the version on `/v1/status` (added by the archived change). `/v1/metrics` serialises a `metrics.Stats`-shaped body — a version is not a metric, and adding it there would widen the daemon's API contract (openapi schema, Go struct, `engine-metrics` spec) for no gain. `DAEMON_STATUS_CMD` and `parseDaemonStatus` already exist in `remote/lambda/shared/daemon.ts` and are proven in the start and stop Lambdas.

### Run the two scrapes in parallel

The SSM round-trip dominates this handler's latency. The start Lambda already composes its daemon status probe (`readDaemonActivity`) with its other reads in a single `Promise.all`; the stats Lambda gets the same treatment — metrics scrape and status scrape side by side, with the same 30s/10s wait budgets as today. Total latency is unchanged.

### A status-scrape failure is silent

The version is an ornamental field, not a health signal — the start Lambda keeps its activity read out of `healthy` for exactly this reason. If the metrics scrape succeeds and the status scrape fails or lacks a version, the reply ships without `version` and no error entry is added. When the daemon is unreachable both scrapes fail, and the existing `daemon: unreachable or unrecognisable metrics reply` entry already covers it.

### Omit rather than empty

`result.version` stays `undefined` when unknown, so `JSON.stringify` drops the key — the same absence convention as `lastActiveAt`/`idleSeconds` in this handler. The Go formatters already treat an empty `Version` as "omit the line", so old Lambdas and old daemons degrade to today's output automatically.

## Alternatives considered

- **Add `version` to the daemon's `/v1/metrics` reply** — one scrape instead of two, but changes a serialised contract that also serves `fleet metrics` and the OpenAPI check, and puts a non-metric in a metrics endpoint.
- **Give `remote status` its own SSM read of the daemon** — the issue's option 2; duplicates the hop the stats Lambda now makes, and `remote status` already calls the stats Lambda for the version on running instances, so the plumbing would be dead.
