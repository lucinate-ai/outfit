package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	teatest "github.com/charmbracelet/x/exp/teatest"
	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/fleet"
	"github.com/lucinate-ai/outfit/internal/metrics"
	"github.com/lucinate-ai/outfit/internal/remote"
	"github.com/muesli/termenv"
)

// fakeDashNode is one in-memory fleet node: Start and Stop flip its state and
// its Metrics answer follows, so a program-level test can watch a tile turn
// on and off. metricsErr makes the node answer no metrics at all. A non-nil
// hold keeps StartWithProgress on its first status line until the test
// closes it — an in-flight start the test can watch.
type fakeDashNode struct {
	mu         sync.Mutex
	state      string
	metricsErr error
	starts     int
	stops      int
	hold       <-chan struct{}
}

func newFakeDashNode(state string) *fakeDashNode { return &fakeDashNode{state: state} }

func (f *fakeDashNode) failMetrics(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metricsErr = err
}

func (f *fakeDashNode) Name() string { return "fake" }

func (f *fakeDashNode) Status(ctx context.Context) (daemon.StatusResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return daemon.StatusResponse{State: f.state}, nil
}

func (f *fakeDashNode) Metrics(ctx context.Context) (metrics.Stats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.metricsErr != nil {
		return metrics.Stats{}, f.metricsErr
	}
	s := metrics.Stats{State: f.state, Runner: "llamacpp", ModelID: "org/qwen"}
	if f.state == "running" {
		s.UptimeSeconds = 60
		s.CPU = &metrics.CpuStat{Utilization: 42}
		s.GPUs = []metrics.GpuStat{{Index: 0, Name: "H100", Utilization: 10, MemoryUsed: 1, MemoryTotal: 10}}
		s.Tokens = &metrics.TokenStats{Running: 1, PromptTokens: 100, GenerationTokens: 50, Requests: 3}
	}
	return s, nil
}

func (f *fakeDashNode) Start(ctx context.Context) (daemon.StatusResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	f.state = "running"
	return daemon.StatusResponse{State: "running"}, nil
}

// The fake also reports one progress line on the way, so the dashboard's
// progress path is exercised end to end, through the program's Send — and,
// when told to hold, stays on that line until released.
func (f *fakeDashNode) StartWithProgress(ctx context.Context, progress func(string)) (daemon.StatusResponse, error) {
	progress("instance starting; retrying in 1s")
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
		}
	}
	return f.Start(ctx)
}

func (f *fakeDashNode) StartWith(ctx context.Context, dc *remote.DeployConfig, engineKey string) (daemon.StatusResponse, error) {
	return daemon.StatusResponse{}, errors.New("not driven by the dashboard")
}

func (f *fakeDashNode) Stop(ctx context.Context) (daemon.StatusResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	f.state = "stopped"
	return daemon.StatusResponse{State: "stopped"}, nil
}

func (f *fakeDashNode) Logs(ctx context.Context, offset int64, limit int) (daemon.LogsResponse, error) {
	return daemon.LogsResponse{}, errors.New("not driven by the dashboard")
}

// startFastRound begins the local group's round through the model's own
// door and runs it to completion.
func startFastRound(t *testing.T, m *dashModel) (dashRefreshMsg, bool) {
	t.Helper()
	cmd := m.refreshRemoteGroup(false)
	if cmd == nil {
		return dashRefreshMsg{}, false
	}
	out := cmd()
	r, ok := out.(dashRefreshMsg)
	if !ok {
		t.Fatalf("fast round cmd answered %T, want dashRefreshMsg", out)
	}
	return r, true
}

// landRounds runs each round the model started to completion and applies its
// answer, one at a time.
func landRounds(t *testing.T, m *dashModel, cmds []tea.Cmd) *dashModel {
	t.Helper()
	for _, cmd := range cmds {
		out := cmd()
		msg, ok := out.(dashRefreshMsg)
		if !ok {
			t.Fatalf("round cmd answered %T, want dashRefreshMsg", out)
		}
		next, _ := m.Update(msg)
		m = next.(*dashModel)
	}
	return m
}

// dashKey is a KeyMsg for a one-keystroke string: the model and the test
// type the same words.
func dashKey(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// The tile draw the bar-format helpers produce for a running node. The
// colours come from renderBar itself — they are content, not decoration — so
// the expected string carries them out, and the test pins the profile to
// Ascii so the frame alone is colour-stripped.

func dashBar(label string, pct float64) string {
	const width = 25
	colour := "\033[92m"
	if pct > 90 {
		colour = "\033[31m"
	} else if pct >= 80 {
		colour = "\033[33m"
	}
	filled := int(pct / 100 * width)
	return fmt.Sprintf("  %-9s ", label) + colour +
		strings.Repeat("█", filled) + "\033[0m" +
		strings.Repeat("░", width-filled) + fmt.Sprintf(" %.0f%%", pct)
}

func dashTileExpected(lines []string) string {
	var b strings.Builder
	b.WriteString("╭" + strings.Repeat("─", dashTileW) + "╮\n")
	for _, line := range lines {
		if pad := dashTileW - lipgloss.Width(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		b.WriteString("│" + line + "│\n")
	}
	b.WriteString("╰" + strings.Repeat("─", dashTileW) + "╯")
	return b.String()
}

func TestDashTileRunningByteStable(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	r := fleet.NodeResult{
		Name: "up", Outcome: fleet.OutcomeOK,
		Metrics: metrics.Stats{
			State: "running", Runner: "llamacpp", ModelID: "org/qwen:q4",
			UptimeSeconds: 7200, LastActiveAt: "2026-08-21T10:00:00Z", IdleSeconds: 12,
			CPU:    &metrics.CpuStat{Utilization: 42},
			Memory: &metrics.MemoryStat{Total: 1000, Used: 300},
			GPUs:   []metrics.GpuStat{{Index: 0, Name: "H100", Utilization: 61, MemoryUsed: 80, MemoryTotal: 160}},
			Tokens: &metrics.TokenStats{Running: 2, PromptTokens: 4096, GenerationTokens: 1024, Requests: 17},
		},
	}
	want := dashTileExpected([]string{
		"up  running",
		"llamacpp  org/qwen:q4  (up 2h 0m 0s)",
		"  last active 12s ago",
		dashBar("CPU", 42),
		dashBar("RAM", 30),
		dashBar("GPU util", 61),
		dashBar("GPU mem", 50),
		"",
		"  running:          2",
		"  prompt tokens:    4096",
		"  generation tokens: 1024",
		"  requests:         17",
	})
	if got := dashTile("up", r, false, dashAction{}); got != want {
		t.Errorf("tile mismatch:\ngot:\n%q\nwant:\n%q", got, want)
	}
}

func TestDashTileOutcomeAndEmpty(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	dead := fleet.NodeResult{
		Name: "down", Outcome: fleet.OutcomeUnreachable,
		Err: errors.New("connection refused (127.0.0.1:1)"),
	}
	if got := dashTile("down", dead, false, dashAction{}); got != dashTileExpected([]string{
		"down  unreachable",
		"connection refused (127.0.0.1:1)",
		"", "", "", "", "", "", "", "", "", "",
	}) {
		t.Errorf("outcome tile mismatch:\n%q", got)
	}
	// A node not answered yet is an empty panel naming the node.
	if got := dashTile("down", fleet.NodeResult{Name: "down"}, false, dashAction{}); got != dashTileExpected([]string{
		"down", "waiting for first refresh…",
		"", "", "", "", "", "", "", "", "", "",
	}) {
		t.Errorf("empty tile mismatch:\n%q", got)
	}
}

func TestDashTileStoppedByteStable(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	r := fleet.NodeResult{
		Name: "idle", Outcome: fleet.OutcomeOK,
		Metrics: metrics.Stats{State: "idle"},
	}
	want := dashTileExpected([]string{
		"idle  idle",
		"", "", "", "", "", "", "", "", "", "", "",
	})
	if got := dashTile("idle", r, false, dashAction{}); got != want {
		t.Errorf("stopped tile mismatch:\n%q\nwant:\n%q", got, want)
	}
}

// A node with an action in flight shows the verb and the call's own lines
// instead of its last report: while a cloud wake is working, that is the
// state the tile should carry.
func TestDashTileActionInFlight(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	if got := dashTile("dev-2", fleet.NodeResult{Name: "dev-2"}, false,
		dashAction{verb: "start", line: "instance starting; retrying in 42s"}); got != dashTileExpected([]string{
		"dev-2  starting",
		"instance starting; retrying in 42s",
		"", "", "", "", "", "", "", "", "", "",
	}) {
		t.Errorf("in-flight tile mismatch:\n%q", got)
	}
	// A stop conjugates: the p of stop drops before -ing.
	if got := dashTile("dev-2", fleet.NodeResult{Name: "dev-2"}, false,
		dashAction{verb: "stop"}); got != dashTileExpected([]string{
		"dev-2  stopping",
		"", "", "", "", "", "", "", "", "", "", "",
	}) {
		t.Errorf("bare in-flight tile mismatch:\n%q", got)
	}
}

// The selection is carried by the lit border; a colour profile that keeps
// colour is needed to see it, since the byte-stable profile strips it.
func TestDashTileSelectedBorderLit(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	r := fleet.NodeResult{Name: "n"}
	sel, unsel := dashTile("n", r, true, dashAction{}), dashTile("n", r, false, dashAction{})
	if !strings.Contains(sel, "\x1b[38;5;214m") {
		t.Errorf("selected tile carries no lit border:\n%q", sel)
	}
	if strings.Contains(unsel, "\x1b[38;5;214m") {
		t.Errorf("unselected tile carries the lit border:\n%q", unsel)
	}
}

// A line longer than the panel is hard-cut, never wrapped: the tile keeps
// its fixed shape whatever the model's id or a node's error message says.
func TestDashTileClipsLongLines(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	r := fleet.NodeResult{
		Name: "n", Outcome: fleet.OutcomeOK,
		Metrics: metrics.Stats{State: "running", ModelID: strings.Repeat("m", 60)},
	}
	rows := strings.Split(dashTile("n", r, false, dashAction{}), "\n")
	if len(rows) != dashTileH+2 {
		t.Fatalf("tile is %d rows, want %d", len(rows), dashTileH+2)
	}
	for i := 0; i < len(rows); i++ {
		if w := lipgloss.Width(rows[i]); w != dashTileW+2 {
			t.Errorf("row %d is %d columns, want %d", i, w, dashTileW+2)
		}
	}
}

func TestDashLayoutMath(t *testing.T) {
	cols := []struct {
		w, want int
	}{
		{10, 1}, {44, 1}, {45, 1}, {88, 1},
		{89, 2}, {80, 1},
		{134, 3}, {133, 2}, {179, 4}, {178, 3},
	}
	for _, c := range cols {
		if got := dashCols(c.w); got != c.want {
			t.Errorf("dashCols(%d) = %d, want %d", c.w, got, c.want)
		}
	}
	rows := []struct {
		h, want int
	}{
		{3, 1}, {17, 1}, {18, 1}, {32, 2}, {47, 3}, {46, 2},
	}
	for _, c := range rows {
		if got := dashVisibleRows(c.h); got != c.want {
			t.Errorf("dashVisibleRows(%d) = %d, want %d", c.h, got, c.want)
		}
	}
	// Ten nodes, two columns (w=133), two visible rows (h=32): five grid
	// rows, so the top may scroll to row 3.
	if got := dashClampScroll(0, 10, 133, 32); got != 0 {
		t.Errorf("clamp 0 = %d", got)
	}
	if got := dashClampScroll(3, 10, 133, 32); got != 3 {
		t.Errorf("clamp 3 = %d", got)
	}
	if got := dashClampScroll(9, 10, 133, 32); got != 3 {
		t.Errorf("clamp 9 = %d, want 3", got)
	}
	if got := dashClampScroll(-4, 10, 133, 32); got != 0 {
		t.Errorf("clamp -4 = %d", got)
	}
	// Fewer tiles than a screen: nowhere to scroll to.
	if got := dashClampScroll(5, 3, 133, 32); got != 0 {
		t.Errorf("small-grid clamp = %d, want 0", got)
	}
	// Grid fill is left to right, top to bottom, in file order.
	lines := []string{"t0", "t1", "t2", "t3", "t4"}
	got := dashGridRows(lines, 2)
	want := []string{"t0 t1", "t2 t3", "t4"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("dashGridRows = %q, want %q", got, want)
	}
	// A real tile is a multi-line block, so a grid row joins the tiles'
	// corresponding lines: the second tile's top border sits beside the
	// first tile's, never by its bottom.
	block := func(tag byte) string {
		parts := make([]string, dashTileH+2)
		for l := range parts {
			parts[l] = fmt.Sprintf("%c%02d", tag, l)
		}
		return strings.Join(parts, "\n")
	}
	got = dashGridRows([]string{block('A'), block('B')}, 2)
	if len(got) != 1 {
		t.Fatalf("two blocks in two columns make %d rows, want 1", len(got))
	}
	top, bottom := strings.Split(got[0], "\n")[0], strings.Split(got[0], "\n")[dashTileH+1]
	if len(strings.Split(got[0], "\n")) != dashTileH+2 {
		t.Fatalf("the joined row has %d lines, want %d", len(strings.Split(got[0], "\n")), dashTileH+2)
	}
	if top != "A00 B00" || bottom != fmt.Sprintf("A%02d B%02d", dashTileH+1, dashTileH+1) {
		t.Errorf("block rows misjoined: top %q, bottom %q", top, bottom)
	}
	// A partial row: the lone tile stands in its own column.
	got = dashGridRows([]string{block('A'), block('B'), block('C')}, 2)
	if len(got) != 2 || len(strings.Split(got[0], "\n")) != dashTileH+2 || got[1] != block('C') {
		t.Errorf("partial row: %d rows, %q", len(got), got)
	}
}

// Three real tiles at a wide window must sit side by side — the user's
// original report: the second and third panels' top borders appeared beside
// the first panel's bottom border.
func TestDashGridRealTilesSideBySide(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	running := func(name string) fleet.NodeResult {
		return fleet.NodeResult{
			Name: name, Outcome: fleet.OutcomeOK,
			Metrics: metrics.Stats{
				State: "running", Runner: "llamacpp", ModelID: "unsloth/qwen", UptimeSeconds: 2820,
				LastActiveAt: "2026-08-22T10:00:00Z", IdleSeconds: 29,
				CPU:    &metrics.CpuStat{Utilization: 1},
				Memory: &metrics.MemoryStat{Total: 100, Used: 35},
				GPUs:   []metrics.GpuStat{{Index: 0, Name: "H100", Utilization: 61, MemoryUsed: 89, MemoryTotal: 100}},
				Tokens: &metrics.TokenStats{Running: 1, PromptTokens: 324040, GenerationTokens: 35525},
			},
		}
	}
	m := &dashModel{
		fleetPath: "fleet.yaml",
		entries: []dashEntry{
			{name: "dev-1", kind: fleet.KindDaemon},
			{name: "dev-2", kind: fleet.KindDaemon},
			{name: "dev-3", kind: fleet.KindDaemon},
		},
		results: []fleet.NodeResult{running("dev-1"), running("dev-2"), running("dev-3")},
		actions: make([]dashAction, 3),
		width:   200, height: 40,
	}
	lines := strings.Split(m.View(), "\n")
	// Header, one grid row of 14 lines, footer: nothing extra, nothing short.
	if len(lines) != 2+dashTileH+2 {
		t.Fatalf("board is %d lines, want %d", len(lines), 2+dashTileH+2)
	}
	top, bottom := lines[1], lines[1+dashTileH+1]
	if strings.Count(top, "╭") != 3 || strings.Count(bottom, "╯") != 3 {
		t.Errorf("borders split across lines:\ntop:    %q\nbottom: %q", top, bottom)
	}
	// The frame is drawn from box-drawing runes (three bytes each), so the
	// position is counted in runes, the board's own display columns: the
	// second tile starts one step right of the first tile's left edge.
	col, seen := -1, 0
	for i, r := range []rune(top) {
		if r != '╭' {
			continue
		}
		seen++
		if seen == 2 {
			col = i
			break
		}
	}
	if col != dashTileStep {
		t.Errorf("second tile's top border at column %d, want %d:\n%s", col, dashTileStep, top)
	}
	// A body line carries every tile's wall at once.
	if strings.Count(lines[2], "│") != 6 {
		t.Errorf("body line missing a tile's walls:\n%q", lines[2])
	}
}

// Selection moves and scrolls keep the chosen tile in the visible rows.
func TestDashModelSelectionScroll(t *testing.T) {
	entries := make([]dashEntry, 6)
	results := make([]fleet.NodeResult, 6)
	for i := range entries {
		entries[i] = dashEntry{name: fmt.Sprintf("n%d", i)}
		results[i] = fleet.NodeResult{Name: entries[i].name}
	}
	m := &dashModel{entries: entries, results: results, actions: make([]dashAction, len(entries)), width: 120, height: 40}
	// Two columns, two visible rows: nodes 0-3 need no scrolling, node 4
	// (grid row 2) does.
	for i := 0; i < 4; i++ {
		next, cmd := m.Update(dashKey("j"))
		if cmd != nil {
			t.Fatalf("cursor move %d returned a cmd", i)
		}
		m = next.(*dashModel)
	}
	if got := m.cursor; got != 4 {
		t.Fatalf("cursor = %d, want 4", got)
	}
	// The visible rows are 0-1, so row 2 shows as the lower one: the top
	// sits at row 1.
	if got := m.scrollRow; got != 1 {
		t.Errorf("scrollRow = %d, want 1", got)
	}
	// Paging stays within the grid: up three times lands on the top row, and
	// the fourth up is a no-op.
	for i := 0; i < 3; i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
		m = next.(*dashModel)
	}
	if got := m.scrollRow; got != 0 {
		t.Errorf("page up = %d, want 0", got)
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyPgUp}); cmd != nil {
		t.Errorf("page up at the top returned a cmd")
	}
}

// A tick while a group's round is in flight is absorbed; a round's answer
// lands by generation, and a superseded answer is dropped rather than
// painted over the board.
func TestDashModelRefreshGenerations(t *testing.T) {
	node := newFakeDashNode("running")
	m := &dashModel{
		entries: []dashEntry{{name: "a", kind: fleet.KindDaemon, node: node}},
		results: []fleet.NodeResult{{Name: "a"}},
		actions: make([]dashAction, 1),
		width:   120, height: 40,
	}
	// In flight: the tick reschedules itself but must not begin a second
	// round in the group.
	m.fastBusy = true
	m.fastGen = 7
	m2, _ := m.Update(dashTickMsg{})
	if m2.(*dashModel).fastGen != 7 {
		t.Fatal("tick while refreshing started another round")
	}
	m = m2.(*dashModel)
	// A stale answer: discarded.
	stale := []fleet.NodeResult{{Outcome: fleet.OutcomeOK}}
	fresh := []fleet.NodeResult{{Outcome: fleet.OutcomeUnreachable}}
	m2, _ = m.Update(dashRefreshMsg{gen: 6, idx: []int{0}, results: stale})
	if m2.(*dashModel).results[0].Outcome != "" {
		t.Fatalf("stale round painted the board: %v", m2.(*dashModel).results[0].Outcome)
	}
	m3, _ := m2.(*dashModel).Update(dashRefreshMsg{gen: 7, idx: []int{0}, results: fresh})
	mm := m3.(*dashModel)
	if mm.fastBusy {
		t.Fatal("round completion did not clear the flag")
	}
	if mm.results[0].Outcome != fleet.OutcomeUnreachable {
		t.Fatalf("current round not applied: %v", mm.results[0].Outcome)
	}
	// A live round: per-node answers, in entry order.
	msg, ok := startFastRound(t, mm)
	if !ok {
		t.Fatal("no round began")
	}
	if !msg.results[0].OK() || msg.results[0].Metrics.State != "running" {
		t.Errorf("live node answer: %+v", msg.results[0])
	}
}

// A kind: remote environment refreshes on its own slower deadline: the round
// starts only when that time comes, starting it spends the deadline, and the
// manual refresh brings it forward whatever the deadline says.
func TestDashModelRemoteCadence(t *testing.T) {
	orig := dashboardRemoteRefreshInterval
	dashboardRemoteRefreshInterval = time.Minute
	defer func() { dashboardRemoteRefreshInterval = orig }()

	local := newFakeDashNode("running")
	r1 := newFakeDashNode("running")
	r2 := newFakeDashNode("running")
	m := &dashModel{
		entries: []dashEntry{
			{name: "local", kind: fleet.KindDaemon, node: local},
			{name: "r1", kind: fleet.KindRemote, node: r1},
			{name: "r2", kind: fleet.KindRemote, node: r2},
		},
		results: make([]fleet.NodeResult, 3),
		actions: make([]dashAction, 3),
		width:   120, height: 40,
	}
	// Cold open: the local round and the cloud round are both due — the
	// cloud deadline has never been spent.
	cmds := m.startRounds()
	if len(cmds) != 2 {
		t.Fatalf("cold open started %d rounds, want one per group", len(cmds))
	}
	m = landRounds(t, m, cmds)
	for i := range m.entries {
		if !m.results[i].OK() || m.results[i].Metrics.State != "running" {
			t.Errorf("cold round missed %s: %+v", m.entries[i].name, m.results[i])
		}
	}
	if m.fastBusy || m.slowBusy {
		t.Fatal("rounds still marked in flight after their answers landed")
	}
	if !m.nextSlowAt.After(time.Now()) {
		t.Fatal("starting the cloud round did not spend its deadline")
	}
	// Before the deadline, a tick is due for the local round only.
	cmds = m.startRounds()
	if len(cmds) != 1 {
		t.Fatalf("early tick started %d rounds, want the local one", len(cmds))
	}
	m = landRounds(t, m, cmds)
	// At the deadline, both groups are due again.
	m.nextSlowAt = time.Now().Add(-time.Millisecond)
	if cmds = m.startRounds(); len(cmds) != 2 {
		t.Fatalf("due tick started %d rounds, want both groups", len(cmds))
	}
	m = landRounds(t, m, cmds)
	// A manual refresh is due for every node, whatever the deadline says.
	m.nextSlowAt = time.Now().Add(time.Hour)
	m2, _ := m.Update(dashKey("r"))
	mm := m2.(*dashModel)
	if mm.nextSlowAt.IsZero() {
		t.Error("the manual refresh did not bring the cloud deadline forward")
	}
	if !mm.fastBusy || !mm.slowBusy {
		t.Errorf("the manual refresh did not start both groups: fastBusy=%v slowBusy=%v", mm.fastBusy, mm.slowBusy)
	}
}

// One dead node does not keep its neighbour's answer waiting: each node
// answers on its own, and the fan-out calls them concurrently.
func TestDashRoundDoesNotWaitOnDeadNode(t *testing.T) {
	up := newFakeDashNode("running")
	down := newFakeDashNode("stopped")
	down.failMetrics(errors.New("connection refused (127.0.0.1:9)"))
	m := &dashModel{
		entries: []dashEntry{
			{name: "up", kind: fleet.KindDaemon, node: up},
			{name: "down", kind: fleet.KindDaemon, node: down},
		},
		results: make([]fleet.NodeResult, 2),
		actions: make([]dashAction, 2),
		width:   120, height: 40,
	}
	msg, ok := startFastRound(t, m)
	if !ok {
		t.Fatal("round did not run")
	}
	if !msg.results[0].OK() {
		t.Errorf("up node: %+v", msg.results[0])
	}
	if msg.results[1].Outcome != fleet.OutcomeUnreachable {
		t.Errorf("down node: %+v", msg.results[1])
	}
}

// s starts; x asks, n declines, y confirms. A node that already has an
// action in flight takes no more starts, and a finished start clears the
// node so a stop can follow.
func TestDashModelStartAndStop(t *testing.T) {
	node := newFakeDashNode("stopped")
	m := &dashModel{
		entries: []dashEntry{{name: "a", kind: fleet.KindDaemon, node: node}},
		results: []fleet.NodeResult{{Name: "a"}},
		actions: make([]dashAction, 1),
		width:   120, height: 40,
	}
	m2, cmd := m.Update(dashKey("s"))
	if cmd == nil {
		t.Fatal("s did not start")
	}
	mm := m2.(*dashModel)
	if mm.actions[0].verb != "start" {
		t.Fatalf("no action recorded on the node: %+v", mm.actions[0])
	}
	// In flight: no second start on the same node.
	if _, cmd2 := mm.Update(dashKey("s")); cmd2 != nil {
		t.Fatal("a node with an action in flight started again")
	}
	smsg, _ := cmd().(dashActionMsg)
	if node.starts != 1 {
		t.Fatal("start not called")
	}
	m3, _ := mm.Update(smsg)
	mm = m3.(*dashModel)
	if mm.actions[0].verb != "" {
		t.Fatalf("the finished action was not cleared: %+v", mm.actions[0])
	}
	if !strings.Contains(mm.statusLine, "a: start — running") {
		t.Errorf("start status line: %q", mm.statusLine)
	}
	// Decline: no call.
	m4, _ := mm.Update(dashKey("x"))
	if !m4.(*dashModel).confirm {
		t.Fatal("x did not confirm")
	}
	m5, _ := m4.(*dashModel).Update(dashKey("n"))
	if m5.(*dashModel).confirm {
		t.Fatal("n did not decline")
	}
	if node.stops != 0 {
		t.Fatal("stop called on a declined confirmation")
	}
	// Confirm: the stop flies.
	m6, _ := m5.(*dashModel).Update(dashKey("x"))
	m7, cmd2 := m6.(*dashModel).Update(dashKey("y"))
	if cmd2 == nil {
		t.Fatal("y did not stop")
	}
	pmsg, _ := cmd2().(dashActionMsg)
	if node.stops != 1 {
		t.Fatal("stop not called")
	}
	m8, _ := m7.Update(pmsg)
	pmm := m8.(*dashModel)
	if pmm.confirm {
		t.Fatal("confirmation flag stuck after the stop")
	}
	if !strings.Contains(pmm.statusLine, "a: stop — stopped") {
		t.Errorf("stop status line: %q", pmm.statusLine)
	}
}

// An action on an entry that could not build a node reports the standing
// reason and calls nothing.
func TestDashModelActionOnStandingNode(t *testing.T) {
	m := &dashModel{
		entries: []dashEntry{{
			name:     "broken",
			kind:     fleet.KindDaemon,
			standing: fleet.NodeResult{Name: "broken", Outcome: fleet.OutcomeConfigError, Err: errors.New(`tokenEnv "NOPE" is set nowhere`)},
		}},
		results: []fleet.NodeResult{{Name: "broken", Outcome: fleet.OutcomeConfigError, Err: errors.New(`tokenEnv "NOPE" is set nowhere`)}},
		actions: make([]dashAction, 1),
		width:   120, height: 40,
	}
	m2, cmd := m.Update(dashKey("s"))
	if cmd != nil {
		t.Fatal("nothing to call on an unbuilt node")
	}
	if !strings.Contains(m2.(*dashModel).statusLine, "set nowhere") {
		t.Errorf("status line: %q", m2.(*dashModel).statusLine)
	}
}

// Two nodes start at the same time: the action is one per node, in-flight
// progress lands on the node's own tile, and each final clears its own node.
func TestDashModelConcurrentStarts(t *testing.T) {
	a := newFakeDashNode("stopped")
	b := newFakeDashNode("stopped")
	m := &dashModel{
		entries: []dashEntry{
			{name: "a", kind: fleet.KindRemote, node: a},
			{name: "b", kind: fleet.KindRemote, node: b},
		},
		results: make([]fleet.NodeResult, 2),
		actions: make([]dashAction, 2),
		width:   120, height: 40,
	}
	var caught []tea.Msg
	m.send = func(msg tea.Msg) { caught = append(caught, msg) }

	next, cmd := m.Update(dashKey("s"))
	if cmd == nil {
		t.Fatal("s did not start node a")
	}
	m = next.(*dashModel)
	if m.actions[0].verb != "start" {
		t.Fatalf("node a's action not recorded: %+v", m.actions[0])
	}
	// In flight: node a takes no more starts…
	if _, cmd2 := m.Update(dashKey("s")); cmd2 != nil {
		t.Fatal("node a started again while its start was in flight")
	}
	// …but node b starts too.
	next, _ = m.Update(dashKey("j"))
	m = next.(*dashModel)
	next, cmdB := m.Update(dashKey("s"))
	if cmdB == nil {
		t.Fatal("node b did not start while node a was in flight")
	}
	m = next.(*dashModel)
	if m.actions[0].verb != "start" || m.actions[1].verb != "start" {
		t.Fatalf("both actions should be in flight: %+v", m.actions)
	}
	if v := m.View(); !strings.Contains(v, "a  starting") || !strings.Contains(v, "b  starting") {
		t.Errorf("the in-flight tiles do not carry the verb:\n%s", v)
	}
	// Run both calls: each reports its own line through the send door.
	msgA := cmd()
	msgB := cmdB()
	for _, msg := range caught {
		next, _ = m.Update(msg)
		m = next.(*dashModel)
	}
	if m.actions[0].line != "instance starting; retrying in 1s" ||
		m.actions[1].line != "instance starting; retrying in 1s" {
		t.Errorf("progress lines not on the nodes: %+v", m.actions)
	}
	if v := m.View(); !strings.Contains(v, "retrying in 1s") {
		t.Errorf("the tile does not show the call's line:\n%s", v)
	}
	// Each final clears its own node and leaves its line in the footer.
	next, _ = m.Update(msgA)
	m = next.(*dashModel)
	if m.actions[0].verb != "" {
		t.Fatalf("node a's final did not clear it: %+v", m.actions[0])
	}
	if m.actions[1].verb != "start" {
		t.Fatalf("node a's final touched node b: %+v", m.actions[1])
	}
	if !strings.Contains(m.statusLine, "a: start — running") {
		t.Errorf("status line: %q", m.statusLine)
	}
	next, _ = m.Update(msgB)
	m = next.(*dashModel)
	if m.actions[1].verb != "" || !strings.Contains(m.statusLine, "b: start — running") {
		t.Errorf("node b's final: %+v, line %q", m.actions, m.statusLine)
	}
}

// The end-to-end path: the real program against in-memory nodes — a tile
// turns on with s, the stop confirmation names the node, and y turns it
// off. q leaves with the terminal restored, so WaitFinished sees a clean
// exit.
func TestDashProgramStartsAndStopsANode(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	interval := dashboardRefreshInterval
	dashboardRefreshInterval = 15 * time.Millisecond
	defer func() { dashboardRefreshInterval = interval }()

	node := newFakeDashNode("stopped")
	release := make(chan struct{})
	node.hold = release // hold the start on its first status line until told
	// The progress door, as runDashProgram wires it — through an atomic box,
	// because the program is already rendering (and a value-receiver View
	// copies the whole model, send word included) by the time the test can
	// hand it the program's Send. A start that flies before the wiring
	// reports into nothing, the model's nil path.
	var door atomic.Pointer[func(tea.Msg)]
	m := &dashModel{
		fleetPath: "fleet.yaml",
		entries:   []dashEntry{{name: "alpha", kind: fleet.KindDaemon, node: node}},
		results:   []fleet.NodeResult{{Name: "alpha"}},
		actions:   make([]dashAction, 1),
		send: func(msg tea.Msg) {
			if p := door.Load(); p != nil {
				(*p)(msg)
			}
		},
	}
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))
	send := tm.GetProgram().Send
	door.Store(&send)
	out := tm.Output()
	seen := func(what string, d time.Duration) {
		teatest.WaitFor(t, out, func(b []byte) bool { return bytes.Contains(b, []byte(what)) }, teatest.WithDuration(d))
	}
	seen("stopped", 5*time.Second)
	tm.Type("s")
	// The call reports itself mid-flight — through the program's Send, onto
	// the tile — and stays there until it finishes.
	seen("instance starting; retrying in 1s", 5*time.Second)
	close(release)
	seen("alpha  running", 5*time.Second)
	tm.Type("x")
	seen("stop alpha?", 5*time.Second)
	tm.Type("y")
	seen("stop — stopped", 5*time.Second)
	tm.Type("q")
	tm.WaitFinished(t)
	if node.starts != 1 || node.stops != 1 {
		t.Fatalf("starts=%d stops=%d, want 1 1", node.starts, node.stops)
	}
}

// The piped invocation fails before any part of the view exists, and names
// the command that carries the same data into a pipe.
func TestFleetDashboardRefusesPipedOutput(t *testing.T) {
	captureStdout(t, func() {
		err := cmdFleet([]string{"dashboard"})
		if err == nil {
			t.Fatal("dashboard ran without a terminal")
		}
		if !strings.Contains(err.Error(), "fleet metrics --watch") {
			t.Fatalf("error does not name the piped alternative: %v", err)
		}
	})
}

// A fleet file that cannot be read fails the command before the view, and a
// node whose token reference is unresolvable is a standing row, not a gap.
func TestDashModelForFleetFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if _, err := dashModelFor(""); err == nil {
		t.Fatal("missing fleet file did not fail")
	}
	writeFleetFile(t, "nodes:\n  - name: broken\n    host: 127.0.0.1\n    port: 1\n    tokenEnv: NO_SUCH_OUTFIT_VAR\n")
	m, err := dashModelFor("")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.entries) != 1 || m.entries[0].node != nil {
		t.Fatalf("entries: %+v", m.entries)
	}
	if m.entries[0].standing.Outcome != fleet.OutcomeConfigError {
		t.Fatalf("standing outcome: %v", m.entries[0].standing.Outcome)
	}
	view := m.View()
	if !strings.Contains(view, "broken") || !strings.Contains(view, "config-error") {
		t.Errorf("board does not show the broken node:\n%s", view)
	}
}
