## 1. Refactor format functions to accept io.Writer

- [x] 1.1 Update `runMetricsOnce` signature to accept `io.Writer` parameter
- [x] 1.2 Refactor `formatMetricsTable` to write to `io.Writer` instead of `os.Stdout`
- [x] 1.3 Refactor `formatMetricsJSON` to write to `io.Writer` instead of `os.Stdout`
- [x] 1.4 Refactor `renderBar` to accept `io.Writer` parameter
- [x] 1.5 Update all callers of format functions to pass `os.Stdout`

## 2. Add bar format output

- [x] 2.1 Add `formatMetricsBar` function with header line, CPU/RAM/GPU bars, and token stats
- [x] 2.2 Add `renderBar` function with coloured block characters and percentage display
- [x] 2.3 Implement colour thresholds: green ≤80%, yellow 80–90%, red >90%
- [x] 2.4 Handle multi-GPU case with indexed labels (GPU 0, GPU 1, etc.)
- [x] 2.5 Handle stopped instance — show header only, no bars
- [x] 2.6 Add `bar` as a valid format option in `--format` flag validation

## 3. Make bar the default format

- [x] 3.1 Change default value of `--format` flag from `table` to `bar`
- [x] 3.2 Update flag help text to reflect new default
- [x] 3.3 Update tests that relied on table being the default to use `--format=table` explicitly

## 4. Watch mode screen clearing

- [x] 4.1 Replace separator line with ANSI screen clear (`\033[2J\033[H`) in `runMetricsWatch`
- [x] 4.2 Pre-render metrics output into `strings.Builder` before clearing screen
- [x] 4.3 Write buffered output after clear to eliminate blank-frame delay
- [x] 4.4 Update watch mode tests to verify correct output without separator assertions

## 5. Tests

- [x] 5.1 Add test for bar format output with all metrics present
- [x] 5.2 Add test for bar format with stopped instance
- [x] 5.3 Verify all existing metrics tests still pass with new default format
- [x] 5.4 Run full test suite with coverage check (≥80%)
