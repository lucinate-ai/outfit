// The `work board` model: the columns, the selection, the detail, the add
// form, and how keys and messages move them. As with the fleet dashboard's
// model, every rule here is plain Go over plain data — the clock, the
// intervals and the API's address are inputs — so the suite drives the
// whole screen without a terminal. The board is a client of the work list
// API alone: like the one-shot work commands, it never touches the items
// file, the state, or the logs.

package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/spinloop-ai/spinloop/internal/orchestrator"
)

// The board's cadences and its clock, variables so a test never waits on
// one or is at the mercy of the other. The refresh cadence is the fleet
// dashboard's own pace: one GET per tick, and the run it watches changes
// on the scale of agent turns, not milliseconds. The tail cadence matches
// `work logs -f`, which the detail pane tails the same way.
var (
	workBoardRefreshInterval = 5 * time.Second
	workBoardTailInterval    = 1 * time.Second
	workBoardSpinInterval    = 100 * time.Millisecond
)

// workBoardStaleThreshold is how many refresh intervals a reading may age
// before the title bar says so rather than draw it as the present state of
// the run — the dashboard's rule, and the same three rather than one so a
// single late round does not flicker the board grey.
const workBoardStaleThreshold = 3

// workBoardStaleAfter is when the board's reading counts as aged. A
// function of the cadence, so a test that shortens the cadence shortens
// the threshold with it.
func workBoardStaleAfter() time.Duration {
	return workBoardStaleThreshold * workBoardRefreshInterval
}

// workBoardNow is the board's clock, a variable so elapsed times a test
// renders are the test's to fix.
var workBoardNow = time.Now

// workBoardVerb is one of the board's actions on an item, as its status
// lines name it.
type workBoardVerb string

const (
	workAbort  workBoardVerb = "abort"
	workRetry  workBoardVerb = "retry"
	workRemove workBoardVerb = "remove"
	workAdd    workBoardVerb = "add"
)

// progress is what the status line shows while the action is in flight:
// the tool's one spinner, the verb, and how long the call has been out —
// an abort holds the call for the run's stop grace, so the wait has to
// show it is moving.
func (a workBoardAction) progress(now time.Time) string {
	line := spinnerFrame(now) + " " + string(a.verb) + "ing " + a.id
	if a.since.IsZero() {
		return line
	}
	elapsed := now.Sub(a.since)
	if elapsed < 0 {
		elapsed = 0
	}
	return line + "  " + formatDuration(int(elapsed.Seconds()))
}

// workBoardAction is the one action in flight: the board sends one call at
// a time, so one slot carries it — the verb, the item it concerns, and
// when it began. The zero value is an idle board.
type workBoardAction struct {
	verb  workBoardVerb
	id    string
	since time.Time
}

// workBoardForm is the add form: five fields, the cursor on one of them,
// and — where the API refused the last send — the refusal itself, kept
// visibly for the corrected send to replace.
type workBoardForm struct {
	fields []textinput.Model // id, instructions, dir, tags, priority — workFormLabel's order
	cursor int               // the field taking keystrokes
	err    string            // the API's refusal of the last send; cleared by the next
}

// The form's fields, in the order the form draws them and the labels it
// draws beside them. Required marks the three an item cannot do without —
// a mark, not a validator: the API stays the only judge of an item's
// shape, and says so itself.
var (
	workFormLabels   = []string{"id*", "instructions*", "dir*", "tags", "priority"}
	workFormPromptW  = 13
	workFormPriority = 4 // the index of the one field the form guards itself
)

// newWorkBoardForm builds the form with its inputs sized and blink off:
// the board is a standing surface, and a caret blinking on its own is
// both animation the spec does not ask for and movement a byte-stable
// render test cannot allow.
func newWorkBoardForm(width int) workBoardForm {
	f := workBoardForm{fields: make([]textinput.Model, len(workFormLabels))}
	for i := range f.fields {
		ti := textinput.New()
		ti.Cursor.Blink = false
		ti.Width = width
		ti.Prompt = ""
		f.fields[i] = ti
	}
	f.focus(0)
	return f
}

// focus moves the caret to one field: into it, and out of whichever was.
func (f *workBoardForm) focus(i int) {
	if i < 0 || i >= len(f.fields) {
		return
	}
	f.fields[f.cursor].Blur()
	f.cursor = i
	f.fields[i].Focus()
}

// dirty reports whether anything has been typed — which decides whether an
// escape closes the form or first asks to discard.
func (f *workBoardForm) dirty() bool {
	for _, ti := range f.fields {
		if ti.Value() != "" {
			return true
		}
	}
	return false
}

// values returns the form as the item the API will be asked to add. Tags
// are the field split on spaces — the same values `work add --tag` takes
// one flag at a time; whether they are well-formed is the API's to say.
func (f *workBoardForm) values() (workAddBody, error) {
	priority := 0
	if v := strings.TrimSpace(f.fields[workFormPriority].Value()); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return workAddBody{}, fmt.Errorf("priority %q is not a number", v)
		}
		priority = n
	}
	var tags []string
	if v := strings.Join(strings.Fields(f.fields[3].Value()), " "); v != "" {
		tags = strings.Fields(v)
	}
	return workAddBody{
		ID:           f.fields[0].Value(),
		Instructions: f.fields[1].Value(),
		Dir:          f.fields[2].Value(),
		Tags:         tags,
		Priority:     priority,
	}, nil
}

// clear drops the form's text and any refusal, ready for the next item.
func (f *workBoardForm) clear() {
	for i := range f.fields {
		f.fields[i].SetValue("")
		f.fields[i].SetCursor(0)
	}
	f.cursor = 0
	f.err = ""
	f.focus(0)
}

// workBoardModel is the program's state. The items are the last reading;
// the cursor names a column and a row within it; the detail, the removal
// question and the add form are the three modes a screen stands in, and
// keys route form → question → detail → board.
type workBoardModel struct {
	base  string
	token string

	items     []orchestrator.ItemView // the last reading, in the API's order
	readingAt time.Time               // when it was taken
	readErr   string                  // why the last round failed; "" when it did not

	cursor  [2]int          // column, then row within that column
	scrolls [4]int          // the first visible card row per column
	busy    bool            // a read round is in flight
	action  workBoardAction // the one action in flight, if any

	statusLine string

	detail     bool
	detailItem orchestrator.ItemView // the item in view, kept from the reading
	detailGen  int                   // bumped per open; stale log replies are discarded
	detailBusy bool                  // a log round is in flight
	detailSeen string                // the whole log as last fetched, for the suffix
	detailLog  string                // the tailed lines, trimmed to the pane
	detailNote string                // why the pane is empty

	formOpen bool
	formAsk  bool // the discard question stands in front of the form
	form     workBoardForm

	confirm bool // a removal stands in front of the board, waiting on its yes

	width, height int
}

// The board's columns, in the order the spec stands them.
var workBoardColumns = []struct {
	title string
	state string
}{
	{"Backlog", orchestrator.StateBacklog},
	{"Running", orchestrator.StateRunning},
	{"Done", orchestrator.StateDone},
	{"Failed", orchestrator.StateFailed},
}

// Msgs.

type workBoardTickMsg time.Time
type workBoardSpinMsg time.Time
type workBoardTailTickMsg time.Time

// workBoardReadMsg is one completed round of the list. at is when the
// answer arrived; Update draws it only if it is newer than what is on
// screen — the dashboard's race guard, for the same reason.
type workBoardReadMsg struct {
	items []orchestrator.ItemView
	at    time.Time
	err   error
}

// workBoardActionMsg is one completed action.
type workBoardActionMsg struct {
	verb workBoardVerb
	id   string
	err  error
}

// workBoardTailMsg is one completed poll of the item in view's log. gen
// ties it to the detail view that started it.
type workBoardTailMsg struct {
	gen  int
	log  string
	gone bool // the item left the list: its log 404s, and the tail ends
	err  error
}

func workBoardTickCmd() tea.Cmd {
	return tea.Tick(workBoardRefreshInterval, func(time.Time) tea.Msg { return workBoardTickMsg{} })
}

func workBoardSpinCmd() tea.Cmd {
	return tea.Tick(workBoardSpinInterval, func(time.Time) tea.Msg { return workBoardSpinMsg{} })
}

func workBoardTailTickCmd() tea.Cmd {
	return tea.Tick(workBoardTailInterval, func(time.Time) tea.Msg { return workBoardTailTickMsg{} })
}

// newWorkBoardModel builds the board over the API it will call. The first
// round arrives on Init, as everywhere else; a board that has read nothing
// yet draws four empty columns, not an error.
func newWorkBoardModel(base, token string) *workBoardModel {
	return &workBoardModel{base: base, token: token}
}

func (m *workBoardModel) Init() tea.Cmd {
	return tea.Batch(workBoardTickCmd(), m.startRound())
}

// columnIndexes buckets the reading into the four columns, each column
// holding its items in the API's order — the order `work list` prints, so
// the two surfaces cannot show the run in two orders.
func (m *workBoardModel) columnIndexes() [4][]int {
	var cols [4][]int
	for i, v := range m.items {
		for c := range workBoardColumns {
			if v.State == workBoardColumns[c].state {
				cols[c] = append(cols[c], i)
				break
			}
		}
	}
	return cols
}

// selectedItem is the card under the cursor, or nil where the cursor
// stands on nothing — an empty column, or an empty board.
func (m *workBoardModel) selectedItem() *orchestrator.ItemView {
	cols := m.columnIndexes()
	c, r := m.cursor[0], m.cursor[1]
	if c >= len(cols) || r < 0 || r >= len(cols[c]) {
		return nil
	}
	v := m.items[cols[c][r]]
	return &v
}

func (m *workBoardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clamp()
	case workBoardTickMsg:
		// One tick, rescheduling itself, starting a round only when none is
		// in flight — a slow API stretches a round rather than overlapping
		// the next.
		return m, tea.Batch(workBoardTickCmd(), m.startRound())
	case workBoardReadMsg:
		m.busy = false
		if msg.err != nil {
			m.readErr = msg.err.Error()
			return m, nil
		}
		if msg.at.Before(m.readingAt) {
			return m, nil // an older answer than what is on screen
		}
		m.readErr = ""
		m.items = msg.items
		m.readingAt = msg.at
		m.retainDetail()
		m.clamp()
	case workBoardSpinMsg:
		if m.action.verb == "" {
			return m, nil
		}
		return m, workBoardSpinCmd()
	case workBoardActionMsg:
		m.action = workBoardAction{}
		if msg.verb == workAdd {
			// The form carries its own answer: closed on acceptance, still
			// open under the API's refusal so a field can be corrected.
			if msg.err == nil {
				m.formOpen = false
				m.formAsk = false
				m.form.clear()
				m.statusLine = fmt.Sprintf("item %q added", msg.id)
			} else {
				m.form.err = msg.err.Error()
			}
		} else {
			m.statusLine = workBoardActionLine(msg)
		}
		// What the action changed is what the operator is waiting to see:
		// a round is due now rather than at the tick.
		return m, m.startRound()
	case workBoardTailTickMsg:
		if !m.detail {
			return m, nil
		}
		return m, tea.Batch(workBoardTailTickCmd(), m.startTailRound())
	case workBoardTailMsg:
		m.detailBusy = false
		if !m.detail || msg.gen != m.detailGen {
			return m, nil // the view closed, or reopened on another item
		}
		m.applyTail(msg)
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

// updateKey routes a key by mode: the form, then the removal question,
// then the detail, then the board.
func (m *workBoardModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.formOpen {
		if m.formAsk {
			return m, m.updateFormAsk(msg)
		}
		return m, m.updateFormKey(msg)
	}
	if m.confirm {
		switch msg.String() {
		case "y":
			v := m.selectedItem()
			m.confirm = false
			if v == nil {
				return m, nil
			}
			return m, m.beginAction(workRemove, v.ID)
		case "n", "esc":
			m.confirm = false
			m.statusLine = "declined — nothing removed"
		case "q", "ctrl+c":
			m.confirm = false
			return m, tea.Quit
		}
		return m, nil
	}
	if m.detail {
		if msg.String() == "esc" {
			m.detail = false
		}
		return m, nil
	}
	return m, m.updateBoardKey(msg)
}

func (m *workBoardModel) updateBoardKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q", "ctrl+c":
		return tea.Quit
	case "up":
		m.moveRow(-1)
	case "down":
		m.moveRow(1)
	case "left":
		m.moveColumn(-1)
	case "right":
		m.moveColumn(1)
	case "enter":
		if v := m.selectedItem(); v != nil {
			return m.openDetail(*v)
		}
	case "a":
		// No client-side guard on state: a backlog abort is the API's
		// refusal to make, in its own words. Nor one on the action slot:
		// beginAction answers a second key with its own status line.
		if v := m.selectedItem(); v != nil {
			return m.beginAction(workAbort, v.ID)
		}
	case "t":
		// As with abort, the API refuses a retry of what has not failed.
		if v := m.selectedItem(); v != nil {
			return m.beginAction(workRetry, v.ID)
		}
	case "x":
		if m.selectedItem() != nil {
			m.confirm = true
		}
	case "n":
		m.formOpen = true
		m.formAsk = false
		m.form = newWorkBoardForm(m.formFieldWidth())
	case "r":
		return m.startRound()
	}
	return nil
}

// updateFormKey answers the keys the add form reads. Typing and the caret
// keys belong to the focused textinput; up/down step the field cursor;
// enter advances and, on the last field, sends. The priority field takes
// only digits and a leading minus — a non-number could not even be asked
// of the API, so the keystroke simply does not enter the field.
func (m *workBoardModel) updateFormKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		if m.form.dirty() {
			m.formAsk = true // discard is a deliberate act, not one escape
		} else {
			m.closeForm("nothing added")
		}
		return nil
	case "up":
		m.form.focus(m.form.cursor - 1)
		return nil
	case "down", "tab":
		m.form.focus(m.form.cursor + 1)
		return nil
	case "enter":
		if m.form.cursor < len(m.form.fields)-1 {
			m.form.focus(m.form.cursor + 1)
			return nil
		}
		return m.sendForm()
	}
	if m.form.cursor == workFormPriority && msg.Type == tea.KeyRunes {
		allowed := make([]rune, 0, len(msg.Runes))
		for _, r := range msg.Runes {
			if (r >= '0' && r <= '9') || r == '-' {
				allowed = append(allowed, r)
			}
		}
		if len(allowed) == 0 {
			return nil
		}
		msg.Runes = allowed
		msg.Type = tea.KeyRunes
	}
	ti, cmd := m.form.fields[m.form.cursor].Update(msg)
	m.form.fields[m.form.cursor] = ti
	return cmd
}

// sendForm is the form's enter on the last field: the assembled item goes
// to the API — the API alone judges its shape — and the form stays open
// under the refusal, its text intact, if the API says no.
func (m *workBoardModel) sendForm() tea.Cmd {
	body, err := m.form.values()
	if err != nil {
		m.form.err = err.Error() // the one shape fault the form can state: its own priority field
		return nil
	}
	m.form.err = ""
	return m.beginAdd(body)
}

func (m *workBoardModel) closeForm(status string) {
	m.formOpen = false
	m.formAsk = false
	m.form.clear()
	m.statusLine = status
}

// updateFormAsk answers the discard question: it defaults to keeping the
// work, so only a deliberate yes closes and says nothing was sent.
func (m *workBoardModel) updateFormAsk(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y":
		m.closeForm("nothing added")
	case "n", "esc":
		m.formAsk = false
	case "q", "ctrl+c":
		m.formOpen = false
		m.formAsk = false
		return tea.Quit
	}
	return nil
}

// startRound opens one read of the list, if none is in flight. The answer
// comes back as a msg stamped with the time it arrived, not the time the
// round started.
func (m *workBoardModel) startRound() tea.Cmd {
	if m.busy {
		return nil
	}
	m.busy = true
	base, token := m.base, m.token
	return func() tea.Msg {
		data, err := workRequest(base, token, "GET", "/v1/items", nil)
		if err != nil {
			return workBoardReadMsg{at: workBoardNow(), err: err}
		}
		var out struct {
			Data []orchestrator.ItemView `json:"data"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return workBoardReadMsg{at: workBoardNow(), err: fmt.Errorf("reading the work list: %w", err)}
		}
		return workBoardReadMsg{items: out.Data, at: workBoardNow()}
	}
}

// beginAction sets off one call against an item. One action at a time:
// a second key while a call is out is answered by the status line, not a
// second call. The call rides the same bound the one-shot commands put on
// every call.
func (m *workBoardModel) beginAction(verb workBoardVerb, id string) tea.Cmd {
	if m.action.verb != "" {
		m.statusLine = "still " + string(m.action.verb) + "ing " + m.action.id
		return nil
	}
	base, token := m.base, m.token
	m.action = workBoardAction{verb: verb, id: id, since: workBoardNow()}
	// The repaint chain that animates the spinner is scheduled as a
	// command, not pushed through the program's Send: a Send from inside
	// Update deadlocks the loop — it cannot read a message until Update
	// returns, and Update is the one sending. The tick handler keeps
	// the chain restacking while the call is out.
	var run tea.Cmd
	switch verb {
	case workAbort:
		run = func() tea.Msg {
			_, err := workRequest(base, token, "POST", "/v1/items/"+url.PathEscape(id)+"/abort", nil)
			return workBoardActionMsg{verb: verb, id: id, err: err}
		}
	case workRetry:
		run = func() tea.Msg {
			_, err := workRequest(base, token, "POST", "/v1/items/"+url.PathEscape(id)+"/retry", nil)
			return workBoardActionMsg{verb: verb, id: id, err: err}
		}
	case workRemove:
		run = func() tea.Msg {
			_, err := workRequest(base, token, "DELETE", "/v1/items/"+url.PathEscape(id), nil)
			return workBoardActionMsg{verb: verb, id: id, err: err}
		}
	}
	return tea.Batch(run, workBoardSpinCmd())
}

// beginAdd sends the form's item to the API's own add path. The form
// validates nothing about the item's shape — the API is the only judge,
// and the form is still open to receive its answer — but the priority the
// form assembled must still parse, since the form guards that field.
func (m *workBoardModel) beginAdd(body workAddBody) tea.Cmd {
	if m.action.verb != "" {
		m.statusLine = "still " + string(m.action.verb) + "ing " + m.action.id
		return nil
	}
	base, token := m.base, m.token
	m.action = workBoardAction{verb: workAdd, id: body.ID, since: workBoardNow()}
	run := func() tea.Msg {
		_, err := workRequest(base, token, "POST", "/v1/items", body)
		return workBoardActionMsg{verb: workAdd, id: body.ID, err: err}
	}
	return tea.Batch(run, workBoardSpinCmd())
}

// workBoardActionLine is the status line's account of a finished action:
// the API's own words for a refusal, unchanged — the refusal reads the
// way the API states it — and the plain fact for a success.
func workBoardActionLine(msg workBoardActionMsg) string {
	if msg.err != nil {
		return msg.err.Error()
	}
	switch msg.verb {
	case workAbort:
		return fmt.Sprintf("item %q stopped: it is back in the backlog", msg.id)
	case workRetry:
		return fmt.Sprintf("item %q is back in the backlog", msg.id)
	case workRemove:
		return fmt.Sprintf("item %q removed", msg.id)
	}
	return fmt.Sprintf("item %q added", msg.id)
}

// openDetail freezes the cursor onto the item in view, takes a copy of it
// (the reading behind may move on), and starts its tail.
func (m *workBoardModel) openDetail(v orchestrator.ItemView) tea.Cmd {
	m.detail = true
	m.detailItem = v
	m.detailGen++
	m.detailBusy = false
	m.detailSeen = ""
	m.detailLog = ""
	m.detailNote = ""
	return tea.Batch(workBoardTailTickCmd(), m.startTailRound())
}

// startTailRound polls the item in view's kept output once. The API
// answers with the whole kept log, so the round fetches it and applyTail
// appends only what is new — the same trick `work logs -f` plays.
func (m *workBoardModel) startTailRound() tea.Cmd {
	if m.detailBusy || m.detailItem.ID == "" {
		return nil
	}
	if v := m.itemByID(m.detailItem.ID); v != nil && v.State != orchestrator.StateBacklog && v.State != orchestrator.StateRunning {
		return nil // the item ended: whatever the last round brought is the whole log
	}
	m.detailBusy = true
	gen := m.detailGen
	base, token, id := m.base, m.token, m.detailItem.ID
	return func() tea.Msg {
		log, err := workLogFetch(base, token, id)
		if err != nil {
			return workBoardTailMsg{gen: gen, gone: workItemGone(err), err: err}
		}
		return workBoardTailMsg{gen: gen, log: log}
	}
}

// applyTail folds one poll into the pane: the suffix beyond what was last
// seen is appended, the pane keeps only what it can show, and a 404 — the
// item gone from the list, its kept output with it — ends the tail as
// cleanly as a follow ends.
func (m *workBoardModel) applyTail(msg workBoardTailMsg) {
	if msg.gone {
		m.detailNote = "the item is no longer in the list"
		return
	}
	if msg.err != nil {
		if m.detailLog == "" {
			m.detailNote = msg.err.Error()
		}
		return
	}
	added := msg.log
	if strings.HasPrefix(msg.log, m.detailSeen) {
		added = msg.log[len(m.detailSeen):]
	}
	m.detailSeen = msg.log
	if added != "" {
		m.detailLog = lastLines(m.detailLog+added, m.detailCapacity())
		m.detailNote = ""
	} else if m.detailLog == "" {
		m.detailNote = "no kept output yet"
	}
}

// itemByID finds an item in the latest reading by id.
func (m *workBoardModel) itemByID(id string) *orchestrator.ItemView {
	for i := range m.items {
		if m.items[i].ID == id {
			v := m.items[i]
			return &v
		}
	}
	return nil
}

// retainDetail keeps the view current with the reading: the item's own
// fields refresh where it is still listed; where it has left the list the
// view keeps what it had, and its tail ends on its own terms.
func (m *workBoardModel) retainDetail() {
	if !m.detail {
		return
	}
	if v := m.itemByID(m.detailItem.ID); v != nil {
		m.detailItem = *v
	}
}

// clamp re-fits the cursor and every column's window to the reading and
// the frame — after each read (cards may have moved or gone) and each
// resize (the window is smaller or roomier than it was).
func (m *workBoardModel) clamp() {
	cols := m.columnIndexes()
	c, r := m.cursor[0], m.cursor[1]
	if c >= len(cols) || (len(cols[c]) == 0 && m.anyCards(cols)) {
		// The cursor's column emptied while another has cards: step to the
		// nearest one that has, the way the arrows skip empties.
		if next := m.nearestColumn(c); next >= 0 {
			c = next
		}
	}
	if r >= len(cols[c]) {
		r = len(cols[c]) - 1
	}
	if r < 0 {
		r = 0
	}
	m.cursor = [2]int{c, r}
	for i := range m.scrolls {
		m.scrolls[i] = workBoardClampScroll(m.scrolls[i], len(cols[i]), m.visibleCards())
	}
	m.keepVisible()
}

func (m *workBoardModel) anyCards(cols [4][]int) bool {
	for _, col := range cols {
		if len(col) > 0 {
			return true
		}
	}
	return false
}

// nearestColumn is the closest column holding cards, searched outwards
// from where the cursor stands; -1 when every column is empty.
func (m *workBoardModel) nearestColumn(from int) int {
	for d := 1; d < len(workBoardColumns); d++ {
		if from-d >= 0 && len(m.columnIndexes()[from-d]) > 0 {
			return from - d
		}
		if from+d < len(workBoardColumns) && len(m.columnIndexes()[from+d]) > 0 {
			return from + d
		}
	}
	return -1
}

// moveRow steps within the cursor's column; the window follows.
func (m *workBoardModel) moveRow(delta int) {
	cols := m.columnIndexes()
	c := m.cursor[0]
	r := m.cursor[1] + delta
	if r < 0 {
		r = 0
	}
	if r >= len(cols[c]) {
		r = len(cols[c]) - 1
	}
	if r < 0 {
		r = 0
	}
	m.cursor = [2]int{c, r}
	m.keepVisible()
}

// moveColumn steps to the next column that holds cards, skipping the ones
// that do not; a column with nothing in it is nowhere to select.
func (m *workBoardModel) moveColumn(delta int) {
	cols := m.columnIndexes()
	c := m.cursor[0] + delta
	for c >= 0 && c < len(workBoardColumns) && len(cols[c]) == 0 {
		c += delta
	}
	if c < 0 || c >= len(workBoardColumns) {
		return
	}
	m.cursor = [2]int{c, 0}
	m.keepVisible()
}

// keepVisible scrolls the cursor's column until its card is on screen.
func (m *workBoardModel) keepVisible() {
	cols := m.columnIndexes()
	c, r := m.cursor[0], m.cursor[1]
	avail := m.visibleCards()
	if r < m.scrolls[c] || r >= m.scrolls[c]+avail {
		m.scrolls[c] = r
	}
	m.scrolls[c] = workBoardClampScroll(m.scrolls[c], len(cols[c]), avail)
}

// workBoardClampScroll bounds a column's window so it never scrolls past
// the cards it has.
func workBoardClampScroll(top, cards, avail int) int {
	limit := cards - avail
	if limit < 0 {
		limit = 0
	}
	if top > limit {
		return limit
	}
	if top < 0 {
		return 0
	}
	return top
}

func (m workBoardModel) effWidth() int {
	if m.width < 1 {
		return 80
	}
	return m.width
}

func (m workBoardModel) effHeight() int {
	if m.height < 1 {
		return 24
	}
	return m.height
}
