# Proposal

## Why

GPU utilisation is collected only from `nvidia-smi`, so a macOS host — the
ordinary home of a local `spinloop serve`/daemon node, and where most harness
users run — reports engine stats and CPU/RAM with no GPU figure at all
(issue #217; the collector itself flags the gap in `internal/metrics`). The
metrics plumbing already treats every GPU figure as optional, so filling the
gap is one collector branch plus one parser.

## What Changes

- On macOS the collector gains a GPU source: the IORegistry accelerator node
  (via `ioreg`), from which it reports GPU utilisation and the GPU's name
  (e.g. "Apple M5 Max"). Absent an accelerator node (a VM, say), the GPU
  stat stays absent and silent, as it is today.
- GPU memory and temperature are not reported on macOS — Apple Silicon has
  unified memory with no per-GPU total, and temperature needs root — so the
  renderers stop presenting an absent figure as a zero: no "GPU mem" series
  where no GPU ever reports a total, and the table format's `mem=`/`temp=`
  segments are omitted rather than drawn `0B/0B`/`0C`.
- History, bar/gauge/table formats, the dashboard, and the wire shape are
  otherwise unchanged: existing series and formats pick the GPU up with no
  format-specific work.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `engine-metrics`: "System stats collection" gains a macOS GPU source
  (utilisation and name from the IORegistry accelerator; memory/temperature
  omitted where the host reports none). "Graceful platform degradation"
  replaces the scenario that a macOS host lacks GPU stats — macOS now has a
  GPU source — with the host-without-an-accelerator case staying silent.
- `remote-metrics-bar-format`: the GPU memory series (bar and gauge) is drawn
  only where the reading or a retained sample reports a GPU memory total; a
  utilisation-only GPU draws its util series alone. Table GPU lines omit the
  memory and temperature segments when the reading carries none.

## Impact

- `internal/metrics`: `collect.go` darwin branch in `gpus()`, a new IORegistry
  parser in `parse.go`; fixtures in `metrics_test.go`.
- `cmd/spinloop/metrics_render.go`: `currentGPUMem`/`barSeriesList` series
  selection and `renderGPUTable` segment omission; dashboard and serve view
  inherit through the shared renderers.
- `docs/maintainer/internals.md` note on the sampling cost precedent if
  warranted; no API, OpenAPI, or wire-shape change (`GpuStat` fields already
  carry the zero-means-absent convention).

Assumption (from the issue's scope, "Sample the GPU util"): utilisation and
name only on macOS; GPU memory and temperature stay absent. Intel/AMD macOS
spellings of the IORegistry keys are accepted by the parser as a cheap
fallback but are out of testable scope here.
