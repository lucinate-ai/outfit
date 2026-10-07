// The `fleet dashboard` renderers: tiles, the grid, and the clipping that
// keeps a panel the shape of the frame. A panel draws the very lines the bar
// format of `fleet metrics` prints for the same node — renderStatBars,
// renderTokenLines and the last-active line are shared, not reimplemented —
// so the two surfaces cannot word a number differently.
//
// Lipgloss frames the panel; everything it receives is already exactly
// dashTileW wide and dashTileH tall, so the frame never has to decide how to
// fit anything — it only pads and borders. That is what keeps the grid
// rectangular on every terminal size.

package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	ansi "github.com/charmbracelet/x/ansi"
	"github.com/spinloop-ai/spinloop/internal/fleet"
	"github.com/spinloop-ai/spinloop/internal/metrics"
)

// Tile geometry. Content is dashTileW by dashTileH; the frame adds one
// character per side, and neighbours sit one space apart. The bar line is the
// width constraint: 41 columns, so 42 fits it whole; a running node with one
// GPU fills all 12 lines exactly.
const (
	dashTileW    = 42
	dashTileH    = 12
	dashTileStep = dashTileW + 2 + 1 // frame plus the gap to the next tile
	dashTileRowH = dashTileH + 2 + 1
)

// The frame's fixed rows above and below the grid.
const (
	dashHeaderH = 1
	dashFooterH = 1
)

// dashCols is how many tiles fit across at a given width.
func dashCols(w int) int {
	c := (w + 1) / dashTileStep
	if c < 1 {
		return 1
	}
	return c
}

// dashVisibleRows is how many tile rows sit between header and footer at a
// given height.
func dashVisibleRows(h int) int {
	r := (h - dashHeaderH - dashFooterH) / dashTileRowH
	if r < 1 {
		return 1
	}
	return r
}

// dashClampScroll bounds the first visible row so the grid never scrolls
// past its ends.
func dashClampScroll(scrollRow, entries, w, h int) int {
	cols := dashCols(w)
	rows := (entries + cols - 1) / cols
	if rows < 1 {
		rows = 1
	}
	top := rows - dashVisibleRows(h)
	if top < 0 {
		top = 0
	}
	if scrollRow < 0 {
		return 0
	}
	if scrollRow > top {
		return top
	}
	return scrollRow
}

// dashClip hard-cuts a line at width display columns. It is a cut, not a wrap
// — wrapping would push a bar line onto two rows and break the block's
// alignment — and it is ANSI-aware (ansi.CutWc), so a colour code mid-line
// survives and a wide character counts as two.
func dashClip(line string, width int) string {
	if lipgloss.Width(line) <= width {
		return line
	}
	return ansi.CutWc(line, 0, width)
}

// dashHintGap separates one key-help entry from the next, and is what
// dashKeyHints splits a hint line on.
const dashHintGap = "   "

// dashKeyHints draws a key-help line: each entry is a key and what that key
// does, and entries are three spaces apart (dashHintGap). The key keeps the
// terminal's own text colour and what it does is drawn a step back in the
// muted ink, so the keys themselves are what a glance over the footer picks
// out. An entry that is one word — no key and its meaning — is left alone.
func dashKeyHints(hints string) string {
	if hints == "" {
		return ""
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(brandInkDim))
	parts := strings.Split(hints, dashHintGap)
	for i, part := range parts {
		key, does, ok := strings.Cut(part, " ")
		if !ok {
			continue
		}
		parts[i] = key + " " + dim.Render(does)
	}
	return strings.Join(parts, dashHintGap)
}

// dashHealthTier is a panel's health at a glance, distinct from the state
// and outcome text already on the tile — a colour a viewer reads without
// having to read the words.
type dashHealthTier int

const (
	dashHealthy dashHealthTier = iota
	dashAttention
	dashUnhealthy
	dashNotServing
	dashUnknown
)

// dashHealthGlyph is the coloured status mark the tile's header bar carries
// beside a node's name, in the same raw-ANSI style renderBar already uses for
// the resource bars inside the tile — the tile body is one plain string
// wrapped in a single lipgloss style at the border, so per-character colour
// here has to be ANSI, not lipgloss.Color. Not serving shares unknown's
// faded shade and keeps the dot: the two greys are told apart by their mark,
// a filled dot for a known undeployed node against the ? for one that has
// not answered yet.
func dashHealthGlyph(tier dashHealthTier) string {
	return dashHealthColour(tier) + dashHealthMark(tier) + ansiReset
}

// dashHealthColour and dashHealthMark are the glyph's two halves, kept apart
// because the header bar draws them either side of its own foreground colour
// and cannot use a mark that has already reset the background behind it.
func dashHealthColour(tier dashHealthTier) string {
	switch tier {
	case dashHealthy:
		return ansiGreen
	case dashAttention:
		return ansiYellow
	case dashNotServing, dashUnknown:
		return ansiGrey
	default:
		return ansiRed
	}
}

func dashHealthMark(tier dashHealthTier) string {
	if tier == dashUnknown {
		return "?"
	}
	return "●"
}

// The tile header bar's own codes. The bar is one dark background across the
// tile's full width with light text on it, so a node's name reads as the
// panel's title rather than as another line of its body; the health glyph
// keeps its own colour on top of it, which is what the tier is read from. The
// surface it sits on is the board's own (see palette.go).
const (
	dashHeaderBG     = "\033[48;5;" + barSurface + "m"
	dashHeaderFG     = "\033[97m"
	dashHeaderGlyphW = 2 // the glyph and the space after it
)

// dashBarStyle is text on a bar's surface, in one of the inks above.
func dashBarStyle(fg string) lipgloss.Style {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(barSurface)).
		Foreground(lipgloss.Color(fg))
}

// dashTitleBar is the board's own top line, on the same lifted surface a
// tile's header bar sits on: the product and the screen on the left, and what
// is on that screen — the fleet file, the node count, the log's state — on the
// right. The grid and the detail view both draw their header from this, so the
// two screens cannot title themselves differently.
//
// A terminal too narrow for both keeps the left half and drops the right,
// rather than wrapping the bar onto a second row.
func dashTitleBar(screen, detail string, w int) string {
	if w < 1 {
		return ""
	}
	// At least this much between the two halves, so they read as two groups
	// rather than as one run of text that happens to have a space in it.
	const minGap = 3
	const brand = " spinloop"
	left := brand + "  " + screen
	right := detail + " "
	pad := w - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < minGap {
		right, pad = "", w-lipgloss.Width(left)
	}
	bar := dashBarStyle(brandAccent).Bold(true).Render(brand)
	if pad < 0 {
		return dashClip(bar+dashBarStyle(brandInk).Render("  "+screen), w)
	}
	bar += dashBarStyle(brandInk).Render("  " + screen + strings.Repeat(" ", pad))
	if right != "" {
		bar += dashBarStyle(brandInkDim).Render(right)
	}
	return bar
}

// dashTileHeader draws a tile's first line as that bar: the health glyph, the
// line's text, and background to the tile's full width. The text is clipped
// before the padding is measured, so the bar is exactly dashTileW columns
// whatever it carries.
func dashTileHeader(text string, tier dashHealthTier) string {
	body := dashClip(text, dashTileW-dashHeaderGlyphW)
	pad := dashTileW - dashHeaderGlyphW - lipgloss.Width(body)
	if pad < 0 {
		pad = 0
	}
	return dashHeaderBG + dashHealthColour(tier) + dashHealthMark(tier) +
		dashHeaderFG + " " + body + strings.Repeat(" ", pad) + ansiReset
}

// dashStaleThreshold is how many of a node's own intervals its newest reading
// may age before a panel draws it as out of date rather than as the node's
// current state. Three rather than one, so an ordinary late round does not
// flicker the board grey.
const dashStaleThreshold = 3

// dashStaleAfter is how old a reading of a node of this kind may be before the
// panel says so.
func dashStaleAfter(kind string) time.Duration {
	if kind == fleet.KindCloud {
		return dashStaleThreshold * dashboardCloudRefreshInterval
	}
	return dashStaleThreshold * dashboardRefreshInterval
}

// dashNodeView is everything a panel says about one node: the lines the bar
// format prints for it, with no width clipping or height padding, and the
// health tier its glyph is coloured from. The tile and the detail view's
// metrics section both draw from this, so the two can never word a number
// differently, and the lines and the tier are produced together, so the colour
// and the text it sits beside cannot disagree about what the node is doing.
//
// Every input is an argument — the reading, the action in flight, the time the
// panel is drawn at, and how old a reading of this node may be before it is
// out of date. No clock is read here, so the same arguments always produce the
// same panel, and every combination of an action's phase against a reading can
// be enumerated in a test.
//
// The shapes are the state of the node's business: a start or stop in flight
// (the action's own account beside the node's last report — the action says
// what the operator asked for, the report says what the node is doing, and a
// boot half done already carries a state and whatever it measures), not
// answered yet (an empty panel naming the node), answered and working (the
// full bar block), or answered and not working (outcome plus reason, which is
// what every one-shot surface shows). A reading older than staleAfter carries
// its age wherever it is drawn, and takes the panel to the unknown tier: a
// stale reading is not a wrong reading, but drawing it identically to a
// current one is.
func dashNodeView(name string, r fleet.NodeResult, a dashAction, now time.Time, staleAfter time.Duration, gauge bool, lineW int) ([]string, dashHealthTier) {
	age := dashReadingAge(r, now, staleAfter)
	var b strings.Builder
	switch {
	case a.verb != "":
		fmt.Fprintf(&b, "%s  %s\n", name, dashActionProgress(a, now))
		if a.phase != nil {
			fmt.Fprintln(&b, fleet.RenderPhase(*a.phase, now))
		}
		if r.OK() {
			if s := r.Metrics.State; s != "" {
				fmt.Fprintln(&b, dashStateLine(s, r.Metrics)+age)
			}
			dashTileReportBody(&b, r.Metrics, true, gauge, lineW, now)
		}
	case r.Outcome == "":
		fmt.Fprintf(&b, "%s\nwaiting for first refresh…\n", name)
	case !r.OK():
		fmt.Fprintf(&b, "%s  %s%s\n", name, r.Outcome, age)
		if d := r.Detail(); d != "" {
			fmt.Fprintln(&b, d)
		}
	default:
		fmt.Fprintf(&b, "%s  %s%s\n", name, dashStateLine(r.Metrics.State, r.Metrics), age)
		// In bar the series also come from the retained history, which
		// survives a stop: a stopped node with a window still has series to
		// draw, and its current reading simply carries no fallback for them.
		resources := r.Metrics.State == "running"
		if !gauge && len(r.Metrics.History) > 0 {
			resources = true
		}
		dashTileReportBody(&b, r.Metrics, resources, gauge, lineW, now)
	}
	lines := strings.Split(b.String(), "\n")
	lines = lines[:len(lines)-1] // the trailing newline splits an extra empty piece
	return lines, dashHealthTierFor(r, a, age != "")
}

// dashReadingAge is the "· 3m ago" a panel carries once its newest reading has
// aged past staleAfter, or "" while the reading is current. A reading with no
// time on it — one built outside the fan-out — is never called stale, since
// there is nothing to measure its age against.
func dashReadingAge(r fleet.NodeResult, now time.Time, staleAfter time.Duration) string {
	if r.At.IsZero() || staleAfter <= 0 {
		return ""
	}
	age := now.Sub(r.At)
	if age < staleAfter {
		return ""
	}
	return "  · " + formatDuration(int(age.Seconds())) + " ago"
}

// dashHealthTierFor derives a panel's health tier. Priority order matches
// dashNodeView's own shape switch, so the tier and the shape it is rendered
// into never disagree: an action in flight is always attention, regardless of
// the last completed refresh; then no refresh yet is unknown — there is no
// status to read, so the panel shows a grey "?"; then a reading that has aged
// past its cadence is unknown too, since it no longer describes the node now;
// then a crashed engine or a failed outcome is unhealthy; then a running
// engine the daemon has explicitly reported not ready is attention — the case
// this tier exists for, a cloud node whose process is up but still loading
// weights; then an answer that carries no state at all is unknown; then a node
// that answered with nothing serving is not serving, a faded dot rather than
// the green of a node that is up and serving — idle, the daemon with nothing
// started; stopped, a daemon engine that was stopped; undeployed, a cloud
// environment with no instance at all; anything else, including a running
// engine the daemon reports no readiness for at all (an older daemon, or a
// runner with no known health check), is healthy, so this degrades to the
// pre-readiness behaviour rather than showing a tier the daemon cannot
// actually back.
func dashHealthTierFor(r fleet.NodeResult, a dashAction, stale bool) dashHealthTier {
	switch {
	case a.verb != "":
		return dashAttention
	case r.Outcome == "":
		return dashUnknown
	case stale:
		return dashUnknown
	case !r.OK() || r.Metrics.State == "crashed":
		return dashUnhealthy
	case r.Metrics.State == "running" && r.Metrics.Ready == "not-ready":
		return dashAttention
	case r.Metrics.State == "":
		return dashUnknown
	case r.Metrics.State == "idle" || r.Metrics.State == "stopped" || r.Metrics.State == "undeployed":
		return dashNotServing
	default:
		return dashHealthy
	}
}

// dashTileContent is one tile's inside: dashNodeView's lines padded to the
// tile's fixed height and clipped to its fixed width, with the first line
// drawn as the header bar — tile-only, not part of dashNodeView, so the detail
// view (which draws the same lines full-screen) keeps a plain first line.
func dashTileContent(name string, r fleet.NodeResult, a dashAction, now time.Time, staleAfter time.Duration, gauge bool) string {
	lines, tier := dashNodeView(name, r, a, now, staleAfter, gauge, dashBarLineW)
	if len(lines) == 0 {
		lines = []string{""}
	}
	lines[0] = dashTileHeader(lines[0], tier)
	for len(lines) < dashTileH {
		lines = append(lines, "")
	}
	if len(lines) > dashTileH {
		lines = lines[:dashTileH]
	}
	for i, line := range lines {
		lines[i] = dashClip(line, dashTileW)
	}
	return strings.Join(lines, "\n")
}

// dashTileServingLine is a report's "what it serves" line: the runner and
// the model — or "" when the answer carries neither. Uptime rides on the
// tile's state line instead (dashStateLine), where a long runner or model ID
// cannot clip it off.
func dashTileServingLine(m metrics.Stats) string {
	line := m.Runner
	if m.ModelID != "" {
		if line != "" {
			line += "  "
		}
		line += m.ModelID
	}
	return line
}

// dashStateLine pairs a node's state with its uptime, when it has one, so
// uptime stays visible on the tile's state line rather than risking the
// clip on the (potentially long) serving line below it.
func dashStateLine(state string, m metrics.Stats) string {
	if m.UptimeSeconds <= 0 {
		return state
	}
	uptime := "(up " + formatDuration(m.UptimeSeconds) + ")"
	if state == "" {
		return uptime
	}
	return state + "  " + uptime
}

// dashTileReportBody appends what a node's last answer carries beneath its
// name line: the serving line, the last-active line, and — when resources is
// set — the resource bars and token counters. Each part prints only when the
// answer has it. A settled tile gates the resources block on the node being
// running; the in-flight tile draws whatever there is, because a boot half
// done has some of the facts and not the rest.
func dashTileReportBody(w io.Writer, m metrics.Stats, resources bool, gauge bool, lineW int, now time.Time) {
	if line := dashTileServingLine(m); line != "" {
		fmt.Fprintln(w, line)
	}
	// The active figure and, for a kept cloud environment, the relative keep
	// after it — one line, from the same read, whatever the engine's state. A
	// read without either — a local node, an unkept or lapsed environment —
	// draws nothing.
	renderActiveIndented(w, m.LastActiveAt, m.IdleSeconds, m.RetainUntil, now)
	if resources {
		if gauge {
			renderStatGauges(w, m.CPU, m.Memory, m.GPUs)
		} else {
			renderStatBars(w, m.CPU, m.Memory, m.GPUs, m.History, lineW)
		}
		renderTokenLines(w, m.Tokens)
	}
}

// dashTile frames one panel; the selected one carries a lit border.
func dashTile(name string, r fleet.NodeResult, selected bool, a dashAction, now time.Time, staleAfter time.Duration, gauge bool) string {
	style := lipgloss.NewStyle().
		Width(dashTileW).Height(dashTileH).
		Border(lipgloss.RoundedBorder())
	if selected {
		style = style.BorderForeground(lipgloss.Color(brandAccent))
	} else {
		style = style.BorderForeground(lipgloss.Color("240"))
	}
	return style.Render(dashTileContent(name, r, a, now, staleAfter, gauge))
}

// dashGridRows lays tiles out left to right, top to bottom, in fleet-file
// order — the same order every one-shot surface lists the nodes in. A grid
// row joins the corresponding lines of the tiles it places, a missing line
// as an empty cell. Joining the tiles themselves would do that once, between
// the blocks: each tile is a dashTileH+2-line block, so the second tile's
// top border would land beside the first tile's bottom border and its body
// would shift down a line.
func dashGridRows(tiles []string, cols int) []string {
	lines := make([][]string, len(tiles))
	for i, tile := range tiles {
		lines[i] = strings.Split(tile, "\n")
	}
	rows := make([]string, 0, (len(tiles)+cols-1)/cols)
	for i := 0; i < len(tiles); i += cols {
		end := i + cols
		if end > len(tiles) {
			end = len(tiles)
		}
		var body []string
		for line := 0; ; line++ {
			cells := make([]string, end-i)
			done := true
			for c := i; c < end; c++ {
				if line < len(lines[c]) {
					cells[c-i] = lines[c][line]
					done = false
				}
			}
			if done {
				break
			}
			body = append(body, strings.Join(cells, " "))
		}
		rows = append(rows, strings.Join(body, "\n"))
	}
	return rows
}
