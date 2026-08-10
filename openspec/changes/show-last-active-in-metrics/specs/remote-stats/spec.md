## ADDED Requirements

### Requirement: Reporting when the endpoint last did work

The metrics report SHALL include how long it has been since the endpoint's
engine last did any work, taken from the activity the on-instance daemon
tracks, in every format the command supports. The figure exists to answer "is
this endpoint doing anything?" at a glance, without the reader having to infer
it from the running-request count.

The figure SHALL be labelled "last active" rather than "idle": `idle` is
already an engine *state* meaning nothing has been started, and one report
SHALL NOT carry two meanings of the word. The elapsed time SHALL be rendered
the same way the command's other durations are, so an uptime and a last-active
figure read alike.

An endpoint whose daemon reports no activity — because no engine has run yet,
or because the daemon could not be reached — SHALL omit the figure rather than
show one implying the endpoint has been quiet since it started.

#### Scenario: A working endpoint reports its last activity

- **WHEN** the user runs `outfit remote metrics` against a running endpoint
  whose engine has served work
- **THEN** the report shows how long ago that work happened, labelled "last
  active"

#### Scenario: Every format carries the figure

- **WHEN** the user runs `outfit remote metrics` with `--format=bar`,
  `--format=table`, or `--format=json`
- **THEN** each output carries the last-active figure in its own idiom

#### Scenario: An endpoint that has done nothing omits the figure

- **WHEN** the user runs `outfit remote metrics` against an endpoint whose
  engine has not yet done any work
- **THEN** the report shows no last-active figure

#### Scenario: An unreachable daemon omits the figure

- **WHEN** the control plane cannot reach the instance's daemon to collect
  metrics
- **THEN** the report shows no last-active figure, and the rest of the report
  renders as it does today

## MODIFIED Requirements

### Requirement: Stats subcommand

The system SHALL provide a `metrics` subcommand (`outfit remote metrics`) that reports the current state of a remote inference instance. It SHALL accept the same Outfit resolution as `start`, `stop`, and `deploy` — an optional positional Outfit path, defaulting to `./Outfit` when present — and SHALL require the Outfit to name a `REMOTE` environment.

A stopped instance SHALL still report when its engine last did work, when that
is known — the question "how long has this been doing nothing?" is most worth
answering about something that is not running.

#### Scenario: Stats with a running instance

- **WHEN** the user runs `outfit remote metrics` with a running instance
- **THEN** the command reports the instance state, runner, model, GPU info, CPU/RAM usage, token counts, request counts, and when the engine was last active

#### Scenario: Stats with a stopped instance

- **WHEN** the user runs `outfit remote metrics` and the instance is stopped
- **THEN** the command reports `state: stopped` and no resource or token
  metrics, showing the last-active figure when one is known

#### Scenario: Stats resolves the Outfit

- **WHEN** the user runs `outfit remote metrics` in a directory with an `Outfit` that has a `REMOTE` instruction
- **THEN** the command uses that Outfit's remote environment without an explicit path argument

#### Scenario: Stats with explicit Outfit path

- **WHEN** the user runs `outfit remote metrics ./some/Outfit`
- **THEN** the command uses that Outfit's `REMOTE` environment
