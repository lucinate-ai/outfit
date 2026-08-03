## Context

The `remote stats` command in `cmd/outfit/remote.go` currently prints a human-readable key-value table to stdout. The subcommand is dispatched at line 46 of the `cmdRemote` switch, handled by `cmdRemoteStats` (lines 403-518). The library layer (`internal/remote.Stats`) and response types remain unchanged — only the CLI command name and output formatting change.

## Goals / Non-Goals

**Goals:**
- Rename `outfit remote stats` to `outfit remote metrics`
- Add `--format` flag: `table` (default, existing behaviour) and `json` (structured JSON)
- Add `--watch`/`-w` flag: poll metrics every 60 seconds
- Keep the library layer (`internal/remote`) untouched — types, `Stats()` function, and `callStats()` are unchanged
- Maintain `--cost` flag behaviour

**Non-Goals:**
- No backwards-compatibility alias for `stats`
- No server-side changes — the Lambda and its response types are unchanged
- No changes to `internal/remote` response structs or `Config.StatsURL`

## Decisions

**Rename function and dispatch, not refactor.** The `cmdRemoteStats` function will be renamed to `cmdRemoteMetrics` and the dispatch case updated. No extraction or structural refactor — the change is a label swap plus a new code path for JSON output.

**JSON output uses the existing `StatsResponse` struct directly.** The `remote.StatsResponse` type already carries the full response shape with proper JSON tags. The `--format=json` path will `json.Marshal` the response struct directly, producing clean, machine-parseable output. No intermediate struct or transformation layer is needed.

**Format flag validation at parse time.** Invalid `--format` values will error immediately (same pattern as other flag validations in the codebase), rather than silently falling through to table output.

**Cost data in JSON output is gated on `--cost`.** When `--format=json`, the cost estimate is only included in the output if `--cost` was also passed. This matches the table behaviour and avoids an unnecessary AWS Price List API call.

**Watch mode uses a simple timer loop.** When `--watch` is set, the command runs in a loop: query → format → print separator → sleep 60s → repeat. A separator line (`---`) is printed before each refresh to delineate outputs. The interval is fixed at 60 seconds — no `--interval` flag. `SIGINT`/`SIGTERM` stops the loop cleanly.

**Watch is incompatible with `--format=json` for streaming.** With `--format=json --watch`, each refresh produces a separate JSON object (newline-delimited JSON), not a JSON array. This keeps each line independently parseable.

## Risks / Trade-offs

- [Existing users scripting `remote stats` will break] → User requested no backwards compat; this is intentional
- [JSON output for cost uses a computed field not in the API response] → The cost is a derived value (uptime × on-demand price). For JSON, it will be added as a top-level `cost` field when `--cost` is used, alongside the raw `StatsResponse` fields. This means the JSON output isn't a direct `Marshal` of the struct when cost is enabled — a wrapper struct will be used for that case.
- [Watch mode blocks the terminal indefinitely] → Expected behaviour; user presses Ctrl+C to stop. Signal handler ensures clean exit.
- [Watch with table format produces interleaved output] → A separator line (`---`) precedes each refresh. The terminal is cleared between refreshes using ANSI escape codes for a clean display.
