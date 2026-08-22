## Why

`outfit fleet metrics --watch` is a live-redraw dashboard, not a TUI: read-only, no
selection, no actions. To act on what you are looking at you drop out of it and run
`outfit fleet start <node>`. That is backwards for the case that matters most: the
dashboard is exactly where you would want to bring a fleet up from, and it is the one
place you currently cannot — it shows three dead nodes and offers nothing to do about
them (lucinate-ai/outfit#59).

## What Changes

- New `outfit fleet dashboard` subcommand: a genuine interactive TUI over the fleet.
  The view is a tiled metrics grid — one panel per node, showing the same bar-format
  metrics `fleet metrics` renders — refreshed continuously.
- Selection (j/k and the arrow keys) and keyboard-driven start/stop of the selected
  node, through the same node operations the one-shot commands use. Start proceeds
  without confirmation; stop requires an explicit confirmation. Pause is deferred to
  a follow-up (lucinate-ai/outfit#119).
- A status line for last-action results, manual refresh, and clean exit on q /
  Ctrl+C with the terminal restored on every exit path.
- The dashboard is usable from cold: it opens on a fleet where nothing is reachable,
  shows each node's outcome, and starts nodes from within.
- New runtime dependencies: `charmbracelet/bubbletea` and `lipgloss` —
  outfit's first UI framework — plus `charmbracelet/x/ansi` (ANSI-aware line
  clipping) and `golang.org/x/term` (the TTY check). `charmbracelet/bubbles` is
  not needed: overflow scrolling is row math the model owns. Cost on the
  shipped binary, measured on the final dependency set: +383 KB stripped /
  +107 KB gzipped (≈1.8% of the ~6 MB download).
- `fleet metrics --watch` and every other fleet surface stay unchanged — the watch
  mode remains the simple non-interactive redraw for piped and background use.

## Capabilities

### New Capabilities
<!-- None. The dashboard is a new surface of the existing fleet command family,
     which is what `fleet-client` covers. -->

### Modified Capabilities
- `fleet-client`: gains the `outfit fleet dashboard` requirements — an interactive
  tiled view of the fleet with selection, start/stop on the selected node,
  continuous non-stalling refresh, and clean exit, degrading per node exactly as the
  rest of the fleet commands do. No existing requirement changes.

## Impact

- `cmd/outfit`: new files for the command, the tea model, and the tile/frame
  renderers (which reuse the shared metrics render helpers); the `fleet` parent's
  fallback usage line gains `dashboard`; the subcommand registers the same
  completion surface as its siblings.
- `internal/fleet`: unchanged. The dashboard builds its node set once through the
  shared `Config.NewNode` constructor, then drives the existing explicit
  `FanOutNodes` per refresh with the existing metrics call, and drives
  `Node.Start`/`Node.Stop` for actions. No new endpoints, no contract change.
- `go.mod`: bubbletea, lipgloss, x/ansi and their transitive dependencies
  (termenv, uniseg, cancelreader, terminfo; `x/term` and `x/sys` for the TTY
  check). No AWS, no cgo.
- The daemon, the remote control plane, the `Node` contract, and every existing
  command's output are untouched.
- Out of scope, deferred: pause (#119), per-node log tailing, deploy-config push
  from the UI, multi-node actions, and the web UI (#50).
