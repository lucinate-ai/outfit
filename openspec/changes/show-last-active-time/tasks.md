## 1. Carry the fields on the stats shape

- [x] 1.1 Add `LastActiveAt string \`json:"lastActiveAt,omitempty"\`` and `IdleSeconds int \`json:"idleSeconds,omitempty"\`` to `metrics.Stats` in `internal/metrics/metrics.go`, commented as `daemon.StatusResponse` does — absent until an engine has run, `idleSeconds` derived at read time
- [x] 1.2 Add the two fields to the `Stats` schema in `docs/openapi.yaml`, with descriptions matching those already on `StatusResponse`
- [x] 1.3 Run `go test ./internal/daemon/ -run OpenAPI` and confirm the schema/struct comparison passes

## 2. Populate them in the daemon

- [x] 2.1 Extract the `snapshot()` → `lastActiveAt`/`idleSeconds` conversion out of `Daemon.Status` in `internal/daemon/daemon.go` into a small unexported helper on `Daemon`, leaving `Status`'s behaviour identical
- [x] 2.2 Call that helper from `Daemon.Metrics` **before** the `state != StateRunning` early return, so a stopped or crashed engine still reports its last activity
- [x] 2.3 Test: `/v1/metrics` reports `lastActiveAt` and `idleSeconds` for a running engine that has done work
- [x] 2.4 Test: `/v1/metrics` and `/v1/status` report the same `lastActiveAt` for the same daemon at the same moment
- [x] 2.5 Test: `/v1/metrics` omits both fields on a daemon that has never started an engine
- [x] 2.6 Test: after a stop, `/v1/metrics` still reports `lastActiveAt` and `idleSeconds` while omitting token and system figures
- [x] 2.7 Test: polling `/v1/metrics` repeatedly with unchanged counters does not move `lastActiveAt` — reading the record must not count as activity

## 3. Render it in the shared metrics formatters

- [x] 3.1 Add a shared helper to `cmd/outfit/metrics_render.go` that renders the `last active <d> ago` line from a last-active string and an idle-seconds int, gated on the string being non-empty and using `formatDuration` — the same wording and formatter as `cmd/outfit/fleet.go:109`
- [x] 3.2 Call it in `formatMetricsBar` (`cmd/outfit/remote.go:708`) between the header line and `renderStatBars`, indented to the bar-label column, and **outside** the `state != "running"` early return so a stopped endpoint still shows it
- [x] 3.3 Call it in `formatMetricsTable` (`cmd/outfit/remote.go:625`) as a `last active:` row beside `uptime:`, padded to the existing key column, and likewise before the non-running early return
- [x] 3.4 Confirm `formatMetricsJSON` needs no change — it marshals the response struct, so the fields appear once task 4 adds them
- [x] 3.5 Call the shared helper from `renderFleetMetrics` in `cmd/outfit/fleet.go`, before its non-running `continue`. (Corrected during implementation: this task assumed fleet needed no edit, but `renderFleetMetrics` has its own render loop rather than going through `formatMetricsBar`/`formatMetricsTable`, so it needs its own call site. `fleet status` is unchanged, as planned.)

## 4. Relay it through the cloud path

- [x] 4.1 Add `LastActiveAt string \`json:"lastActiveAt"\`` and `IdleSeconds int \`json:"idleSeconds"\`` to `remote.StatsResponse` in `internal/remote/remote.go`, matching the tags the Lambda emits
- [x] 4.2 Add optional `lastActiveAt?: string` and `idleSeconds?: number` to `DaemonMetrics` in `remote/lambda/shared/daemon.ts` and to `StatsResult` in `remote/lambda/shared/stats.ts`
- [x] 4.3 Copy the two fields through in `remote/lambda/stats/index.ts` alongside the other daemon-sourced fields, leaving them absent when the daemon reply omits them or the daemon was unreachable
- [x] 4.4 Extend the Lambda's stats tests to cover the fields present, absent, and a `DAEMON_UNREACHABLE` reply

## 5. Report it from `outfit remote status`

- [x] 5.1 Add `LastActiveAt string \`json:"lastActiveAt"\`` and `IdleSeconds int \`json:"idleSeconds"\`` to `remote.Response` in `internal/remote/remote.go`, grouped and commented as status-specific fields
- [x] 5.2 In the `status(env)` branch of `remote/lambda/start/index.ts`, run `DAEMON_STATUS_CMD` via `runShellCommand` concurrently with `checkHealth` (after `isSsmAgentOnline` passes), parse it with the existing `parseDaemonStatus`, and copy `lastActiveAt`/`idleSeconds` into the reply
- [x] 5.3 Wrap that fetch so an SSM error, an unreachable daemon or an unparseable reply leaves the fields absent and does not affect `healthy` or the status code
- [x] 5.4 Leave the non-running branch untouched — it returns before any SSM call, so a stopped or undeployed environment reports no figure
- [x] 5.5 Leave `start`'s ready reply alone: an engine start counts as activity, so reporting it there would always say `0s`
- [x] 5.6 Print a `last active:` line in `cmdRemoteStatus` (`cmd/outfit/remote.go:519`) after `healthy`/`base_url`, gated on `LastActiveAt != ""` and formatted with `formatDuration`
- [x] 5.7 Lambda test: a running instance with a daemon reporting activity puts both fields in the reply
- [x] 5.8 Lambda test: the health check and the daemon fetch are issued concurrently, not in sequence
- [x] 5.9 Lambda test: an unreachable or unparseable daemon reply leaves the fields absent, `healthy` unchanged, and the response still 200
- [x] 5.10 Lambda test: a stopped instance's reply carries neither field and makes no SSM call
- [x] 5.11 CLI test: `outfit remote status` prints `last active <d> ago` when the reply carries a timestamp, and omits the line when it does not

## 6. Render tests

- [x] 6.1 Test: bar format shows the line under the header and above the bars for a running endpoint with activity
- [x] 6.2 Test: bar format shows the line and no bars for a stopped endpoint with a known last-active time
- [x] 6.3 Test: bar and table formats omit the line entirely when `lastActiveAt` is empty
- [x] 6.4 Test: a present `lastActiveAt` with `idleSeconds` zero renders `last active 0s ago` in bar, table and `remote status` — the omit-at-zero trap from design.md D3
- [x] 6.5 Test: table format shows a `last active:` row aligned with the other keys
- [x] 6.6 Test: JSON format carries both fields through unformatted
- [x] 6.7 Test: `outfit fleet metrics` shows the line for a node with activity and omits it for one without

## 7. Docs

- [x] 7.1 `docs/http-api.md` — list the two fields under `GET /v1/metrics` and say they come from the same record as `/v1/status`, including for a stopped engine
- [x] 7.2 `docs/commands/remote.md` — note that both `status` and `metrics` report when the endpoint last did work; that it needs a control plane redeployed with `pnpm deploy` to appear; and that a stopped instance cannot report it because reaching the daemon needs a running box
- [x] 7.3 `docs/commands/fleet.md` — extend the Metrics section to mention the line, pointing at the existing "last active" explanation in the status section rather than repeating the wording rationale
- [x] 7.4 `docs/commands/serve.md` — update the sentence about the 15-second sampler so it says both `/v1/status` and `/v1/metrics` report the figure
- [x] 7.5 Update the sample metrics or status output in `README.md` if it shows one

## 8. Verify

- [x] 8.1 `gofmt` the changed Go files
- [x] 8.2 `go test ./... -cover` passes with coverage at or above 80%
- [x] 8.3 Run the Lambda test suite (`pnpm test` in `remote/`)
- [x] 8.4 Check an old-daemon reply (no fields) against the new CLI renders exactly today's output for both `remote metrics` and `remote status` — the graceful degradation in design.md D5
- [x] 8.5 Confirm `outfit remote status` still performs no TCP probe and has no side effects
