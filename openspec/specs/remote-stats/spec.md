# Remote Stats Specification

## Purpose

Define the `outfit remote metrics` command: reading token usage, resource consumption, and GPU information from a running remote inference instance.
## Requirements
### Requirement: Stats subcommand

The system SHALL provide a `metrics` subcommand (`outfit remote metrics`) that reports the current state of a remote inference instance. It SHALL accept the same Outfit resolution as `start`, `stop`, and `deploy` — an optional positional Outfit path, defaulting to `./Outfit` when present — and SHALL require the Outfit to name a `REMOTE` environment. When the instance is running, the report SHALL include the outfit version from the daemon, carried by the stats Lambda reply.

#### Scenario: Stats with a running instance

- **WHEN** the user runs `outfit remote metrics` with a running instance
- **THEN** the command reports the instance state, runner, model, outfit version, GPU info, CPU/RAM usage, token counts, and request counts

#### Scenario: Stats with a stopped instance

- **WHEN** the user runs `outfit remote metrics` and the instance is stopped
- **THEN** the command reports `state: stopped` and no metrics

#### Scenario: Stats resolves the Outfit

- **WHEN** the user runs `outfit remote metrics` in a directory with an `Outfit` that has a `REMOTE` instruction
- **THEN** the command uses that Outfit's remote environment without an explicit path argument

#### Scenario: Stats with explicit Outfit path

- **WHEN** the user runs `outfit remote metrics ./some/Outfit`
- **THEN** the command uses that Outfit's `REMOTE` environment

#### Scenario: Version is shown in stats output

- **WHEN** the user runs `outfit remote metrics` with a running instance
- **THEN** the output includes the outfit version

### Requirement: Optional cost estimation

When the user passes `--cost`, the stats report SHALL include an estimated on-demand cost for the current running session. The cost SHALL be computed from the instance type's on-demand price (fetched from the AWS Price List API for the deployed region) multiplied by the elapsed time since launch. Without `--cost`, no price lookup is performed and no cost is shown.

#### Scenario: Cost is shown with flag

- **WHEN** the user runs `outfit remote metrics --cost` with a running instance
- **THEN** the report includes the estimated cost for the current session

#### Scenario: Cost is not shown by default

- **WHEN** the user runs `outfit remote metrics` without `--cost`
- **THEN** the report does not include a cost line

### Requirement: Tabular display

The stats output SHALL support three formats via the `--format` flag: `bar` (default), `table`, and `json`. The `bar` format SHALL produce a compact display with horizontal progress bars for resource metrics, colour-coded by utilization level. The `table` format SHALL produce a tab-separated key-value table, one line per metric, with the key column left-aligned and values right of it. The `json` format SHALL output the response as a JSON object to standard output. Progress and error messages SHALL go to standard error regardless of format.

#### Scenario: Clean output

- **WHEN** the command succeeds
- **THEN** standard output contains only the stats data with no progress or debug lines

#### Scenario: Default format is bar

- **WHEN** the user runs `outfit remote metrics` without `--format`
- **THEN** the output is in bar format

#### Scenario: Table format is explicit

- **WHEN** the user runs `outfit remote metrics --format=table`
- **THEN** the output is in table format

#### Scenario: Bar format is explicit

- **WHEN** the user runs `outfit remote metrics --format=bar`
- **THEN** the output is in bar format with progress bars for resource metrics

#### Scenario: JSON format

- **WHEN** the user runs `outfit remote metrics --format=json`
- **THEN** the output is valid JSON containing the instance state, runner, model, GPU info, CPU/RAM usage, and token counts

#### Scenario: JSON format with cost

- **WHEN** the user runs `outfit remote metrics --format=json --cost` with a running instance
- **THEN** the JSON output includes a cost estimate field

#### Scenario: Invalid format errors

- **WHEN** the user runs `outfit remote metrics --format=csv`
- **THEN** the command exits with an error and usage message

### Requirement: Watch mode

The system SHALL support a `--watch`/`-w` flag that repeatedly queries metrics every 60 seconds. When enabled, the command SHALL clear the screen and redraw the output in place for each refresh, producing no scrollback accumulation. Each refresh SHALL pre-render the metrics output into a buffer before clearing the screen, so the redisplay is instantaneous after the network round-trip. The command SHALL continue until the user sends `SIGINT` (Ctrl+C) or `SIGTERM`, at which point it SHALL exit cleanly.

#### Scenario: Watch mode repeats output

- **WHEN** the user runs `outfit remote metrics --watch`
- **THEN** the command prints metrics, waits 60 seconds, clears the screen, and prints updated metrics

#### Scenario: Watch redraws in place

- **WHEN** the user runs `outfit remote metrics -w`
- **THEN** each refresh after the first clears the screen before displaying new output, with no separator lines

#### Scenario: Watch with JSON format

- **WHEN** the user runs `outfit remote metrics --watch --format=json`
- **THEN** each refresh clears the screen and outputs a JSON object

#### Scenario: Watch with cost

- **WHEN** the user runs `outfit remote metrics --watch --cost`
- **THEN** each refresh includes the cost estimate

#### Scenario: Watch stops on interrupt

- **WHEN** the user runs `outfit remote metrics -w` and presses Ctrl+C
- **THEN** the command exits cleanly without error

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

