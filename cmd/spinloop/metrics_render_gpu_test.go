package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spinloop-ai/spinloop/internal/metrics"
)

// A GPU that reports utilisation only (macOS) draws its utilisation series and
// no memory series, on the bar and gauge surfaces alike.
func TestUtilisationOnlyGPUDrawsNoMemorySeries(t *testing.T) {
	macGPU := []metrics.GpuStat{{Index: 0, Name: "Apple M5 Max", Utilization: 56}}
	history := []metrics.HistorySample{
		{GPUs: []metrics.HistoryGPU{{Index: 0, Util: 40}}},
		{GPUs: []metrics.HistoryGPU{{Index: 0, Util: 56}}},
	}
	var bar, gauge bytes.Buffer
	renderStatBars(&bar, nil, nil, macGPU, history, barLineW)
	renderStatGauges(&gauge, nil, nil, macGPU)
	for name, out := range map[string]string{"bar": bar.String(), "gauge": gauge.String()} {
		if !strings.Contains(out, "GPU util") || !strings.Contains(out, " 56%") {
			t.Errorf("%s: missing GPU util: %q", name, out)
		}
		if strings.Contains(out, "GPU mem") {
			t.Errorf("%s: drew a GPU mem series: %q", name, out)
		}
	}
}

// A GPU that reports a memory total still draws both series.
func TestGPUWithMemoryTotalDrawsBothSeries(t *testing.T) {
	gpus := []metrics.GpuStat{{Index: 0, Name: "NVIDIA L40S", Utilization: 12,
		MemoryUsed: 8 << 30, MemoryTotal: 16 << 30, Temperature: 42}}
	var bar, gauge bytes.Buffer
	renderStatBars(&bar, nil, nil, gpus, nil, barLineW)
	renderStatGauges(&gauge, nil, nil, gpus)
	for name, out := range map[string]string{"bar": bar.String(), "gauge": gauge.String()} {
		if !strings.Contains(out, "GPU util") || !strings.Contains(out, "GPU mem") || !strings.Contains(out, " 50%") {
			t.Errorf("%s: %q", name, out)
		}
	}
}

func TestRenderGPUTableOmitsAbsentFigures(t *testing.T) {
	mac := metrics.GpuStat{Index: 0, Name: "Apple M5 Max", Utilization: 56}
	nvidia := metrics.GpuStat{Index: 0, Name: "NVIDIA L40S", Utilization: 12,
		MemoryUsed: 8 << 30, MemoryTotal: 16 << 30, Temperature: 42}

	var b bytes.Buffer
	renderGPUTable(&b, []metrics.GpuStat{mac})
	if got, want := b.String(), "\n  GPU 0: Apple M5 Max  util=56%\n"; got != want {
		t.Errorf("utilisation-only = %q, want %q", got, want)
	}

	b.Reset()
	renderGPUTable(&b, []metrics.GpuStat{nvidia})
	if got, want := b.String(), "\n  GPU 0: NVIDIA L40S  util=12%  mem=8.0 GB/16.0 GB  temp=42C\n"; got != want {
		t.Errorf("full reading = %q, want %q", got, want)
	}

	// Two utilisation-only GPUs: the totals line drops its memory figure.
	other := mac
	other.Index = 1
	b.Reset()
	renderGPUTable(&b, []metrics.GpuStat{mac, other})
	if strings.Contains(b.String(), "mem") || !strings.Contains(b.String(), "  avg util: 56%\n") {
		t.Errorf("two utilisation-only GPUs = %q", b.String())
	}

	// A mixed pair keeps the totals line whole.
	b.Reset()
	renderGPUTable(&b, []metrics.GpuStat{nvidia, other})
	if !strings.Contains(b.String(), "avg util: 34%  total mem: 8.0 GB/16.0 GB") {
		t.Errorf("mixed pair = %q", b.String())
	}
}
