## Why

"Stats" is a vague name that doesn't convey the operational nature of this data. "Metrics" better describes the resource utilisation, token counts, and cost information the command reports. Adding structured JSON output enables scripting and automation alongside the existing human-readable table.

## What Changes

- Rename `outfit remote stats` to `outfit remote metrics` — the `stats` subcommand is removed entirely (no backwards-compatibility alias)
- Add `--format` flag with two values: `table` (default, current behaviour) and `json` (structured JSON output)
- Add `--watch`/`-w` flag to poll metrics every 60 seconds
- Existing `--cost` flag continues to work as before

## Capabilities

### New Capabilities

—

### Modified Capabilities

- `remote-stats`: Subcommand renamed from `stats` to `metrics`; new `--format` flag with `table` (default) and `json` output modes added; new `--watch/-w` flag for periodic polling

## Impact

- `cmd/outfit/remote.go`: Rename `cmdRemoteStats` to `cmdRemoteMetrics`, update dispatch switch, add `--format` flag, JSON output formatter, and `--watch/-w` polling loop
- `cmd/outfit/complete.go`: Update completion entries: `stats` → `metrics`, add `--format` and `--watch/-w` to remote flags
- `cmd/outfit/remote_test.go`: Update all test function names and assertions for `metrics` subcommand; add tests for JSON output and watch mode
- `internal/remote/remote_test.go`: No changes (library types remain the same)
- `openspec/specs/remote-stats/spec.md`: Delta spec to rename subcommand, add format and watch requirements
