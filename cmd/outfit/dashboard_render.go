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
	"strings"

	"github.com/charmbracelet/lipgloss"
	ansi "github.com/charmbracelet/x/ansi"
	"github.com/lucinate-ai/outfit/internal/fleet"
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

// dashTileContent is one panel's inside: the node's name and the facts the
// bar format prints for it. The shapes are the state of the node's business
// — a start or stop in flight (the call's own status lines, which replace
// the last report, because while a call is working that is the truth), not
// answered yet (an empty panel naming the node), answered and working (the
// full bar block), or answered and not working (outcome plus reason, which
// is what every one-shot surface shows).
func dashTileContent(name string, r fleet.NodeResult, a dashAction) string {
	var b strings.Builder
	switch {
	case a.verb != "":
		fmt.Fprintf(&b, "%s  %s\n", name, dashVerbProgress(a.verb))
		if a.line != "" {
			fmt.Fprintln(&b, a.line)
		}
	case r.Outcome == "":
		fmt.Fprintf(&b, "%s\nwaiting for first refresh…\n", name)
	case !r.OK():
		fmt.Fprintf(&b, "%s  %s\n", name, r.Outcome)
		if d := r.Detail(); d != "" {
			fmt.Fprintln(&b, d)
		}
	default:
		fmt.Fprintf(&b, "%s  %s\n", name, r.Metrics.State)
		serving := r.Metrics.Runner
		if r.Metrics.ModelID != "" {
			if serving != "" {
				serving += "  "
			}
			serving += r.Metrics.ModelID
		}
		if r.Metrics.UptimeSeconds > 0 {
			if serving != "" {
				serving += "  "
			}
			serving += "(up " + formatDuration(r.Metrics.UptimeSeconds) + ")"
		}
		if serving != "" {
			fmt.Fprintln(&b, serving)
		}
		renderLastActiveIndented(&b, r.Metrics.LastActiveAt, r.Metrics.IdleSeconds)
		if r.Metrics.State == "running" {
			renderStatBars(&b, r.Metrics.CPU, r.Metrics.Memory, r.Metrics.GPUs)
			renderTokenLines(&b, r.Metrics.Tokens)
		}
	}
	lines := strings.Split(b.String(), "\n")
	lines = lines[:len(lines)-1] // the trailing newline splits an extra empty piece
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

// dashTile frames one panel; the selected one carries a lit border.
func dashTile(name string, r fleet.NodeResult, selected bool, a dashAction) string {
	style := lipgloss.NewStyle().
		Width(dashTileW).Height(dashTileH).
		Border(lipgloss.RoundedBorder())
	if selected {
		style = style.BorderForeground(lipgloss.Color("214"))
	} else {
		style = style.BorderForeground(lipgloss.Color("240"))
	}
	return style.Render(dashTileContent(name, r, a))
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
