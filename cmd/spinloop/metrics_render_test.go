package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/spinloop-ai/spinloop/internal/fleet"
	"github.com/spinloop-ai/spinloop/internal/metrics"
	"github.com/spinloop-ai/spinloop/internal/remote"
)

func ptrPct(v float64) *float64 { return &v }

func ptrInt(v int) *int { return &v }

func TestBarGlyph(t *testing.T) {
	cases := []struct {
		pct  float64
		want rune
	}{
		{0, '▁'}, {12.4, '▁'}, {12.5, '▁'}, {30, '▃'}, {50, '▄'},
		{75, '▆'}, {87.4, '▇'}, {87.5, '▇'}, {100, '▇'},
	}
	for _, c := range cases {
		if got := barGlyph(c.pct); got != c.want {
			t.Errorf("barGlyph(%v) = %c, want %c", c.pct, got, c.want)
		}
	}
}

// The sparkline never draws a full block: the highest value caps at the
// seven-eighths glyph, so a maxed row leaves a sliver of space above it and
// adjacent rows read as separate bars.
func TestBarGlyphNeverFullBlock(t *testing.T) {
	for pct := 0.0; pct <= 100.0; pct += 0.5 {
		if g := barGlyph(pct); g == '█' {
			t.Fatalf("barGlyph(%v) = full block, want seven-eighths or less", pct)
		}
	}
	if g := barGlyph(100); g != '▇' {
		t.Errorf("barGlyph(100) = %c, want ▇", g)
	}
}

func TestPoolMaxKeepsPeaks(t *testing.T) {
	// A pool keeps its highest reading, so the spike a threshold is about
	// survives the downsampling instead of being averaged away.
	got := poolMax([]float64{1, 9, 2, 8, 3, 7}, 3)
	if len(got) != 3 || got[0] != 9 || got[1] != 8 || got[2] != 7 {
		t.Errorf("poolMax = %v, want [9 8 7]", got)
	}
	// A series no wider than the draw is passed through untouched.
	in := []float64{1, 2, 3}
	if got := poolMax(in, 5); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("an unwidened series changed: %v", got)
	}
	// An uneven split pools the same values, one column each.
	got = poolMax([]float64{1, 5, 2, 9, 3, 4, 8}, 3)
	if len(got) != 3 || got[0] != 5 || got[1] != 9 || got[2] != 8 {
		t.Errorf("uneven pool = %v, want [5 9 8]", got)
	}
	// The newest sample lands in the final column: at the tile's width a
	// 29-sample window splits unevenly, and the split's rounding leaves the
	// last reading out of that column — and the trailing figure is the last
	// reading's value, so it must be in the pool. The values rise, so the
	// last column's maximum is its newest sample only if the sample is there.
	vals := make([]float64, 29)
	for i := range vals {
		vals[i] = float64(i + 1)
	}
	if got := poolMax(vals, 25); got[24] != 29 {
		t.Errorf("the newest sample dropped out of the last pool: %v, want 29", got[24])
	}
}

func TestRenderSparkline(t *testing.T) {
	var b bytes.Buffer
	renderSparkline(&b, "CPU", []float64{20, 30}, 40)
	// The trailing figure is the latest sample's percentage — the exact value
	// the last glyph approximates — not the series' first.
	want := "  CPU       " + strings.Repeat(" ", 38) + "▂" + ansiGreen + "▃" + ansiReset + " 30%\n"
	if got := b.String(); got != want {
		t.Errorf("sparkline = %q, want %q", got, want)
	}
}

// Only the final glyph takes the state colour, on the gauge's 80/90
// thresholds, whatever the rest of the window did — the window here holds a
// red spike earlier that must stay uncoloured.
func TestRenderSparklineColoursOnlyTheLastPoint(t *testing.T) {
	cases := []struct {
		last float64
		want string
	}{
		{79.9, ansiGreen + "▆" + ansiReset + " 80%\n"},
		{85, ansiYellow + "▆" + ansiReset + " 85%\n"},
		{95, ansiRed + "▇" + ansiReset + " 95%\n"},
	}
	for _, c := range cases {
		var b bytes.Buffer
		renderSparkline(&b, "CPU", []float64{5, 95, c.last}, 40)
		if !strings.HasSuffix(b.String(), c.want) {
			t.Errorf("last point %v: %q, want suffix %q", c.last, b.String(), c.want)
		}
		// Every earlier glyph is uncoloured: the escapes appear once, around
		// the final glyph only.
		if n := strings.Count(b.String(), "\033["); n != 2 {
			t.Errorf("last point %v: %d escape sequences, want 2", c.last, n)
		}
	}
}

func TestRenderGauge(t *testing.T) {
	var b bytes.Buffer
	renderGauge(&b, "CPU", 42, gaugeW)
	want := "  CPU       " + ansiGreen + strings.Repeat("█", 10) + ansiReset + strings.Repeat("░", 15) + " 42%\n"
	if got := b.String(); got != want {
		t.Errorf("gauge = %q, want %q", got, want)
	}
	// A value beyond 100 fills the gauge rather than spilling past it.
	b.Reset()
	renderGauge(&b, "CPU", 150, gaugeW)
	want = "  CPU       " + ansiRed + strings.Repeat("█", 25) + ansiReset + " 150%\n"
	if got := b.String(); got != want {
		t.Errorf("out-of-range gauge = %q, want %q", got, want)
	}
}

func TestValidateMetricsFormat(t *testing.T) {
	for _, f := range []string{"bar", "gauge", "table", "json"} {
		if err := validateMetricsFormat(f); err != nil {
			t.Errorf("%q rejected: %v", f, err)
		}
	}
	err := validateMetricsFormat("csv")
	if err == nil || !strings.Contains(err.Error(), `"csv"`) {
		t.Errorf("csv: %v, want the bad value named", err)
	}
}

// A daemon that predates the history — or a series it never reported — draws
// exactly as the old bar format did, byte for byte.
func TestRenderStatBarsFallsBackToGaugeWithoutHistory(t *testing.T) {
	cpu := &metrics.CpuStat{Utilization: 42}
	mem := &metrics.MemoryStat{Total: 1000, Used: 300}
	gpus := []metrics.GpuStat{{Index: 0, Name: "H100", Utilization: 61, MemoryUsed: 80, MemoryTotal: 160}}

	var got, want bytes.Buffer
	renderStatBars(&got, cpu, mem, gpus, nil, barLineW)
	renderStatGauges(&want, cpu, mem, gpus)
	if got.String() != want.String() {
		t.Errorf("no-history bar differs from the old drawing:\ngot:\n%q\nwant:\n%q", got.String(), want.String())
	}
	if !strings.Contains(got.String(), "░") {
		t.Errorf("the fallback drew no gauge: %q", got.String())
	}
}

func TestRenderStatBarsDrawsHistory(t *testing.T) {
	history := []metrics.HistorySample{
		{Time: 1, CPU: ptrPct(10), Mem: ptrPct(20)},
		{Time: 2, CPU: ptrPct(20), Mem: ptrPct(30)},
		{Time: 3, CPU: ptrPct(95), Mem: ptrPct(40)},
	}
	var b bytes.Buffer
	renderStatBars(&b, nil, nil, nil, history, barLineW)
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("drew %d lines, want CPU and RAM: %q", len(lines), b.String())
	}
	if !strings.Contains(lines[0], ansiRed+"▇"+ansiReset+" 95%") {
		t.Errorf("CPU line lost its spike or its red last point: %q", lines[0])
	}
	if !strings.Contains(lines[1], ansiGreen+"▃"+ansiReset+" 40%") {
		t.Errorf("RAM line: %q", lines[1])
	}
}

// Two adjacent series both pinned at 100% draw at seven-eighths, so neither
// row reaches the top of its cell: the rows read as separate bars rather than
// one solid block. The full block must not appear anywhere in the render.
func TestRenderStatBarsMaxedRowsStopShortOfFull(t *testing.T) {
	history := []metrics.HistorySample{
		{Time: 1, CPU: ptrPct(100), Mem: ptrPct(100)},
		{Time: 2, CPU: ptrPct(100), Mem: ptrPct(100)},
	}
	var b bytes.Buffer
	renderStatBars(&b, nil, nil, nil, history, barLineW)
	out := b.String()
	if strings.Contains(out, "█") {
		t.Errorf("a maxed series drew a full block:\n%s", out)
	}
	if !strings.Contains(out, "▇") {
		t.Errorf("a maxed series did not draw the seven-eighths block:\n%s", out)
	}
	if !strings.Contains(out, " 100%") {
		t.Errorf("a maxed series lost its trailing figure:\n%s", out)
	}
}

// The fallback is per series: a series with retained readings draws them, a
// series without falls back to its current reading on the same screen.
func TestRenderStatBarsFallsBackPerSeries(t *testing.T) {
	history := []metrics.HistorySample{
		{Time: 1, CPU: ptrPct(10)},
		{Time: 2, CPU: ptrPct(20)},
	}
	var b bytes.Buffer
	renderStatBars(&b, nil, &metrics.MemoryStat{Total: 1000, Used: 300}, nil, history, barLineW)
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("drew %d lines, want 2: %q", len(lines), b.String())
	}
	if strings.Contains(lines[0], "░") || !strings.Contains(lines[0], " 20%") {
		t.Errorf("CPU did not draw its history: %q", lines[0])
	}
	if !strings.Contains(lines[1], "░") || !strings.Contains(lines[1], " 30%") {
		t.Errorf("RAM did not fall back to the gauge: %q", lines[1])
	}
}

// A stopped engine carries no current figures, so the bar format draws the
// retained readings alone — including the GPU series the engine ran on, which
// the current reading no longer names.
func TestRenderStatBarsStoppedEngineDrawsHistoryAlone(t *testing.T) {
	history := []metrics.HistorySample{
		{Time: 1, CPU: ptrPct(10), GPUs: []metrics.HistoryGPU{{Index: 0, Util: 50, Mem: ptrPct(50)}}},
		{Time: 2, CPU: ptrPct(20), GPUs: []metrics.HistoryGPU{{Index: 0, Util: 60, Mem: ptrPct(60)}}},
	}
	var b bytes.Buffer
	renderStatBars(&b, nil, nil, nil, history, barLineW)
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("drew %d lines, want CPU, GPU util and GPU mem: %q", len(lines), b.String())
	}
	if !strings.HasPrefix(lines[0], "  CPU       ") {
		t.Errorf("first line: %q", lines[0])
	}
	// One GPU: the plain "GPU" prefix, as the gauge used it.
	if !strings.Contains(lines[1], "GPU util") || !strings.Contains(lines[1], " 60%") {
		t.Errorf("GPU util line: %q", lines[1])
	}
	if !strings.Contains(lines[2], "GPU mem") || !strings.Contains(lines[2], " 60%") {
		t.Errorf("GPU mem line: %q", lines[2])
	}

	// Two GPUs: the index goes in the label.
	history = []metrics.HistorySample{
		{Time: 1, CPU: ptrPct(10), GPUs: []metrics.HistoryGPU{
			{Index: 0, Util: 10, Mem: ptrPct(10)},
			{Index: 1, Util: 90, Mem: ptrPct(20)},
		}},
	}
	b.Reset()
	renderStatBars(&b, nil, nil, nil, history, barLineW)
	out := b.String()
	if !strings.Contains(out, "GPU 0 util") || !strings.Contains(out, "GPU 1 util") ||
		!strings.Contains(out, "GPU 0 mem") || !strings.Contains(out, "GPU 1 mem") {
		t.Errorf("two GPUs: %q", out)
	}
}

// A memory reading with no total reports 0, not a division by it.
func TestBarSeriesListMemoryWithoutTotal(t *testing.T) {
	var b bytes.Buffer
	renderStatBars(&b, nil, &metrics.MemoryStat{Total: 0, Used: 100}, nil, nil, barLineW)
	if !strings.Contains(b.String(), " 0%") || strings.Contains(b.String(), "NaN") {
		t.Errorf("a memory reading with no total: %q", b.String())
	}
}

// A GPU that appears in only some samples: each of its series draws the
// samples that carry it, and the pick yields nothing for the rest.
func TestRenderStatBarsGPUInOnlySomeSamples(t *testing.T) {
	history := []metrics.HistorySample{
		{Time: 1, GPUs: []metrics.HistoryGPU{{Index: 0, Util: 10, Mem: ptrPct(20)}}},
		{Time: 2, GPUs: []metrics.HistoryGPU{{Index: 1, Util: 90, Mem: ptrPct(80)}}},
	}
	var b bytes.Buffer
	renderStatBars(&b, nil, nil, nil, history, barLineW)
	out := b.String()
	// Both GPUs are named in the union, so all four series draw.
	for _, want := range []string{"GPU 0 util", "GPU 1 util", "GPU 0 mem", "GPU 1 mem"} {
		if !strings.Contains(out, want) {
			t.Fatalf("series %q not drawn: %q", want, out)
		}
	}
	// Each series drew exactly the one sample that carried it.
	for _, want := range []string{" 10%", " 90%", " 20%", " 80%"} {
		if !strings.Contains(out, want) {
			t.Errorf("series missing its sample's value %s: %q", want, out)
		}
	}
}

// The gauge format draws the current reading only, whatever the history
// holds — and a stopped engine's reading carries nothing, so it draws none.
func TestRenderStatGaugesIgnoresHistory(t *testing.T) {
	cpu := &metrics.CpuStat{Utilization: 50}
	var b bytes.Buffer
	renderStatGauges(&b, cpu, nil, nil)
	if !strings.Contains(b.String(), " 50%") || strings.Contains(b.String(), "▁") {
		t.Errorf("gauge drew history or the wrong value: %q", b.String())
	}
	b.Reset()
	renderStatGauges(&b, nil, nil, nil)
	if b.String() != "" {
		t.Errorf("a stopped engine drew gauges: %q", b.String())
	}
}

// The serve view's format draws both halves together: each series as a gauge
// of its current reading with its retained history as a sparkline beneath,
// the gauge carrying the label and the sparkline a blank one, so the pair
// stacks in the label column.
func TestRenderStatCombined(t *testing.T) {
	cpu := &metrics.CpuStat{Utilization: 42}
	mem := &metrics.MemoryStat{Total: 1000, Used: 300}
	gpus := []metrics.GpuStat{{Index: 0, Name: "H100", Utilization: 61, MemoryUsed: 80, MemoryTotal: 160}}
	history := []metrics.HistorySample{
		{Time: 1, CPU: ptrPct(10), Mem: ptrPct(20), GPUs: []metrics.HistoryGPU{{Index: 0, Util: 30, Mem: ptrPct(10)}}},
		{Time: 2, CPU: ptrPct(95), Mem: ptrPct(30), GPUs: []metrics.HistoryGPU{{Index: 0, Util: 91, Mem: ptrPct(20)}}},
	}
	var b bytes.Buffer
	renderStatCombined(&b, cpu, mem, gpus, history)
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	// One line per series, in the bar series' order.
	if len(lines) != 4 {
		t.Fatalf("drew %d lines, want 4: %q", len(lines), b.String())
	}
	for i, label := range []string{"  CPU       ", "  RAM       ", "  GPU util  ", "  GPU mem   "} {
		if !strings.HasPrefix(lines[i], label) {
			t.Errorf("line for %s: %q", label, lines[i])
		}
	}
	// The line is label, gauge half, figure, bar half — the figure between
	// the two halves, and it is the current reading, not the bar's last
	// sample.
	cpuBar, _ := sparklineBlock([]float64{10, 95}, serveBarW)
	wantCPU := "  CPU       " + gaugeBlock(42, serveGaugeW) + " 42% " + cpuBar
	if lines[0] != wantCPU {
		t.Errorf("CPU line:\ngot  %q\nwant %q", lines[0], wantCPU)
	}
	// Both halves take the state colour: the gauge over its whole fill, the
	// sparkline on its last glyph only — and the sparkline stops one grade
	// short of the full block, so the 95% sample draws the top of the seven
	// sub-full glyphs.
	if !strings.Contains(lines[0], ansiGreen+strings.Repeat("█", 8)+ansiReset+strings.Repeat("░", 12)) {
		t.Errorf("CPU gauge: %q", lines[0])
	}
	if !strings.Contains(lines[0], ansiRed+"▇"+ansiReset) {
		t.Errorf("CPU sparkline: %q", lines[0])
	}
	// Every line is label, gauge half, figure and bar half: the halves sit
	// side by side, aligned across the series. 62 is label (12), gauge half,
	// " 42%"-style figure (4, two digits in this data), a space, bar half.
	for i, line := range lines {
		if w := lipgloss.Width(line); w != 12+serveGaugeW+4+1+serveBarW {
			t.Errorf("line %d is %d columns wide, want %d: %q", i, w, 12+serveGaugeW+4+1+serveBarW, line)
		}
	}
}

// A memory reading with no total reports 0, not a division by it, in either
// half of the combined format.
func TestRenderStatCombinedMemoryWithoutTotal(t *testing.T) {
	var b bytes.Buffer
	renderStatCombined(&b, nil, &metrics.MemoryStat{Total: 0, Used: 100}, nil, nil)
	if !strings.Contains(b.String(), " 0%") || strings.Contains(b.String(), "NaN") {
		t.Errorf("a memory reading with no total: %q", b.String())
	}
}

// Without retained history each series carries its gauge alone, its bar
// half left blank.
func TestRenderStatCombinedWithoutHistory(t *testing.T) {
	cpu := &metrics.CpuStat{Utilization: 42}
	mem := &metrics.MemoryStat{Total: 1000, Used: 300}
	gpus := []metrics.GpuStat{{Index: 0, Name: "H100", Utilization: 61, MemoryUsed: 80, MemoryTotal: 160}}
	var b bytes.Buffer
	renderStatCombined(&b, cpu, mem, gpus, nil)
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	blankBar := strings.Repeat(" ", serveBarW)
	want := []string{
		"  CPU       " + gaugeBlock(42, serveGaugeW) + " 42% " + blankBar,
		"  RAM       " + gaugeBlock(30, serveGaugeW) + " 30% " + blankBar,
		"  GPU util  " + gaugeBlock(61, serveGaugeW) + " 61% " + blankBar,
		"  GPU mem   " + gaugeBlock(50, serveGaugeW) + " 50% " + blankBar,
	}
	if len(lines) != len(want) {
		t.Fatalf("drew %d lines, want %d: %q", len(lines), len(want), b.String())
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d:\ngot  %q\nwant %q", i, lines[i], want[i])
		}
	}
}

// A stopped engine carries no current figures, so the combined format draws
// the retained readings alone — the sparkline lines with their blank label
// column, no gauges at all.
// A stopped engine carries no current figures, so the combined format draws
// each series' history alone: the gauge half blank, the series' label kept,
// and the bar's latest sample as the line's figure.
func TestRenderStatCombinedStoppedEngineDrawsHistoryAlone(t *testing.T) {
	history := []metrics.HistorySample{
		{Time: 1, CPU: ptrPct(10), Mem: ptrPct(20), GPUs: []metrics.HistoryGPU{{Index: 0, Util: 50, Mem: ptrPct(50)}}},
		{Time: 2, CPU: ptrPct(20), Mem: ptrPct(30), GPUs: []metrics.HistoryGPU{{Index: 0, Util: 60, Mem: ptrPct(60)}}},
	}
	var b bytes.Buffer
	renderStatCombined(&b, nil, nil, nil, history)
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("drew %d lines, want CPU, RAM, GPU util and GPU mem: %q", len(lines), b.String())
	}
	blankGauge := strings.Repeat(" ", serveGaugeW)
	wantPrefix := []string{
		"  CPU       " + blankGauge + " 20% ",
		"  RAM       " + blankGauge + " 30% ",
		"  GPU util  " + blankGauge + " 60% ",
		"  GPU mem   " + blankGauge + " 60% ",
	}
	for i, want := range wantPrefix {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("line %d:\ngot  %q\nwant prefix %q", i, lines[i], want)
		}
	}
	// The bar ends the line, its two samples as its two right-most glyphs,
	// the newest in the state colour.
	wantSuffix := []string{
		"▁" + ansiGreen + "▂" + ansiReset,
		"▂" + ansiGreen + "▃" + ansiReset,
		"▄" + ansiGreen + "▅" + ansiReset,
		"▄" + ansiGreen + "▅" + ansiReset,
	}
	for i, want := range wantSuffix {
		if !strings.HasSuffix(lines[i], want) {
			t.Errorf("line %d must end with the bar's two samples:\ngot %q", i, lines[i])
		}
	}
}

func TestFormatMetricsBarStoppedWithHistory(t *testing.T) {
	results := []fleet.NodeResult{{
		Name: "prod", Outcome: fleet.OutcomeOK,
		Metrics: metrics.Stats{
			State: "stopped", ModelID: "org/qwen:q4",
			LastActiveAt: "2026-08-21T10:00:00Z", IdleSeconds: 12,
			History: []metrics.HistorySample{
				{Time: 1, CPU: ptrPct(10)},
				{Time: 2, CPU: ptrPct(20)},
			},
		},
	}}
	var b bytes.Buffer
	if err := renderFleetMetrics(&b, results, "bar"); err != nil {
		t.Fatal(err)
	}
	want := "prod  stopped  org/qwen:q4\n" +
		"  active    12s ago\n" +
		"  CPU       " + strings.Repeat(" ", 38) + "▁" + ansiGreen + "▂" + ansiReset + " 20%\n"
	if got := b.String(); got != want {
		t.Errorf("stopped bar = %q, want %q", got, want)
	}

	// The gauge format draws no series for a stopped endpoint: the header and
	// the active line, and nothing after.
	b.Reset()
	if err := renderFleetMetrics(&b, results, "gauge"); err != nil {
		t.Fatal(err)
	}
	want = "prod  stopped  org/qwen:q4\n" +
		"  active    12s ago\n"
	if got := b.String(); got != want {
		t.Errorf("stopped gauge = %q, want %q", got, want)
	}
}

func TestFormatMetricsBarRunning(t *testing.T) {
	resp := &remote.StatsResponse{
		Environment: "prod", State: "running", InstanceType: "g5.xlarge",
		ModelID: "org/qwen:q4", Version: "0.4.3",
		LastActiveAt: "2026-08-21T10:00:00Z", IdleSeconds: 3,
		CPU:    &metrics.CpuStat{Utilization: 62},
		Memory: &metrics.MemoryStat{Total: 1000, Used: 300},
		GPUs:   []metrics.GpuStat{{Index: 0, Name: "H100", Utilization: 61, MemoryUsed: 80, MemoryTotal: 160}},
		Tokens: &remote.TokenStats{Running: 2, PromptTokens: 4096, GenerationTokens: 1024, Requests: ptrInt(17)},
		History: []metrics.HistorySample{
			{Time: 1, CPU: ptrPct(10), Mem: ptrPct(20), GPUs: []metrics.HistoryGPU{{Index: 0, Util: 50, Mem: ptrPct(50)}}},
			{Time: 2, CPU: ptrPct(20), Mem: ptrPct(30), GPUs: []metrics.HistoryGPU{{Index: 0, Util: 61, Mem: ptrPct(50)}}},
		},
	}
	var b bytes.Buffer
	if err := renderFleetMetrics(&b, nodeResultsFor(resp), "bar"); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.HasPrefix(got, "prod  running  g5.xlarge  org/qwen:q4  0.4.3\n") {
		t.Errorf("header: %q", got)
	}
	if !strings.Contains(got, "  active    3s ago\n") {
		t.Errorf("active line missing: %q", got)
	}
	// Every series drew a sparkline from the history: no gauge in the output.
	if strings.Contains(got, "░") {
		t.Errorf("a series fell back to the gauge although history holds it: %q", got)
	}
	for _, label := range []string{"CPU", "RAM", "GPU util", "GPU mem"} {
		if !strings.Contains(got, label) {
			t.Errorf("series %q missing: %q", label, got)
		}
	}
	if !strings.Contains(got, "  running:          2\n") || !strings.Contains(got, "  requests:         17\n") {
		t.Errorf("token block missing: %q", got)
	}
}

// An engine family whose metrics expose no cumulative request counter yields
// statistics without the figure, and the token block draws no line for it.
func TestFormatMetricsBarNoRequestCount(t *testing.T) {
	resp := &remote.StatsResponse{
		Environment: "prod", State: "running", InstanceType: "g5.xlarge",
		ModelID: "org/qwen:q4", Version: "0.4.3",
		Tokens: &remote.TokenStats{Running: 2, PromptTokens: 4096, GenerationTokens: 1024},
	}
	var b bytes.Buffer
	if err := renderFleetMetrics(&b, nodeResultsFor(resp), "bar"); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "  running:          2\n") {
		t.Errorf("running line missing: %q", got)
	}
	if strings.Contains(got, "  requests:") {
		t.Errorf("requests line drawn for an engine that exposes no request count: %q", got)
	}
}

func TestFormatMetricsJSONCarriesHistory(t *testing.T) {
	resp := &remote.StatsResponse{
		Environment: "prod", State: "running",
		CPU: &metrics.CpuStat{Utilization: 62},
		History: []metrics.HistorySample{
			{Time: 1786276800, CPU: ptrPct(62), GPUs: []metrics.HistoryGPU{{Index: 0, Util: 61, Mem: ptrPct(50)}}},
		},
	}
	var b bytes.Buffer
	if err := renderFleetMetrics(&b, nodeResultsFor(resp), "json"); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{`"history"`, `"t":`, `"c":`, `"g":`, `"i":`, `"u":`} {
		if !strings.Contains(got, want) {
			t.Errorf("json %s: missing %s", want, got)
		}
	}
	// Absent history stays absent, as on the daemon.
	b.Reset()
	resp.History = nil
	if err := renderFleetMetrics(&b, nodeResultsFor(resp), "json"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "history") {
		t.Errorf("absent history serialised: %s", b.String())
	}
}

// nodeResultsFor turns a control-plane stats reply into the one node result a
// fan-out would produce for it, so a test can state its input as the reply and
// assert on what the renderer draws.
func nodeResultsFor(resp *remote.StatsResponse) []fleet.NodeResult {
	return []fleet.NodeResult{{
		Name: resp.Environment, Outcome: fleet.OutcomeOK,
		Instance: fleet.Instance{
			ID: resp.InstanceID, Type: resp.InstanceType, Version: resp.Version,
			RetainUntil: resp.RetainUntil,
		},
		Metrics: metrics.Stats{
			State: resp.State, Runner: resp.Runner, ModelID: resp.ModelID,
			UptimeSeconds: resp.UptimeSeconds, Tokens: resp.Tokens,
			GPUs: resp.GPUs, CPU: resp.CPU, Memory: resp.Memory,
			History: resp.History, Errors: resp.Errors,
			LastActiveAt: resp.LastActiveAt, IdleSeconds: resp.IdleSeconds,
			RetainUntil: resp.RetainUntil,
		},
	}}
}
