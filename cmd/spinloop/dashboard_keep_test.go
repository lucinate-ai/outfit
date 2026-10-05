package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/fleet"
	"github.com/spinloop-ai/spinloop/internal/inference"
	"github.com/spinloop-ai/spinloop/internal/metrics"
)

// keeperDashNode wraps the fake with the keep capability: a node the dashboard's
// type assertion finds to be a Keeper, so the keep key and prompt are offered
// for it. Keep records the duration and returns a deadline; keepErr makes it
// refuse, to exercise a failed keep, and deadline fixes the control plane's
// reply when a test wants a specific one.
type keeperDashNode struct {
	f        *fakeDashNode
	keeps    int
	keepDur  time.Duration
	keepErr  error
	deadline string
}

var (
	_ fleet.Node   = (*keeperDashNode)(nil)
	_ fleet.Keeper = (*keeperDashNode)(nil)
)

func (n *keeperDashNode) Name() string { return n.f.Name() }

func (n *keeperDashNode) Status(ctx context.Context) (daemon.StatusResponse, error) {
	return n.f.Status(ctx)
}

func (n *keeperDashNode) Metrics(ctx context.Context) (metrics.Stats, error) { return n.f.Metrics(ctx) }

func (n *keeperDashNode) Start(ctx context.Context) (daemon.StatusResponse, error) {
	return n.f.Start(ctx)
}

func (n *keeperDashNode) StartWith(ctx context.Context, dc *inference.DeployConfig, engineKey string) (daemon.StatusResponse, error) {
	return n.f.StartWith(ctx, dc, engineKey)
}

func (n *keeperDashNode) Stop(ctx context.Context) (daemon.StatusResponse, error) {
	return n.f.Stop(ctx)
}

func (n *keeperDashNode) Logs(ctx context.Context, offset int64, limit int) (daemon.LogsResponse, error) {
	return n.f.Logs(ctx, offset, limit)
}

func (n *keeperDashNode) Keep(ctx context.Context, d time.Duration) (string, error) {
	if n.keepErr != nil {
		return "", n.keepErr
	}
	n.keeps++
	n.keepDur = d
	if n.deadline != "" {
		return n.deadline, nil
	}
	return time.Now().Add(d).UTC().Format(time.RFC3339), nil
}

// keeperModel is a one-node board over a keeperDashNode, the shape every keep
// test drives.
func keeperModel(node *keeperDashNode) *dashModel {
	return &dashModel{
		entries: []dashEntry{{name: node.f.Name(), kind: fleet.KindCloud, node: node}},
		results: []fleet.NodeResult{{Name: node.f.Name()}},
		actions: make([]dashAction, 1),
		width:   120, height: 40,
	}
}

// openKeepPrompt presses k on the model and returns the model with the prompt
// open, failing the test if k did not open it.
func openKeepPrompt(t *testing.T, m *dashModel) *dashModel {
	t.Helper()
	next, cmd := m.Update(dashKey("k"))
	if cmd != nil {
		t.Fatal("opening the prompt set off an action")
	}
	m = next.(*dashModel)
	if !m.keepPrompt {
		t.Fatal("k did not open the keep prompt")
	}
	return m
}

// typeKey sends one key to the model and hands back the model, dropping the
// command — what the prompt's typing and backspace never produce.
func typeKey(m *dashModel, s string) *dashModel {
	next, _ := m.Update(dashKey(s))
	return next.(*dashModel)
}

// TestDashKeepPromptOpensOnTheKeyForARemoteNode: k opens the prompt, pre-filled
// with 4h, for a node that can be kept.
func TestDashKeepPromptOpensOnTheKeyForARemoteNode(t *testing.T) {
	node := &keeperDashNode{f: newFakeDashNode("stopped")}
	m := keeperModel(node)
	m = openKeepPrompt(t, m)
	if m.keepBuf != "4h" {
		t.Errorf("prompt not pre-filled with 4h, got %q", m.keepBuf)
	}
}

// A local daemon node has no retention tag to set, so k drives nothing for it:
// the prompt does not open and no action is set off.
func TestDashKeepDrivesNothingForALocalNode(t *testing.T) {
	f := newFakeDashNode("stopped")
	m := &dashModel{
		entries: []dashEntry{{name: "box", kind: fleet.KindDaemon, node: f}},
		results: []fleet.NodeResult{{Name: "box"}},
		actions: make([]dashAction, 1),
		width:   120, height: 40,
	}
	next, cmd := m.Update(dashKey("k"))
	if cmd != nil {
		t.Fatal("k on a local node set off an action")
	}
	if next.(*dashModel).keepPrompt {
		t.Fatal("k opened the keep prompt for a local node")
	}
}

// While the prompt is open the board stands still: navigation keys go to the
// prompt (and change nothing), not to the grid.
func TestDashKeepPromptStandsStillOverNavigation(t *testing.T) {
	node := &keeperDashNode{f: newFakeDashNode("stopped")}
	m := keeperModel(node)
	m = openKeepPrompt(t, m)
	before := m.cursor
	for _, key := range []string{"down", "up", "left", "right", "r"} {
		next, _ := m.Update(dashKey(key))
		m = next.(*dashModel)
	}
	if m.cursor != before {
		t.Errorf("navigation moved the selection while the prompt was open: %d -> %d", before, m.cursor)
	}
	if !m.keepPrompt {
		t.Fatal("the prompt closed on a navigation key")
	}
}

// A confirmed entry that is a positive duration sets off a keep of that length;
// the pre-filled 4h is the one-Enter common case.
func TestDashKeepConfirmsADuration(t *testing.T) {
	node := &keeperDashNode{f: newFakeDashNode("stopped"), deadline: "2030-01-02T04:00:00Z"}
	m := keeperModel(node)
	m = openKeepPrompt(t, m)
	_, cmd := m.Update(dashKey("enter"))
	if cmd == nil {
		t.Fatal("enter did not set off the keep")
	}
	if m.keepPrompt {
		t.Fatal("the prompt stayed open after a valid confirm")
	}
	if m.actions[0].verb != "keep" {
		t.Fatalf("no keep recorded on the node: %+v", m.actions[0])
	}
	runAction(t, cmd)
	if node.keeps != 1 || node.keepDur != 4*time.Hour {
		t.Errorf("keep not sent with the pre-filled 4h: keeps=%d dur=%s", node.keeps, node.keepDur)
	}
}

// A typed duration replaces the pre-fill and is what the keep is sent with.
func TestDashKeepTypesADuration(t *testing.T) {
	for _, tc := range []struct {
		entry string
		want  time.Duration
	}{
		{"90m", 90 * time.Minute},
		{"1h30m", 90 * time.Minute},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			node := &keeperDashNode{f: newFakeDashNode("stopped")}
			m := keeperModel(node)
			m = openKeepPrompt(t, m)
			// Clear the pre-filled 4h, then type the entry.
			m = typeKey(m, "backspace")
			m = typeKey(m, "backspace")
			m = typeKey(m, tc.entry)
			next, cmd := m.Update(dashKey("enter"))
			m = next.(*dashModel)
			if cmd == nil {
				t.Fatalf("enter did not set off the keep for %q", tc.entry)
			}
			runAction(t, cmd)
			if node.keepDur != tc.want {
				t.Errorf("keep sent with %s, want %s", node.keepDur, tc.want)
			}
		})
	}
}

// A confirmed entry that is not a positive duration leaves the prompt open with
// the reason at the foot, and sends nothing: 4hours is not a duration, and an
// empty entry is not one either.
func TestDashKeepLeavesThePromptOpenOnAnInvalidEntry(t *testing.T) {
	for _, entry := range []string{"4hours", ""} {
		t.Run(entry, func(t *testing.T) {
			node := &keeperDashNode{f: newFakeDashNode("stopped")}
			m := keeperModel(node)
			m = openKeepPrompt(t, m)
			// Clear the pre-filled 4h; type the entry when there is one.
			m = typeKey(m, "backspace")
			m = typeKey(m, "backspace")
			if entry != "" {
				m = typeKey(m, entry)
			}
			next, cmd := m.Update(dashKey("enter"))
			if cmd != nil {
				t.Fatal("an invalid entry set off a keep")
			}
			m = next.(*dashModel)
			if !m.keepPrompt {
				t.Fatal("the prompt closed on an invalid entry")
			}
			if m.keepErr == "" {
				t.Fatal("no reason shown for the invalid entry")
			}
			if node.keeps != 0 {
				t.Fatal("a keep was sent for an invalid entry")
			}
		})
	}
}

// esc cancels the prompt and sends nothing; q cancels and quits.
func TestDashKeepCancels(t *testing.T) {
	t.Run("esc", func(t *testing.T) {
		node := &keeperDashNode{f: newFakeDashNode("stopped")}
		m := openKeepPrompt(t, keeperModel(node))
		next, cmd := m.Update(dashKey("esc"))
		if cmd != nil {
			t.Fatal("esc set off a command")
		}
		if next.(*dashModel).keepPrompt {
			t.Fatal("esc did not cancel the prompt")
		}
		if node.keeps != 0 {
			t.Fatal("esc sent a keep")
		}
	})
	t.Run("q", func(t *testing.T) {
		node := &keeperDashNode{f: newFakeDashNode("stopped")}
		m := openKeepPrompt(t, keeperModel(node))
		_, cmd := m.Update(dashKey("q"))
		if cmd == nil {
			t.Fatal("q did not quit")
		}
		if node.keeps != 0 {
			t.Fatal("q sent a keep")
		}
	})
}

// A keep in flight rides the node's action: the tile carries the spinner and
// the keeping verb, a busy node takes no second keep, and completion clears the
// action, brings the node forward for one more read, and names the deadline.
func TestDashKeepInFlightAndCompletion(t *testing.T) {
	dashFixNow(t, dashTestClock)
	node := &keeperDashNode{f: newFakeDashNode("stopped"), deadline: "2030-01-02T04:00:00Z"}
	m := keeperModel(node)
	m = openKeepPrompt(t, m)
	next, cmd := m.Update(dashKey("enter"))
	m = next.(*dashModel)

	// In flight: the tile shows the keep, and the node takes no second keep.
	if m.actions[0].verb != "keep" {
		t.Fatalf("no keep recorded: %+v", m.actions[0])
	}
	tile := dashTestTile(node.f.Name(), m.results[0], true, m.actions[0])
	if !strings.Contains(tile, "keeping") {
		t.Errorf("in-flight tile does not show the keep:\n%s", tile)
	}
	knext, _ := m.Update(dashKey("k"))
	if knext.(*dashModel).keepPrompt {
		t.Fatal("a busy node opened a second keep prompt")
	}

	// Completion: the action clears, the node is read again now, and the footer
	// names the deadline the control plane set.
	smsg, _ := runAction(t, cmd).(dashActionMsg)
	if smsg.retainUntil != "2030-01-02T04:00:00Z" {
		t.Errorf("completion message did not carry the deadline: %q", smsg.retainUntil)
	}
	m2, _ := m.Update(smsg)
	mm := m2.(*dashModel)
	if mm.actions[0].verb != "" {
		t.Fatalf("the finished keep was not cleared: %+v", mm.actions[0])
	}
	if mm.dueAt(0).After(time.Now()) {
		t.Error("the kept node was not brought forward for one more read")
	}
	if !strings.Contains(mm.statusLine, "keep — retain until 2030-01-02T04:00:00Z") {
		t.Errorf("completion line: %q", mm.statusLine)
	}
}

// A keep that the control plane refuses is reported as a failure with its
// reason, and the board stays open — including the named no-update-URL case.
func TestDashKeepFailureShowsItsReason(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errors.New("no update_url configured: the cloud deployment needs to be updated for keep support"), "no update_url"},
		{errors.New("keep returned HTTP 404: no running instance"), "no running instance"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			node := &keeperDashNode{f: newFakeDashNode("stopped"), keepErr: tc.err}
			m := keeperModel(node)
			m = openKeepPrompt(t, m)
			next, cmd := m.Update(dashKey("enter"))
			m = next.(*dashModel)
			fmsg, _ := runAction(t, cmd).(dashActionMsg)
			m2, _ := m.Update(fmsg)
			mm := m2.(*dashModel)
			if mm.actions[0].verb != "" {
				t.Fatalf("the failed keep was not cleared: %+v", mm.actions[0])
			}
			if !strings.Contains(mm.statusLine, "keep failed —") {
				t.Errorf("failure line does not say it failed: %q", mm.statusLine)
			}
			if !strings.Contains(mm.statusLine, tc.want) {
				t.Errorf("failure line missing the reason %q: %q", tc.want, mm.statusLine)
			}
		})
	}
}

// The abort key drives nothing on a keep in flight: only a start is abortable,
// so a keep's wait runs to its own end.
func TestDashAbortDrivesNothingOnAKeep(t *testing.T) {
	node := &keeperDashNode{f: newFakeDashNode("stopped")}
	m := keeperModel(node)
	m = openKeepPrompt(t, m)
	next, _ := m.Update(dashKey("enter"))
	m = next.(*dashModel)
	if m.actions[0].verb != "keep" {
		t.Fatal("no keep in flight")
	}
	anext, _ := m.Update(dashKey("a"))
	mm := anext.(*dashModel)
	if mm.actions[0].verb != "keep" {
		t.Fatalf("the abort ended the keep in flight: %+v", mm.actions[0])
	}
	if mm.actions[0].aborted {
		t.Fatal("the abort marked the keep as abandoned")
	}
}

// The keep hint shows only where the key would drive something: a cloud node
// shows it, a local node hides it, and a busy cloud node hides it. The start
// and stop entries sit beside it by the node's own state: a stopped cloud
// environment shows keep and start, a running one shows keep and stop, and a
// busy one shows neither.
func TestDashKeepHintOnlyWhereItDrivesSomething(t *testing.T) {
	read := func(state string) fleet.NodeResult {
		return fleet.NodeResult{Name: "env", Outcome: fleet.OutcomeOK, Metrics: metrics.Stats{State: state}}
	}
	t.Run("stopped cloud shows keep and start", func(t *testing.T) {
		node := &keeperDashNode{f: newFakeDashNode("stopped")}
		m := keeperModel(node)
		m.results[0] = read("stopped")
		if got, want := m.gridKeys(), "↑↓←→ move   s start   k keep   g format   r refresh   q quit"; got != want {
			t.Errorf("grid hint:\ngot:  %q\nwant: %q", got, want)
		}
		if got, want := m.detailKeys(), "esc back   s start   k keep   f follow"; got != want {
			t.Errorf("detail hint:\ngot:  %q\nwant: %q", got, want)
		}
	})
	t.Run("running cloud shows keep and stop", func(t *testing.T) {
		node := &keeperDashNode{f: newFakeDashNode("running")}
		m := keeperModel(node)
		m.results[0] = read("running")
		if got, want := m.gridKeys(), "↑↓←→ move   k keep   x stop   g format   r refresh   q quit"; got != want {
			t.Errorf("grid hint:\ngot:  %q\nwant: %q", got, want)
		}
		if got, want := m.detailKeys(), "esc back   k keep   x stop   f follow"; got != want {
			t.Errorf("detail hint:\ngot:  %q\nwant: %q", got, want)
		}
	})
	t.Run("local node hides it", func(t *testing.T) {
		f := newFakeDashNode("stopped")
		m := &dashModel{
			entries: []dashEntry{{name: "box", kind: fleet.KindDaemon, node: f}},
			results: []fleet.NodeResult{{Name: "box", Outcome: fleet.OutcomeOK, Metrics: metrics.Stats{State: "stopped"}}},
			actions: make([]dashAction, 1),
			width:   120, height: 40,
		}
		if got, want := m.gridKeys(), "↑↓←→ move   s start   g format   r refresh   q quit"; got != want {
			t.Errorf("grid hint:\ngot:  %q\nwant: %q", got, want)
		}
	})
	t.Run("busy cloud hides it", func(t *testing.T) {
		node := &keeperDashNode{f: newFakeDashNode("stopped")}
		m := keeperModel(node)
		m.results[0] = read("stopped")
		m = openKeepPrompt(t, m)
		next, _ := m.Update(dashKey("enter"))
		m = next.(*dashModel)
		if got, want := m.gridKeys(), "↑↓←→ move   g format   r refresh   q quit"; got != want {
			t.Errorf("grid hint:\ngot:  %q\nwant: %q", got, want)
		}
	})
}

// The deadline rides the node's read onto the tile and the detail screen, as a
// relative keep after the active figure on the same line, and a read without one
// draws no keep.
func TestDashTileAndDetailShowKeep(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dashFixNow(t, at)
	node := &keeperDashNode{f: newFakeDashNode("stopped")}
	m := keeperModel(node)
	r := fleet.NodeResult{
		Name:    "env",
		Outcome: fleet.OutcomeOK,
		Metrics: metrics.Stats{
			State: "stopped", Runner: "llamacpp", ModelID: "org/m",
			LastActiveAt: "2025-12-31T23:58:15Z", IdleSeconds: 125,
			RetainUntil: at.Add(2 * time.Hour).UTC().Format(time.RFC3339),
		},
	}
	m.results[0] = r

	tile := dashTestTile("env", r, true, dashAction{})
	if line := aLineContaining(tile, "2m 5s ago", "keep for 2h"); line == "" {
		t.Errorf("tile did not put the keep on the active line:\n%s", tile)
	}
	lines := m.detailNodeLines()
	found := false
	for _, l := range lines {
		if strings.Contains(l, "keep for 2h") {
			found = true
		}
	}
	if !found {
		t.Errorf("detail view missing the keep:\n%s", strings.Join(lines, "\n"))
	}
}

// A read without a deadline — a local node, or an unkept or lapsed environment —
// draws no keep on the tile.
func TestDashTileOmitsKeepWhenAbsent(t *testing.T) {
	r := fleet.NodeResult{
		Name:    "box",
		Outcome: fleet.OutcomeOK,
		Metrics: metrics.Stats{State: "running", Runner: "llamacpp", ModelID: "org/m",
			CPU: &metrics.CpuStat{Utilization: 30}},
	}
	tile := dashTestTile("box", r, true, dashAction{})
	if strings.Contains(tile, "keep for") {
		t.Errorf("tile invented a keep the read does not carry:\n%s", tile)
	}
}
