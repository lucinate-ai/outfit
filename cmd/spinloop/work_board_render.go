// The `work board` renderers: the columns and their cards, the item's
// detail, and the add form. The contract is the fleet dashboard's: every
// line is clipped (never wrapped) and every block is pre-sized before
// lipgloss frames it, so the grid stays rectangular at any terminal size.
// The colours come from palette.go in the two groups that must not be
// swapped — the state words and marks wear the state colours `work list`
// uses, and the brand accent appears only on the tool's own chrome and on
// the thing the operator has selected.

package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/spinloop-ai/spinloop/internal/orchestrator"
)

// Card geometry: three content lines — id, instructions, meta — inside a
// rounded frame, plus one gap line between cards. Column headers, the
// title bar and the footer are the frame's fixed rows.
const (
	workBoardCardH    = 5
	workBoardCardStep = workBoardCardH + 1
	workBoardMinColW  = 16
	workBoardFixedH   = 3 // title bar + column header row + footer
)

// ansiFaint stands the board back behind the add form: the surface behind
// the modal keeps drawing, dimmed, because the board keeps living.
const ansiFaint = "\033[2m"

// View draws the frame: the title bar, the columns, the footer — with the
// add form standing over them while it is open.
func (m workBoardModel) View() string {
	if m.detail {
		return m.detailView()
	}
	w := m.effWidth()
	now := workBoardNow()
	cols := m.columnIndexes()
	widths := workBoardColWidths(w, m.narrow())

	parts := []string{dashTitleBar("work board", m.titleDetail(now), w)}
	colsH := 1 + m.visibleCards()*workBoardCardStep
	narrow := m.narrow()
	var blocks [][]string
	var drawn []int
	for c := range workBoardColumns {
		if narrow && c != m.cursor[0] {
			continue
		}
		blocks = append(blocks, m.columnBlock(c, cols[c], widths, colsH, now))
		drawn = append(drawn, c)
	}
	parts = append(parts, workBoardJoinRows(blocks, pickWidths(widths, drawn, w, narrow)))
	parts = append(parts, m.footerLine(w, m.boardKeys()))
	view := strings.Join(parts, "\n")
	if m.formOpen {
		view = m.formOverlay(view)
	}
	return view
}

// titleDetail is the title bar's right half: where the board is reading,
// how much it holds, and — once the reading has aged past three
// cadences — how old it is, because a stale reading drawn identically to
// a fresh one is not the run's present state.
func (m workBoardModel) titleDetail(now time.Time) string {
	detail := fmt.Sprintf("%s   %d items", m.base, len(m.items))
	if age := m.readingAge(now); age != "" {
		return detail + "   reading " + age
	}
	return detail
}

// readingAge is the "3m ago" the title bar carries once the reading is
// stale, or "" while it is current.
func (m workBoardModel) readingAge(now time.Time) string {
	if m.readingAt.IsZero() {
		return ""
	}
	age := now.Sub(m.readingAt)
	if age < workBoardStaleAfter() {
		return ""
	}
	return formatDuration(int(age.Seconds())) + " ago"
}

// narrow is the one-column-per-screen mode: below the point where four
// columns are legible, the cursor's column stands alone at full width
// rather than four unreadable strips.
func (m workBoardModel) narrow() bool {
	return (m.effWidth()-3)/4 < workBoardMinColW
}

// workBoardColWidths splits the body across the columns, spending every
// column of width — the remainder from the division goes to the leading
// columns one at a time.
func workBoardColWidths(w int, narrow bool) []int {
	if narrow {
		return []int{w}
	}
	n := len(workBoardColumns)
	base, extra := (w-(n-1))/n, (w-(n-1))%n
	widths := make([]int, n)
	for i := range widths {
		widths[i] = base
		if i < extra {
			widths[i]++
		}
	}
	return widths
}

// visibleCards is how many cards a column shows between the column
// headers and the footer.
func (m workBoardModel) visibleCards() int {
	r := (m.effHeight() - workBoardFixedH) / workBoardCardStep
	if r < 1 {
		return 1
	}
	return r
}

// columnBlock is one column: its header, then the window of cards the
// cursor is looking at — always the selection's neighbourhood, which is
// how a fuller column than the screen drops cards nowhere silently.
func (m workBoardModel) columnBlock(c int, idx []int, widths []int, height int, now time.Time) []string {
	w := widths[len(widths)-1] // the full width in narrow mode, else this column's own
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(brandInkDim))
	marker := "  "
	if m.cursor[0] == c {
		marker = lipgloss.NewStyle().Foreground(lipgloss.Color(brandAccent)).Render("▸ ")
	}
	count := dim.Render(fmt.Sprintf("%d", len(idx)))
	header := dashClip(marker+workBoardColumns[c].title+" "+count, w)

	lines := []string{header}
	avail := (height - 1) / workBoardCardStep
	top := m.scrolls[c]
	for slot := 0; slot < avail; slot++ {
		i := top + slot
		if i >= len(idx) {
			break
		}
		// The card is a block: its lines join the column line by line,
		// exactly as the dashboard's grid joins its tiles — appending the
		// block as one entry would drop its newlines into a single row.
		lines = append(lines, strings.Split(m.card(m.items[idx[i]], idx[i] == m.selectedIndex(), w, now), "\n")...)
		lines = append(lines, "")
	}
	if len(idx) == 0 {
		lines = append(lines, dim.Render("  —"))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = dashClip(lines[i], w)
	}
	return lines
}

// selectedIndex is the flat item index under the cursor, or -1.
func (m workBoardModel) selectedIndex() int {
	cols := m.columnIndexes()
	c, r := m.cursor[0], m.cursor[1]
	if r < 0 || r >= len(cols[c]) {
		return -1
	}
	return cols[c][r]
}

// card is one item: its id (and priority, where it has one) on the bar,
// its instructions clipped to the card, and a meta line stating its
// state in the colour that reports it, beside what that state means —
// the node and elapsed time of a running item, the end of an ended one.
func (m workBoardModel) card(v orchestrator.ItemView, selected bool, w int, now time.Time) string {
	inner := w - 2
	bar := dashClip(workBoardCardBar(v, inner), inner)
	body := dashClip(workBoardWrapOne(v.Instructions, inner), inner)
	meta := dashClip(workBoardCardMeta(v, now, inner), inner)

	style := lipgloss.NewStyle().
		Width(inner).Height(3).
		Border(lipgloss.RoundedBorder())
	if selected {
		style = style.BorderForeground(lipgloss.Color(brandAccent))
	} else {
		style = style.BorderForeground(lipgloss.Color("240"))
	}
	return style.Render(bar + "\n" + body + "\n" + meta)
}

// workBoardCardBar is a card's header line: the state mark, the id, and
// the priority where the item carries one.
func workBoardCardBar(v orchestrator.ItemView, inner int) string {
	line := workBoardStateMark(v.State) + " " + v.ID
	if v.Priority != 0 {
		line += fmt.Sprintf("  p%d", v.Priority)
	}
	return line
}

// workBoardStateMark is the card's coloured dot — the state read at a
// glance, in the same colours `work list` puts on the state word.
func workBoardStateMark(state string) string {
	return workBoardStateColour(state) + "●" + ansiReset
}

// workBoardStateColour is the raw-ANSI colour of a state, drawn from the
// very words `work list` colours with: amber is running, green done, red
// failed; backlog is everything not happening, so it is the faded grey.
// The brand accent is not in this switch and does not colour a card.
func workBoardStateColour(state string) string {
	switch state {
	case orchestrator.StateRunning:
		return ansiYellow
	case orchestrator.StateDone:
		return ansiGreen
	case orchestrator.StateFailed:
		return ansiRed
	default:
		return ansiGrey
	}
}

// workBoardCardMeta is a card's third line: the state word as the work
// list colours it, and beside it what the state carries — the node and
// the time a running item has been up, the end of an ended item.
func workBoardCardMeta(v orchestrator.ItemView, now time.Time, inner int) string {
	state := workListColouredState(v.State, true)
	switch v.State {
	case orchestrator.StateRunning:
		extra := v.Node
		if e := workBoardElapsed(v.StartedAt, now); e != "" {
			if extra != "" {
				extra += "  "
			}
			extra += e
		}
		return state + "  " + extra
	case orchestrator.StateDone, orchestrator.StateFailed:
		return state + "  ended " + workBoardClock(v.EndedAt)
	default:
		return state
	}
}

// workBoardElapsed is how long a running item has been up, counted when
// it is drawn — so it counts up as the operator watches, and never
// carries the time the reading was taken as the time it matters.
func workBoardElapsed(rfc3339 string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return ""
	}
	secs := int(now.Sub(t).Seconds())
	if secs < 0 {
		secs = 0
	}
	return formatDuration(secs)
}

// workBoardClock is an instant as HH:MM, or a dash where the record has
// none or none the board can read.
func workBoardClock(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return "-"
	}
	return t.Local().Format("15:04")
}

// workBoardWrapOne is one instructions line clipped to width; the card
// shows the beginning and the detail pane holds the rest.
func workBoardWrapOne(s string, w int) string {
	return dashClip(strings.ReplaceAll(s, "\n", " "), w)
}

// workBoardWrap breaks text into lines of at most w columns, on spaces,
// hard-cutting any single word longer than the line. It is the detail
// pane's wrap: full instructions, kept to the frame's width.
func workBoardWrap(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			for lipgloss.Width(word) > w {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, dashClip(word, w))
				word = ansiCutRest(word, w)
			}
			if line == "" {
				line = word
			} else if lipgloss.Width(line)+1+lipgloss.Width(word) <= w {
				line += " " + word
			} else {
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// ansiCutRest drops the first w display columns of a plain string, the
// remainder of a word that overflowed.
func ansiCutRest(s string, w int) string {
	// Plain text only (instructions arrive plain from the API), so
	// counting runes is counting columns.
	runes := []rune(s)
	if w >= len(runes) {
		return ""
	}
	return string(runes[w:])
}

// pickWidths is the width of each drawn column: the one full width when
// the board is narrow, otherwise the drawn columns' own slices of the
// four-way split.
func pickWidths(widths []int, drawn []int, w int, narrow bool) []int {
	out := make([]int, len(drawn))
	for i, c := range drawn {
		if narrow {
			out[i] = w
		} else {
			out[i] = widths[c]
		}
	}
	return out
}

// workBoardJoinRows lays the column blocks side by side, one display line
// at a time — the grid's join, which keeps every row the frame's width
// however uneven the columns' contents are.
func workBoardJoinRows(blocks [][]string, widths []int) string {
	lines := make([]string, 0, workBoardCardStep*16)
	n := 0
	for _, b := range blocks {
		if len(b) > n {
			n = len(b)
		}
	}
	for line := 0; line < n; line++ {
		cells := make([]string, len(blocks))
		for c, b := range blocks {
			cell := ""
			if line < len(b) {
				cell = b[line]
			}
			cells[c] = padTo(cell, widths[c])
		}
		lines = append(lines, strings.TrimRight(strings.Join(cells, " "), " "))
	}
	return strings.Join(lines, "\n")
}

// padTo pads a clipped line to exactly w display columns.
func padTo(line string, w int) string {
	if n := lipgloss.Width(line); n < w {
		return line + strings.Repeat(" ", w-n)
	}
	return line
}

// footerLine is the board's bottom line: the keys that would do something
// where the cursor stands, replaced by the removal or retry question while one is
// pending and by an in-flight action's progress while a call is out; the
// status line rides at the end.
func (m workBoardModel) footerLine(w int, keys string) string {
	line := dashKeyHints(keys)
	if m.action.verb != "" {
		line = m.action.progress(workBoardNow())
	}
	if m.confirm != "" {
		v := m.selectedItem()
		id := ""
		if v != nil {
			id = fmt.Sprintf(" %q", v.ID)
		}
		line = string(m.confirm) + " item" + id + "?" + dashHintGap +
			dashKeyHints("y yes"+dashHintGap+"n no")
	}
	if m.statusLine != "" {
		line += "   " + m.statusLine
	}
	if m.busy {
		line += "   reading…"
	}
	return dashClip(line, w)
}

// boardKeys names the keys that would do something for what is selected:
// abort is named only on a running card, retry only on a failed one, remove
// only on one that is not running, detail only where there is an item to open.
func (m workBoardModel) boardKeys() string {
	parts := []string{}
	cols := m.columnIndexes()
	if m.anyCards(cols) {
		parts = append(parts, "↑↓←→ select")
	}
	v := m.selectedItem()
	if v != nil {
		parts = append(parts, "enter detail")
		if v.State == orchestrator.StateRunning {
			parts = append(parts, "a abort")
		} else {
			if v.State == orchestrator.StateFailed {
				parts = append(parts, "t retry")
			}
			parts = append(parts, "x remove")
		}
	}
	parts = append(parts, "n add", "r refresh", "q quit")
	return strings.Join(parts, dashHintGap)
}

// detailView is the full-screen item: the fields the reading carries, the
// instructions whole, and the kept log tailed into what remains.
// detailLayout is the detail's one arithmetic: the fields that fit the
// frame, and the log rows the rest of the frame can show. The view and
// the tail both ask here, so the buffer never holds lines the pane
// could not draw.
func (m workBoardModel) detailLayout() (fields []string, logAvail int) {
	fields = workBoardFitFields(m.detailFields(), m.effHeight())
	logAvail = m.effHeight() - 5 - len(fields)
	if logAvail < 1 {
		logAvail = 1
	}
	return fields, logAvail
}

func (m workBoardModel) detailView() string {
	w := m.effWidth()
	v := m.detailItem
	fields, logAvail := m.detailLayout()

	state := workListColouredState(v.State, true)
	header := dashTitleBar("work board  ·  "+v.ID, state, w)
	divider := strings.Repeat("─", w)

	logLines := strings.Split(strings.TrimRight(m.detailLog, "\n"), "\n")
	if m.detailLog == "" {
		note := m.detailNote
		if note == "" {
			note = "waiting for the log…"
		}
		logLines = []string{note}
	}
	if len(logLines) > logAvail {
		logLines = logLines[len(logLines)-logAvail:]
	}

	parts := make([]string, 0, len(fields)+len(logLines)+4)
	parts = append(parts, header, divider)
	for _, line := range fields {
		parts = append(parts, dashClip(line, w))
	}
	parts = append(parts, divider)
	for _, line := range logLines {
		parts = append(parts, dashClip(line, w))
	}
	parts = append(parts, divider)
	parts = append(parts, m.footerLine(w, "esc back"))
	return strings.Join(parts, "\n")
}

// detailFields is the detail's whole item: every field the record
// carries, then the instructions unwrapped to the frame's width. The
// labels are the dim ink, the values the terminal's own.
func (m workBoardModel) detailFields() []string {
	v := m.detailItem
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(brandInkDim))
	label := func(s string) string { return dim.Render(s) }
	var out []string
	add := func(labelStr, value string) {
		if value == "" {
			return
		}
		out = append(out, label(labelStr+"  ")+value)
	}
	add("dir", v.Dir)
	add("tags", strings.Join(v.Tags, "  "))
	add("node", v.Node)
	add("started", workBoardClock(v.StartedAt))
	add("ended", workBoardClock(v.EndedAt))
	if v.State == orchestrator.StateFailed && v.Why != "" {
		// The reason has something to say, unlike the one-line fields:
		// the label carries its first line and the rest wraps beneath
		// the label's room, its red kept on every row.
		const indent = "     " // as wide as "why  ", the label's room
		w := m.effWidth() - len(indent)
		for i, line := range workBoardWrap(v.Why, w) {
			if i == 0 {
				out = append(out, label("why  ")+ansiRed+line+ansiReset)
			} else {
				out = append(out, indent+ansiRed+line+ansiReset)
			}
		}
	}
	if m.readingAge(workBoardNow()) != "" {
		add("reading", dim.Render(m.readingAge(workBoardNow())))
	}
	out = append(out, label("instructions"))
	out = append(out, workBoardWrap(v.Instructions, m.effWidth())...)
	return out
}

// workBoardFitFields keeps the field section inside the frame. The log
// pane already trims itself to what it can show; the fields owe the
// terminal the same honesty — what fits is drawn, and a dim note counts
// the rows the frame left behind rather than letting them pile past the
// bottom edge unseen.
func workBoardFitFields(fields []string, h int) []string {
	room := h - 6 // header, three dividers, a line of log, footer
	if room < 1 {
		room = 1
	}
	if len(fields) <= room {
		return fields
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(brandInkDim))
	note := dim.Render("⋯ +" + fmt.Sprintf("%d", len(fields)-(room-1)) + " lines")
	return append(append([]string{}, fields[:room-1]...), note)
}

// detailCapacity is how many log lines the pane can show — the same
// figure the tail trims its buffer to, so the buffer never holds what
// the view could never draw.
func (m workBoardModel) detailCapacity() int {
	_, logAvail := m.detailLayout()
	return logAvail
}

// formFieldWidth is the width each textinput is given inside the form:
// the modal less its frame, the field marker, and the label column.
func (m workBoardModel) formFieldWidth() int {
	w := m.formWidth() - 2 - 2 - workFormPromptW - 2
	if w < 10 {
		return 10
	}
	return w
}

func (m workBoardModel) formWidth() int {
	w := m.effWidth() - 4
	if w > 64 {
		return 64
	}
	if w < 30 {
		return m.effWidth()
	}
	return w
}

// formOverlay draws the add form over the board: the board's lines stand
// behind faint — still drawn, still live, stepped back — and the modal
// box sits centred over them.
func (m workBoardModel) formOverlay(view string) string {
	w := m.effWidth()
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = ansiFaint + line + ansiReset
		}
	}
	box := m.formBox()
	boxLines := strings.Split(box, "\n")
	pad := (m.effHeight() - len(boxLines)) / 2
	if pad < 0 {
		pad = 0
	}
	left := (w - lipgloss.Width(boxLines[0])) / 2
	if left < 0 {
		left = 0
	}
	for i, bline := range boxLines {
		row := pad + i
		if row >= len(lines) {
			break
		}
		lines[row] = strings.Repeat(" ", left) + bline
	}
	return strings.Join(lines, "\n")
}

// formBox is the modal: the five fields with their labels, the active one
// marked and its caret standing in it, the API's refusal of the last send
// where there is one, and the keys the form answers to.
func (m workBoardModel) formBox() string {
	inner := m.formWidth() - 2
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(brandInkDim))
	bold := lipgloss.NewStyle().Bold(true)

	var b strings.Builder
	b.WriteString(dashClip(" new work item"+strings.Repeat(" ", max(0, inner-15)), inner))
	b.WriteByte('\n')
	for i, label := range workFormLabels {
		marker := "  "
		labelStyle := dim
		if m.form.cursor == i {
			marker = lipgloss.NewStyle().Foreground(lipgloss.Color(brandAccent)).Render("❯ ")
			labelStyle = lipgloss.NewStyle()
		}
		cell := marker + labelStyle.Render(fmt.Sprintf("%-*s", workFormPromptW, label))
		cell += m.form.fields[i].View()
		b.WriteString(dashClip(cell, inner))
		b.WriteByte('\n')
	}
	if m.form.err != "" {
		b.WriteString(dashClip(ansiRed+m.form.err+ansiReset, inner))
		b.WriteByte('\n')
	}
	if m.formAsk {
		b.WriteString(dashClip(bold.Render("discard this item?")+" everything typed is kept until you say yes"+
			dashHintGap+dashKeyHints("y discard"+dashHintGap+"n keep"), inner))
	} else {
		b.WriteString(dashClip(dashKeyHints("enter next/send"+dashHintGap+"up/down field"+dashHintGap+
			"esc cancel"+dashHintGap+"* required"), inner))
	}
	style := lipgloss.NewStyle().
		Width(inner).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(brandAccent))
	return style.Render(b.String())
}
