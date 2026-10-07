# Local Serving Specification

## Purpose

Define `spinloop serve`: launching `llama-server` for the model a Spinloop
describes — from the Spinloop's own instructions, or from a llama.cpp preset
`.ini` it points at — so the same file that configures the harness can also start
the server behind it. `serve` is harness-agnostic and never touches harness
config.
## Requirements
### Requirement: Serve basics

`spinloop serve [path]` SHALL read a Spinloop (default `./Spinloop`, aliases and
directories accepted like every Spinloop command), build the command for the
engine its `PROVIDER` names, print it in copy-pasteable shell form, and run it.
`--dry-run`/`-n` SHALL print the command without launching. A missing binary
SHALL produce an install hint naming **that** engine rather than a raw exec
error.

A run whose stdout is not a terminal SHALL forward the engine's stdout and
stderr to serve's own, exactly as before the view existed. A run on a terminal
SHALL run the engine under the serve view, whose log section is fed by the
engine's captured output (see The serve view); the command serve prints before
running SHALL be written to stderr there, since stdout is the view's screen.

#### Scenario: Dry run

- **WHEN** the user runs `spinloop serve --dry-run`
- **THEN** the resolved command is printed and no server starts

#### Scenario: Engine not installed

- **WHEN** the selected engine's binary cannot be found
- **THEN** the error suggests installing that engine, not another one

#### Scenario: A piped run forwards the engine's output

- **WHEN** `spinloop serve` runs with its stdout piped or redirected
- **THEN** the engine's stdout and stderr are forwarded to serve's own, no
  view opens, and the printed command stays on stdout

### Requirement: Choosing the engine

`spinloop serve` SHALL launch the inference engine the Spinloop's `PROVIDER` names,
the local counterpart of the runner `spinloop cloud deploy` selects from the same
instruction. `llamacpp` SHALL run `llama-server`; `omlx` SHALL run the oMLX CLI;
`vllm` SHALL run `vllm serve`, with the model passed as its positional
argument, the served name as `--served-model-name`, and the context window as
`--max-model-len`; `mtplx` SHALL run `mtplx serve`, with the model as
`--model`, the served name as `--model-id`, the context window as
`--context-window`, and a `--download` flag so the engine fetches a model it
does not have itself. There SHALL be no default: a `PROVIDER` that is not a
self-hosted engine SHALL fail, naming the providers that can be served, rather
than launching an engine the Spinloop did not ask for.

An engine whose executable is normally installed outside the `PATH` SHALL also be
looked for at its conventional install location, so a user who has never put it
on their `PATH` can still serve.

#### Scenario: Provider selects the engine

- **WHEN** the Spinloop says `PROVIDER omlx`
- **THEN** the printed command runs the oMLX CLI, not `llama-server`

#### Scenario: vLLM is servable

- **WHEN** the Spinloop says `PROVIDER vllm` with a `MODEL`
- **THEN** the printed command runs `vllm serve` with that model as the
  positional argument

#### Scenario: MTPLX is servable

- **WHEN** the Spinloop says `PROVIDER mtplx` with a `MODEL` naming an MTPLX
  optimised repo or a local path
- **THEN** the printed command runs `mtplx serve` with that model as `--model`
  and includes `--download`

#### Scenario: MTPLX served name and context

- **WHEN** the Spinloop says `PROVIDER mtplx` with `ALIAS qwen` and
  `CONTEXT 128k`
- **THEN** the printed command carries `--model-id qwen` and
  `--context-window 128000`

#### Scenario: A provider that is not a local engine

- **WHEN** the Spinloop names a hosted provider such as `ollama` or `openrouter`
- **THEN** the command fails, listing the providers that can be served locally

#### Scenario: Engine installed outside the PATH

- **WHEN** the oMLX CLI is not on the `PATH` but is present in its macOS app
  bundle
- **THEN** `serve` uses the bundled executable

### Requirement: Preset-less serving

Without a `PRESET`, the Spinloop SHALL supply the command directly and MUST name
a `MODEL`: a value that looks like a local file (ending `.gguf`, or starting
with `/`, `./`, `../`, or `~`) becomes the model path, anything else is
treated as a Hugging Face repo reference. `ALIAS` sets the served model's
reported name, `CONTEXT` (parsed with the standard lenient size format) sets
the context size, and `BASEURL`'s host and port set the server's bind address.

#### Scenario: Hugging Face repo

- **WHEN** the Spinloop has `MODEL unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_XL`,
  `ALIAS qwen3.6`, and `CONTEXT 32768`
- **THEN** the command carries the repo reference, the alias, and a context
  size of 32768

#### Scenario: Local file

- **WHEN** the Spinloop has `MODEL ./models/qwen.gguf`
- **THEN** the command loads that file as a model path rather than a repo

#### Scenario: No model to serve

- **WHEN** the Spinloop has neither `PRESET` nor `MODEL`
- **THEN** the command fails explaining serve needs one of them

### Requirement: Serving a multi-model engine

An engine that loads a directory of models and selects one per request SHALL be
servable with neither a `PRESET` nor a `MODEL`, since it needs nothing at launch
to know what to load. For such an engine the Spinloop's `MODEL` and `ALIAS` SHALL
keep their usual meaning — the id the harness requests — rather than becoming
launch flags, and `CONTEXT` SHALL size the harness's window only. `BASEURL`'s
host and port SHALL still set the server's bind address, and SHALL be omitted
entirely when the Spinloop states no `BASEURL`, so the engine's own defaults stand.

#### Scenario: Bare Spinloop starts the server

- **WHEN** the Spinloop states only `PROVIDER omlx`
- **THEN** the server is launched with no bind flags and no model flags, rather
  than failing for want of a model

#### Scenario: Bind address from BASEURL

- **WHEN** the Spinloop adds `BASEURL http://127.0.0.1:9100/v1`
- **THEN** the command binds the server to that host and port

### Requirement: Preset-based serving

With a `PRESET`, the referenced `.ini` SHALL supply the command: a relative
preset reference resolves against the Spinloop's own source — a local directory
join when the Spinloop was read from disk, URL-relative resolution when the
Spinloop was fetched from a URL — so the pair can travel together either way. A
`PRESET` MAY itself be an absolute `http://`/`https://` URL regardless of
where the Spinloop lives, in which case it is fetched over HTTP. Fetching a
remote `PRESET` SHALL happen only when a command that consumes it (`spinloop
serve`, `spinloop cloud deploy`) actually builds its launch or deploy
configuration — never merely because the Spinloop was read or applied. The
preset's `[*]`/`[global]` section holds shared defaults; each named section is
one model whose keys are server arguments with dashes stripped. The served
section is chosen by the Spinloop's `ALIAS`, matched case-insensitively; a
preset with exactly one section is always served; no sections is an error;
several sections with no matching name SHALL fail listing the available
sections. Values the Spinloop itself states SHALL override the preset's.

A preset SHALL be read in the flag vocabulary of the engine the `PROVIDER`
names, never in a vocabulary inferred from the file. A preset written for one
engine is therefore not portable to another: read in the wrong vocabulary it
would parse cleanly and produce a command with silently rewritten or dropped
flags.

#### Scenario: Spinloop overrides the preset

- **WHEN** the preset section sets `ctx-size = 4096` and the Spinloop says
  `CONTEXT 32768`
- **THEN** the command carries a context size of 32768

#### Scenario: Ambiguous preset

- **WHEN** the preset defines several sections and the Spinloop's `ALIAS` matches
  none
- **THEN** the command fails listing the section names to choose from

#### Scenario: Another engine's preset keys are left alone

- **WHEN** an oMLX preset contains a key that llama.cpp would treat as a short
  alias
- **THEN** the key is rendered as written, not rewritten to llama.cpp's
  long-form flag

#### Scenario: A remote preset

- **WHEN** the Spinloop sets `PRESET https://example.com/preset.ini`
- **THEN** `spinloop serve` fetches that URL and serves the section its `ALIAS`
  selects

#### Scenario: A preset relative to a URL-sourced Spinloop

- **WHEN** a Spinloop fetched from `https://example.com/team/Spinloop` sets
  `PRESET ./preset.ini`
- **THEN** `spinloop serve` fetches `https://example.com/team/preset.ini`

#### Scenario: A preset is not fetched by commands that do not need it

- **WHEN** `spinloop harness apply` runs against a Spinloop whose `PRESET` is a URL
- **THEN** the preset URL is never fetched, matching how a local `PRESET` is
  already ignored by `apply`

### Requirement: Flag rendering

Preset keys SHALL render as the engine's flags. Where an engine defines short
aliases, they SHALL be canonicalised to their long form so the same flag written
different ways collapses to one when layers merge, later layers overriding
earlier ones in place; an engine that defines none SHALL pass its keys through
unchanged. Flags the engine accepts bare SHALL render bare when truthy and be
dropped when falsy (`0`, `false`, `off`, `no`). Printed commands SHALL quote only
the tokens that need it, and SHALL pass values to the server verbatim.

#### Scenario: Layer override by canonical name

- **WHEN** the global section sets `c = 4096` and the model section sets
  `ctx-size = 8192` for llama.cpp
- **THEN** the command carries a single context-size flag with value 8192

#### Scenario: Boolean flags

- **WHEN** a llama.cpp preset sets `jinja = 1` and `mmap = 0`
- **THEN** the command includes a bare `--jinja` and no mmap flag

#### Scenario: An engine with no short aliases

- **WHEN** an oMLX preset sets `model-dir` and `memory-guard`
- **THEN** both render as their long-form flags with their values unchanged

### Requirement: Parallelism

`CONTEXT` SHALL always mean the context window a single request gets, whatever
engine serves it. A Spinloop's `PARALLEL` SHALL set the number of concurrent
request slots, translated per engine so that `CONTEXT`'s meaning holds:

- For `llamacpp`, `PARALLEL n` SHALL be rendered as `--parallel n`. Because
  llama.cpp treats `--ctx-size` as a total budget divided across its parallel
  slots, when the Spinloop **also** states `CONTEXT`, the rendered `--ctx-size`
  SHALL be `context_tokens * n` rather than `context_tokens`, so each slot
  still gets the context the Spinloop asked for. `CONTEXT` set with no
  `PARALLEL` SHALL render `--ctx-size` as `context_tokens`, unscaled, exactly
  as before this capability existed.
- For `vllm`, `PARALLEL n` SHALL be rendered as `--max-num-seqs n`.
  `--max-model-len` (from `CONTEXT`) SHALL NOT be scaled by `PARALLEL`: vLLM's
  concurrency is bounded independently of a single request's context length.
- For `omlx`, `PARALLEL n` SHALL be rendered as `--max-concurrent-requests n`.
  oMLX has no context flag to scale either way.
- For `mtplx`, `PARALLEL n` SHALL be rendered as `--max-active-requests n`,
  MTPLX's cap on admitted concurrent requests. `--context-window` (from
  `CONTEXT`) SHALL NOT be scaled by `PARALLEL`. MTPLX's scheduling mode — how
  admitted requests execute, serially or in parallel — SHALL remain a preset
  concern: a Spinloop's `PARALLEL` SHALL NOT select it.

A Spinloop stating no `PARALLEL` SHALL produce a command identical to one from
before this capability existed, for all four engines. A `PARALLEL` value
SHALL be validated as a positive integer at the point it is used (`serve`, a
daemon-pushed config, or `cloud deploy`); a value that is not SHALL fail
naming the invalid value rather than being passed to the engine.

When `CONTEXT` is supplied by a `PRESET` section's own `ctx-size` rather than
by the Spinloop, `PARALLEL` SHALL NOT rescale it — only a Spinloop-stated
`CONTEXT` participates in the `llamacpp` multiply. A `PARALLEL` value SHALL
still override a preset's own `np`/`parallel` (llama.cpp), `max-num-seqs`
(vLLM), `max-concurrent-requests` (oMLX), or `max-active-requests` (MTPLX)
value by the same override-by-canonical-name rule `CONTEXT` already uses
against a preset's `ctx-size`.

#### Scenario: llama.cpp context is scaled by parallel slots

- **WHEN** a Spinloop states `PROVIDER llamacpp`, `CONTEXT 128k`, and
  `PARALLEL 2`
- **THEN** the printed command includes `--ctx-size 256000` and `--parallel 2`

#### Scenario: llama.cpp parallel with no context set

- **WHEN** a Spinloop states `PROVIDER llamacpp` and `PARALLEL 4` with no
  `CONTEXT`
- **THEN** the printed command includes `--parallel 4` and no `--ctx-size`
  flag is added

#### Scenario: llama.cpp context with no parallel set is unscaled

- **WHEN** a Spinloop states `PROVIDER llamacpp` and `CONTEXT 128k` with no
  `PARALLEL`
- **THEN** the printed command includes `--ctx-size 128000`, exactly as it
  would have before `PARALLEL` existed

#### Scenario: A preset's own context is not rescaled

- **WHEN** a Spinloop states `PROVIDER llamacpp`, `PARALLEL 2`, and a `PRESET`
  whose section sets `ctx-size` but the Spinloop itself states no `CONTEXT`
- **THEN** the printed command includes `--parallel 2` and the preset's
  `ctx-size` value passes through unscaled

#### Scenario: vLLM concurrency does not touch context

- **WHEN** a Spinloop states `PROVIDER vllm`, `CONTEXT 128k`, and `PARALLEL 4`
- **THEN** the printed command includes `--max-model-len 128000` and
  `--max-num-seqs 4`, with neither value derived from the other

#### Scenario: oMLX concurrency

- **WHEN** a Spinloop states `PROVIDER omlx` and `PARALLEL 8`
- **THEN** the printed command includes `--max-concurrent-requests 8` and no
  context flag, exactly as with no `PARALLEL` stated

#### Scenario: MTPLX concurrency does not touch context

- **WHEN** a Spinloop states `PROVIDER mtplx`, `CONTEXT 128k`, and
  `PARALLEL 4`
- **THEN** the printed command includes `--max-active-requests 4` and
  `--context-window 128000`, with neither value derived from the other

#### Scenario: No PARALLEL means no change

- **WHEN** a Spinloop states no `PARALLEL`, for any of the four engines
- **THEN** the printed command is identical to what it would have been before
  this capability existed

#### Scenario: Invalid parallel count

- **WHEN** a Spinloop states `PARALLEL 0`, a negative number, or a non-numeric
  value, for any of the four engines
- **THEN** the command fails naming the invalid value rather than passing it
  to the engine

#### Scenario: Spinloop PARALLEL overrides a preset's own value

- **WHEN** a Spinloop states `PROVIDER llamacpp` and `PARALLEL 2`, and its
  `PRESET` section separately sets `np = 4`
- **THEN** the printed command includes `--parallel 2`, not `--parallel 4`

### Requirement: Secrets never reach the server command

`serve` SHALL NOT resolve or pass an API key to the engine it launches. Because
the command is printed before it runs, and an engine may accept its key as a
command-line flag, passing one would expose the secret on screen and to any local
process. Configuring authentication on the server is the engine's own concern.

#### Scenario: A set key is not passed through

- **WHEN** the provider's API key variable is set in the environment
- **THEN** neither the printed command nor the launched process carries the key

### Requirement: Control API flag

`spinloop serve` SHALL accept `-a`/`--api` to expose the control API over the
foreground engine, as defined by the `daemon-api` capability. Serve SHALL
remain a foreground command with no daemon flag — long-lived supervision is
`spinloop daemon`'s job. The flag SHALL change only whether the control API
listens: the foreground behaviour — the view on a terminal, stdio forwarding
off one — is the same with and without it.

#### Scenario: Plain serve is unchanged

- **WHEN** the user runs `spinloop serve` without `--api`, with output not on
  a terminal
- **THEN** the engine runs in the foreground with stdio forwarded, exactly as
  before

#### Scenario: Serve with the API stays foreground

- **WHEN** the user runs `spinloop serve -a`
- **THEN** the engine runs in the foreground with the control API listening
  beside it, and the foreground behaviour is the same as without the flag

### Requirement: The serve view

`spinloop serve` on a terminal SHALL open a full-screen view of the engine it
is running: the engine's metrics, the engine's log, and a line naming the keys
the view answers to, in the three-section layout of the fleet dashboard's node
detail screen. The view SHALL need a terminal to draw on: a run whose output
is not a terminal SHALL NOT open it, and `--dry-run` SHALL open it neither,
printing the command without launching as it does today.

The view's metrics section SHALL show the same facts, in the same wording,
that the dashboard's detail screen and the metrics formats show for the same
engine — state, what is served, last active, the resource series, and the
token and request counters — read from the daemon the serve process runs
in-process rather than over the network, refreshed on the dashboard's own
local cadence. A reading the view could not renew SHALL be shown with its age
rather than drawn identically to one just read.

The view SHALL draw every resource series the reading carries in both formats
at once, each series on one line: its gauge of the current reading and its
bar of the retained history side by side — the same series and labelling the
bar and gauge formats use, with the bar format's no-history rule where a
series has no history to draw, so a series with none carries its gauge
alone, its bar half blank.

#### Scenario: The view opens on a terminal

- **WHEN** the user runs `spinloop serve` at an interactive terminal
- **THEN** the view opens showing the engine's metrics above its log, with a
  line naming the keys the view answers to

#### Scenario: A piped run gets no view

- **WHEN** `spinloop serve` runs with its stdout piped or redirected
- **THEN** no view opens and the engine's output is forwarded to serve's own

#### Scenario: The metrics match the detail screen

- **WHEN** the view is open on a running engine
- **THEN** its metrics section shows the same state, serving facts, last
  active, resource series and counters the dashboard's detail screen shows
  for the same engine, in full rather than clipped

#### Scenario: Every series is drawn in both formats

- **WHEN** the reading carries a CPU series with a retained history
- **THEN** the view draws the series on one line: its gauge of the current
  reading and its bar of the retained history, side by side

#### Scenario: A series with no history carries its gauge alone

- **WHEN** a series has no retained history to draw
- **THEN** the view draws its gauge of the current reading only, its bar
  half left blank

### Requirement: The serve view's log

The view's log section SHALL show the engine's log, tailing and following it
the same way the dashboard's detail view follows its node's log: new output
appears while the view is open without the operator asking for it. An engine
that has written nothing yet SHALL show a waiting note, not an empty pane.

The up and down arrow keys SHALL scroll the log pane: up moves the visible
window one line towards the oldest retained line, down one line towards the
newest; a press at either end SHALL leave the window where it is. Page up and
page down SHALL move the window by the pane's height. While the window is on
the newest line, new output SHALL appear in the pane as it is written; while
the window is scrolled away from it, new output SHALL be retained and the pane
SHALL stay where the operator put it until they scroll back to the newest
line, where it sticks again.

The operator SHALL be able to pause and resume the log's follow from the
keyboard, independently of the rest of the view: while paused, the pane SHALL
stop picking up new output, and the view SHALL show whether the log is
following or paused. Nothing written while paused is lost: resuming SHALL
fetch and show whatever the engine wrote in the meantime, and pausing SHALL
not affect the metrics section's own refresh.

#### Scenario: The log pane follows new output

- **WHEN** the engine writes to its log while the view is open and the window
  is on the newest line
- **THEN** the new lines appear in the log section without the operator
  pressing any key

#### Scenario: Scrolling the log

- **WHEN** the operator presses the up arrow
- **THEN** the window moves one line towards the older output, and the down
  arrow moves it back, line for line, until it is on the newest line again
  and sticks to it

#### Scenario: Paging the log

- **WHEN** the operator presses page up
- **THEN** the window moves by the pane's height towards the older output,
  clamped at the oldest retained line

#### Scenario: An engine that has written nothing yet

- **WHEN** the view is open and the engine has written no log output
- **THEN** the log section shows a waiting note, not an empty pane

#### Scenario: Pausing the log

- **WHEN** the operator pauses the log's follow
- **THEN** the pane stops picking up new output and the view shows that the
  log is paused, while the metrics section keeps refreshing on its own cadence

#### Scenario: Resuming the log

- **WHEN** the operator resumes a paused log
- **THEN** whatever the engine wrote while paused appears in the pane, and
  new output continues to appear as it is written

### Requirement: The serve view's keys and exit

The view SHALL name its keys on screen and offer only the ones that would do
something in it: the up and down arrows and page up and page down scroll the
log, `f` pauses and resumes the log's follow, and `q` or Ctrl+C leaves. The
view SHALL NOT offer start, stop, keep or abort keys: the engine is serve's
own — starting is serve's job, and stopping it is what leaving does.

`q` and Ctrl+C SHALL stop the engine — gracefully, escalating as a stop does
elsewhere — and exit serve. When the engine exits on its own, whatever the
cause, the view SHALL close and serve SHALL exit with the engine's exit
status, exactly as a foreground serve does today.

#### Scenario: The key help names only live keys

- **WHEN** the view draws its key help line
- **THEN** it names the scroll, follow and quit keys, and nothing the view
  cannot do

#### Scenario: Quitting stops the engine

- **WHEN** the operator presses q or Ctrl+C
- **THEN** the engine is stopped and serve exits

#### Scenario: The engine's own exit closes the view

- **WHEN** the engine process exits while the view is open
- **THEN** the view closes and serve exits with the engine's exit status

### Requirement: Engine output capture under the serve view

When serve runs the view, the engine's stdout and stderr SHALL be captured to
the daemon's state-dir engine log — the same file `spinloop daemon` writes —
rather than forwarded to serve's stdio. The capture SHALL hold the engine's
whole output from its first line, so the log the view tails is complete, and
the log's path SHALL be the one the control API's status reports. With
`--api`, the API's log endpoint SHALL serve the captured file rather than
reporting the log missing.

The engine's stdout SHALL be presented to the engine as a pseudo-terminal,
and the captured terminal output SHALL be normalised into log lines the same
way the daemon's engine log capture does: terminal escape sequences SHALL NOT
reach the log file, a line the engine redraws in place — a download progress
bar among them — SHALL be recorded as its state, the first and final state
always and any further distinct state at most once per fixed interval, and
the engine's stderr SHALL be written to the log file directly. A model
download's progress SHALL therefore appear in the view's log section while
the download runs, rather than the log showing the engine come up and then
silence.

#### Scenario: The engine's output lands in the log

- **WHEN** the engine writes to stdout or stderr while the view is open
- **THEN** the output is appended to the engine log file named in the
  daemon's status, and appears in the view's log section

#### Scenario: A model download's progress is shown

- **WHEN** the engine downloads a model while the view is open, printing its
  progress to stdout only because stdout is a terminal
- **THEN** the view's log section shows the download's progress as it runs,
  not silence

#### Scenario: serve --api's log endpoint serves the capture

- **WHEN** `spinloop serve --api` runs under the view and a client asks its
  log endpoint for the engine log
- **THEN** the reply carries the engine's output, not the missing-log answer

#### Scenario: Off the terminal nothing is captured

- **WHEN** `spinloop serve` runs with its output not on a terminal
- **THEN** the engine's output is forwarded to serve's stdio and the run
  writes no engine log file

### Requirement: The view's metrics sampling

For an engine that exposes a metrics endpoint, a view run SHALL switch that
endpoint on before the engine starts — the same switch a supervised engine
gets — so the counters and the history the bars draw are the engine's own.
The daemon's activity and history sampling SHALL run for the life of the view,
the same sampling a running engine gets under the daemon, so the bars have a
retained history to draw. An engine with no metrics endpoint SHALL run with no
added switch, and its view SHALL draw the host's series — CPU, RAM and the
GPUs — as any node does.

#### Scenario: A metrics-capable engine is switched on

- **WHEN** `spinloop serve` opens the view for an engine that has a metrics
  endpoint
- **THEN** the engine is launched with its metrics endpoint on, the same way
  a supervised engine is

#### Scenario: History accrues for the bars

- **WHEN** the engine has been running for several sampler ticks under the
  view
- **THEN** the bars draw the retained history of the ticks so far

#### Scenario: An engine with no metrics endpoint

- **WHEN** `spinloop serve` opens the view for an engine that exposes no
  metrics endpoint
- **THEN** the engine is launched without any added switch, and the view
  draws the host's CPU, RAM and GPU series

