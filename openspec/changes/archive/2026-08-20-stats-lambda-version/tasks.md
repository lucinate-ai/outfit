## 1. Lambda types

- [x] 1.1 Add `version?: string` to `StatsResult` in `remote/lambda/shared/stats.ts`, with the relay comment matching the other daemon-sourced fields
- [x] 1.2 Add `version?: string` to `DaemonStatus` in `remote/lambda/shared/daemon.ts`, mirroring the Go `StatusResponse`

## 2. Stats Lambda handler

- [x] 2.1 In `remote/lambda/stats/index.ts`, scrape `/v1/status` (`DAEMON_STATUS_CMD`) in parallel with the existing metrics scrape and set `result.version` from the parsed status; omit it on failure or absence
- [x] 2.2 Keep a status-scrape failure silent when the metrics scrape succeeded — no new error entry, reply still `200`

## 3. Tests (`remote/`)

- [x] 3.1 Make the `runShellCommand` mock in `remote/test/stats-relay.test.ts` command-aware (`DAEMON_METRICS_CMD` vs `DAEMON_STATUS_CMD`), and give the `parseDaemonStatus` fixture in `remote/test/stats.test.ts` the new field
- [x] 3.2 Extend `remote/test/stats-relay.test.ts`: version carried through unchanged; absent when the daemon reports none; absent without error when the daemon is unreachable
- [x] 3.3 Run `pnpm test` and `pnpm build` in `remote/` and fix any fallout

## 4. Verification

- [x] 4.1 `go test ./...` still green (no Go edits expected)

Follow-up outside this change: redeploy the control plane (`pnpm deploy` in `remote/`) so live environments start reporting the version.
