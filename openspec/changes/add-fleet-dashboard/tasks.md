## 1. Dependencies and command skeleton

- [x] 1.1 Add `charmbracelet/bubbletea`, `charmbracelet/lipgloss`,
      `charmbracelet/x/ansi` (ANSI-aware line clipping) and `golang.org/x/term`
      (the TTY check) to `go.mod` and record the measured binary-size delta
      (no `bubbles`: the overflow grid scrolls by the model's own row math —
      see design.md). Measured on the final dependency set: +383 KB stripped
      / +107 KB gzipped (18,226 → 18,610 KB stripped, 6,117 → 6,224 KB
      gzipped, ≈1.8% of the ~6 MB download) — smaller than the proposal-time
      probe, which included `bubbles`
- [x] 1.2 Register the `fleet dashboard` subcommand in the tree: `--fleet` flag
      (shared `fleetFileUsage`), its `Long` text, the same completion
      registration as its siblings, and `dashboard` in the `fleet` parent's
      fallback usage string
- [x] 1.3 Fleet-file resolution and the pre-view guard: resolve the file as the
      other fleet commands do (a file problem fails before the view opens),
      build the node set once through `Config.NewNode` (a node that cannot be
      built is a standing `config-error` result, never in the live set), and
      refuse a non-terminal stdout with a message naming `fleet metrics --watch`
      before any raw mode is entered
- [x] 1.4 Wire the `tea.Program` (alternate screen) and the exit paths: quit key
      and interrupt exit without an error and with the terminal restored

## 2. The model

- [x] 2.1 The model's state: node set, per-node `NodeResult`s, selection
      (fleet-file order), the refresh generation, the status line, the
      stop-confirmation state, and the action-in-flight flag; the refresh and
      action intervals as variables the tests can pin
- [x] 2.2 The refresh: a `tea.Cmd` that runs `FanOutNodes(MetricsCall, nodes)`
      with a one-interval context deadline, returns the result tagged with its
      generation, and never starts while a round is in flight; the tick
      reschedules itself (one tick, one round — a one-shot `tea.Tick` without a
      reschedule leaves the board still after the second round); a stale or
      superseded result is discarded rather than painted
- [x] 2.3 Navigation: `j`/`k`/up/down move the selection in file order (no
      wrapping); selection change scrolls the grid so the tile stays visible
- [x] 2.4 Start on the selected node: sent without confirmation through
      `Node.Start` in an action command; the reply (the resulting state, or the
      daemon's refusal) lands in the status line; nothing is sent while the
      node's own action is in flight (see 2.9 for the per-node shape)
- [x] 2.5 Stop with explicit confirmation: the stop key opens the confirmation
      (footer prompt, all other keys ignored), decline or escape sends nothing,
      confirm sends `Node.Stop` on the selected node only, and the reply lands
      in the status line
- [x] 2.6 The manual refresh key (`r`): an immediate fleet-wide round outside
      the interval, subject to the same in-flight and stale rules
- [x] 2.7 Model tests: selection movement and clamping, the stop-confirm flow
      (declined, confirmed, escaped), start and stop dispatch to a fake
      `fleet.Node` (in-memory state), status-line outcomes for accepted and
      refused actions, and stale-generation discard
- [x] 2.8 Per-kind refresh cadence: local daemon machines refresh on the tick,
      `kind: remote` environments on a 60-second deadline the model spends when
      it starts their round (their status is a signed control-plane call, not a
      local socket), with separate in-flight flags and generations per group so
      a slow cloud round never withholds a local one; `r` is due for every node
      whatever the deadlines say. The kind comes from the fleet-file entry, not
      the `Node` contract. While here: the program now holds the model by
      pointer, because Bubble Tea never reads a value model's `Init` back and
      the first round's answers (real cloud calls, for remote environments)
       would otherwise be discarded as stale — see design.md
- [x] 2.9 Per-node actions, and a start's progress on the node's own tile: an
      action is one per node, not one per board — each node carries a
      `dashAction` (verb plus the call's latest line) beside its entry, a
      node already starting refuses a second start and a stop ask without
      blocking the other nodes, so two cloud wakes run side by side, and a
      finished action clears only its own node. `remoteNode` implements the
      optional `fleet.ProgressStarter` so the control plane's retry lines reach
      `remote.Start`'s progress callback, and the model feeds them back through
      the program's `Send` (a `send` field on the model — safe from any
      goroutine, a no-op after the program has left, so a start that outlives
      the view reports into nothing). Model tests: the concurrent-start
      refusal and per-node clears, the progress lines landing on the right
      nodes, and the held-start program test watching the tile carry the
      start's line until it finishes

## 3. The renderers

- [x] 3.1 The tile: one framed panel per node, content computed from the
      `NodeResult` as a string — the bar-format metrics block (state, what it
      serves, last active with the shared wording, resource bars, token and
      request lines) reusing the `metrics_render.go` helpers, degrading when a
      node answers with fewer facts, and the typed outcome plus reason for a
      node whose answer was a failure — with lipgloss framing the selected tile
      distinctly; a result not yet seen is an empty-state panel naming the
      node
- [x] 3.2 The grid and frame: columns/rows from the terminal size (minimum one
      each), the model-owned scroll row for an overflowing grid (selection and
      page keys keep the tile visible), and the three-part frame — header
      (title, fleet file, node count), grid, footer (key help or the confirm
      prompt, the status line, the "refreshing" marker)
- [x] 3.3 Tile render tests, byte-stable in the repo's renderer idiom: a
      running node with GPUs, a stopped node, an unreachable node, an unseen
      node, and selected versus unselected (the last under a colour-keeping
      profile, since the byte-stable profile strips the border colour)
- [x] 3.4 Layout tests: (width, height, node count) → columns, rows, and scroll
      offset, including the minimum-one case, the exactly-fits case, and the
      scrolling case
- [x] 3.5 The grid row joins the corresponding lines of the tiles it places,
      not the tiles themselves — each tile is a multi-line block, so joining
      the blocks glued the second tile's top border to the first tile's bottom
      border and shifted its body down a line (caught on a real three-node
      fleet; the layout test's single-line placeholders could not see it).
       Regression tested at the join level with multi-line blocks and at the
       View level with real framed tiles side by side
- [x] 3.6 The in-flight tile: a node with an action in flight shows the verb
      and the call's latest status line in place of its last report — the
      boot's progress is the node's truth until the report returns — and the
      verb alone while the call has not yet said anything; the verb
      conjugates (a stop is stopping, not stoping). Byte-stable tile tests for
      both shapes

## 4. Verification

- [x] 4.1 `teatest` (the Bubble Tea test program) end-to-end with the fake
      nodes (refresh interval pinned): a cold fleet opens with every panel
      showing its outcome, `s` brings a node to running across refreshes, the
      stop-confirm flow takes it back down, and quitting exits cleanly
- [x] 4.2 CLI-level tests through the command seam: the non-TTY refusal names
      `fleet metrics --watch`, and a fleet-file failure fails before any
      terminal interaction
- [x] 4.3 `gofmt`, `go vet ./...`, and `go test ./... -cover` at or above 80%
      (cmd/outfit at 88.7%; the `-race` suite passes, matching CI)
- [x] 4.4 `openspec validate add-fleet-dashboard --strict` passes clean
- [x] 4.5 Update `AGENTS.md`: the `fleet dashboard` command and its files, the
      watch-mode-versus-dashboard split, and the new UI dependencies
