## Why

The default metrics output is a verbose key-value table that's hard to scan quickly, and `--watch` mode appends each refresh with a separator line instead of redrawing in place. Both issues make monitoring less effective.

## What Changes

- Add a new `--format=bar` output mode that renders CPU, RAM, GPU utilization, and GPU memory as coloured progress bars (green ≤80%, yellow 80–90%, red >90%)
- Make `bar` the default format for `outfit remote metrics` (previously `table`)
- In `--watch` mode, clear the screen and redraw in place instead of appending with `---` separators
- Pre-render metrics into a buffer before clearing the screen to eliminate the blank-frame delay between refreshes

## Capabilities

### New Capabilities
- `remote-metrics-bar-format`: Bar graph output with colour-coded thresholds for resource utilization metrics

### Modified Capabilities
- `remote-stats`: Default output format changes from table to bar; watch mode now clears screen instead of appending; format validation includes new `bar` option

## Impact

- `cmd/outfit/remote.go`: New `formatMetricsBar`, `renderBar` functions; refactored format functions to accept `io.Writer`; updated `runMetricsOnce` and `runMetricsWatch`
- `cmd/outfit/remote_test.go`: New tests for bar format and updated tests that relied on table defaults
- No new dependencies — uses stdlib `io.Writer`, `strings.Builder`, and ANSI escape codes already in the codebase
