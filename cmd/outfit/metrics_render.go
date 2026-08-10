// Shared metrics rendering. Both `outfit remote metrics` (one cloud endpoint)
// and `outfit fleet metrics` (a node per machine) display the same
// internal/metrics stats, so the parts that draw those stats live here and
// each caller supplies only its own heading — the environment and instance
// type for remote, the node name for fleet.

package main

import (
	"fmt"
	"io"

	"github.com/lucinate-ai/outfit/internal/metrics"
)

// renderStatBars draws the resource bars: CPU, RAM, then each GPU's
// utilisation and memory. Used by the bar format on both sides.
func renderStatBars(w io.Writer, cpu *metrics.CpuStat, mem *metrics.MemoryStat, gpus []metrics.GpuStat) {
	if cpu != nil {
		renderBar(w, "CPU", cpu.Utilization)
	}
	if mem != nil {
		pct := 0.0
		if mem.Total > 0 {
			pct = float64(mem.Used) / float64(mem.Total) * 100
		}
		renderBar(w, "RAM", pct)
	}
	for _, g := range gpus {
		prefix := "GPU"
		if len(gpus) > 1 {
			prefix = fmt.Sprintf("GPU %d", g.Index)
		}
		renderBar(w, prefix+" util", float64(g.Utilization))
		memPct := 0.0
		if g.MemoryTotal > 0 {
			memPct = float64(g.MemoryUsed) / float64(g.MemoryTotal) * 100
		}
		renderBar(w, prefix+" mem", memPct)
	}
}

// renderTokenLines draws the engine's token and request counters, the block
// both formats share.
func renderTokenLines(w io.Writer, tokens *metrics.TokenStats) {
	if tokens == nil {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  running:          %d\n", tokens.Running)
	fmt.Fprintf(w, "  prompt tokens:    %d\n", tokens.PromptTokens)
	fmt.Fprintf(w, "  generation tokens: %d\n", tokens.GenerationTokens)
	fmt.Fprintf(w, "  requests:         %d\n", tokens.Requests)
}

// renderGPUTable draws the per-GPU lines of the table format, plus the
// totals line when there is more than one GPU.
func renderGPUTable(w io.Writer, gpus []metrics.GpuStat) {
	if len(gpus) == 0 {
		return
	}
	fmt.Fprintln(w)
	for _, g := range gpus {
		fmt.Fprintf(w, "  GPU %d: %s  util=%d%%  mem=%s/%s  temp=%dC\n",
			g.Index, g.Name, g.Utilization, formatBytes(g.MemoryUsed), formatBytes(g.MemoryTotal), g.Temperature)
	}
	if len(gpus) > 1 {
		var totalUtil, totalMemUsed, totalMemTotal int64
		for _, g := range gpus {
			totalUtil += int64(g.Utilization)
			totalMemUsed += g.MemoryUsed
			totalMemTotal += g.MemoryTotal
		}
		avgUtil := int(totalUtil) / len(gpus)
		fmt.Fprintf(w, "  avg util: %d%%  total mem: %s/%s\n",
			avgUtil, formatBytes(totalMemUsed), formatBytes(totalMemTotal))
	}
}

// renderCPUMemTable draws the table format's CPU and RAM lines.
func renderCPUMemTable(w io.Writer, cpu *metrics.CpuStat, mem *metrics.MemoryStat) {
	if cpu != nil {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  CPU: %.0f%% util\n", cpu.Utilization)
	}
	if mem != nil {
		pct := float64(mem.Used) / float64(mem.Total) * 100
		fmt.Fprintf(w, "  RAM: %s/%s (%.0f%%)\n", formatBytes(mem.Used), formatBytes(mem.Total), pct)
	}
}

// renderCollectionErrors reports metric-collection problems on stderr, so the
// data on stdout stays clean for piping.
func renderCollectionErrors(w io.Writer, errs []string) {
	if len(errs) == 0 {
		return
	}
	fmt.Fprintln(w, "metric collection errors:")
	for _, e := range errs {
		fmt.Fprintf(w, "  - %s\n", e)
	}
}
