# Design

## Context

`internal/metrics.Collector` is a host-command collector with an injectable
runner and platform: each stat is one command plus one pure parser
(`nvidia-smi`/`vmstat`/`free` on Linux, `top`/`sysctl`/`vm_stat` on macOS),
and every figure is optional — a missing source is omitted silently, a
failing one is reported (`engine-metrics` spec). The GPU slot is the only
one with no macOS branch: `gpus()` returns `nil, nil` off Linux.

Verified on the development host (Apple M5 Max, macOS):
`ioreg -rd1 -c IOAccelerator -w 0` answers quickly with the AGX accelerator
node carrying

```
"PerformanceStatistics" = {... "Renderer Utilization %"=3,
  "Device Utilization %"=91, "TiledSceneBytes"=..., ...}
"model" = "Apple M5 Max"
```

and no GPU memory total (unified memory) and no temperature. A host with no
accelerator service answers with empty output and exit 0.

## Goals / Non-Goals

**Goals:**
- One utilisation figure per accelerator, per sampler tick, at the same
  trust level as the existing figures (fixture-testable, no privileges).
- Absent GPU memory/temperature stay absent through collection, history,
  and every renderer.

**Non-Goals:**
- GPU memory attribution on Apple Silicon (unified memory has no per-GPU
  total; any denominator would be the RAM bar restated).
- Per-process GPU attribution, MACb/ANE figures, Intel-Mac-specific
  validation, and the remote Lambda path (Linux-only, unchanged).

## Decisions

**Source: `ioreg -rd1 -c IOAccelerator -w 0` over the alternatives.**
`top` has no GPU line on current macOS; `powermetrics` needs root; a direct
IOKit call needs cgo, which would break the pure-Go leaf and cross-compile.
`ioreg` matches the package's existing shape (host command + text parser +
injectable runner) and is already the standard trick GPU monitors use.
`-w 0` turns line-wrapping off so the dictionary parses intact when stdout
is a pipe, not a tty.

**Parse the text, don't switch to plist XML.** A line-oriented scan of each
service block (`+-o …` headers) for `"PerformanceStatistics"`, then
`"Device Utilization %"`, falls out of the same fixture style as
`ParseGPUStats`; `-a` XML would need a plist decoder for three keys.
Per block: utilisation from `Device Utilization %` (the device-wide figure —
`Renderer`/`Tiler` are per-engine halves, not summed); name from `"model"`,
falling back to the service name; index = order of appearance, so the rare
multi-accelerator Mac still yields one `GpuStat` each with the labels the
formats already derive from `Index`. The parser also accepts the AMD
drivers' `"Device Utilization (%)"` inside a `"Performance Statistics"`
(spaced) dictionary as a fallback for Intel-Mac AMD cards — cheap, but not
verifiable on this host. A block with neither key contributes no GPU.
Empty output parses to `nil, nil` — the silent-absent case; a non-zero exit
is a present-and-failing error and surfaces as `gpu: …`, exactly like
`nvidia-smi` today.

**Absence in the renderers follows the shape's own rule.** `currentGPUMem`
returns nil (not `0`) when `MemoryTotal == 0`, so `barSeriesList` drops the
series when no retained sample carried a total either — the GPU then draws
its util line alone on every bar/gauge surface, dashboard tiles included.
`renderGPUTable` builds its `mem=`/`temp=` segments conditionally, and its
multi-GPU totals line guards the memory ratio against a summed-zero total.
Treating `Temperature == 0` as absent is safe: real GPU readings under load
never sit at zero.

**Sampling cost stays where the sampler already absorbs it.** The daemon
collects on its background tick, never inline in a handler, so the
accelerator dump (tens of KB, sub-second, on a loaded host — same precedent
as macOS CPU's `top -l 1`, see the `systemSample` comment) is invisible to
API latency. No cadence change.

## Risks / Trade-offs

- Apple could rename the dictionary or keys in a future macOS → the parser
  yields no GPU and the host degrades exactly as it does today: silently
  absent, not an error.
- `Device Utilization %` is what the driver samples; it may lag a
  sub-tick burst. Acceptable at a 15s cadence, where every existing figure
  is equally a sample.
- Dropping the zeroed `GPU mem` series changes existing gauge output on any
  hypothetical source that reports a zero total today; the new specs adopt
  that case deliberately (a zero bar for an unknown total was the lie).

## Migration Plan

No migration: additive on the collection side, and the render change is
covered by the two delta specs. Stale `GpuStat` wire data from older daemons
(NVIDIA) carries totals and renders unchanged. Update the now-false comments
naming this gap (`collect.go` header and `gpus()`, the `metrics` package
doc) as part of the implementation.
