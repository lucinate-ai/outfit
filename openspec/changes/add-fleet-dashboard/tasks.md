## 1. Dependencies and command skeleton

- [ ] 1.1 Add `charmbracelet/bubbletea`, `charmbracelet/lipgloss`, and
      `charmbracelet/bubbles` to `go.mod` (viewport is the only bubbles package
      used) and record the measured binary-size delta in the commit
- [ ] 1.2 Register the `fleet dashboard` subcommand in the tree: `--fleet` flag
      (shared `fleetFileUsage`), its `Long` text, the same completion
      registration as its siblings, and `dashboard` in the `fleet` parent's
      fallback usage string
- [ ] 1.3 Fleet-file resolution and the pre-view guard: resolve the file as the
      other fleet commands do (a file problem fails before the view opens),
      build the node set once through `Config.NewNode` (a node that cannot be
      built is a standing `config-error` result, never in the live set), and
      refuse a non-terminal stdout with a message naming `fleet metrics --watch`
      before any raw mode is entered
- [ ] 1.4 Wire the `tea.Program` (alternate screen) and the exit paths: quit key
      and interrupt exit without an error and with the terminal restored

## 2. The model

- [ ] 2.1 The model's state: node set, per-node `NodeResult`s, selection
      (fleet-file order), the refresh generation, the status line, the
      stop-confirmation state, and the action-in-flight flag; the refresh and
      action intervals as variables the tests can pin
- [ ] 2.2 The refresh: a `tea.Cmd` that runs `FanOutNodes(MetricsCall, nodes)`
      with a one-interval context deadline, returns the result tagged with its
      generation, and never starts while a round is in flight; a stale or
      superseded result is discarded rather than painted
- [ ] 2.3 Navigation: `j`/`k`/up/down move the selection in file order (no
      wrapping); selection change scrolls the grid so the tile stays visible
- [ ] 2.4 Start on the selected node: sent without confirmation through
      `Node.Start` in an action command; the reply (the resulting state, or the
      daemon's refusal) lands in the status line; nothing is sent while another
      action is in flight
- [ ] 2.5 Stop with explicit confirmation: the stop key opens the confirmation
      (footer prompt, all other keys ignored), decline or escape sends nothing,
      confirm sends `Node.Stop` on the selected node only, and the reply lands
      in the status line
- [ ] 2.6 The manual refresh key: an immediate fleet-wide round outside the
      interval, subject to the same in-flight and stale rules
- [ ] 2.7 Model tests: selection movement and clamping, the stop-confirm flow
      (declined, confirmed, escaped), start and stop dispatch to a fake
      `fleet.Node` (in-memory state, the fleet package's own test idiom),
      status-line outcomes for accepted and refused actions, and
      stale-generation discard

## 3. The renderers

- [ ] 3.1 The tile: one framed panel per node, content computed from the
      `NodeResult` as a string — the bar-format metrics block (state, what it
      serves, last active with the shared wording, resource bars, token and
      request lines) reusing the `metrics_render.go` helpers, degrading when a
      node answers with fewer facts, and the typed outcome plus reason for a
      node whose answer was a failure — with lipgloss framing the selected tile
      distinctly; a result not yet seen is an empty-state panel naming the
      node
- [ ] 3.2 The grid and frame: columns/rows from the terminal size (minimum one
      each), the viewport for an overflowing grid (selection and page keys keep
      it under control), and the three-part frame — header (title, fleet file,
      node count), grid, footer (key help or the confirm prompt, the status
      line, the "refreshing" marker)
- [ ] 3.3 Tile render tests, byte-stable in the repo's renderer idiom: a
      running node with GPUs, one without system facts (the remote-kind
      answer), a stopped node, an unreachable node, a config-error node, an
      unseen node, and selected versus unselected
- [ ] 3.4 Layout tests: (width, height, node count) → columns, rows, and scroll
      offset, including the minimum-one case, the exactly-fits case, and the
      scrolling case

## 4. Verification

- [ ] 4.1 `tea.TestProgram` end-to-end with the fake nodes (refresh interval
      pinned): a cold fleet opens with every panel showing its outcome, `s`
      brings a node to running across refreshes, the stop-confirm flow takes it
      back down, and quitting exits cleanly
- [ ] 4.2 CLI-level tests through the command seam: the non-TTY refusal names
      `fleet metrics --watch`, and a fleet-file failure fails before any
      terminal interaction
- [ ] 4.3 `gofmt`, `go vet ./...`, and `go test ./... -cover` at or above 80%
- [ ] 4.4 `openspec validate add-fleet-dashboard --strict` passes clean
- [ ] 4.5 Update `AGENTS.md`: the `fleet dashboard` command and its files, the
      watch-mode-versus-dashboard split, and the new UI dependencies
