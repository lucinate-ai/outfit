package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	teatest "github.com/charmbracelet/x/exp/teatest"

	"github.com/spinloop-ai/spinloop/internal/orchestrator"
)

// The board's whole screen logic is driven here without a terminal: a fake
// work list API answers, keys and messages go straight into Update, and
// the view is read like a printout. The intervals and the clock are the
// package variables the model reads, so nothing in a test ever waits on a
// ticker or renders a moving time the test did not choose.

// wbAPI is a fake work list API: the orchestrator's real shapes, the
// orchestrator's real refusals, and a record of every call the board
// made — which is how the tests prove the negative ones too.
type wbAPI struct {
	mu      sync.Mutex
	items   []orchestrator.ItemView
	logs    map[string]string
	calls   []string // "METHOD /path"
	lastAdd workAddBody
	failGET bool // answer GET /v1/items with a fault, as a stopped orchestrator would
	srv     *httptest.Server
}

func newWBAPI(t *testing.T, items []orchestrator.ItemView, logs map[string]string) *wbAPI {
	t.Helper()
	a := &wbAPI{items: items, logs: logs}
	if a.logs == nil {
		a.logs = map[string]string{}
	}
	a.srv = httptest.NewServer(a)
	t.Cleanup(a.srv.Close)
	return a
}

func (a *wbAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, r.Method+" "+r.URL.Path)
	out := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if v != nil {
			json.NewEncoder(w).Encode(v)
		}
	}
	list := func(v string) string {
		tail := strings.TrimPrefix(r.URL.Path, "/v1/items/")
		tail = strings.TrimSuffix(tail, "/log")
		tail = strings.TrimSuffix(tail, "/retry")
		return strings.TrimSuffix(tail, "/abort")
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/items":
		if a.failGET {
			out(http.StatusInternalServerError,
				map[string]any{"error": map[string]string{"message": "the orchestrator is not answering"}})
			return
		}
		out(http.StatusOK, map[string]any{"data": a.items})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/items":
		var body workAddBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			out(http.StatusBadRequest, map[string]any{"error": map[string]string{"message": "unreadable body"}})
			return
		}
		a.lastAdd = body
		for _, v := range a.items {
			if v.ID == body.ID {
				out(http.StatusConflict, map[string]any{"error": map[string]string{
					"message": fmt.Sprintf("item %q is already in the list", body.ID),
				}})
				return
			}
		}
		a.items = append(a.items, orchestrator.ItemView{
			ID: body.ID, Instructions: body.Instructions, Dir: body.Dir,
			Tags: body.Tags, Priority: body.Priority, State: orchestrator.StateBacklog,
		})
		out(http.StatusOK, map[string]any{"ok": true})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/retry"):
		id := list(r.URL.Path)
		for i := range a.items {
			if a.items[i].ID == id {
				if a.items[i].State != orchestrator.StateFailed {
					out(http.StatusConflict, map[string]any{"error": map[string]string{
						"message": fmt.Sprintf("item %q is not failed: it is %s", id, a.items[i].State),
					}})
					return
				}
				a.items[i].State = orchestrator.StateBacklog
				a.items[i].Node, a.items[i].EndedAt, a.items[i].Why = "", "", ""
				out(http.StatusOK, map[string]any{"ok": true})
				return
			}
		}
		out(http.StatusNotFound, map[string]any{"error": map[string]string{
			"message": fmt.Sprintf("the work list does not carry item %q", id),
		}})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/abort"):
		id := list(r.URL.Path)
		for i := range a.items {
			if a.items[i].ID == id {
				if a.items[i].State != orchestrator.StateRunning {
					out(http.StatusConflict, map[string]any{"error": map[string]string{
						"message": fmt.Sprintf("item %q is not running (%s)", id, a.items[i].State),
					}})
					return
				}
				a.items[i].State = orchestrator.StateBacklog
				a.items[i].Node = ""
				out(http.StatusOK, map[string]any{"ok": true})
				return
			}
		}
		out(http.StatusNotFound, map[string]any{"error": map[string]string{
			"message": fmt.Sprintf("the work list does not carry item %q", id),
		}})
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/items/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/items/")
		for i := range a.items {
			if a.items[i].ID == id {
				if a.items[i].State == orchestrator.StateRunning {
					out(http.StatusConflict, map[string]any{"error": map[string]string{
						"message": fmt.Sprintf("item %q is running — abort it first", id),
					}})
					return
				}
				a.items = append(a.items[:i], a.items[i+1:]...)
				delete(a.logs, id)
				out(http.StatusOK, map[string]any{"ok": true})
				return
			}
		}
		out(http.StatusNotFound, map[string]any{"error": map[string]string{
			"message": fmt.Sprintf("the work list does not carry item %q", id),
		}})
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/log"):
		id := list(r.URL.Path)
		for _, v := range a.items {
			if v.ID == id {
				out(http.StatusOK, map[string]any{"log": a.logs[id]})
				return
			}
		}
		out(http.StatusNotFound, map[string]any{"error": map[string]string{
			"message": fmt.Sprintf("the work list does not carry item %q", id),
		}})
	default:
		out(http.StatusNotFound, nil)
	}
}

func (a *wbAPI) setLog(id, log string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logs[id] = log
}

func (a *wbAPI) callCount(fragment string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, c := range a.calls {
		if strings.Contains(c, fragment) {
			n++
		}
	}
	return n
}

func (a *wbAPI) lastBody() workAddBody {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastAdd
}

// newWBTestModel builds a board over the fake API with the cadences the
// test drives — never a live ticker — and a fixed clock.
func newWBTestModel(t *testing.T, a *wbAPI) *workBoardModel {
	t.Helper()
	restore := []func(){
		setVar(&workBoardRefreshInterval, time.Hour),
		setVar(&workBoardTailInterval, time.Hour),
		setVar(&workBoardSpinInterval, time.Hour),
		setVar(&workBoardNow, func() time.Time { return time.Unix(1700000000, 0) }),
	}
	t.Cleanup(func() {
		for _, f := range restore {
			f()
		}
	})
	m := newWorkBoardModel(a.srv.URL, "tok")
	m.width, m.height = 100, 30
	return m
}

func setVar[T any](p *T, v T) func() {
	old := *p
	*p = v
	return func() { *p = old }
}

// wbKey names a keystroke the way the footer names it.
func wbKey(name string) tea.KeyMsg {
	switch name {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
}

// wbKeys feeds keys through Update and returns the last command back.
func wbKeys(t *testing.T, m *workBoardModel, keys ...string) tea.Cmd {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = m.Update(wbKey(k))
	}
	return cmd
}

// wbRound runs one read of the list to its answer and folds it in.
func wbRound(t *testing.T, m *workBoardModel) {
	t.Helper()
	wbLand(t, m, m.startRound())
}

// wbLand lands the call a key set off: the answer is folded in, and the
// read the answer kicks is landed too — the round trip the program would
// play. An action's command is a batch when it also starts the spinner's
// repaint chain: the call is the chain's first command, and its timer is
// left unrun — a test would only wait on it.
func wbLand(t *testing.T, m *workBoardModel, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no call to land (busy?)")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		msg = batch[0]()
	}
	_, next := m.Update(msg)
	if next != nil {
		m.Update(next())
	}
}

// wbAct is press-and-land: keys, then the call they started.
func wbAct(t *testing.T, m *workBoardModel, keys ...string) {
	t.Helper()
	wbLand(t, m, wbKeys(t, m, keys...))
}

var wbANSI = regexp.MustCompile("\x1b\\[[0-9;]*m")

func wbPlain(s string) string { return wbANSI.ReplaceAllString(s, "") }

func wbItem(id, state string) orchestrator.ItemView {
	v := orchestrator.ItemView{ID: id, Instructions: "do the " + id, Dir: "./" + id, State: state}
	switch state {
	case orchestrator.StateRunning:
		v.Node = "node-1"
		v.StartedAt = time.Unix(1700000000, 0).Add(-125 * time.Second).UTC().Format(time.RFC3339)
	case orchestrator.StateDone, orchestrator.StateFailed:
		v.EndedAt = time.Unix(1700000000, 0).Add(-40 * time.Minute).UTC().Format(time.RFC3339)
	}
	if state == orchestrator.StateFailed {
		v.Why = "the agent gave up"
	}
	return v
}

// --- the command and its gate ---

func TestWorkBoard_CommandSeamRefusesWithoutATerminal(t *testing.T) {
	err := cmdWorkBoard([]string{"--url", "http://127.0.0.1:1", "--api-token", "t"})
	if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("seam error = %v, want the terminal refusal", err)
	}
}

func TestWorkBoard_TicksRescheduleWithoutDoublingRounds(t *testing.T) {
	a := newWBAPI(t, nil, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	if m.busy {
		t.Fatal("the board sat busy after a round")
	}
	// A tick reschedules itself and starts a round; a second tick while
	// that round is out must not send another one.
	_, cmd := m.Update(workBoardTickMsg{})
	if cmd == nil {
		t.Fatal("a tick scheduled nothing")
	}
	if !m.busy {
		t.Fatal("the tick started no round")
	}
	before := a.callCount("GET /v1/items")
	_, cmd = m.Update(workBoardTickMsg{}) // rounds must not overlap
	if cmd == nil {
		t.Error("a waiting tick unscheduled itself")
	}
	m.Update(workBoardReadMsg{items: nil, at: workBoardNow()})
	if a.callCount("GET /v1/items") != before {
		t.Error("the overlapping tick spent a call")
	}
	// Idle messages answer nothing: no chain while nothing is happening.
	if _, cmd := m.Update(workBoardSpinMsg{}); cmd != nil {
		t.Error("a spin message chained with no action in flight")
	}
	if _, cmd := m.Update(workBoardTailTickMsg{}); cmd != nil {
		t.Error("a tail tick chained with no detail open")
	}
}

func TestWorkBoard_ActionProgressAndSpinChain(t *testing.T) {
	fixed := time.Unix(1700000000, 0)
	act := workBoardAction{verb: workAbort, id: "crank", since: fixed.Add(-3 * time.Second)}
	line := act.progress(fixed)
	if !strings.Contains(line, "aborting crank") || !strings.Contains(line, "3s") {
		t.Errorf("progress line = %q", line)
	}

	a := newWBAPI(t, []orchestrator.ItemView{wbItem("crank", orchestrator.StateRunning)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "right")
	cmd := wbKeys(t, m, "a") // the call goes out and stays unanswered
	if cmd == nil {
		t.Fatal("the abort started nothing")
	}
	// The repaint chain rides a command alongside the call — never the
	// program's Send, which from inside Update would deadlock the loop.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("the abort is %T, want a two-part batch", cmd())
	}
	// While the call is out the chain keeps going…
	_, cmd = m.Update(workBoardSpinMsg{})
	if cmd == nil {
		t.Error("the spinner chain stopped mid-action")
	}
	// …and stops the moment the action answers.
	m.Update(workBoardActionMsg{verb: workAbort, id: "crank"})
	if _, cmd := m.Update(workBoardSpinMsg{}); cmd != nil {
		t.Error("the spinner chained past the answer")
	}
}

func TestWorkBoard_SecondActionWhileOneIsOutSendsNothing(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("crank", orchestrator.StateRunning)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	cmd := wbKeys(t, m, "right", "a")
	if cmd == nil {
		t.Fatal("the abort started nothing")
	}
	// The first call is still out when the key arrives again.
	if cmd2 := wbKeys(t, m, "a"); cmd2 != nil {
		t.Error("a second abort was set off while the first was out")
	}
	if !strings.Contains(m.statusLine, "still aborting") {
		t.Errorf("status = %q, want the still-busy line", m.statusLine)
	}
	// The form is turned the same away, and by the same words.
	wbKeys(t, m, "n")
	wbKeys(t, m, "later", "enter", "someday", "enter", "./d", "enter", "enter", "enter")
	if cmd2 := wbKeys(t, m, "enter"); cmd2 != nil {
		t.Error("an add was set off while the abort was out")
	}
	if !strings.Contains(m.statusLine, "still aborting") {
		t.Errorf("status = %q, want the still-busy line", m.statusLine)
	}
	wbLand(t, m, cmd)
}

func TestWorkBoard_GeometryKeepsItsFloors(t *testing.T) {
	m := workBoardModel{width: 10, height: 4}
	if m.visibleCards() != 1 || m.detailCapacity() < 1 || m.formFieldWidth() < 10 {
		t.Errorf("a crippled frame starved: cards=%d log=%d field=%d",
			m.visibleCards(), m.detailCapacity(), m.formFieldWidth())
	}
	if got := (workBoardAction{verb: workRemove, id: "x"}).progress(time.Unix(1700000000, 0)); got == "" || strings.Contains(got, "  0s") {
		t.Errorf("an unstarted action reads %q", got)
	}
	// A form taller than the screen: it stands at the top and loses the
	// rows the screen has no room for — it does not panic, and what
	// fits is what shows.
	m = workBoardModel{width: 64, height: 9}
	m.formOpen = true
	m.form = newWorkBoardForm(m.formFieldWidth())
	view := wbPlain(m.View())
	if !strings.Contains(view, "new work item") {
		t.Errorf("the cramped form lost its title:\n%s", view)
	}
	if n := len(strings.Split(m.View(), "\n")); n > 9 {
		t.Errorf("the cramped frame overflowed its screen: %d lines", n)
	}
	// The cut rest of a word shorter than the cut is nothing.
	if ansiCutRest("short", 10) != "" {
		t.Error("cutting past the end left something")
	}
}

func TestWorkBoard_NarrowTerminalDrawsTheCursorColumnAlone(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{
		wbItem("solo", orchestrator.StateBacklog),
		wbItem("crank", orchestrator.StateRunning),
	}, nil)
	m := newWBTestModel(t, a)
	m.width = 30 // far too narrow for four columns
	wbRound(t, m)
	if !m.narrow() {
		t.Fatal("30 columns were not called narrow")
	}
	view := wbPlain(m.View())
	if !strings.Contains(view, "solo") || strings.Contains(view, "crank") {
		t.Errorf("narrow board drew more than the cursor's column:\n%s", view)
	}
	wbKeys(t, m, "right")
	view = wbPlain(m.View())
	if !strings.Contains(view, "crank") || strings.Contains(view, "solo") {
		t.Errorf("stepping did not carry the narrow board to the next column:\n%s", view)
	}
	// The form still fits: labels survive the floor width.
	wbKeys(t, m, "n")
	view = wbPlain(m.View())
	for _, want := range []string{"new work item", "instructions*", "priority"} {
		if !strings.Contains(view, want) {
			t.Errorf("the narrow form lost %q:\n%s", want, view)
		}
	}
	// Narrower still: the field hits its floor and the form still draws.
	wbKeys(t, m, "esc")
	m.width = 20
	wbKeys(t, m, "n")
	if !strings.Contains(wbPlain(m.View()), "new work item") {
		t.Error("the floor-width form did not draw")
	}
}

func TestWorkBoard_PriorityThatCouldNotParseIsCaughtBeforeTheAPI(t *testing.T) {
	a := newWBAPI(t, nil, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "n")
	wbKeys(t, m, "later", "enter", "someday", "enter", "./d", "enter", "enter", "1-3")
	cmd := wbKeys(t, m, "enter") // the filter allows 1-3; the parser does not
	if cmd != nil {
		t.Error("an unparseable priority was sent to the API")
	}
	if !strings.Contains(m.form.err, "not a number") {
		t.Errorf("form carries no fault: %q", m.form.err)
	}
	if !m.formOpen {
		t.Error("the form closed over its own fault")
	}
	if a.callCount("POST /v1/items") != 0 {
		t.Error("the API was asked for the broken item anyway")
	}
	// Corrected: it goes.
	wbKeys(t, m, "backspace", "backspace", "2")
	cmd = wbKeys(t, m, "enter")
	if cmd == nil {
		t.Fatal("the corrected send went nowhere")
	}
	wbLand(t, m, cmd)
	if a.callCount("POST /v1/items") != 1 {
		t.Error("the corrected send did not reach the API")
	}
}

func TestWorkBoard_QuitFromTheDiscardQuestion(t *testing.T) {
	a := newWBAPI(t, nil, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "n")
	wbKeys(t, m, "x", "esc")
	if !m.formAsk {
		t.Fatal("no discard question stood")
	}
	cmd := wbKeys(t, m, "q")
	if cmd == nil || fmt.Sprintf("%T", cmd()) != "tea.QuitMsg" {
		t.Error("q from the discard question did not end the program")
	}
}

func TestWorkBoard_AFaultyTailKeepsWhatWasShown(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("crank", orchestrator.StateRunning)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "right", "enter")
	// A fault over an empty pane is the note; over a filled one it
	// changes nothing — the rows already shown stand.
	m.Update(workBoardTailMsg{gen: m.detailGen, err: errBoardTest})
	if !strings.Contains(wbPlain(m.View()), "the API went quiet") {
		t.Error("the empty pane carried no fault")
	}
	m.Update(workBoardTailMsg{gen: m.detailGen, log: "kept line\n"})
	m.Update(workBoardTailMsg{gen: m.detailGen, err: errBoardTest})
	view := wbPlain(m.View())
	if !strings.Contains(view, "kept line") {
		t.Errorf("a fault erased the shown log:\n%s", view)
	}
	// Stale answers — an older gen — are discarded, not folded in.
	m.Update(workBoardTailMsg{gen: m.detailGen + 1, log: "stale\n"})
	if strings.Contains(wbPlain(m.View()), "stale") {
		t.Error("a stale tail answer landed")
	}
}

var errBoardTest = fmt.Errorf("the API went quiet")

func TestWorkBoard_ClockReadingToleratesGarbage(t *testing.T) {
	bad := wbItem("solo", orchestrator.StateBacklog)
	bad.StartedAt = "not a time"
	a := newWBAPI(t, []orchestrator.ItemView{bad}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	if got := workBoardClock(bad.StartedAt); got != "-" {
		t.Errorf("an unreadable instant rendered %q, want a dash", got)
	}
	if got := workBoardElapsed(bad.StartedAt, workBoardNow()); got != "" {
		t.Errorf("an unreadable start rendered %q, want silence", got)
	}
	if workBoardClampScroll(-1, 5, 3) != 0 || workBoardClampScroll(9, 0, 3) != 0 {
		t.Error("a window escaped its cards")
	}
	// A word too wide for the pane is cut, not wrapped or dropped.
	lines := workBoardWrap("x"+strings.Repeat("y", 200), 10)
	for _, l := range lines {
		if len(l) > 10 {
			t.Errorf("a wrapped line overflowed the width: %q", l)
		}
	}
}

func TestWorkBoard_NoURLNamesTheFlag(t *testing.T) {
	_, err := runWork(t, "board")
	if err == nil || !strings.Contains(err.Error(), "--url") {
		t.Fatalf("error = %v, want one naming --url", err)
	}
}

func TestWorkBoard_PipedRunRefusedNamingWorkList(t *testing.T) {
	_, err := runWork(t, "board", "--url", "http://127.0.0.1:1", "--api-token", "t")
	if err == nil {
		t.Fatal("a piped board was not refused")
	}
	if !strings.Contains(err.Error(), "spinloop work list") {
		t.Errorf("error = %q, want it to name spinloop work list", err)
	}
	if strings.Contains(err.Error(), "--url") {
		t.Errorf("error = %q, want the terminal refusal, not the flag one", err)
	}
}

func TestWorkBoard_TwoTokenFlagsRefused(t *testing.T) {
	_, err := runWork(t, "board", "--url", "http://127.0.0.1:1",
		"--api-token", "a", "--api-token-file", "/dev/null")
	if err == nil || !strings.Contains(err.Error(), "--api-token") || !strings.Contains(err.Error(), "--api-token-file") {
		t.Fatalf("error = %v, want one naming both token flags", err)
	}
}

// --- columns and cards ---

func TestWorkBoard_ColdRunDrawsFourEmptyColumns(t *testing.T) {
	a := newWBAPI(t, nil, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	view := wbPlain(m.View())
	for _, want := range []string{"Backlog 0", "Running 0", "Done 0", "Failed 0", "—"} {
		if !strings.Contains(view, want) {
			t.Errorf("cold view missing %q:\n%s", want, view)
		}
	}
}

func TestWorkBoard_ColumnsHoldTheirStatesWithCounts(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{
		wbItem("solo", orchestrator.StateBacklog),
		wbItem("crank", orchestrator.StateRunning),
		wbItem("past", orchestrator.StateDone),
		wbItem("bust", orchestrator.StateFailed),
		wbItem("later", orchestrator.StateBacklog),
	}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	view := wbPlain(m.View())
	for _, want := range []string{"Backlog 2", "Running 1", "Done 1", "Failed 1"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	for _, id := range []string{"solo", "crank", "past", "bust", "later"} {
		if !strings.Contains(view, id) {
			t.Errorf("view missing card %q:\n%s", id, view)
		}
	}
}

func TestWorkBoard_RunningCardCarriesNodeElapsedAndStateColour(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("crank", orchestrator.StateRunning)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	view := m.View()
	if !strings.Contains(view, "\x1b[33mrunning") {
		t.Error("a running card does not carry the amber the work list gives running")
	}
	plain := wbPlain(view)
	if !strings.Contains(plain, "node-1") {
		t.Errorf("running card missing node:\n%s", plain)
	}
	if !strings.Contains(plain, "2m 5s") {
		t.Errorf("running card missing elapsed:\n%s", plain)
	}
	// The count-up is a function of the clock when the card is drawn.
	setVar(&workBoardNow, func() time.Time { return time.Unix(1700000000+120, 0) })
	later := wbPlain(m.View())
	if !strings.Contains(later, "4m 5s") {
		t.Errorf("elapsed did not count up with the clock:\n%s", later)
	}
}

func TestWorkBoard_StateColoursMatchTheWorkList(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{
		wbItem("past", orchestrator.StateDone), wbItem("bust", orchestrator.StateFailed),
	}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	view := m.View()
	if !strings.Contains(view, "\x1b[92mdone") {
		t.Error("a done card does not carry the work list's green")
	}
	if !strings.Contains(view, "\x1b[31mfailed") {
		t.Error("a failed card does not carry the work list's red")
	}
}

func TestWorkBoard_LongInstructionsClipNotWrap(t *testing.T) {
	long := wbItem("long", orchestrator.StateBacklog)
	long.Instructions = strings.Repeat("word ", 80) + "ENDWORD"
	a := newWBAPI(t, []orchestrator.ItemView{long}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	view := m.View()
	plain := wbPlain(view)
	if strings.Contains(plain, "ENDWORD") {
		t.Error("the clipped tail of the instructions leaked into the card")
	}
	// The card keeps its shape: the frame's line count is the fixed one.
	if n, want := len(strings.Split(view, "\n")), 3+m.visibleCards()*workBoardCardStep; n != want {
		t.Errorf("board drew %d lines, want %d:\n%s", n, want, view)
	}
}

func TestWorkBoard_ColumnWindowFollowsTheSelection(t *testing.T) {
	var items []orchestrator.ItemView
	for i := 0; i < 10; i++ {
		items = append(items, wbItem(fmt.Sprintf("item%02d", i), orchestrator.StateBacklog))
	}
	a := newWBAPI(t, items, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	first := wbPlain(m.View())
	if !strings.Contains(first, "item00") || strings.Contains(first, "item09") {
		t.Errorf("window did not open on the selection:\n%s", first)
	}
	wbKeys(t, m, "up") // the top of a column is its own wall
	if m.cursor[1] != 0 {
		t.Errorf("up at the top walked to row %d", m.cursor[1])
	}
	wbKeys(t, m, "down", "down", "down", "down", "down", "down")
	moved := wbPlain(m.View())
	if !strings.Contains(moved, "item06") {
		t.Errorf("moving down did not bring the selection into view:\n%s", moved)
	}
	if !strings.Contains(moved, "item04") || strings.Contains(moved, "item00") {
		t.Errorf("the window did not leave the passed cards behind:\n%s", moved)
	}
}

func TestWorkBoard_ArrowsSkipEmptyColumns(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{
		wbItem("wait", orchestrator.StateBacklog), wbItem("gone", orchestrator.StateDone),
	}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "right")
	if m.cursor[0] != 2 {
		t.Errorf("cursor column = %d, want 2 (Running is empty; the cursor lands on Done)", m.cursor[0])
	}
	wbKeys(t, m, "right")
	if m.cursor[0] != 2 {
		t.Errorf("cursor walked past the last card column to %d", m.cursor[0])
	}
}

func TestWorkBoard_CardMovesBetweenReads(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("crank", orchestrator.StateRunning)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	if v := strings.Count(wbPlain(m.View()), "Running 1"); v != 1 {
		t.Fatal("crank was not running before the move")
	}
	a.mu.Lock()
	a.items[0].State = orchestrator.StateDone
	a.mu.Unlock()
	wbRound(t, m)
	view := wbPlain(m.View())
	if !strings.Contains(view, "Running 0") || !strings.Contains(view, "Done 1") {
		t.Errorf("the card did not move with the run:\n%s", view)
	}
}

// --- refresh and staleness ---

func TestWorkBoard_DroppedAPIAgesTheBoardAndRecovers(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("solo", orchestrator.StateBacklog)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	a.failGET = true
	wbRound(t, m)
	// The reading has now aged past three cadences: the board is still
	// alive, still drawn, and honest about its age.
	setVar(&workBoardNow, func() time.Time { return time.Unix(1700000000+4*3600, 0) })
	view := wbPlain(m.View())
	if !strings.Contains(view, "solo") {
		t.Errorf("a failed round emptied the board:\n%s", view)
	}
	if !strings.Contains(view, "reading") || !strings.Contains(view, "ago") {
		t.Errorf("the stale reading is not marked with its age:\n%s", view)
	}
	a.failGET = false
	wbRound(t, m)
	if strings.Contains(wbPlain(m.View()), " ago") {
		t.Error("the age mark survived a good read")
	}
}

func TestWorkBoard_RReadsAtOnce(t *testing.T) {
	a := newWBAPI(t, nil, nil)
	m := newWBTestModel(t, a)
	before := a.callCount("GET /v1/items")
	cmd := wbKeys(t, m, "r")
	if cmd == nil {
		t.Fatal("r started no round")
	}
	wbLand(t, m, cmd)
	if a.callCount("GET /v1/items") != before+1 {
		t.Error("r did not ask the API at once")
	}
}

func TestWorkBoard_QuitKeysEndTheProgram(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		a := newWBAPI(t, nil, nil)
		m := newWBTestModel(t, a)
		cmd := wbKeys(t, m, k)
		if cmd == nil {
			t.Fatalf("%q quit nothing", k)
		}
		if got := fmt.Sprintf("%T", cmd()); got != "tea.QuitMsg" {
			t.Errorf("%q sent %s, want the quit message", k, got)
		}
	}
}

// --- detail and tail ---

func TestWorkBoard_DetailShowsTheWholeFailedItem(t *testing.T) {
	failed := wbItem("bust", orchestrator.StateFailed)
	failed.Instructions = strings.Repeat("detail ", 40) + "FULLSTOP"
	failed.Tags = []string{"kind=fix", "area=api"}
	a := newWBAPI(t, []orchestrator.ItemView{failed}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	mb := wbKeys(t, m, "right", "right", "right", "enter") // to the Failed column, then in
	if mb == nil {
		t.Fatal("enter opened nothing")
	}
	// The batch's tail chain is never run by hand — its tick is the test's
	// hour-long stand-in for the live one — but the round it started is
	// driven directly below by the tail tests.
	if !m.detail {
		t.Fatal("the detail did not open")
	}
	view := wbPlain(m.View())
	for _, want := range []string{"FULLSTOP", "./bust", "kind=fix", "area=api", "the agent gave up", "esc back"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail missing %q:\n%s", want, view)
		}
	}
}

func TestWorkBoard_DetailKeepsTallInstructionsInsideTheFrame(t *testing.T) {
	big := wbItem("bust", orchestrator.StateFailed)
	big.Instructions = strings.Repeat("sentence that keeps going and going. ", 120) + "ENDSTOP"
	a := newWBAPI(t, []orchestrator.ItemView{big}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "right", "right", "right", "enter")
	view := wbPlain(m.View())
	lines := strings.Split(view, "\n")
	// Nothing may fall past the bottom edge unseen: the frame closes at
	// the terminal's height, footer and all.
	if len(lines) != m.effHeight() {
		t.Errorf("the detail drew %d lines in a frame of %d:\n%s", len(lines), m.effHeight(), view)
	}
	for _, want := range []string{"esc back", "./bust", "⋯ +"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail missing %q — the frame did not close honestly:\n%s", want, view)
		}
	}
	// What does not fit is counted, not silently shown in part: the
	// tail of the instructions yields to the note.
	if strings.Contains(view, "ENDSTOP") {
		t.Error("the overflow past the note still showed its tail")
	}
}

func TestWorkBoard_DetailWrapsTheFailureReason(t *testing.T) {
	failed := wbItem("bust", orchestrator.StateFailed)
	// A reason far wider than the frame: its tail must still be readable,
	// not chopped at the edge — the whole point of wrapping it.
	failed.Why = "the agent gave up because " + strings.Repeat("the harness demanded more ", 20) + "ENDREASON"
	a := newWBAPI(t, []orchestrator.ItemView{failed}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "right", "right", "right", "enter")
	lines := strings.Split(wbPlain(m.View()), "\n")
	if !strings.Contains(strings.Join(lines, "\n"), "ENDREASON") {
		t.Fatalf("the reason was cut off at the frame's edge:\n%s", strings.Join(lines, "\n"))
	}
	// The reason holds together on its own rows under the label, and no
	// line overruns the frame it is clipped to.
	var reasonRows int
	for _, l := range lines {
		if strings.HasPrefix(l, "why") || strings.HasPrefix(l, "     ") {
			if strings.Contains(l, "harness demanded") || strings.Contains(l, "agent gave up") || strings.Contains(l, "ENDREASON") {
				reasonRows++
			}
		}
	}
	if reasonRows < 2 {
		t.Errorf("the reason did not wrap across rows (saw %d):\n%s", reasonRows, strings.Join(lines, "\n"))
	}
	for _, l := range lines {
		if n := lipgloss.Width(l); n > m.effWidth() {
			t.Errorf("a detail line ran to %d columns, past the frame of %d:\n%q", n, m.effWidth(), l)
		}
	}
}

func TestWorkBoard_DetailEscReturnsAndRefusesQuit(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("bust", orchestrator.StateFailed)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "right", "right", "right", "enter")
	cmd := wbKeys(t, m, "q")
	if cmd != nil {
		if got := fmt.Sprintf("%T", cmd()); got == "tea.quitMsg" {
			t.Error("the board quit from inside the detail")
		}
	}
	if !m.detail {
		t.Error("q disturbed the detail")
	}
	wbKeys(t, m, "esc")
	if m.detail {
		t.Error("esc did not return to the board")
	}
	if m.cursor[0] != 3 || m.cursor[1] != 0 {
		t.Errorf("the selection moved: %v", m.cursor)
	}
}

func TestWorkBoard_DetailTailsAndStops(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("crank", orchestrator.StateRunning)},
		map[string]string{"crank": "first line\n"})
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "right", "enter") // openDetail starts the first round itself

	// A first fetch shows what is there; an empty pane is a note, not a
	// fault. The answer below is the one openDetail's started round is
	// answering with — injected as the program would deliver it.
	m.Update(workBoardTailMsg{gen: m.detailGen, log: "first line\n"})
	if !strings.Contains(wbPlain(m.View()), "first line") {
		t.Error("the tail did not show the kept output")
	}
	// The agent writes more: the next poll appends only what is new.
	a.setLog("crank", "first line\nsecond line\n")
	msg := m.startTailRound()
	if msg == nil {
		t.Fatal("the tail did not poll again")
	}
	m.Update(msg())
	view := wbPlain(m.View())
	if !strings.Contains(view, "second line") || strings.Count(view, "first line") != 1 {
		t.Errorf("the tail did not append the suffix once:\n%s", view)
	}
	// The item ends: the tail stops, whatever the last poll brought standing.
	a.mu.Lock()
	a.items[0].State = orchestrator.StateDone
	a.mu.Unlock()
	wbRound(t, m)
	if cmd := m.startTailRound(); cmd != nil {
		t.Error("the tail kept polling an ended item")
	}
}

func TestWorkBoard_DetailOfAnItemWithNoLogIsEmptyNotAFault(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("crank", orchestrator.StateRunning)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "right", "enter")
	m.Update(workBoardTailMsg{gen: m.detailGen, log: ""})
	view := wbPlain(m.View())
	if !strings.Contains(view, "no kept output yet") {
		t.Errorf("empty pane without a note:\n%s", view)
	}
	// The item is removed mid-view: the next poll 404s, and the tail ends
	// with a note — not with a fault, and not with the pane going blank.
	a.mu.Lock()
	a.items = nil
	a.mu.Unlock()
	wbRound(t, m)
	msg := m.startTailRound()
	if msg == nil {
		t.Fatal("the tail did not poll once more to find the item gone")
	}
	m.Update(msg())
	view = wbPlain(m.View())
	if !strings.Contains(view, "no longer in the list") {
		t.Errorf("gone item left no note:\n%s", view)
	}
}

// --- actions ---

func TestWorkBoard_AbortMovesTheCardBackToBacklog(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("crank", orchestrator.StateRunning)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbAct(t, m, "right", "a")
	if a.callCount("POST /v1/items/crank/abort") != 1 {
		t.Fatal("the abort did not reach the API")
	}
	if !strings.Contains(m.statusLine, "back in the backlog") {
		t.Errorf("status = %q, want the stopped line", m.statusLine)
	}
	view := wbPlain(m.View())
	if !strings.Contains(view, "Running 0") || !strings.Contains(view, "Backlog 1") {
		t.Errorf("the card did not move back:\n%s", view)
	}
}

func TestWorkBoard_BacklogAbortIsRefusedTheAPISWay(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("solo", orchestrator.StateBacklog)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbAct(t, m, "a")
	if !strings.Contains(m.statusLine, `item "solo" is not running`) {
		t.Errorf("status = %q, want the API's own refusal", m.statusLine)
	}
	if !strings.Contains(wbPlain(m.View()), "solo") {
		t.Error("the refusal took the board down with it")
	}
}

func TestWorkBoard_RemovalAsksFirst(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("solo", orchestrator.StateBacklog)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "x")
	footer := wbPlain(m.footerLine(m.effWidth(), m.boardKeys()))
	if !strings.Contains(footer, `remove item "solo"?`) {
		t.Errorf("the question did not stand: %q", footer)
	}
	if a.callCount("DELETE") != 0 {
		t.Error("the removal was sent before the yes")
	}
	wbKeys(t, m, "n")
	if a.callCount("DELETE") != 0 {
		t.Error("a declined removal was sent anyway")
	}
	if !strings.Contains(m.statusLine, "nothing removed") {
		t.Errorf("status = %q, want the declined line", m.statusLine)
	}
	if !strings.Contains(wbPlain(m.View()), "solo") {
		t.Error("the declined card vanished")
	}
}

func TestWorkBoard_RemovalOnYesGoesThrough(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{
		wbItem("solo", orchestrator.StateBacklog), wbItem("later", orchestrator.StateBacklog),
	}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbAct(t, m, "x", "y")
	if a.callCount("DELETE /v1/items/solo") != 1 {
		t.Fatal("the yes sent no DELETE")
	}
	if !strings.Contains(m.statusLine, `"solo" removed`) {
		t.Errorf("status = %q", m.statusLine)
	}
	view := wbPlain(m.View())
	if strings.Contains(view, "● solo") || !strings.Contains(view, "later") || !strings.Contains(view, "Backlog 1") {
		t.Errorf("the board outlived its card:\n%s", view)
	}
}

func TestWorkBoard_RunningRemovalRefusedNamingTheAbort(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("crank", orchestrator.StateRunning)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbAct(t, m, "right", "x", "y")
	if !strings.Contains(m.statusLine, "abort it first") {
		t.Errorf("status = %q, want the refusal naming the abort", m.statusLine)
	}
	view := wbPlain(m.View())
	if !strings.Contains(view, "● crank") || !strings.Contains(view, "Running 1") {
		t.Errorf("the refused removal took the card away:\n%s", view)
	}
}

// --- the add form ---

func TestWorkBoard_FormOpensSizedAndStill(t *testing.T) {
	a := newWBAPI(t, nil, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "n")
	if !m.formOpen {
		t.Fatal("n opened no form")
	}
	for i, f := range m.form.fields {
		if f.Cursor.Blink {
			t.Errorf("field %d blinks on its own", i)
		}
	}
	view := wbPlain(m.View())
	for _, want := range []string{"new work item", "id*", "instructions*", "dir*", "tags", "priority", "❯"} {
		if !strings.Contains(view, want) {
			t.Errorf("form missing %q:\n%s", want, view)
		}
	}
	// Byte-stable with the clock fixed: nothing moves on its own.
	if m.View() != m.View() {
		t.Error("the form's view is not byte-stable")
	}
}

func TestWorkBoard_PriorityFieldTakesOnlyNumbers(t *testing.T) {
	a := newWBAPI(t, nil, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "n")
	calls := len(a.calls)
	wbKeys(t, m, "down", "down", "down", "down") // to priority
	wbKeys(t, m, "12")
	wbKeys(t, m, "abc")
	wbKeys(t, m, "3")
	if got := m.form.fields[workFormPriority].Value(); got != "123" {
		t.Errorf("priority field = %q, want 123 — the letters must not stand", got)
	}
	if len(a.calls) != calls {
		t.Error("keystrokes called the API")
	}
}

func TestWorkBoard_FormAddsEndToEnd(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("solo", orchestrator.StateBacklog)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "n")
	wbKeys(t, m, "karma", "enter", "make it so", "enter", "./repo", "enter", "enter")
	cmd := wbKeys(t, m, "7", "enter") // enter on the last field sends
	if cmd == nil {
		t.Fatal("the last enter sent nothing")
	}
	wbLand(t, m, cmd)
	if a.callCount("POST /v1/items") != 1 {
		t.Fatal("the send reached no POST")
	}
	body := a.lastBody()
	if body.ID != "karma" || body.Instructions != "make it so" || body.Dir != "./repo" || body.Priority != 7 {
		t.Errorf("the API was sent %+v", body)
	}
	if m.formOpen {
		t.Error("the accepted form did not close")
	}
	if !strings.Contains(m.statusLine, `"karma" added`) {
		t.Errorf("status = %q", m.statusLine)
	}
	view := wbPlain(m.View())
	if !strings.Contains(view, "Backlog 2") || !strings.Contains(view, "karma") {
		t.Errorf("the new card does not stand under Backlog:\n%s", view)
	}
}

func TestWorkBoard_FormRefusalIsCorrectedInPlace(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("dup", orchestrator.StateBacklog)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "n")
	wbKeys(t, m, "dup", "enter", "again", "enter", "./d", "enter", "enter")
	cmd := wbKeys(t, m, "enter") // send with the id the list already carries
	if cmd == nil {
		t.Fatal("the first enter sent nothing")
	}
	wbLand(t, m, cmd) // a refused send still spends its follow-up read
	if !m.formOpen {
		t.Fatal("a refusal closed the form")
	}
	if !strings.Contains(m.form.err, `item "dup" is already in the list`) {
		t.Errorf("form carries no API refusal: %q", m.form.err)
	}
	// Correct the id in the form and send again: up to the first field,
	// rub it out, type the new one, walk back down.
	wbKeys(t, m, "up", "up", "up", "up")
	wbKeys(t, m, "backspace", "backspace", "backspace", "fresh")
	cmd = wbKeys(t, m, "down", "down", "down", "down", "enter")
	if cmd == nil {
		t.Fatal("the second enter sent nothing")
	}
	wbLand(t, m, cmd)
	if m.formOpen {
		t.Error("the corrected send did not close the form")
	}
	if a.callCount("POST /v1/items") != 2 {
		t.Errorf("POSTs = %d, want 2", a.callCount("POST /v1/items"))
	}
	if a.lastBody().ID != "fresh" {
		t.Errorf("the second send was %+v", a.lastBody())
	}
	if !strings.Contains(wbPlain(m.View()), "Backlog 2") {
		t.Error("the corrected item never reached the board")
	}
}

func TestWorkBoard_FormEscapeGuard(t *testing.T) {
	a := newWBAPI(t, nil, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	// Empty: esc closes and says so, sending nothing.
	wbKeys(t, m, "n", "esc")
	if m.formOpen {
		t.Error("esc did not close an empty form")
	}
	if !strings.Contains(m.statusLine, "nothing added") {
		t.Errorf("status = %q", m.statusLine)
	}
	// Typed: esc asks once; declining keeps everything; discarding sends nothing.
	wbKeys(t, m, "n")
	wbKeys(t, m, "half a thought", "esc")
	if !m.formOpen || !m.formAsk {
		t.Fatal("esc on typed text neither kept the form nor asked")
	}
	wbKeys(t, m, "n")
	if !m.formOpen || m.formAsk || m.form.fields[0].Value() != "half a thought" {
		t.Error("the kept form lost its place or its text")
	}
	wbKeys(t, m, "esc", "y")
	if m.formOpen {
		t.Error("a deliberate discard did not close the form")
	}
	if a.callCount("POST /v1/items") != 0 {
		t.Error("the form sent anything")
	}
}

func TestWorkBoard_BoardLivesBehindTheForm(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("crank", orchestrator.StateRunning)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "n")
	a.mu.Lock()
	a.items[0].State = orchestrator.StateDone
	a.mu.Unlock()
	wbRound(t, m)
	view := wbPlain(m.View())
	if !strings.Contains(view, "new work item") {
		t.Error("the round closed the form")
	}
	if !strings.Contains(view, "Done 1") {
		t.Errorf("the board behind the form stopped moving:\n%s", view)
	}
	wbKeys(t, m, "esc")
	if !strings.Contains(wbPlain(m.View()), "Done 1") {
		t.Error("the card did not stand where the board was left")
	}
}

// --- completion and the program itself ---

func TestWorkBoard_CompletionIsQuietAndTakeNoPositionals(t *testing.T) {
	cands, directive := completeQuiet(t, "work", "board", "--")
	if !hasAll(cands, "--url", "--api-token", "--api-token-file") {
		t.Errorf("flags offered = %v", cands)
	}
	cands, directive = completeQuiet(t, "work", "board", "some-id")
	if len(cands) != 0 {
		t.Errorf("positionals offered: %v", cands)
	}
	if directive != ":4" { // NoFileComp: an id is never a path here
		t.Errorf("directive = %q, want :4", directive)
	}
}

// wbFeed reads the live program's frames into one accumulated screen,
// colour stripped: a board that has nothing new to say stops redrawing,
// so a per-frame reader would miss words it needed — the whole screen
// read so far is the honest needle ground.
type wbFeed struct {
	mu     sync.Mutex
	plain  strings.Builder
	done   chan struct{}
	closed bool
}

func newWBFeed(t *testing.T, out io.Reader) *wbFeed {
	t.Helper()
	f := &wbFeed{done: make(chan struct{})}
	go func() {
		buf := make([]byte, 32*1024)
		for {
			select {
			case <-f.done:
				return
			default:
			}
			n, err := out.Read(buf)
			if n > 0 {
				f.mu.Lock()
				f.plain.WriteString(wbPlain(string(buf[:n])))
				f.mu.Unlock()
			}
			if err != nil && err != io.EOF {
				return // the program is done; what was read is all there is
			}
			// teatest's output is a buffer: empty is not closed. The
			// next frame is a poll away, so idle gently and keep reading.
			time.Sleep(20 * time.Millisecond)
		}
	}()
	t.Cleanup(func() { close(f.done) })
	return f
}

func (f *wbFeed) until(t *testing.T, what string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := strings.Contains(f.plain.String(), what)
		f.mu.Unlock()
		if got {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.mu.Lock()
	screen := f.plain.String()
	f.mu.Unlock()
	if n := len(screen); n > 1200 {
		screen = screen[n-1200:]
	}
	t.Fatalf("the board never showed %q — screen so far:\n%s", what, screen)
}

func TestWorkBoard_ProgramSmoke(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{
		wbItem("solo", orchestrator.StateBacklog),
		wbItem("crank", orchestrator.StateRunning),
	}, nil)
	restore := []func(){
		setVar(&workBoardRefreshInterval, 50*time.Millisecond),
		setVar(&workBoardNow, time.Now),
	}
	defer func() {
		for _, f := range restore {
			f()
		}
	}()
	m := newWorkBoardModel(a.srv.URL, "tok")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(140, 30))
	feed := newWBFeed(t, tm.Output())
	feed.until(t, "Backlog 1")
	feed.until(t, "crank")
	// The real loop now, not the test's hand: an abort's call and its
	// spinner tick ride a batch, which only the program itself expands
	// — the answer lands on the status line and the kicked read moves
	// the card through the very loop the binary runs.
	tm.Send(tea.KeyMsg{Type: tea.KeyRight})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	feed.until(t, `item "crank" stopped`)
	feed.until(t, "Backlog 2")
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestWorkBoard_RetryMovesAFailedCardBackToBacklog(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("crank", orchestrator.StateFailed)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbAct(t, m, "right", "t", "y")
	if a.callCount("POST /v1/items/crank/retry") != 1 {
		t.Fatal("the retry did not reach the API")
	}
	if !strings.Contains(m.statusLine, `item "crank" is back in the backlog`) {
		t.Errorf("status = %q, want the retried line", m.statusLine)
	}
	view := wbPlain(m.View())
	if !strings.Contains(view, "Failed 0") || !strings.Contains(view, "Backlog 1") {
		t.Errorf("the card did not move back:\n%s", view)
	}
}

func TestWorkBoard_RetryOfAnItemThatHasNotFailedIsRefusedTheAPISWay(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("solo", orchestrator.StateBacklog)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbAct(t, m, "t", "y")
	if !strings.Contains(m.statusLine, `item "solo" is not failed`) {
		t.Errorf("status = %q, want the API's own refusal", m.statusLine)
	}
	if !strings.Contains(wbPlain(m.View()), "solo") {
		t.Error("the refusal took the board down with it")
	}
}

func TestWorkBoard_RetryIsNamedOnlyOnAFailedCard(t *testing.T) {
	cases := map[string]bool{
		orchestrator.StateBacklog: false,
		orchestrator.StateRunning: false,
		orchestrator.StateDone:    false,
		orchestrator.StateFailed:  true,
	}
	for state, want := range cases {
		a := newWBAPI(t, []orchestrator.ItemView{wbItem("solo", state)}, nil)
		m := newWBTestModel(t, a)
		wbRound(t, m)
		keys := m.boardKeys()
		if m.selectedItem() == nil {
			wbKeys(t, m, "right")
			keys = m.boardKeys()
		}
		for i := 0; i < 3 && m.selectedItem() == nil; i++ {
			wbKeys(t, m, "right")
			keys = m.boardKeys()
		}
		if got := strings.Contains(keys, "t retry"); got != want {
			t.Errorf("%s card: keys %q, retry named = %v, want %v", state, keys, got, want)
		}
	}
}

func TestWorkBoard_RetryAsksFirst(t *testing.T) {
	a := newWBAPI(t, []orchestrator.ItemView{wbItem("crank", orchestrator.StateFailed)}, nil)
	m := newWBTestModel(t, a)
	wbRound(t, m)
	wbKeys(t, m, "right", "t")
	footer := wbPlain(m.footerLine(m.effWidth(), m.boardKeys()))
	if !strings.Contains(footer, `retry item "crank"?`) {
		t.Errorf("the question did not stand: %q", footer)
	}
	if a.callCount("/retry") != 0 {
		t.Error("the retry was sent before the yes")
	}
	wbKeys(t, m, "n")
	if a.callCount("/retry") != 0 {
		t.Error("a declined retry was sent anyway")
	}
	if !strings.Contains(m.statusLine, "nothing retried") {
		t.Errorf("status = %q, want the declined line", m.statusLine)
	}
	if !strings.Contains(wbPlain(m.View()), "Failed 1") {
		t.Error("the declined card left the Failed column")
	}
	// Escape abandons the question the same way.
	wbKeys(t, m, "t", "esc")
	if a.callCount("/retry") != 0 {
		t.Error("an abandoned retry was sent anyway")
	}
}
