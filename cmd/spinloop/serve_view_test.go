package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/metrics"
)

func fixDashNow(t *testing.T, at time.Time) {
	t.Helper()
	dashNow = func() time.Time { return at }
	t.Cleanup(func() { dashNow = time.Now })
}

// newTestServeView is the model a test drives directly: the window already
// measured at 100x30, the two data paths injected, and nothing else set.
func newTestServeView(readMetrics func() (metrics.Stats, error), readLog func(int64, int) (daemon.LogsResponse, error)) *serveView {
	m := &serveView{
		spinloopPath: "Spinloop",
		logOffset:    daemon.TailLog,
		logBudget:    serveViewLogBudget,
		logFollow:    true,
		readMetrics:  readMetrics,
		readLog:      readLog,
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func requireQuit(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("the message must return tea.Quit, got no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("the message must return tea.Quit, got a %T", cmd())
	}
}

// The model opens knowing nothing: the window unmeasured, the first read a
// tail, the follow on, and the frame carrying its two waiting notes until the
// first replies land.
func TestServeViewInitialState(t *testing.T) {
	fixDashNow(t, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	m := newTestServeView(nil, nil)
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init must schedule the first reading and the first log poll")
	}
	if m.logOffset != daemon.TailLog || !m.logFollow || m.logBudget != serveViewLogBudget {
		t.Errorf("the view must open on the tail, following, with its line budget: %+v", m)
	}
	v := m.View()
	if !strings.Contains(v, "waiting for the first reading…") {
		t.Errorf("View before a reading must carry the waiting note:\n%s", v)
	}
	if !strings.Contains(v, "waiting for the log…") {
		t.Errorf("View before a log read must carry the waiting note:\n%s", v)
	}
}

func TestServeViewMetricsReadReplacesPrior(t *testing.T) {
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	fixDashNow(t, at)
	first := metrics.Stats{State: "running", UptimeSeconds: 10}
	second := metrics.Stats{State: "running", UptimeSeconds: 40}
	next := first
	m := newTestServeView(func() (metrics.Stats, error) { return next, nil }, nil)

	m.Update(m.startMetricsRead()())
	if m.stats.UptimeSeconds != 10 || !m.statsAt.Equal(at) {
		t.Fatalf("the first reading was not stored: %+v at %v", m.stats, m.statsAt)
	}
	next = second
	m.Update(m.startMetricsRead()())
	if m.stats.UptimeSeconds != 40 {
		t.Errorf("the second reading must replace the first: %+v", m.stats)
	}
}

func TestServeViewMetricsFailedReadKeepsLast(t *testing.T) {
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	fixDashNow(t, at)
	var fail bool
	m := newTestServeView(func() (metrics.Stats, error) {
		if fail {
			return metrics.Stats{}, errors.New("the host probe wedged")
		}
		return metrics.Stats{State: "running", UptimeSeconds: 10}, nil
	}, nil)
	m.Update(m.startMetricsRead()())
	if m.stats.UptimeSeconds != 10 || !m.statsAt.Equal(at) {
		t.Fatal("the first reading was not stored")
	}

	fail = true
	m.Update(m.startMetricsRead()())
	if m.stats.UptimeSeconds != 10 || !m.statsAt.Equal(at) {
		t.Error("a failed read must leave the last reading in place, its age included")
	}
}

func TestServeViewMetricsSupersededReadDiscarded(t *testing.T) {
	fixDashNow(t, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	m := newTestServeView(func() (metrics.Stats, error) {
		return metrics.Stats{State: "running"}, nil
	}, nil)
	cmd := m.startMetricsRead()
	m.metricsGen++
	m.Update(cmd())
	if !m.statsAt.IsZero() {
		t.Error("a superseded read's reply must be discarded")
	}
}

// A reading the view could not renew is not drawn identically to one just
// read: past the board's own stale rule the state line carries the age.
func TestServeViewStaleReadingCarriesItsAge(t *testing.T) {
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	fixDashNow(t, at)
	m := newTestServeView(func() (metrics.Stats, error) {
		return metrics.Stats{State: "running"}, nil
	}, nil)
	m.Update(m.startMetricsRead()())
	if m.staleSuffix() != "" {
		t.Errorf("a fresh reading carries no age: %q", m.staleSuffix())
	}

	fixDashNow(t, at.Add(dashStaleThreshold*dashboardRefreshInterval+time.Second))
	if suffix := m.staleSuffix(); !strings.HasSuffix(suffix, "ago") {
		t.Errorf("a reading past the stale threshold must carry its age, got %q", suffix)
	}
}

// The first poll reads the tail — the backlog — budgeted the way the detail
// view budgets its tail; every poll after resumes from the stored offset with
// no byte limit, the daemon's own bounds doing the rest.
func TestServeViewLogFirstReadIsBacklog(t *testing.T) {
	var gotOffset int64
	var gotLimit int
	m := newTestServeView(nil, func(offset int64, limit int) (daemon.LogsResponse, error) {
		gotOffset, gotLimit = offset, limit
		return daemon.LogsResponse{Content: "one\ntwo\n", NextOffset: 8}, nil
	})
	m.Update(m.startLogPoll()())
	if gotOffset != daemon.TailLog {
		t.Errorf("the first poll must read the tail, got offset %d", gotOffset)
	}
	if gotLimit != serveViewLogTailBytes {
		t.Errorf("the first poll must budget the backlog, got limit %d", gotLimit)
	}
	if m.logContent != "one\ntwo\n" || m.logOffset != 8 {
		t.Errorf("the backlog was not stored: %q at %d", m.logContent, m.logOffset)
	}
}

func TestServeViewLogPollsAppendWithoutDuplicates(t *testing.T) {
	var calls [][2]int64
	m := newTestServeView(nil, func(offset int64, limit int) (daemon.LogsResponse, error) {
		calls = append(calls, [2]int64{offset, int64(limit)})
		if offset == daemon.TailLog {
			return daemon.LogsResponse{Content: "one\ntwo\n", NextOffset: 8}, nil
		}
		return daemon.LogsResponse{Content: "three\n", NextOffset: 14}, nil
	})
	m.Update(m.startLogPoll()())
	m.Update(m.startLogPoll()())
	if m.logContent != "one\ntwo\nthree\n" {
		t.Errorf("the append must keep the backlog and add the new lines once: %q", m.logContent)
	}
	if len(calls) != 2 || calls[1][0] != 8 || calls[1][1] != 0 {
		t.Errorf("the second poll must resume from the stored offset with no byte limit: %v", calls)
	}
}

// A file that shrank — truncated or replaced — drops the content from the
// file it replaced and resumes from the reply's end.
func TestServeViewLogStaleOffsetResumes(t *testing.T) {
	m := newTestServeView(nil, func(offset int64, limit int) (daemon.LogsResponse, error) {
		if offset == daemon.TailLog {
			return daemon.LogsResponse{Content: "one\ntwo\n", NextOffset: 8}, nil
		}
		return daemon.LogsResponse{NextOffset: 3, Size: 3, StaleOffset: true}, nil
	})
	m.Update(m.startLogPoll()())
	m.Update(m.startLogPoll()())
	if m.logContent != "" {
		t.Errorf("a stale offset must drop the content from the replaced file: %q", m.logContent)
	}
	if m.logOffset != 3 {
		t.Errorf("the cursor must resume from the reply's end, got %d", m.logOffset)
	}
	if m.logBehind != 0 {
		t.Errorf("the window must be back on the newest line, behind %d", m.logBehind)
	}
}

func TestServeViewLogSupersededPollDiscarded(t *testing.T) {
	m := newTestServeView(nil, func(offset int64, limit int) (daemon.LogsResponse, error) {
		return daemon.LogsResponse{Content: "one\n", NextOffset: 5}, nil
	})
	cmd := m.startLogPoll()
	m.logGen++
	m.Update(cmd())
	if m.logContent != "" || m.logOffset != daemon.TailLog {
		t.Errorf("a superseded poll's reply must be discarded: %q at %d", m.logContent, m.logOffset)
	}
}

// A long session never grows an unbounded buffer: the tail is trimmed to the
// line budget, the file on disk keeping the whole record.
func TestServeViewLogTrimmedToBudget(t *testing.T) {
	m := newTestServeView(nil, func(offset int64, limit int) (daemon.LogsResponse, error) {
		var b strings.Builder
		for i := 0; i < 10; i++ {
			fmt.Fprintf(&b, "line %d\n", i)
		}
		return daemon.LogsResponse{Content: b.String(), NextOffset: 100}, nil
	})
	m.logBudget = 5
	m.Update(m.startLogPoll()())
	if got := lineCount(m.logContent); got != 5 {
		t.Errorf("the tail must be trimmed to the budget of 5 lines, got %d", got)
	}
	if !strings.HasPrefix(m.logContent, "line 5\n") {
		t.Errorf("the trim must keep the newest lines: %q", m.logContent)
	}
}

// A failed poll leaves the prior content in place; with no content at all it
// says why the pane is empty rather than drawing nothing.
func TestServeViewLogReadFailure(t *testing.T) {
	m := newTestServeView(nil, func(offset int64, limit int) (daemon.LogsResponse, error) {
		return daemon.LogsResponse{}, errors.New("the log is gone")
	})
	m.Update(m.startLogPoll()())
	if m.logNote != "log read failed: the log is gone" {
		t.Errorf("a failed first read must say so, got note %q", m.logNote)
	}
	if v := m.View(); !strings.Contains(v, "log read failed: the log is gone") {
		t.Errorf("the empty pane must carry the failure note:\n%s", v)
	}

	m.logContent = "one\n"
	m.Update(m.startLogPoll()())
	if m.logContent != "one\n" {
		t.Errorf("a failed poll must leave the prior content in place: %q", m.logContent)
	}
}

// The scroll: one line at a time, a page at a time, clamped at both ends —
// a press at either end leaves the window where it is.
func TestServeViewKeysScroll(t *testing.T) {
	m := newTestServeView(nil, func(offset int64, limit int) (daemon.LogsResponse, error) {
		var b strings.Builder
		for i := 0; i < 30; i++ {
			fmt.Fprintf(&b, "line %02d\n", i)
		}
		return daemon.LogsResponse{Content: b.String(), NextOffset: 100}, nil
	})
	m.Update(m.startLogPoll()())
	// 30 lines against a 24-row pane: the window can sit at most 6 lines
	// behind the newest.
	cases := []struct {
		key  tea.Msg
		want int
	}{
		{tea.KeyMsg{Type: tea.KeyUp}, 1},
		{tea.KeyMsg{Type: tea.KeyDown}, 0},
		{tea.KeyMsg{Type: tea.KeyDown}, 0}, // on the newest line, down stays put
		{tea.KeyMsg{Type: tea.KeyPgUp}, 6},
		{tea.KeyMsg{Type: tea.KeyUp}, 6}, // at the oldest retained line, up stays put
		{tea.KeyMsg{Type: tea.KeyPgDown}, 0},
		{tea.KeyMsg{Type: tea.KeyDown}, 0},
	}
	for i, tc := range cases {
		m.Update(tc.key)
		if m.logBehind != tc.want {
			t.Errorf("key %d: behind = %d, want %d", i, m.logBehind, tc.want)
		}
	}
}

// On the newest line, new output keeps the window there; scrolled away, the
// window stays put as the new lines land behind it.
func TestServeViewWindowSticksAndStays(t *testing.T) {
	calls := 0
	m := newTestServeView(nil, func(offset int64, limit int) (daemon.LogsResponse, error) {
		calls++
		if calls == 1 {
			var b strings.Builder
			for i := 0; i < 30; i++ {
				fmt.Fprintf(&b, "line %02d\n", i)
			}
			return daemon.LogsResponse{Content: b.String(), NextOffset: 100}, nil
		}
		return daemon.LogsResponse{Content: "line 30\n", NextOffset: 110}, nil
	})
	m.Update(m.startLogPoll()())
	m.Update(m.startLogPoll()())
	if m.logBehind != 0 {
		t.Fatalf("on the newest line the window must stay there, behind %d", m.logBehind)
	}
	if !strings.HasSuffix(m.logContent, "line 30\n") {
		t.Fatalf("the new line must land in the pane: %q", m.logContent)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyUp}) // scroll one line away
	if m.logBehind != 1 {
		t.Fatalf("the up arrow must move the window one line, behind %d", m.logBehind)
	}
	m.Update(m.startLogPoll()())
	if m.logBehind != 2 {
		t.Errorf("scrolled away, new output must stay behind the window, behind %d", m.logBehind)
	}
	if !strings.HasSuffix(m.logContent, "line 30\n") {
		t.Errorf("the new line must be retained while the pane stays put: %q", m.logContent)
	}
}

// f pauses and resumes the follow: paused, the log tick starts no poll while
// the metrics tick keeps its own cadence; resumed, the next tick starts a
// poll again.
func TestServeViewFollowPauseAndResume(t *testing.T) {
	calls := 0
	m := newTestServeView(
		func() (metrics.Stats, error) { return metrics.Stats{State: "running"}, nil },
		func(offset int64, limit int) (daemon.LogsResponse, error) {
			calls++
			return daemon.LogsResponse{Content: "a\n", NextOffset: 2}, nil
		},
	)
	m.Update(m.startLogPoll()())

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if m.logFollow {
		t.Error("f must pause the follow")
	}
	m.Update(serveViewLogTickMsg{})
	if m.logBusy || calls != 1 {
		t.Errorf("a paused follow must not start a poll (busy %v, %d calls)", m.logBusy, calls)
	}
	m.Update(serveViewMetricsTickMsg{})
	if !m.metricsBusy {
		t.Error("pausing the log must not pause the metrics refresh")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if !m.logFollow {
		t.Fatal("f must resume the follow")
	}
	m.Update(serveViewLogTickMsg{})
	if !m.logBusy {
		t.Error("the next tick after resuming must start a poll")
	}
}

// While the follow is paused the offset holds; the next poll reads from it, so
// whatever the engine wrote in the meantime arrives on the resume.
func TestServeViewPauseLosesNothing(t *testing.T) {
	m := newTestServeView(nil, func(offset int64, limit int) (daemon.LogsResponse, error) {
		if offset == daemon.TailLog {
			return daemon.LogsResponse{Content: "a\n", NextOffset: 2}, nil
		}
		return daemon.LogsResponse{Content: "b\nc\n", NextOffset: 10}, nil
	})
	m.Update(m.startLogPoll()())
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if m.logOffset != 2 {
		t.Fatalf("the pause must hold the offset, got %d", m.logOffset)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m.Update(m.startLogPoll()())
	if m.logContent != "a\nb\nc\n" {
		t.Errorf("nothing written while paused may be lost: %q", m.logContent)
	}
}

// The keys that leave: q and Ctrl+C both stop the engine — outside Update,
// in the command — and the view quits on the round trip.
func TestServeViewQuitStopsTheEngine(t *testing.T) {
	for _, key := range []tea.Msg{
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")},
		tea.KeyMsg{Type: tea.KeyCtrlC},
	} {
		stopped := 0
		m := newTestServeView(nil, nil)
		m.stop = func() { stopped++ }

		_, cmd := m.Update(key)
		if cmd == nil {
			t.Fatalf("%v must return the stop command", key)
		}
		if stopped != 0 {
			t.Fatalf("the stop must not run inside Update")
		}
		msg := cmd()
		if stopped != 1 {
			t.Errorf("the stop command must run the graceful stop")
		}
		if _, ok := msg.(serveViewStoppedMsg); !ok {
			t.Fatalf("the stop command must report back, got a %T", msg)
		}
		_, quit := m.Update(msg)
		requireQuit(t, quit)
	}
}

// The engine's own exit closes the view too, whatever its cause.
func TestServeViewEngineExitedQuits(t *testing.T) {
	m := newTestServeView(nil, nil)
	_, cmd := m.Update(serveViewEngineExitedMsg{})
	requireQuit(t, cmd)
}

// The frame: the title bar with the path and the log's state, the metrics
// section in the view's own format — each series its gauge and its bar side
// by side on one line — the tailed log, the dividers, and the footer naming
// exactly the keys the view answers to.
func TestServeViewFrame(t *testing.T) {
	fixDashNow(t, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	stats := metrics.Stats{
		State:         "running",
		UptimeSeconds: 90,
		Runner:        "llama.cpp",
		ModelID:       "org/model",
		CPU:           &metrics.CpuStat{Utilization: 42},
		Memory:        &metrics.MemoryStat{Total: 1000, Used: 500},
		History:       []metrics.HistorySample{{Time: 1, CPU: f64ptr(10), Mem: f64ptr(40)}},
		Tokens:        &metrics.TokenStats{PromptTokens: 10, GenerationTokens: 5, Requests: ptrInt(2)},
	}
	m := newTestServeView(
		func() (metrics.Stats, error) { return stats, nil },
		func(offset int64, limit int) (daemon.LogsResponse, error) {
			return daemon.LogsResponse{Content: "alpha\nbeta\n", NextOffset: 12}, nil
		},
	)
	m.Update(m.startMetricsRead()())
	m.Update(m.startLogPoll()())

	v := m.View()
	for _, want := range []string{
		"serve",                       // the screen
		"Spinloop",                    // the path, right of the screen
		"log: following",              // the log's state
		"running  (up 1m 30s)",        // state with uptime
		"llama.cpp  org/model",        // what is served
		" 42%",                        // the CPU gauge of the current reading
		" 50%",                        // the RAM gauge
		"prompt tokens:", "requests:", // the counters
		"alpha", "beta", // the tailed log
		"scroll", "follow", "quit", // the footer's keys
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the frame is missing %q:\n%s", want, v)
		}
	}
	// The gauge of each series sits with its bar on the series' own line:
	// the label once, and both drawings on it.
	if strings.Count(v, "CPU") != 1 {
		t.Errorf("the CPU series must draw once, label and all:\n%s", v)
	}
	if strings.Count(v, "RAM") != 1 {
		t.Errorf("the RAM series must draw once, label and all:\n%s", v)
	}
	for _, line := range strings.Split(v, "\n") {
		if strings.Contains(line, "CPU") && (!strings.Contains(line, "█") || !strings.Contains(line, "▁")) {
			t.Errorf("the CPU line must carry its gauge and its bar side by side:\n%q", line)
		}
	}
	// Every line fits the window.
	for i, line := range strings.Split(v, "\n") {
		if w := lipgloss.Width(line); w > 100 {
			t.Errorf("line %d is %d columns wide, want at most 100: %q", i, w, line)
		}
	}
}

// Statistics without a request figure draw no requests line: the view shows
// the figures the engine exposes and nothing it does not.
func TestServeViewNoRequestCount(t *testing.T) {
	fixDashNow(t, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	stats := metrics.Stats{
		State:         "running",
		UptimeSeconds: 90,
		Runner:        "llama.cpp",
		ModelID:       "org/model",
		CPU:           &metrics.CpuStat{Utilization: 42},
		Tokens:        &metrics.TokenStats{PromptTokens: 10, GenerationTokens: 5},
	}
	m := newTestServeView(
		func() (metrics.Stats, error) { return stats, nil },
		func(offset int64, limit int) (daemon.LogsResponse, error) {
			return daemon.LogsResponse{Content: "alpha\n", NextOffset: 6}, nil
		},
	)
	m.Update(m.startMetricsRead()())
	m.Update(m.startLogPoll()())

	v := m.View()
	if strings.Contains(v, "requests:") {
		t.Errorf("a requests line drawn for statistics without the figure:\n%s", v)
	}
	if !strings.Contains(v, "prompt tokens:") {
		t.Errorf("the prompt tokens line is missing:\n%s", v)
	}
}

// The same frame, the follow paused: the title bar says so.
func TestServeViewFramePaused(t *testing.T) {
	fixDashNow(t, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	m := newTestServeView(nil, nil)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if v := m.View(); !strings.Contains(v, "log: paused") {
		t.Errorf("the title bar must name the paused log:\n%s", v)
	}
}

// A window shorter than the frame's fixed rows: the log pane floors at one
// row, and the frame still draws — a negative pane would clip out of range.
func TestServeViewTinyWindowFloorsTheLogPane(t *testing.T) {
	m := newTestServeView(nil, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 3})
	if _, logH := m.sectionHeights(); logH != 1 {
		t.Errorf("the log pane must floor at one row on a 3-row window, got %d", logH)
	}
	if m.View() == "" {
		t.Error("the frame must still draw on a tiny window")
	}
}

func f64ptr(v float64) *float64 { return &v }
