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

### Bubble Tea and lipgloss; grid overflow is the model's own row math

The TUI framework is `charmbracelet/bubbletea` (the Elm-architecture program
loop) and `lipgloss` for the framed panels. Grid overflow was planned on
`bubbles/viewport` and dropped while implementing: lipgloss 1.x's `MaxWidth`
mangles the border it is combined with, its `Width` wraps long lines (which
breaks the shared bar block's alignment), so each tile line is hard-cut
anyway — and with every line already exactly tile-width, scrolling the grid
is one integer (the first visible row) plus a slice of the already-rendered
rows. Owning that integer in the model is less code than driving a widget,
and the layout becomes a pure function the tests table directly. Alternatives:

- **Hand-rolled over `golang.org/x/term`**: measured +17 KB stripped / +3 KB
  gzipped, against +790 KB stripped / +227 KB gzipped for the Bubble Tea stack.
  Size is not the deciding factor either way — on a 6 MB download the difference
  is 0.05% vs 3.7% (final landing, with `bubbles` dropped: +383 KB stripped /
  +107 KB gzipped, ≈1.8%). The hand-rolled option means owning raw-mode terminal code
  (Ctrl+C is byte `0x03` in raw mode, not a signal; arrow keys are `ESC [ A`
   sequences with a bare-ESC disambiguation window; terminal size must be re-read
   on resize) and re-owning it as the deferred features (log pane, filter,
   multi-select) grow the key surface. Bubble Tea's model/update/view split also
   makes the interesting logic — selection, confirmation, refresh scheduling — a
   pure function testable in-process (its test stack, `x/exp/teatest`), while
   the terminal layer stays untested glue either way.
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
`FanOutNodes(ctx, fleet.MetricsCall, nodes)` with a context deadline of the
group's interval, wrapped so the result arrives as a message tagged with a
generation number. The nodes split into two groups by the fleet file's own
`kind` (the entry's, not the `Node` contract's — no interface change):

- The **local machines** (`kind: daemon`, the default) refresh on the tick —
  a short interval, seconds, because each read is a socket to a machine on
  the sideboard.
- The **cloud environments** (`kind: remote`) refresh on a slower deadline —
  a minute. Each of their statuses is a signed call through the control
  plane, a Lambda invocation, so the board would spend real calls polling a
  neighbor's pace for nothing: scale-to-zero instances change state on the
  scale of minutes, not seconds. The deadline moves when the cloud round
  starts; the tick checks it and starts the cloud round only when it has
  passed. On a cold open it has never been spent, so the first tick starts
  both groups.

The cadence is the group's: the tick reschedules itself whenever it fires —
one tick, one due round per group — and a round starts in a group only when
none of the group's rounds is in flight. (A one-shot `tea.Tick` with no
reschedule leaves the board still after the second round: the first round
comes from `Init`, the tick starts the second, and nothing ever starts a
third.) `r` refreshes at the operator's request and is due for every node,
cloud or local, whatever the deadlines say. Two invariants fall out of that
shape, each per group:

- **No overlap.** A tick that arrives while a group's round is in flight
  reschedules but does not start a second one for that group; a stale result
  (generation older than the group's) is discarded, so a slow round can never
  paint over a fast one. The groups' generations are separate, so a cloud
  round taking its whole minute never withholds the local rounds.
- **No hostage.** The deadline is the per-node budget: a node that has not
  answered within its group's interval is shown with its outcome this round
  (the classification `fanOutEach`/`classify` already gives timeouts) and
  retried next round, while the rest of the fleet keeps its cadence. A slow
  cloud round stretches only the cloud group's cycle.

One bubbletea wrinkle the shape depends on: the program must hold the model
by **pointer**. With a value model, `Init`'s mutations (the rounds it starts,
their generations) happen on the receiver copy and the program never reads it
back, so the first round's answers — for remote environments, real control-
plane calls — would be discarded as stale, and the cloud deadline's first
spend would not count. `Update` returns its mutated model, so it is the only
safe door; `Init` is not.

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

Each node has its own action state — the model keeps one `dashAction`
(the verb and the call's latest status line) beside each entry — and the
confirmation belongs to the node under the cursor:

- node idle → `s` on it: the start command runs on its own goroutine. The
  node's `dashAction` records the verb from that moment; a second `s` on the
  same node is refused (the status line says it is still starting), but the
  operator can move to another node and start it — actions are per node, so
  two cloud wakes run side by side rather than the board waiting out the
  slowest cloud.
- node idle → `x` on it: `confirmStop` enters; the footer swaps its key help
  for `stop <name>? y/n`; every key but `y`/`n`/escape is ignored; `y` sends
  the stop (same in-flight handling), `n`/escape return with nothing sent.
- in flight → the call's reply clears the node's `dashAction` (the tile
  returns to the node's next report) and sets the status line.

The in-flight start is the dashboard's one piece of cross-goroutine
conversation. The call runs in a Bubble Tea command — its own goroutine,
with no deadline of its own, because a cold cloud wake takes minutes and a
deadline would report a failure to a slow success — and a `kind: remote`
start reports a line on every retry ("instance starting; retrying in Ns").
The lines would die with the call if the dashboard dropped them, as the one-
shot `fleet start` does; so `remoteNode` implements an optional
`fleet.ProgressStarter` alongside the plain `Node` verbs, and `beginAction`
asserts for it — a daemon node's start is one POST and one reply and does not
implement it, and the assertion is what keeps the `Node` contract and every
existing driver untouched. The model feeds the lines back through a `send`
field: the `tea.Program`'s `Send`, safe from any goroutine and a no-op once
the program has left, so a start that outlives the view reports into nothing
rather than panicking. The lines land as `dashActionProgressMsg`s, the
handler stores the latest on the node's `dashAction`, and the tile renders
it — the verb plus the latest line, in place of the node's last report, for
the life of the call. A node that reports nothing shows the verb alone.

The status line is a single string the model owns; a refresh does not clear it
(the operator may have just read it), the next finished action replaces it,
and it is rendered at most to the footer width. It carries outcomes, not
progress: with two wakes in flight the footer is still one line, and the
boots are watched on their own tiles.

### Layout: fixed tile size, grid fills the frame, the model scrolls the overflow

The tile is a fixed content size, framed by a lipgloss rounded border; the
selected tile's border is recolored. The geometry is chosen off the shared
bar block: content 42×12 — the bar line is 41 columns, which is the width
constraint, and a running node with one GPU fills all 12 lines exactly
(header, serving, last-active, four bars, separator, four token lines) — so
the frame is 44×14 and rows sit one apart. Columns = the most tiles that fit
across the terminal width, visible rows = the most that fit between the
header and footer lines; both minimum one. When the node count exceeds the
visible grid, the model holds the first visible row and renders that slice of
the rows: `j`/`k` move the selection in file order (no wrapping) and, when the
selection's row leaves the visible slice, the scroll row moves so the tile
stays visible; page-up/page-down step a visible-row height; the scroll row is
clamped to the grid's extent. Every line is hard-cut to the tile width with an
ANSI-aware cut (`x/ansi`), so a long model id or error message degrades to
truncation rather than reflow. The terminal size comes from
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
- **Program level**: `teatest.NewTestModel` end-to-end with the fake nodes —
  cold fleet opens, `s` brings a node to running across refresh ticks (the
  interval var, like `fleetLogsInterval`, is a test variable), stop-confirm
  down to stopped, quit exits 0.
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
