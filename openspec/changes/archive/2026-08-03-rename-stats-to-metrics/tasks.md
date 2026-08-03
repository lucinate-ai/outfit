## 1. Rename subcommand from `stats` to `metrics`

- [x] 1.1 Rename `cmdRemoteStats` to `cmdRemoteMetrics` in `cmd/outfit/remote.go`
- [x] 1.2 Update dispatch switch: `case "stats"` to `case "metrics"`, handler to `cmdRemoteMetrics`
- [x] 1.3 Update usage string in `cmdRemote`: `stats` to `metrics` in the error messages
- [x] 1.4 Update `flag.NewFlagSet` name from `"remote stats"` to `"remote metrics"`
- [x] 1.5 Rename function comment block header

## 2. Add `--format` flag and JSON output

- [x] 2.1 Add `format` string flag (`--format`) with default `"table"` and valid values `table`, `json`
- [x] 2.2 Validate flag value at parse time; error on unknown format
- [x] 2.3 Extract table formatting logic into `formatMetricsTable(resp, withCost, price)` function
- [x] 2.4 Add JSON output path: marshal `StatsResponse` to JSON; when `--cost` is set, wrap in struct that includes cost field
- [x] 2.5 Dispatch on format flag to call table or JSON formatter
- [x] 2.6 Use `resp.InstanceType` for cost lookup instead of hardcoded `"g6e.xlarge"`

## 3. Add `--watch/-w` flag

- [x] 3.1 Add `watch` bool flag (`--watch`/`-w`) to `cmdRemoteMetrics`
- [x] 3.2 Refactor metrics query+format into `runMetricsOnce(cfg, format, withCost)` helper
- [x] 3.3 Add watch loop: call `runMetricsOnce`, print separator, sleep 60s, repeat
- [x] 3.4 Add signal handler for `SIGINT`/`SIGTERM` to break the loop cleanly
- [x] 3.5 Add separator line between refreshes (skip before first output)

## 4. Update completions

- [x] 4.1 Update `complete.go`: `stats` to `metrics` in remote subcommands list
- [x] 4.2 Add `--format` to remote flags list
- [x] 4.3 Add `--format` value mapping (`table`, `json`)
- [x] 4.4 Add `--watch` and `-w` to remote flags list

## 5. Update tests

- [x] 5.1 Rename `cmd/outfit/remote_test.go` test functions: `TestRemoteStats_*` to `TestRemoteMetrics_*`
- [x] 5.2 Update test assertions: command invocations from `stats` to `metrics`
- [x] 5.3 Add test: `TestRemoteMetrics_DefaultFormat` — default format is table
- [x] 5.4 Add test: `TestRemoteMetrics_JsonFormat` — JSON output is valid and contains expected fields
- [x] 5.5 Add test: `TestRemoteMetrics_JsonFormatWithCost` — JSON output includes cost when `--cost` is set
- [x] 5.6 Add test: `TestRemoteMetrics_InvalidFormat` — unknown format value produces error
- [x] 5.7 Add test: `TestRemoteMetrics_WatchMode` — watch flag triggers repeated queries (use short interval in test)
- [x] 5.8 Add test: `TestRemoteMetrics_WatchWithJson` — watch + json produces separate JSON objects per cycle
- [x] 5.9 Run `go test ./...` and verify all tests pass with >= 80% coverage

## 6. Run verification

- [x] 6.1 Run `go vet ./...`
- [x] 6.2 Run `gofmt -w ./...`
- [x] 6.3 Run `go test ./... -cover`