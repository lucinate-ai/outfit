# Cloud Stats Specification

## Purpose

Define the `spinloop metrics` command's reading of a cloud environment's
instance: token usage, resource consumption, and GPU information from a
running cloud inference instance.
## Requirements
### Requirement: An environment's metrics are reported

The system SHALL provide a `metrics` command (`spinloop metrics --env <name>`) that reports the current state of a cloud inference instance. It SHALL select which environment it reports on using the same `--env <name>` flag every other read verb uses. A Spinloop given as an argument is read only for its `ENV` instructions and adjacent `.env`, never to select the environment. When the instance is running, the report SHALL include the spinloop version from the daemon, carried by the stats Lambda reply.

#### Scenario: Stats with a running instance

- **WHEN** the user runs `spinloop metrics --env <name>` with a running instance
- **THEN** the command reports the instance state, runner, model, spinloop version, GPU info, CPU/RAM usage, token counts, and request counts

#### Scenario: Stats with a stopped instance

- **WHEN** the user runs `spinloop metrics --env <name>` and the instance is stopped
- **THEN** the command reports `state: stopped` and no metrics

#### Scenario: Stats names the environment with the flag

- **WHEN** the user runs `spinloop metrics --env dev-2`
- **THEN** the command reports on the `dev-2` environment's instance

#### Scenario: Version is shown in stats output

- **WHEN** the user runs `spinloop metrics --env <name>` with a running instance
- **THEN** the output includes the spinloop version

#### Scenario: The cloud spelling names its replacement

- **WHEN** the operator runs `spinloop cloud metrics`
- **THEN** it fails naming `spinloop metrics --env <name>` as the command that
  replaced it

### Requirement: An environment's cost is reported on request

When the user passes `--cost`, the stats report SHALL include an estimated on-demand cost for the current running session. The cost SHALL be computed from the instance type's on-demand price (fetched from the AWS Price List API for the deployed region) multiplied by the elapsed time since launch. Without `--cost`, no price lookup is performed and no cost is shown.

#### Scenario: Cost is shown with flag

- **WHEN** the user runs `spinloop metrics --env <name> --cost` with a running instance
- **THEN** the report includes the estimated cost for the current session

#### Scenario: Cost is not shown by default

- **WHEN** the user runs `spinloop metrics --env <name>` without `--cost`
- **THEN** the report does not include a cost line

### Requirement: The metrics report's tabular display

The stats output SHALL support four formats via the `--format` flag: `gauge` (default), `bar`, `table`, and `json`. The `gauge` format SHALL produce a compact display with horizontal progress gauges for the current reading, colour-coded by utilization level. The `bar` format SHALL produce a compact display drawing each resource series as a sparkline of the daemon's retained history, with the latest point colour-coded by utilization level. The `table` format SHALL produce a tab-separated key-value table, one line per metric, with the key column left-aligned and values right of it. The `json` format SHALL output the response as a JSON object to standard output. Progress and error messages SHALL go to standard error regardless of format.

#### Scenario: Clean output

- **WHEN** the command succeeds
- **THEN** standard output contains only the stats data with no progress or debug lines

#### Scenario: Default format is gauge

- **WHEN** the user runs `spinloop metrics --env <name>` without `--format`
- **THEN** the output is in gauge format

#### Scenario: Table format is explicit

- **WHEN** the user runs `spinloop metrics --env <name> --format=table`
- **THEN** the output is in table format

#### Scenario: Bar format is explicit

- **WHEN** the user runs `spinloop metrics --env <name> --format=bar`
- **THEN** the output is in bar format, drawing each resource series as a sparkline of the daemon's retained history

#### Scenario: Gauge format is explicit

- **WHEN** the user runs `spinloop metrics --env <name> --format=gauge`
- **THEN** the output is in gauge format with progress gauges for the current reading

#### Scenario: JSON format

- **WHEN** the user runs `spinloop metrics --env <name> --format=json`
- **THEN** the output is valid JSON containing the instance state, runner, model, GPU info, CPU/RAM usage, and token counts

#### Scenario: JSON format with cost

- **WHEN** the user runs `spinloop metrics --env <name> --format=json --cost`
- **THEN** the JSON output includes a cost estimate field

#### Scenario: Invalid format errors

- **WHEN** the user runs `spinloop metrics --env <name> --format=csv`
- **THEN** the command exits with an error and usage message

### Requirement: The metrics report refreshes on request

The system SHALL support a `--watch`/`-w` flag that repeatedly queries metrics every 60 seconds. When enabled, the command SHALL clear the screen and redraw the output in place for each refresh, producing no scrollback accumulation. Each refresh SHALL pre-render the metrics output into a buffer before clearing the screen, so the redisplay is instantaneous after the network round-trip. The command SHALL continue until the user sends `SIGINT` (Ctrl+C) or `SIGTERM`, at which point it SHALL exit cleanly.

#### Scenario: Watch mode repeats output

- **WHEN** the user runs `spinloop metrics --env <name> --watch`
- **THEN** the command prints metrics, waits 60 seconds, clears the screen, and prints updated metrics

#### Scenario: Watch redraws in place

- **WHEN** the user runs `spinloop metrics --env <name> -w`
- **THEN** each refresh after the first clears the screen before displaying new output, with no separator lines

#### Scenario: Watch with JSON format

- **WHEN** the user runs `spinloop metrics --env <name> --watch --format=json`
- **THEN** each refresh clears the screen and outputs a JSON object

#### Scenario: Watch with cost

- **WHEN** the user runs `spinloop metrics --env <name> --watch --cost`
- **THEN** each refresh includes the cost estimate

#### Scenario: Watch stops on interrupt

- **WHEN** the user runs `spinloop metrics --env <name> -w` and presses Ctrl+C
- **THEN** the command exits cleanly without error

### Requirement: An environment's metrics report when it last did work

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

- **WHEN** the user runs `spinloop metrics --env <name>` against a running endpoint
  whose engine has served work
- **THEN** the report shows how long ago that work happened, labelled "last
  active"

#### Scenario: Every format carries the figure

- **WHEN** the user runs `spinloop metrics --env <name>` with `--format=bar`,
  `--format=table`, or `--format=json`
- **THEN** each output carries the last-active figure in its own idiom

#### Scenario: An endpoint that has done nothing omits the figure

- **WHEN** the user runs `spinloop metrics --env <name>` against an endpoint whose
  engine has not yet done any work
- **THEN** the report shows no last-active figure

#### Scenario: An unreachable daemon omits the figure

- **WHEN** the control plane cannot reach the instance's daemon to collect
  metrics
- **THEN** the report shows no last-active figure, and the rest of the report
  renders as it does today

### Requirement: History in the metrics report

When the on-instance daemon's metrics reply carries a history of system readings, the report SHALL carry it through to the command's output: the `json` format SHALL include the readings, and the `bar` format SHALL draw them. Where the daemon's reply carries no history, the report SHALL omit the field and the `bar` format SHALL fall back per the bar format specification. The control plane's relay of the daemon's reply SHALL NOT alter the readings it carries.

#### Scenario: JSON carries the daemon's history

- **WHEN** the instance's daemon reports a retained history and the user runs `spinloop metrics --env <name> --format=json`
- **THEN** the JSON output includes the history's readings

#### Scenario: Bar draws the relayed history

- **WHEN** the instance's daemon reports a retained history and the user runs `spinloop metrics --env <name> --format=bar`
- **THEN** each resource series is drawn as a sparkline from the readings the control plane relayed

#### Scenario: A daemon without history degrades

- **WHEN** the instance runs a daemon whose reply carries no history and the user runs `spinloop metrics --env <name> --format=bar`
- **THEN**    the report omits the history field and bar format draws the current reading in the gauge's filled style

### Requirement: The stats reply carries the retention deadline

The stats Lambda's reply SHALL carry the instance's retention deadline — the
time until which the idle sweep leaves the instance alone, parsed from the
instance's Retain-Until tag, which the Lambda already reads — when that tag is
a time in the future, and SHALL omit it when the tag is absent or a time that
has passed. The deadline is a fact the control plane holds about the instance,
not one the on-instance daemon reports: it SHALL be present for a stopped
instance the sweep would otherwise terminate, and absent for an environment
with no instance at all.

A control plane that predates the field SHALL simply omit it, with no error:
every reader of the reply treats an absent deadline as "no active retention"
rather than as a failure.

The client SHALL map the deadline onto the shared stats shape every fleet and
cloud stats surface reads from, so a dashboard panel, a one-shot fleet report,
and a one-shot cloud report cannot word the same read differently.

The deadline SHALL be reported on the report's active-figure line — rendered by
the client as a relative remaining time, e.g. `keep for 2h`, not the absolute
timestamp — in every format the command supports, and omitted in every format
when the read carries none, following the same omission rule the active figure
uses for a figure it does not have.

The relative remaining time SHALL be worded in hours and minutes only, with
any zero unit dropped — `keep for 2h`, `keep for 1h 30m`, `keep for 24m` — and
SHALL NOT carry a seconds component: a keep is set in minutes or hours, and in
a panel that re-renders, a seconds figure changes on every refresh without
changing what the operator can do about it. A remaining time of less than a
minute SHALL still render — as `keep for 1m` — so the figure is present for
any future deadline and absent only once the deadline has passed.

#### Scenario: A retained instance's stats carry the deadline

- **WHEN** the user reads the stats of an environment whose instance carries a
  Retain-Until tag at a time in the future
- **THEN** the reply carries that deadline

#### Scenario: A passed tag carries no deadline

- **WHEN** the user reads the stats of an instance whose Retain-Until tag is a
  time that has passed
- **THEN** the reply carries no deadline: a passed tag holds no active
  retention

#### Scenario: An untagged or instance-less environment carries no deadline

- **WHEN** the user reads the stats of an instance with no Retain-Until tag,
  or of an environment with no instance
- **THEN** the reply carries no deadline

#### Scenario: An older control plane is read without error

- **WHEN** the user reads the stats of an environment whose control plane
  predates the field
- **THEN** the read succeeds, the deadline is simply absent, and the rest of
  the report renders as before

#### Scenario: Every format carries the deadline

- **WHEN** the user reads the stats of a retained environment with `bar`,
  `table`, or `json` output
- **THEN** each output carries the deadline in its own idiom on the active
   figure's line, and each omits it when the read carries none

#### Scenario: The relative time carries no seconds

- **WHEN** the user reads the stats of a retained environment whose deadline
  is two hours, five minutes, and thirty seconds away
- **THEN** the report's active-figure line renders `keep for 2h 5m`

#### Scenario: A whole-hour or whole-minute deadline drops zero units

- **WHEN** the user reads the stats of a retained environment whose deadline
  is exactly two hours away, and later of one that is exactly twenty-four
  minutes away
- **THEN** the report's active-figure line renders `keep for 2h`, and later
  `keep for 24m`

#### Scenario: A sub-minute keep still renders

- **WHEN** the user reads the stats of a retained environment whose deadline
  is forty-five seconds away
- **THEN** the report's active-figure line renders `keep for 1m`
