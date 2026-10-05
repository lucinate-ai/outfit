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
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spinloop-ai/spinloop/internal/daemon"
	"github.com/spinloop-ai/spinloop/internal/fleet"
)

// dashboardRefreshInterval is how often the board re-reads the local daemon
// machines. It is one call per machine per tick, so the cost is the fleet's
// size times this rate; the figures it draws — utilization, token counters,
// idle time — are sampled by each daemon every 15 seconds anyway, so polling
// faster than this re-fetches readings that have not changed. It is a
// variable so a test never waits for a slow node.
var dashboardRefreshInterval = 5 * time.Second

// dashboardCloudRefreshInterval is the cadence for kind: cloud
// environments instead. Each of their statuses is a signed call through the
// cloud control plane — a Lambda invocation, not a socket on the
// sideboard — so the board refreshes the local machines on the tick and the
// cloud environments on this slower deadline. Variable, for the same reason.
var dashboardCloudRefreshInterval = 60 * time.Second

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
// node: the verb, the call's current phase, when the action began, and the
// call's own context — the abort's door. A node with nothing in flight carries
// the zero value.
//
// The phase is one value that each report replaces outright, so a situation
// the call has moved on from is never left on the tile; it is nil until the
// call reports one, and for a stop, whose call reports none.
type dashAction struct {
	verb    string             // "start" or "stop"
	phase   *fleet.StartPhase  // what the call is doing now; nil until it reports
	since   time.Time          // when the action was issued; zero where the board has no clock on it
	cancel  context.CancelFunc // end the wait on the call; nil where the zero value sits
	aborted bool               // the operator ended the wait, so the line says so
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

	confirm bool // a stop is waiting on its confirmation
	// A keep is waiting on its duration: the prompt is open, the duration typed
	// so far (pre-filled so the common case is one Enter), and the parse reason
	// shown at the foot when a confirmed entry is not a positive duration.
	keepPrompt bool
	keepBuf    string
	keepErr    string
	statusLine string

	// gauge is the board's resource-series format: false draws the bar
	// format (the sparkline of each node's retained history), true the gauge
	// format (the current reading). Board-wide, toggled by g — one format for
	// every panel rather than a choice per node. The board opens in gauge;
	// dashModelFor sets it.
	gauge bool

	// send feeds a message back into the program from outside the Update
	// loop — the in-flight progress of a start, which its call reports from
	// its own goroutine. It is the tea.Program's Send, safe from any
	// goroutine and a no-op once the program has left; nil where the model
	// is driven directly and nothing is wired to the program.
	send func(msg tea.Msg)

	fastBusy, slowBusy bool        // a round in flight in that group
	nextReadAt         []time.Time // parallel to entries; when each node is next due to be read

	width, height int

	// detail is the full-screen view of the node under the cursor, opened by
	// enter and closed by escape. The cursor never moves while it is open, so
	// the node in view is always entries[cursor]; detail carries only whether
	// the view is open and the state of its own log tail.
	detail           bool
	detailLogGen     int    // bumped on every open, so a reply from a closed or superseded view is discarded
	detailLogBusy    bool   // a log round is in flight for the node in view
	detailLogFollow  bool   // whether the tick is allowed to start a round; f toggles it
	detailLogOffset  int64  // where the next log round resumes from
	detailLogContent string // the tailed content, trimmed to what the pane can show
	detailLogNote    string // why the pane has no content — empty once it does
}

// dashTickMsg fires on the fast interval.
type dashTickMsg time.Time

// dashRefreshMsg is one completed round of one group. idx and results are
// parallel: the entries this round re-read, and what each answered. Which of
// them are drawn is decided by each reading's own time, not by the round's:
// see the message's handling in Update.
type dashRefreshMsg struct {
	cloud   bool // the cloud group's round
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

// dashNow returns the board's current time. A variable so a test can fix the
// elapsed time an in-flight tile renders.
var dashNow = time.Now

// dashActionProgress returns the tile's in-flight heading: the spinner (the
// tool's own, shared with `fleet deploy`), the verb, and how long the action
// has been running when a.since is set.
//
// A start's phase can hold unchanged for minutes: the attempt that obtains
// capacity holds one request open for the duration of the boot and reports
// nothing while it does. Without a spinner and an elapsed time, a tile in that
// state draws identically to one whose start has stopped making progress. Both
// are computed from now on each repaint rather than stored when a phase
// arrives, so neither can itself go stale.
func dashActionProgress(a dashAction, now time.Time) string {
	verb := spinnerFrame(now) + " " + dashVerbProgress(a.verb)
	if a.since.IsZero() {
		return verb
	}
	elapsed := now.Sub(a.since)
	if elapsed < 0 {
		elapsed = 0
	}
	return verb + "  " + formatDuration(int(elapsed.Seconds()))
}

// dashActionProgressMsg is one phase of a call behind an in-flight action,
// sent to the program by the call's own goroutine as the work proceeds. Each
// replaces the phase the node's action carries.
type dashActionProgressMsg struct {
	node  string
	phase fleet.StartPhase
}

// dashActionMsg is one completed start, stop, or keep.
type dashActionMsg struct {
	node   string
	verb   string // "start", "stop", or "keep"
	status daemon.StatusResponse
	err    error
	// retainUntil is the deadline a keep set, RFC 3339, carried so the footer's
	// outcome can name it. Empty for a start or stop, and for a keep that failed
	// before the control plane set anything.
	retainUntil string
}

// dashTickCmd schedules the next fast tick. The tick is one-shot, so every
// tick reschedules the next: drop the reschedule and the board is still
// after the second round.
func dashTickCmd() tea.Cmd {
	return tea.Tick(dashboardRefreshInterval, func(time.Time) tea.Msg { return dashTickMsg{} })
}

// dashSpinInterval is how often the board repaints while an action is in
// flight. The spinner and the elapsed time beside a verb are computed when the
// tile is drawn, so they advance only as often as the board redraws, and the
// refresh tick alone is far too slow for a spinner to read as one. A variable,
// so a test never waits on it.
var dashSpinInterval = 100 * time.Millisecond

// dashSpinTickMsg fires on that interval while something is in flight.
type dashSpinTickMsg time.Time

func dashSpinTickCmd() tea.Cmd {
	return tea.Tick(dashSpinInterval, func(time.Time) tea.Msg { return dashSpinTickMsg{} })
}

// actionInFlight reports whether any node has a start or stop running, which
// is what the repaint chain runs for.
func (m *dashModel) actionInFlight() bool {
	for _, a := range m.actions {
		if a.verb != "" {
			return true
		}
	}
	return false
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
		if msg.cloud {
			m.slowBusy = false
		} else {
			m.fastBusy = false
		}
		// One rule for every reading: draw it only if it was taken later than
		// the one already on the board for that node. Reads run concurrently
		// and take differing times, so a reading can land after one taken
		// later than it — including a round issued before an action finished
		// and landing after, which would otherwise repaint the node's
		// pre-action state over its post-action report.
		for i, idx := range msg.idx {
			if r := msg.results[i]; r.At.After(m.results[idx].At) {
				m.results[idx] = r
			}
		}
	case dashSpinTickMsg:
		// The repaint chain runs only while there is something to animate; a
		// tick that finds nothing in flight stops it, and the next action
		// starts it again.
		if !m.actionInFlight() {
			return m, nil
		}
		return m, dashSpinTickCmd()
	case dashActionProgressMsg:
		if i := m.indexOf(msg.node); i >= 0 && m.actions[i].verb != "" {
			phase := msg.phase
			m.actions[i].phase = &phase
		}
	case dashActionMsg:
		aborted := false
		if i := m.indexOf(msg.node); i >= 0 {
			aborted = m.actions[i].aborted
			m.actions[i] = dashAction{}
			// The node returns to its kind's own cadence, and is read once
			// more now: what the action changed is what the operator is
			// waiting to see.
			m.scheduleRead(i, time.Time{})
		}
		m.statusLine = dashActionLine(msg, aborted)
	case detailLogTickMsg:
		// The tick reschedules itself only while the view is open on a node
		// that could ever answer; closing it, or a switch to a standing node
		// that opened without ever scheduling this chain, is the shutdown
		// path — the next tick simply stops rather than resurrecting it.
		if !m.detail || m.entries[m.cursor].node == nil {
			return m, nil
		}
		// The chain keeps ticking whether or not follow is on — pausing
		// only skips the round it would have started, so unpausing with f
		// needs nothing more than flipping the flag back.
		var cmd tea.Cmd
		if m.detailLogFollow {
			cmd = m.startDetailLogRound()
		}
		return m, tea.Batch(detailLogTickCmd(), cmd)
	case dashDetailLogMsg:
		m.detailLogBusy = false
		if !m.detail || msg.gen != m.detailLogGen {
			return m, nil
		}
		m.applyDetailLog(msg.result)
	case tea.KeyMsg:
		// The keep prompt stands in front of everything: while it is open,
		// every key goes to it — navigation, selection, refresh, even the stop
		// confirmation — so the board holds still until the duration is entered
		// or cancelled. It is checked before confirm and detail so it works
		// whether it was opened from the grid or the detail view.
		if m.keepPrompt {
			return m, m.updateKeepPromptKey(msg)
		}
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
		if m.detail {
			return m, m.updateDetailKey(msg)
		}
		var cmd tea.Cmd
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if len(m.entries) > 0 {
				cmd = m.openDetail()
			}
		case "down":
			cols := dashCols(m.effWidth())
			if len(m.entries) > 0 {
				if next := m.cursor + cols; next < len(m.entries) {
					m.cursor = next
				} else if curRow, lastRow := m.cursor/cols, (len(m.entries)-1)/cols; curRow < lastRow {
					// The row below exists but is shorter than the grid's
					// width and does not reach this column: land on the
					// last tile that row has, rather than the row itself
					// having nothing to move onto.
					m.cursor = len(m.entries) - 1
				}
				// Otherwise the current row is already the last one: no
				// row below to move onto, so the selection stays put
				// rather than jumping sideways to a different column.
			}
			m.keepVisible()
		case "up":
			cols := dashCols(m.effWidth())
			// Every row above the last is fully populated, so a row above
			// always has a tile in the same column; where there is no row
			// above, the selection stays put rather than jumping to column
			// 0.
			if next := m.cursor - cols; next >= 0 {
				m.cursor = next
			}
			m.keepVisible()
		case "right":
			cols := dashCols(m.effWidth())
			if next := m.cursor + 1; m.cursor%cols+1 < cols && next < len(m.entries) {
				m.cursor = next
			}
			m.keepVisible()
		case "left":
			cols := dashCols(m.effWidth())
			if m.cursor%cols != 0 {
				m.cursor--
			}
			m.keepVisible()
		case "s":
			if len(m.entries) > 0 && m.actions[m.cursor].verb == "" {
				cmd = m.beginAction("start")
			}
		case "k":
			// Open the duration prompt, pre-filled so the common case is one
			// Enter. keepOffered gates it to a node that can be kept and has
			// nothing in flight; elsewhere the key drives nothing.
			if m.keepOffered() {
				m.keepPrompt = true
				m.keepBuf = "4h"
				m.keepErr = ""
			}
		case "a":
			// End the wait on the node's in-flight action — the one-shot
			// equivalent of Ctrl+C. A node with nothing in flight is driven
			// by nothing.
			if len(m.entries) > 0 {
				m.abortAction()
			}
		case "x":
			if len(m.entries) > 0 && m.actions[m.cursor].verb == "" {
				m.confirm = true
			}
		case "g":
			// The board-wide format toggle: every panel redraws in the other
			// format, and g again returns them.
			m.gauge = !m.gauge
		case "r":
			// A manual refresh is due for every node, cloud or local,
			// whatever their own deadlines say.
			m.nextReadAt = make([]time.Time, len(m.entries))
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

// startRounds starts the rounds the board is due for, one per group, over
// whichever of that group's nodes have reached their own next-read time. A
// round is never started over one still in flight in the same group, and a
// group with nothing due starts nothing.
func (m *dashModel) startRounds() []tea.Cmd {
	var cmds []tea.Cmd
	for _, cloud := range []bool{false, true} {
		if cmd := m.refreshCloudGroup(cloud); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// dashNodeInterval is how often one node is read: the short interval while an
// action is in flight on it, and its kind's own cadence otherwise. The reasons
// a cloud environment is read once a minute — each read is a signed call
// through the control plane, and an idle environment's state changes on the
// scale of minutes — hold for an environment nobody is touching and hold for
// neither one the operator has just started.
func dashNodeInterval(kind string, a dashAction) time.Duration {
	if a.verb == "" && kind == fleet.KindCloud {
		return dashboardCloudRefreshInterval
	}
	return dashboardRefreshInterval
}

// isDue reports whether node i is to be read this tick. A node whose cadence
// is the board's own tick interval is due on every tick — the tick is its
// cadence, and comparing a deadline against it would skip a round to the
// tick's own jitter and halve the rate. Only a node read less often than the
// tick carries a deadline that can defer it, which is a cloud environment with
// nothing in flight on it.
func (m *dashModel) isDue(i int, now time.Time) bool {
	if dashNodeInterval(m.entries[i].kind, m.actions[i]) <= dashboardRefreshInterval {
		return true
	}
	return !now.Before(m.dueAt(i))
}

// dueAt is when node i is next due to be read, and scheduleRead records the
// next one. The times are per node rather than per group because a node with
// an action in flight is read on the short interval whatever its kind, while
// the rest of its group keeps its own cadence. The slice grows to fit rather
// than being required up front, so a model built without it reads every node
// on its first round.
func (m *dashModel) dueAt(i int) time.Time {
	if i < len(m.nextReadAt) {
		return m.nextReadAt[i]
	}
	return time.Time{}
}

func (m *dashModel) scheduleRead(i int, at time.Time) {
	for len(m.nextReadAt) <= i {
		m.nextReadAt = append(m.nextReadAt, time.Time{})
	}
	m.nextReadAt[i] = at
}

// refreshCloudGroup starts one round over the due nodes of one group of live
// nodes — the local daemon machines (cloud false) or the cloud environments
// (cloud true) — and returns the round's command, or nil when nothing in the
// group is due or a round is already in flight there. Starting the round
// spends each read node's deadline: each moves to one of its own intervals
// away, so a node the operator is acting on comes round again on the short
// interval while its neighbours keep their own cadence.
//
// The round carries a context with a deadline of its group's kind interval,
// not of the cadence it was started on: a cloud read is a signed call through
// the control plane and takes what it takes, so shortening the cadence during
// an action must not shorten what the call is given to answer in. Each node
// answers independently — the fan-out calls them concurrently — so one slow
// node delays no other, and a slow cloud round stretches only its own group.
func (m *dashModel) refreshCloudGroup(cloud bool) tea.Cmd {
	now := time.Now()
	idx := make([]int, 0, len(m.entries))
	nodes := make([]fleet.Node, 0, len(m.entries))
	for i, e := range m.entries {
		if e.node == nil || (e.kind == fleet.KindCloud) != cloud {
			continue
		}
		if !m.isDue(i, now) {
			continue
		}
		idx = append(idx, i)
		nodes = append(nodes, e.node)
	}
	if len(nodes) == 0 {
		return nil
	}
	if cloud {
		if m.slowBusy {
			return nil
		}
		m.slowBusy = true
	} else {
		if m.fastBusy {
			return nil
		}
		m.fastBusy = true
	}
	for _, i := range idx {
		m.scheduleRead(i, now.Add(dashNodeInterval(m.entries[i].kind, m.actions[i])))
	}
	interval := m.intervalFor(cloud)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), interval)
		defer cancel()
		return dashRefreshMsg{cloud: cloud, idx: idx, results: fleet.FanOutNodes(ctx, fleet.MetricsCall, nodes)}
	}
}

func (m *dashModel) intervalFor(cloud bool) time.Duration {
	if cloud {
		return dashboardCloudRefreshInterval
	}
	return dashboardRefreshInterval
}

// dashActionLine is the footer's account of a finished action, on the same
// result the fleet fan-out gives the one-shot commands, so the board cannot
// claim a start worked when the row of `fleet start` would not. A failure the
// operator's abort caused is not reported as a failure of the node — the wait
// was abandoned, and a success that races the abort is still reported as the
// success, since the line is about what the node did, not what the dashboard
// did.
func dashActionLine(msg dashActionMsg, aborted bool) string {
	r := fleet.Result(msg.node, msg.err, msg.status)
	if !r.OK() {
		if aborted {
			return msg.node + ": " + msg.verb + " abandoned"
		}
		return msg.node + ": " + msg.verb + " failed — " + r.Detail()
	}
	if msg.verb == "keep" {
		// A keep has no engine state to report — its result is the deadline the
		// control plane set, which is what the operator came to set.
		return msg.node + ": keep — retain until " + msg.retainUntil
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
// refresh rounds keep coming and the panel carries their answers beside the
// call's own lines — the node's report is the node's account of the state;
// when the call returns, the panel is that report alone. The action runs on
// its own context, held beside it on the board — the operator's abort is
// that context's cancellation, not a timer.
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
	// The repaint chain runs for as long as anything is in flight, so it is
	// started only when this action is the first.
	spin := !m.actionInFlight()
	ctx, cancel := context.WithCancel(context.Background())
	m.actions[m.cursor] = dashAction{verb: verb, since: dashNow(), cancel: cancel}
	// The node is read on the short interval for the duration of the action,
	// starting now rather than at its kind's next due time.
	m.scheduleRead(m.cursor, time.Time{})
	m.statusLine = dashVerbProgress(verb) + " " + e.name + "…"
	send := m.send
	report := func(p fleet.StartPhase) {
		if send != nil {
			send(dashActionProgressMsg{node: e.name, phase: p})
		}
	}
	starter, isStarter := e.node.(fleet.ProgressStarter)
	var act func(context.Context) (daemon.StatusResponse, error)
	switch {
	case verb == "stop":
		act = e.node.Stop
	case isStarter:
		act = func(ctx context.Context) (daemon.StatusResponse, error) {
			return starter.StartWithProgress(ctx, report)
		}
	default:
		act = e.node.Start
	}
	run := func() tea.Msg {
		status, err := act(ctx)
		cancel()
		return dashActionMsg{node: e.name, verb: verb, status: status, err: err}
	}
	if spin {
		return tea.Batch(run, dashSpinTickCmd())
	}
	return run
}

// beginKeep sets off a keep of the selected node for the confirmed duration.
// It reuses beginAction's scaffolding — one action per node, the tile's
// spinner, the short read interval for the duration of the call — but the call
// itself is a keep, not a start or stop: the node must be a Keeper (a cloud
// environment), and the call returns the deadline it set rather than an engine
// state. A node that is not a Keeper, or that already has an action in flight,
// is driven by nothing, and its reason lands on the status line.
func (m *dashModel) beginKeep(d time.Duration) tea.Cmd {
	e := m.entries[m.cursor]
	m.keepPrompt = false
	m.keepBuf = ""
	m.keepErr = ""
	if e.node == nil {
		m.statusLine = e.name + ": " + e.standing.Detail()
		return nil
	}
	if m.actions[m.cursor].verb != "" {
		m.statusLine = e.name + ": still " + dashVerbProgress(m.actions[m.cursor].verb)
		return nil
	}
	keeper, ok := e.node.(fleet.Keeper)
	if !ok {
		m.statusLine = e.name + ": keep needs a cloud environment"
		return nil
	}
	spin := !m.actionInFlight()
	ctx, cancel := context.WithCancel(context.Background())
	m.actions[m.cursor] = dashAction{verb: "keep", since: dashNow(), cancel: cancel}
	// The node is read on the short interval for the duration of the action,
	// starting now rather than at its kind's next due time: what a keep changed
	// is its own read's retainUntil.
	m.scheduleRead(m.cursor, time.Time{})
	m.statusLine = dashVerbProgress("keep") + " " + e.name + "…"
	run := func() tea.Msg {
		deadline, err := keeper.Keep(ctx, d)
		cancel()
		return dashActionMsg{node: e.name, verb: "keep", retainUntil: deadline, err: err}
	}
	if spin {
		return tea.Batch(run, dashSpinTickCmd())
	}
	return run
}

// updateKeepPromptKey answers the keys the keep prompt reads: printable runes
// append to the duration, backspace removes the last, Enter confirms, esc
// cancels, and q/Ctrl+C cancels and quits (mirroring the stop confirmation's
// own quit). A confirmed entry that is not a positive duration leaves the
// prompt open and names why at the foot, so a mistyped entry is corrected in
// place rather than lost.
func (m *dashModel) updateKeepPromptKey(msg tea.KeyMsg) tea.Cmd {
	switch s := msg.String(); {
	case s == "enter":
		d, err := time.ParseDuration(strings.TrimSpace(m.keepBuf))
		if err != nil || d <= 0 {
			m.keepErr = "enter a duration like 4h"
			return nil
		}
		return m.beginKeep(d)
	case s == "backspace":
		m.keepBuf = chopLastRune(m.keepBuf)
		m.keepErr = ""
	case s == "esc":
		m.keepPrompt = false
		m.keepBuf = ""
		m.keepErr = ""
	case s == "ctrl+c", s == "q":
		m.keepPrompt = false
		m.keepBuf = ""
		m.keepErr = ""
		return tea.Quit
	case msg.Type == tea.KeyRunes:
		m.keepBuf += s
		m.keepErr = ""
	}
	return nil
}

// chopLastRune drops the final rune from a string, counting a wide rune as the
// bytes that make it up rather than cutting a UTF-8 sequence mid-way.
func chopLastRune(s string) string {
	if s == "" {
		return s
	}
	_, size := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-size]
}

// abortAction ends the wait on the selected node's in-flight start. Only a
// start is abortable: it is the one action with no deadline of its own — a
// cold cloud wake takes minutes, so a wait the operator no longer wants to
// watch is a wait only the operator can end. A stop is not: it targets a
// node that is already running, its own call carries no comparable open-
// ended wait, and abandoning the wait on it would leave the operator unsure
// whether the stop still went ahead — so a stop in flight, or no action at
// all, drives nothing here. The abort ends the wait, not the work: the
// call's own loop returns on the done context — at the retry wait or mid-
// request, on the path it already has for a given-up wait — and its final
// message lands as for any finished action: the tile clears, the node may
// be started or stopped again, and the line says the wait was abandoned,
// never that a wake the cloud is carrying was cancelled. What the wake goes
// on to do comes back on the next refresh.
func (m *dashModel) abortAction() {
	i := m.cursor
	if m.actions[i].verb != "start" {
		return
	}
	m.actions[i].aborted = true
	if cancel := m.actions[i].cancel; cancel != nil {
		cancel()
	}
}

// canAbort reports whether the abort key would do anything for the node
// under the cursor: only a start in flight is abortable, so an idle,
// running, or already-stopping node has nothing to abort. The footer uses
// this to drop the abort hint rather than advertise a key that changes
// nothing for the node it describes.
func (m dashModel) canAbort() bool {
	return len(m.entries) > 0 && m.actions[m.cursor].verb == "start"
}

// startOffered and stopOffered report whether the start and stop keys would do
// anything for the node under the cursor, from the same result the node's tile
// draws from: the tile and the footer read one account of the node, so the
// footer cannot name a key for a state the panel does not show. A read answers
// running when it is OK and carries a running state; a read that failed — or
// the absence of one, before the first answer — carries no state, which is not
// running, so start is the key that might still do something, and the one the
// line keeps offering. Both keys need the node to exist and have nothing in
// flight, the same guards the key handlers answer to: an entry that could not
// become a node takes nothing, and a node already acting takes no second
// action. The footer uses the offers to name a key only where it would drive
// something, the same way keepOffered gates the keep hint.
func (m dashModel) startOffered() bool {
	return m.actionOffered() && !m.nodeRunning()
}

func (m dashModel) stopOffered() bool {
	return m.actionOffered() && m.nodeRunning()
}

// actionOffered reports whether the node under the cursor could take an action
// at all: its entry has a node, and the node has nothing in flight.
func (m dashModel) actionOffered() bool {
	if len(m.entries) == 0 {
		return false
	}
	return m.entries[m.cursor].node != nil && m.actions[m.cursor].verb == ""
}

// nodeRunning reports whether the board's current read of the node under the
// cursor answers running: the read answered and carries a running state. A
// model that has no read of the node cannot say it is running.
func (m dashModel) nodeRunning() bool {
	if m.cursor >= len(m.results) {
		return false
	}
	r := m.results[m.cursor]
	return r.OK() && r.Metrics.State == "running"
}

// keepOffered reports whether the keep key would do anything for the node under
// the cursor: the node must support a keep (a cloud environment) and have
// nothing in flight. A local daemon node has no retention tag to set, and a
// node already acting takes no second action. The footer uses this to include
// the keep hint only where it would drive something, the same way canAbort
// gates the abort hint.
func (m dashModel) keepOffered() bool {
	if len(m.entries) == 0 {
		return false
	}
	if m.actions[m.cursor].verb != "" {
		return false
	}
	_, ok := m.entries[m.cursor].node.(fleet.Keeper)
	return ok
}

// gridKeys and detailKeys are the two footers' key help: the entries that
// always apply, plus each action entry named only where its offer is true —
// start and stop from the node's own state, keep from its support for
// retention, and abort from the start in flight on it — so a hint is never
// shown for a key that would drive nothing on the node the line describes.
func (m dashModel) gridKeys() string {
	parts := []string{"↑↓←→ move"}
	if m.startOffered() {
		parts = append(parts, "s start")
	}
	if m.keepOffered() {
		parts = append(parts, "k keep")
	}
	if m.canAbort() {
		parts = append(parts, "a abort")
	}
	if m.stopOffered() {
		parts = append(parts, "x stop")
	}
	return strings.Join(append(parts, "g format", "r refresh", "q quit"), dashHintGap)
}

func (m dashModel) detailKeys() string {
	parts := []string{"esc back"}
	if m.startOffered() {
		parts = append(parts, "s start")
	}
	if m.keepOffered() {
		parts = append(parts, "k keep")
	}
	if m.stopOffered() {
		parts = append(parts, "x stop")
	}
	if m.canAbort() {
		parts = append(parts, "a abort")
	}
	return strings.Join(append(parts, "f follow"), dashHintGap)
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
	if m.detail {
		return m.detailView()
	}
	w, h := m.effWidth(), m.effHeight()
	now := dashNow()
	tiles := make([]string, len(m.entries))
	for i := range m.entries {
		tiles[i] = dashTile(m.entries[i].name, m.results[i], i == m.cursor, m.actions[i],
			now, dashStaleAfter(m.entries[i].kind), m.gauge)
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
	parts = append(parts, m.footerLine(w, m.gridKeys()))
	return strings.Join(parts, "\n")
}

func (m dashModel) headerLine(w int) string {
	word := "nodes"
	if len(m.entries) == 1 {
		word = "node"
	}
	return dashTitleBar("fleet dashboard",
		fmt.Sprintf("%s   (%d %s)", m.fleetPath, len(m.entries), word), w)
}

// footerLine is the frame's bottom line: the given key help, replaced by the
// stop confirmation prompt while one is pending, with the status line and a
// "refreshing" marker appended — shared by the grid and the detail view so
// the two cannot word the confirmation or a status outcome differently.
//
// Only the key help is drawn as key-and-meaning pairs. The prompt's own
// question, the status line and the refreshing marker are prose, not keys, and
// splitting them on a space would draw their first word as though it were one.
func (m dashModel) footerLine(w int, keys string) string {
	line := dashKeyHints(keys)
	if m.keepPrompt && len(m.entries) > 0 {
		// The prompt replaces the key help for its life: what is being typed,
		// and the two keys that resolve it. The parse reason, when a confirmed
		// entry was not a duration, rides at the foot beside the status line.
		line = "keep " + m.entries[m.cursor].name + " for: " + m.keepBuf + "_ " + dashHintGap +
			dashKeyHints("enter keep"+dashHintGap+"esc cancel")
		if m.keepErr != "" {
			line += "   " + m.keepErr
		}
	} else if m.confirm && len(m.entries) > 0 {
		line = "stop " + m.entries[m.cursor].name + "?" + dashHintGap +
			dashKeyHints("y yes"+dashHintGap+"n no")
	}
	if m.statusLine != "" {
		line += "   " + m.statusLine
	}
	if m.fastBusy || m.slowBusy {
		line += "   refreshing"
	}
	return dashClip(line, w)
}
