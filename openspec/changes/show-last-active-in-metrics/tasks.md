## 1. Carry the fields on the stats shape

- [ ] 1.1 Add `LastActiveAt string \`json:"lastActiveAt,omitempty"\`` and `IdleSeconds int \`json:"idleSeconds,omitempty"\`` to `metrics.Stats` in `internal/metrics/metrics.go`, commented as `daemon.StatusResponse` does — absent until an engine has run, `idleSeconds` derived at read time
- [ ] 1.2 Add the two fields to the `Stats` schema in `docs/openapi.yaml`, with descriptions matching those already on `StatusResponse`
- [ ] 1.3 Run `go test ./internal/daemon/ -run OpenAPI` and confirm the schema/struct comparison passes

## 2. Populate them in the daemon

- [ ] 2.1 Extract the `snapshot()` → `lastActiveAt`/`idleSeconds` conversion out of `Daemon.Status` in `internal/daemon/daemon.go` into a small unexported helper on `Daemon`, leaving `Status`'s behaviour identical
- [ ] 2.2 Call that helper from `Daemon.Metrics` **before** the `state != StateRunning` early return, so a stopped or crashed engine still reports its last activity
- [ ] 2.3 Test: `/v1/metrics` reports `lastActiveAt` and `idleSeconds` for a running engine that has done work
- [ ] 2.4 Test: `/v1/metrics` and `/v1/status` report the same `lastActiveAt` for the same daemon at the same moment
- [ ] 2.5 Test: `/v1/metrics` omits both fields on a daemon that has never started an engine
- [ ] 2.6 Test: after a stop, `/v1/metrics` still reports `lastActiveAt` and `idleSeconds` while omitting token and system figures
- [ ] 2.7 Test: polling `/v1/metrics` repeatedly with unchanged counters does not move `lastActiveAt` — reading the record must not count as activity

## 3. Render it in the shared metrics formatters

- [ ] 3.1 Add a shared helper to `cmd/outfit/metrics_render.go` that renders the `last active <d> ago` line from a last-active string and an idle-seconds int, gated on the string being non-empty and using `formatDuration` — the same wording and formatter as `cmd/outfit/fleet.go:109`
- [ ] 3.2 Call it in `formatMetricsBar` (`cmd/outfit/remote.go:708`) between the header line and `renderStatBars`, indented to the bar-label column, and **outside** the `state != "running"` early return so a stopped endpoint still shows it
- [ ] 3.3 Call it in `formatMetricsTable` (`cmd/outfit/remote.go:625`) as a `last active:` row beside `uptime:`, padded to the existing key column, and likewise before the non-running early return
- [ ] 3.4 Confirm `formatMetricsJSON` needs no change — it marshals the response struct, so the fields appear once task 4 adds them
- [ ] 3.5 Confirm `outfit fleet metrics` picks the line up through the shared helper with no edit to `cmd/outfit/fleet.go`, and that `fleet status` is unchanged

## 4. Relay it through the cloud path

- [ ] 4.1 Add `LastActiveAt string \`json:"lastActiveAt"\`` and `IdleSeconds int \`json:"idleSeconds"\`` to `remote.StatsResponse` in `internal/remote/remote.go`, matching the tags the Lambda emits
- [ ] 4.2 Add optional `lastActiveAt?: string` and `idleSeconds?: number` to `DaemonMetrics` in `remote/lambda/shared/daemon.ts` and to `StatsResult` in `remote/lambda/shared/stats.ts`
- [ ] 4.3 Copy the two fields through in `remote/lambda/stats/index.ts` alongside the other daemon-sourced fields, leaving them absent when the daemon reply omits them or the daemon was unreachable
- [ ] 4.4 Extend the Lambda's stats tests to cover the fields present, absent, and a `DAEMON_UNREACHABLE` reply

## 5. Render tests

- [ ] 5.1 Test: bar format shows the line under the header and above the bars for a running endpoint with activity
- [ ] 5.2 Test: bar format shows the line and no bars for a stopped endpoint with a known last-active time
- [ ] 5.3 Test: bar and table formats omit the line entirely when `lastActiveAt` is empty
- [ ] 5.4 Test: a present `lastActiveAt` with `idleSeconds` zero renders `last active 0s ago` in bar and table — the omit-at-zero trap from design.md D3
- [ ] 5.5 Test: table format shows a `last active:` row aligned with the other keys
- [ ] 5.6 Test: JSON format carries both fields through unformatted
- [ ] 5.7 Test: `outfit fleet metrics` shows the line for a node with activity and omits it for one without

## 6. Docs

- [ ] 6.1 `docs/http-api.md` — list the two fields under `GET /v1/metrics` and say they come from the same record as `/v1/status`, including for a stopped engine
- [ ] 6.2 `docs/commands/remote.md` — note that the metrics output reports when the endpoint last did work, and that it needs a control plane redeployed with `pnpm deploy` to appear
- [ ] 6.3 `docs/commands/fleet.md` — extend the Metrics section to mention the line, pointing at the existing "last active" explanation in the status section rather than repeating the wording rationale
- [ ] 6.4 `docs/commands/serve.md` — update the sentence about the 15-second sampler so it says both `/v1/status` and `/v1/metrics` report the figure
- [ ] 6.5 Update the sample metrics output in `README.md` if it shows one

## 7. Verify

- [ ] 7.1 `gofmt` the changed Go files
- [ ] 7.2 `go test ./... -cover` passes with coverage at or above 80%
- [ ] 7.3 Run the Lambda test suite (`pnpm test` in `remote/`)
- [ ] 7.4 Check an old-daemon reply (no fields) against the new CLI renders exactly today's output — the graceful degradation in design.md D5
