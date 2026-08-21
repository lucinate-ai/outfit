## Context

See proposal.md — Why. The shapes that constrain the design:

- The post-unification fleet client (`internal/fleet`) is the entire surface the
  dashboard stands on: `fleet.Resolve` finds the file, `Config.NewNode` builds a
  node (daemon or remote kind, the one shared constructor seam), `FanOutNodes`
  runs a call over an explicit `[]Node` concurrently in order, and
  `fleet.MetricsCall` already returns per node everything a panel needs — state,
  runner, model, uptime, last-active, tokens, CPU/RAM/GPU — in one response.
  `Node.Start`/`Node.Stop` are the action verbs, and `NodeResult`/`Outcome` are
  the typed answers (including the failures) a degraded panel renders.
- The one-shot `fleet start`/`fleet stop` commands (`driveOneNode`) and the
  `fleet logs -f` poll loop are the behavioural precedents: one node at a time,
  a failed node is a reported row, interruption is a clean exit.
- The renderers in `cmd/outfit/metrics_render.go` already write the bar-format
  block for one node's `metrics.Stats` into an `io.Writer`; the dashboard's tile
  is that block framed in a panel, so the rendering logic is reused, not
  re-invented.
- The repo leans "no runtime dependencies" but has already adopted per-surface
  frameworks (Cobra, Viper, hujson); the binary is ~6 MB gzipped today,
  dominated by the AWS SDK.

## Goals / Non-Goals

**Goals:**

- A `fleet dashboard` that is the place to bring a fleet up from: cold-openable,
  selection, start/stop on the selected node, live panels, clean exit.
- One renderer for daemon and remote nodes: the same tile draws a node with full
  system stats and one that reports only engine facts.
- Every behaviour that is already true of the fleet commands (file-order
  stability, typed outcomes, one-node actions, per-node degradation) preserved
  inside the TUI rather than re-decided.

**Non-Goals:**

- No pause (deferred, #119 — the `Node` contract gains `Pause` only there), no
  log tailing, no deploy-config push, no multi-node actions, no fleet-file
  editing.
- No change to any existing command's output, to the daemon API, or to the
  `Node` contract.
- Not a replacement for `fleet metrics --watch`, which stays the pipeable
  redraw.

## Decisions

### Bubble Tea, with lipgloss and bubbles' viewport

The TUI framework is `charmbracelet/bubbletea` (the Elm-architecture program
loop), `lipgloss` for the framed panels, and `bubbles/viewport` for grid
overflow. Alternatives:

- **Hand-rolled over `golang.org/x/term`**: measured +17 KB stripped / +3 KB
  gzipped, against +790 KB stripped / +227 KB gzipped for the Bubble Tea stack.
  Size is not the deciding factor either way — on a 6 MB download the difference
  is 0.05% vs 3.7%. The hand-rolled option means owning raw-mode terminal code
  (Ctrl+C is byte `0x03` in raw mode, not a signal; arrow keys are `ESC [ A`
  sequences with a bare-ESC disambiguation window; terminal size must be re-read
  on resize) and re-owning it as the deferred features (log pane, filter,
  multi-select) grow the key surface. Bubble Tea's model/update/view split also
  makes the interesting logic — selection, confirmation, refresh scheduling — a
  pure function testable in-process with `tea.TestProgram`, while the terminal
  layer stays untested glue either way.
- The repo's per-surface-framework precedent (Cobra for the command tree, Viper
  for the env binding) is the same category of decision: the TUI framework owns
  the interactive surface end to end.

### The code lives in `cmd/outfit`, in three files

- `fleet_dashboard.go` — the cobra command (`--fleet` flag, Long text,
  completion registration like its siblings), the non-TTY guard, fleet-file
  resolution, the node-set build, and the `tea.Program` wiring.
- `dashboard_model.go` — the `tea.Model`: state, `Init`/`Update`/`View`, the
  refresh and action commands, the layout computation.
- `dashboard_render.go` — frame and tile renderers, reusing
  `metrics_render.go` helpers, drawn lipgloss-invariant (panel content is
  computed as a string from `NodeResult`, styled by lipgloss at the frame level).

Following `fleet logs` (whose whole machinery lives in `cmd/outfit`), the logic
stays beside its command instead of a new `internal/` package. Revisit if the
model outgrows the main package's test idioms.

### Nodes are built once; each refresh is one `FanOutNodes(MetricsCall)` over them

At startup: `fleet.Resolve`, then `Config.NewNode` per entry. A node that cannot
be built (an unresolved token reference) is held as a standing
`OutcomeConfigError` result — the same outcome the one-shot surface reports — and
never entered into the live set.

Each refresh is a `tea.Cmd` running in its own goroutine:
`FanOutNodes(ctx, fleet.MetricsCall, nodes)` with a context deadline of one
refresh interval, wrapped so the result arrives as a message tagged with a
generation number. Two invariants fall out of that shape:

- **No overlap.** A tick that arrives while a refresh is in flight is dropped; a
  stale result (generation older than the model's) is discarded, so a slow round
  can never paint over a fast one.
- **No hostage.** The deadline is the per-node budget: a node that has not
  answered within one interval is shown with its outcome this round (the
  classification `fanOutEach`/`classify` already gives timeouts) and retried
  next tick, while the rest of the fleet keeps its cadence.

`StatusCall` is not also fetched: `metrics.Stats` already carries state, runner,
model, uptime and last-active, so one read per node per round answers the panel.
Consequence, accepted: `metrics.Stats` has no `Version`, so a panel does not show
the daemon version that `fleet status` rows do — the driving view trades a
reference fact for a live one, and `fleet status` stays the surface that shows
it. (A remote-kind node answers with fewer facts likewise; the tile degrades
rather than fails.)

**Start is fire-and-forget, and the refresh loop is the waiting.** The daemon
accepts `POST /v1/start` once it has the work queued and returns; weight loading
then takes minutes. The dashboard sends the start as a short `tea.Cmd` (same
pattern as the refresh), reports the reply in the status line, and does not wait
— the ordinary refreshes carry the panel from `starting` through to `running`.
No progress machinery is needed, and a refused start (engine already running) is
a `failed` outcome in the status line, not an error. Stop is the same shape
behind its confirmation. A remote-kind node's stop terminates (the contract
semantics, identical to a one-shot `fleet stop` on that node); the confirmation
is the guard, and the status line shows the control plane's own wording.

### Actions and the confirmation state

The model has one action state machine, shared by start and stop:

- idle → `s`: start command runs; `actionInFlight` suppresses further action
  keys (navigation and refresh still work); the reply sets the status line.
- idle → `x`: `confirmStop` enters; the footer swaps its key help for
  `stop <name>? y/n`; every key but `y`/`n`/escape is ignored; `y` sends the
  stop (same in-flight handling), `n`/escape return to idle with nothing sent.

The status line is a single string the model owns; a refresh does not clear it
(the operator may have just read it), the next action replaces it, and it is
rendered at most to the footer width.

### Layout: fixed tile size, grid fills the frame, viewport scrolls the overflow

The tile is a fixed content size (width chosen so the widest token line fits with
margin, height so the full bar block — header, last-active, resource bars, token
lines — fits without clipping), framed by a lipgloss rounded border; the selected
tile's border is recolored. Columns = the most tiles that fit across the
terminal width, rows = the most that fit between the header and footer lines;
both minimum one. When the node count exceeds columns × rows, the whole grid is
rendered and placed in the `bubbles/viewport`, which the model drives directly:
`j`/`k` move the selection in file order (no wrapping) and, when they cross a
row boundary, `SetY` scrolls the viewport to keep the selected tile visible;
page-up/page-down scroll a viewport height. The terminal size comes from
`tea.WindowSizeMsg` (default 80×24 until the first one), and the frame is
recomputed on every size message.

The frame is three parts, each an independent render function of the model:
header (title + fleet file path + node count), grid, footer (key help or the
stop-confirm prompt, the status line, a "refreshing" marker while a round is in
flight). A node that answered nothing yet — before its first complete refresh —
is drawn as an empty-state panel naming the node, not as a blank.

### Non-TTY is a refusal, not a fallback

Before entering the tea program, the command checks `x/term.IsTerminal` on
stdout; when it is false (piped or backgrounded) it fails with a message that
names `fleet metrics --watch` as the non-interactive equivalent. Falling back to
a redraw would quietly change what a script receives, which is precisely the
surface `--watch` already fills.

### Test seams

- **Tile renderers**: pure `NodeResult` → panel string; byte-stable tests in the
  repo's existing renderer-test idiom — a running node with GPUs, one without,
  a stopped node, an unreachable one, a config-error one, a remote-kind answer
  with no system facts, and selected vs unselected.
- **Layout**: (width, height, node count) → columns, rows, scroll offset table
  tests, including the minimum-one case and the scrolling case.
- **Model**: `Update` driven directly with `tea.KeyMsg`s — selection movement and
  clamping, the stop-confirm flow (declined, confirmed, escape), start/stop
  dispatch to an injected node set (a fake `fleet.Node` over in-memory state, as
  `fleet`'s own tests do), status-line outcomes, and stale-generation discard.
- **Program level**: `tea.TestProgram` end-to-end with the fake nodes — cold
  fleet opens, `s` brings a node to running across refresh ticks (the interval
  var, like `fleetLogsInterval`, is a test variable), stop-confirm down to
  stopped, quit exits 0.
- **CLI level**: the non-TTY guard through the existing command seam (stdout to a
  buffer → error naming watch mode), and a fleet-file failure before any
  terminal interaction.

The dockerised fleet example is not extended: it has no pty, and the fake-node
program tests cover the same loop without a container.

## Risks / Trade-offs

- [A refresh round longer than the interval (several slow nodes) drops panels by
  one tick] → The deadline bounds the round to one interval; the affected panels
  show their outcome for that round and recover on the next. Same information
  `fleet status` shows, at a slower cadence, for a persistently slow node.
- [Bubble Tea's `View` runs often; the grid renders N framed panels per frame] →
  N is a fleet of machines, not a service fleet; even dozens of panels of ~40
  lines are a trivial string build. If it ever shows in profiling, the tile
  strings can be cached per (result, selection) pair.
- [Terminal state after a crash (not a clean exit)] → `tea.WithAltScreen` and
  the program's exit path restore state on quit, interrupt and error; SIGKILL
  can be restored by no program, and the operator's terminal is one
  `reset` away — accepted.
- [The status line is single-writer but refreshes and actions both race it
  across goroutines] → All model mutation happens in `Update` (tea's
  single-threaded model); the async commands only return messages. There is no
  shared mutable state outside the model.
- [A remote-kind node's `stop` terminates an instance, and the dashboard is
  closer to that verb than the CLI is] → The explicit confirmation is the spec
  requirement it exists to serve; the status line reports the control plane's
  own reply, so what happened is the cloud's own wording. Pause (the softer
  verb) lands with #119.
- [Coverage floor (80%) over a new ~600-line surface] → The render/layout/model
  tests above are the coverage plan; the thin tea wiring is the small
  untestable remainder, the same kind of glue the hand-rolled option would have
  made larger.

## Open Questions

None blocking. The pause key, log tailing, and deploy push are deferred
(#119, #50-line) by the proposal; none of them changes this design's seams —
they add a key, a pane, and a call, respectively, against the same model.
