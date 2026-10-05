// The colours and the spinner every surface of the CLI draws from — the
// dashboard's tiles and title bar, `fleet deploy`'s progress lines, the
// resource bars `fleet metrics` and `cloud metrics` print. They live together
// so a second surface finds them rather than writing its own copy, which is
// what left the same ten spinner frames declared twice in this package.
//
// The two groups here are read differently and must not be swapped for one
// another. A state colour answers "how is this going" and belongs to whatever
// it is reporting; the brand colours answer "which tool is this" and belong to
// the tool's own chrome. See `openspec/specs/cli-ux/spec.md`.

package main

import "time"

// The brand colours, taken from the spinloop logo: a near-black ground, a mint
// accent. They carry the same values as the site's `--accent`, `--ink` and
// `--ink-2` tokens, so the CLI and the site are recognisably one product.
//
// The accent says nothing about a node: the board's title bar and the border
// of the selected panel are drawn from it, and nothing else is.
//
// The inks are written as hex and downsampled by lipgloss to whatever the
// terminal can show. The surface is a 256-colour index instead, because a
// tile's header bar is raw ANSI — the tile body is one plain string under a
// single lipgloss style — while the board's title bar takes the same surface
// through lipgloss, so the two are written from one value.
const (
	brandAccent = "#1DE2AD"
	brandInk    = "#EEF3F2"
	brandInkDim = "#9AA7AC"
	barSurface  = "235"
)

// The state colours, as raw ANSI. They report what an engine or a call is
// doing — a resource bar's fill, a node's health mark, a deploy's outcome —
// and are the terminal's own green, amber and red rather than anything of the
// brand's. renderBar and the dashboard's health glyph both draw from these.
const (
	ansiGreen  = "\033[92m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiGrey   = "\033[90m"
	ansiReset  = "\033[0m"
)

// spinnerFrames is the braille cycle drawn beside work still in flight, and
// spinnerStep how long each frame holds. One cycle for the whole tool, so a
// node deploying under `fleet deploy` and a node starting on the dashboard
// show the same thing.
var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

const spinnerStep = 125 * time.Millisecond

// spinnerFrame is the frame showing at time now. It is a function of the time
// alone, so a surface that redraws simply asks again rather than holding a
// counter of its own.
func spinnerFrame(now time.Time) string {
	n := int64(len(spinnerFrames))
	i := (now.UnixNano()/int64(spinnerStep))%n + n
	return string(spinnerFrames[i%n])
}
