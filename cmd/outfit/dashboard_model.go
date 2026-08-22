// The `fleet dashboard` model: what the screen shows and how keys and
// messages change it. Bubble Tea drives it through Init/Update/View, but
// every rule here — refresh, actions, layout — is plain Go over plain data,
// so the suite runs the whole logic without a terminal.

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lucinate-ai/outfit/internal/daemon"
	"github.com/lucinate-ai/outfit/internal/fleet"
)

// dashboardRefreshInterval is how often the board re-reads the local daemon
// machines. It is a variable so a test never waits for a slow node.
var dashboardRefreshInterval = 2 * time.Second

// dashboardRemoteRefreshInterval is the cadence for kind: remote
// environments instead. Each of their statuses is a signed call through the
// cloud control plane — a Lambda invocation, not a socket on the
// sideboard — so the board refreshes the local machines on the tick and the
// cloud environments on this slower deadline. Variable, for the same reason.
var dashboardRemoteRefreshInterval = 60 * time.Second

// dashEntry is one fleet-file entry and what it resolved to. Node is nil
// when the entry could not become a node at all — its token reference names
// a variable that is set nowhere — in which case Standing is the outcome its
// panel shows for the life of the view: the very reason the one-shot
// surfaces print for it, so the board never hides a broken node. Kind is the
// fleet file's own, and it decides the refresh cadence, not the node.
type dashEntry struct {
	name     string
	kind     string
	node     fleet.Node
	standing fleet.NodeResult
}

// dashAction is the board's account of a start or stop in flight on one
// node: the verb and the last line the call has reported. A node with nothing
// in flight carries the zero value.
type dashAction struct {
	verb string // "start" or "stop"
	line string // the call's latest status line; empty until it reports one
}

// dashModel is the program's state: the board, the selection, and the in-
// flight things — a refresh round per node group (local machines, cloud
// environments), a stop's confirmation, a start or stop per node.
type dashModel struct {
	fleetPath string
	entries   []dashEntry
	results   []fleet.NodeResult // parallel to entries; zero until a round lands
	actions   []dashAction       // parallel to entries; a node's in-flight start or stop

	cursor    int // the selected node
	scrollRow int // the first grid row on screen

	confirm    bool // a stop is waiting on its confirmation
	statusLine string

	// send feeds a message back into the program from outside the Update
	// loop — the in-flight progress of a start, which its call reports from
	// its own goroutine. It is the tea.Program's Send, safe from any
	// goroutine and a no-op once the program has left; nil where the model
	// is driven directly and nothing is wired to the program.
	send func(msg tea.Msg)

	fastGen, slowGen   int       // rounds started in each group; each answer carries its number
	fastBusy, slowBusy bool      // a round in flight in that group
	nextSlowAt         time.Time // when the cloud group is next due; zero means due now

	width, height int
}

// dashTickMsg fires on the fast interval.
type dashTickMsg time.Time

// dashRefreshMsg is one completed round of one group. idx and results are
// parallel: the entries this round re-read, and what each answered. An
// answer whose round is no longer the newest in its group is dropped: a late
// reply must not paint over a fresher board.
type dashRefreshMsg struct {
	remote  bool // the cloud group's round
	gen     int
	idx     []int
	results []fleet.NodeResult
}

// dashVerbProgress is the in-flight wording for a verb — the footer and the
// tile share it, since start simply takes -ing and stop drops its p.
func dashVerbProgress(verb string) string {
	if verb == "stop" {
		return "stopping"
	}
	return verb + "ing"
}

// dashActionProgressMsg is one intermediate line of a call behind an in-
// flight action, sent to the program by the call's own goroutine as the work
// proceeds.
type dashActionProgressMsg struct {
	node string
	line string
}

// dashActionMsg is one completed start or stop.
type dashActionMsg struct {
	node   string
	verb   string // "start" or "stop"
	status daemon.StatusResponse
	err    error
}

// dashTickCmd schedules the next fast tick. The tick is one-shot, so every
// tick reschedules the next: drop the reschedule and the board is still
// after the second round.
func dashTickCmd() tea.Cmd {
	return tea.Tick(dashboardRefreshInterval, func(time.Time) tea.Msg { return dashTickMsg{} })
}

// Init starts the rounds the board is due for — the local round plus the
// cloud round, because on a cold open the cloud deadline has never been
// spent — and the tick that keeps them coming.
func (m *dashModel) Init() tea.Cmd {
	return tea.Batch(append([]tea.Cmd{dashTickCmd()}, m.startRounds()...)...)
}

func (m *dashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.keepVisible()
	case dashTickMsg:
		// One tick, the due rounds. The tick reschedules itself whenever it
		// fires, and a round starts in a group only when none is in flight
		// there, so a slow node stretches one round to its deadline rather
		// than overlapping the next.
		return m, tea.Batch(append([]tea.Cmd{dashTickCmd()}, m.startRounds()...)...)
	case dashRefreshMsg:
		if msg.remote {
			if msg.gen != m.slowGen {
				return m, nil
			}
			m.slowBusy = false
		} else {
			if msg.gen != m.fastGen {
				return m, nil
			}
			m.fastBusy = false
		}
		for i, idx := range msg.idx {
			m.results[idx] = msg.results[i]
		}
	case dashActionProgressMsg:
		if i := m.indexOf(msg.node); i >= 0 && m.actions[i].verb != "" {
			m.actions[i].line = msg.line
		}
	case dashActionMsg:
		if i := m.indexOf(msg.node); i >= 0 {
			m.actions[i] = dashAction{}
		}
		m.statusLine = dashActionLine(msg)
	case tea.KeyMsg:
		if m.confirm {
			switch msg.String() {
			case "y":
				return m, m.beginAction("stop")
			case "n", "esc":
				m.confirm = false
			case "q", "ctrl+c":
				m.confirm = false
				return m, tea.Quit
			}
			return m, nil
		}
		var cmd tea.Cmd
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "down", "j":
			if m.cursor < len(m.entries)-1 {
				m.cursor++
			}
			m.keepVisible()
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			m.keepVisible()
		case "s":
			if len(m.entries) > 0 && m.actions[m.cursor].verb == "" {
				cmd = m.beginAction("start")
			}
		case "x":
			if len(m.entries) > 0 && m.actions[m.cursor].verb == "" {
				m.confirm = true
			}
		case "r":
			// A manual refresh is due for every node, cloud or local,
			// whatever their own deadlines say.
			m.nextSlowAt = time.Time{}
			cmds := m.startRounds()
			if len(cmds) > 0 {
				cmd = tea.Batch(cmds...)
			}
		case "pgup":
			m.scrollRow = dashClampScroll(m.scrollRow-dashVisibleRows(m.effHeight()), len(m.entries), m.effWidth(), m.effHeight())
		case "pgdown":
			m.scrollRow = dashClampScroll(m.scrollRow+dashVisibleRows(m.effHeight()), len(m.entries), m.effWidth(), m.effHeight())
		}
		return m, cmd
	}
	return m, nil
}

// startRounds starts the rounds the board is due for: the local machines
// whenever the tick fires, the cloud environments when their own deadline
// has passed. A round is never started over one still in flight in the same
// group, and a group with nothing to read starts nothing.
func (m *dashModel) startRounds() []tea.Cmd {
	var cmds []tea.Cmd
	if cmd := m.refreshRemoteGroup(false); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if !time.Now().Before(m.nextSlowAt) {
		if cmd := m.refreshRemoteGroup(true); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// refreshRemoteGroup starts one round over one group of live nodes — the
// local daemon machines (remote false) or the cloud environments (remote
// true) — and returns the round's command, or nil when the group is empty or
// already has a round in flight. Starting the cloud round spends its
// deadline: it moves to one interval away, so the next due round is a
// whole interval later.
//
// The round carries a context with a deadline of its group's interval, so a
// node slower than the cadence shows an outcome this round and gets its turn
// again next. Each node answers independently — the fan-out calls them
// concurrently — so one slow node delays no other, and a slow cloud round
// stretches only its own group.
func (m *dashModel) refreshRemoteGroup(remote bool) tea.Cmd {
	idx := make([]int, 0, len(m.entries))
	nodes := make([]fleet.Node, 0, len(m.entries))
	for i, e := range m.entries {
		if e.node != nil && (e.kind == fleet.KindRemote) == remote {
			idx = append(idx, i)
			nodes = append(nodes, e.node)
		}
	}
	if len(nodes) == 0 {
		return nil
	}
	if remote {
		if m.slowBusy {
			return nil
		}
		m.slowBusy = true
		m.slowGen++
		m.nextSlowAt = time.Now().Add(dashboardRemoteRefreshInterval)
	} else {
		if m.fastBusy {
			return nil
		}
		m.fastBusy = true
		m.fastGen++
	}
	gen := m.generationFor(remote)
	interval := m.intervalFor(remote)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), interval)
		defer cancel()
		return dashRefreshMsg{remote: remote, gen: gen, idx: idx, results: fleet.FanOutNodes(ctx, fleet.MetricsCall, nodes)}
	}
}

func (m *dashModel) generationFor(remote bool) int {
	if remote {
		return m.slowGen
	}
	return m.fastGen
}

func (m *dashModel) intervalFor(remote bool) time.Duration {
	if remote {
		return dashboardRemoteRefreshInterval
	}
	return dashboardRefreshInterval
}

// dashActionLine is the footer's account of a finished action, on the same
// result the fleet fan-out gives the one-shot commands, so the board cannot
// claim a start worked when the row of `fleet start` would not.
func dashActionLine(msg dashActionMsg) string {
	r := fleet.Result(msg.node, msg.err, msg.status)
	if !r.OK() {
		return msg.node + ": " + msg.verb + " failed — " + r.Detail()
	}
	state := msg.status.State
	if state == "" {
		state = "done"
	}
	return msg.node + ": " + msg.verb + " — " + state
}

// beginAction sets off a start or stop on the selected node. The action runs
// in a Bubble Tea command — its own goroutine — and carries no deadline: a
// cloud instance waking takes minutes, and a deadline would report a failure
// to what is a slow success. The action is one per node, never one per board:
// another node starts the same instant this one is still waking, and each
// call reports itself on its own tile through the progress door, so the
// footer — one line — keeps only the final outcomes. While it flies, the
// refresh rounds keep coming; the panel turns on the next round, which is
// also the daemon's own account of the state.
func (m *dashModel) beginAction(verb string) tea.Cmd {
	e := m.entries[m.cursor]
	m.confirm = false
	if e.node == nil {
		m.statusLine = e.name + ": " + e.standing.Detail()
		return nil
	}
	if m.actions[m.cursor].verb != "" {
		m.statusLine = e.name + ": still " + dashVerbProgress(m.actions[m.cursor].verb)
		return nil
	}
	m.actions[m.cursor] = dashAction{verb: verb}
	m.statusLine = dashVerbProgress(verb) + " " + e.name + "…"
	send := m.send
	progress := func(line string) {
		if send != nil {
			send(dashActionProgressMsg{node: e.name, line: line})
		}
	}
	var act func(context.Context) (daemon.StatusResponse, error)
	switch {
	case verb == "stop":
		act = e.node.Stop
	case e.node.(fleet.ProgressStarter) != nil:
		starter := e.node.(fleet.ProgressStarter)
		act = func(ctx context.Context) (daemon.StatusResponse, error) {
			return starter.StartWithProgress(ctx, progress)
		}
	default:
		act = e.node.Start
	}
	return func() tea.Msg {
		status, err := act(context.Background())
		return dashActionMsg{node: e.name, verb: verb, status: status, err: err}
	}
}

// indexOf finds an entry by name. Fleet-file names are unique — the fleet
// file itself refuses a repeat — so a find is a position, not a set.
func (m *dashModel) indexOf(name string) int {
	for i, e := range m.entries {
		if e.name == name {
			return i
		}
	}
	return -1
}

// keepVisible scrolls so the selected tile lies in the visible rows.
func (m *dashModel) keepVisible() {
	if len(m.entries) == 0 {
		m.scrollRow = 0
		return
	}
	row := m.cursor / dashCols(m.effWidth())
	avail := dashVisibleRows(m.effHeight())
	if row < m.scrollRow || row >= m.scrollRow+avail {
		m.scrollRow = row
	}
	m.scrollRow = dashClampScroll(m.scrollRow, len(m.entries), m.effWidth(), m.effHeight())
}

// effWidth and effHeight report the window, defaulting where none was ever
// reported: Bubble Tea measures the real screen at startup, so a zero can
// only mean not-measured, not a real size.
func (m dashModel) effWidth() int {
	if m.width < 1 {
		return 80
	}
	return m.width
}

func (m dashModel) effHeight() int {
	if m.height < 1 {
		return 24
	}
	return m.height
}

// View draws the frame: the header, the visible grid rows, the footer.
func (m dashModel) View() string {
	w, h := m.effWidth(), m.effHeight()
	tiles := make([]string, len(m.entries))
	for i := range m.entries {
		tiles[i] = dashTile(m.entries[i].name, m.results[i], i == m.cursor, m.actions[i])
	}
	rows := dashGridRows(tiles, dashCols(w))
	lo := m.scrollRow
	if lo > len(rows) {
		lo = len(rows)
	}
	hi := lo + dashVisibleRows(h)
	if hi > len(rows) {
		hi = len(rows)
	}
	parts := []string{m.headerLine(w)}
	if hi > lo {
		parts = append(parts, strings.Join(rows[lo:hi], "\n"))
	}
	parts = append(parts, m.footerLine(w))
	return strings.Join(parts, "\n")
}

func (m dashModel) headerLine(w int) string {
	word := "nodes"
	if len(m.entries) == 1 {
		word = "node"
	}
	return dashClip(fmt.Sprintf("fleet dashboard  %s  (%d %s)", m.fleetPath, len(m.entries), word), w)
}

func (m dashModel) footerLine(w int) string {
	line := "j/k move   s start   x stop   r refresh   q quit"
	if m.confirm && len(m.entries) > 0 {
		line = "stop " + m.entries[m.cursor].name + "?   y yes   n no"
	}
	if m.statusLine != "" {
		line += "   " + m.statusLine
	}
	if m.fastBusy || m.slowBusy {
		line += "   refreshing"
	}
	return dashClip(line, w)
}
