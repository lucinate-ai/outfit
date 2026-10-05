# Tasks

## 1. macOS GPU collection (internal/metrics)

- [x] 1.1 Add `ParseIOAcceleratorGPU(out string) []GpuStat` to `internal/metrics/parse.go`: scan `+-o` service blocks for `PerformanceStatistics`/`Performance Statistics` dictionaries, read `Device Utilization %` (accepting the AMD `Device Utilization (%)` spelling), name from `"model"` with the service name as fallback, index by order of appearance, memory and temperature left zero; verify with parser tests covering an Apple AGX fixture, an AMD-spelling fixture, a two-accelerator fixture, a block lacking the utilization key, and empty output yielding no GPUs.
- [x] 1.2 Give `Collector.gpus()` a darwin branch running `ioreg -rd1 -c IOAccelerator -w 0` through the injectable runner: parsed GPUs on success, silent nil on empty output, reported `gpu:` error on a non-zero exit; verify with collector tests feeding an `ioreg` fixture through the runner map, a silent-absence test, and an exit-failure test, keeping the Linux and CPU/RAM collector tests green.
- [x] 1.3 Update the comments that record the gap — the `internal/metrics` package doc, the `Collector` header, and the `gpus()` "issue #47" note — to describe the macOS accelerator source and its absent memory/temperature; verify with `go vet ./internal/metrics` and `go test ./internal/metrics/...` passing.

## 2. Rendering absent GPU figures (cmd/spinloop)

- [x] 2.1 Make `currentGPUMem` in `cmd/spinloop/metrics_render.go` return nil when `MemoryTotal == 0`, so `barSeriesList` drops a GPU's memory series only when no current reading and no retained sample carries a total; verify with tests that a utilisation-only GPU yields `GPU util` with no `GPU mem` in bar, combined, and gauge surfaces, and that an NVIDIA-shaped reading still yields both.
- [x] 2.2 Make `renderGPUTable` emit `mem=` and `temp=` segments only when the reading carries a total and a temperature respectively, and the multi-GPU totals line drop its `total mem:` figure when the summed total is zero; verify with table-format tests for a utilisation-only GPU, a full NVIDIA-shaped GPU, and a mixed pair keeping the totals line whole.
- [x] 2.3 Run the dashboard, serve-view, fleet, and metrics-render test suites and fix any golden outputs that expected the zeroed `GPU mem` line or `0B/0B` segments, confirming the shared renderers needed no format-specific change in dashboard or fleet code.

## 3. Documentation

- [x] 3.1 Grep `docs/` for claims that GPU figures are Linux/NVIDIA-only or that macOS nodes show no GPU, and update the affected pages (metrics/dashboard guides) to state that macOS reports utilisation only; verify each edited command or output sample still matches the code's actual output.

## 4. Integration checks

- [x] 4.1 Run `gofmt -l .`, `go vet ./...`, and `go test ./... -cover`, verifying a clean format, a clean vet, and total coverage at or above 80%.
- [x] 4.2 Build the binary (`go build -o spinloop ./cmd/spinloop`) and run `spinloop metrics --format=bar` and `--format=table` against a local macOS daemon with a running engine, verifying a live `GPU util` series labelled with the accelerator model and no `GPU mem` line or `mem=`/`temp=` segments.
