# Remote Stats Specification

## Purpose

Define the `outfit remote metrics` command: reading token usage, resource consumption, and GPU information from a running remote inference instance.

## Requirements

### Requirement: Stats subcommand

The system SHALL provide a `metrics` subcommand (`outfit remote metrics`) that reports the current state of a remote inference instance. It SHALL accept the same Outfit resolution as `start`, `stop`, and `deploy` — an optional positional Outfit path, defaulting to `./Outfit` when present — and SHALL require the Outfit to name a `REMOTE` environment.

#### Scenario: Stats with a running instance

- **WHEN** the user runs `outfit remote metrics` with a running instance
- **THEN** the command reports the instance state, runner, model, GPU info, CPU/RAM usage, token counts, and request counts

#### Scenario: Stats with a stopped instance

- **WHEN** the user runs `outfit remote metrics` and the instance is stopped
- **THEN** the command reports `state: stopped` and no metrics

#### Scenario: Stats resolves the Outfit

- **WHEN** the user runs `outfit remote metrics` in a directory with an `Outfit` that has a `REMOTE` instruction
- **THEN** the command uses that Outfit's remote environment without an explicit path argument

#### Scenario: Stats with explicit Outfit path

- **WHEN** the user runs `outfit remote metrics ./some/Outfit`
- **THEN** the command uses that Outfit's `REMOTE` environment

### Requirement: Optional cost estimation

When the user passes `--cost`, the stats report SHALL include an estimated on-demand cost for the current running session. The cost SHALL be computed from the instance type's on-demand price (fetched from the AWS Price List API for the deployed region) multiplied by the elapsed time since launch. Without `--cost`, no price lookup is performed and no cost is shown.

#### Scenario: Cost is shown with flag

- **WHEN** the user runs `outfit remote metrics --cost` with a running instance
- **THEN** the report includes the estimated cost for the current session

#### Scenario: Cost is not shown by default

- **WHEN** the user runs `outfit remote metrics` without `--cost`
- **THEN** the report does not include a cost line

### Requirement: Tabular display

The stats output SHALL support two formats via the `--format` flag: `table` (default) and `json`. The `table` format SHALL produce a tab-separated key-value table, one line per metric, with the key column left-aligned and values right of it. The `json` format SHALL output the response as a JSON object to standard output. Progress and error messages SHALL go to standard error regardless of format.

#### Scenario: Clean output

- **WHEN** the command succeeds
- **THEN** standard output contains only the stats data with no progress or debug lines

#### Scenario: Default format is table

- **WHEN** the user runs `outfit remote metrics` without `--format`
- **THEN** the output is in table format

#### Scenario: Table format is explicit

- **WHEN** the user runs `outfit remote metrics --format=table`
- **THEN** the output is in table format

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

The system SHALL support a `--watch`/`-w` flag that repeatedly queries metrics every 60 seconds. When enabled, the command SHALL print the metrics output, then wait 60 seconds and repeat. Each refresh SHALL be preceded by a separator line to distinguish successive outputs. The command SHALL continue until the user sends `SIGINT` (Ctrl+C) or `SIGTERM`, at which point it SHALL exit cleanly.

#### Scenario: Watch mode repeats output

- **WHEN** the user runs `outfit remote metrics --watch`
- **THEN** the command prints metrics, waits 60 seconds, and prints updated metrics

#### Scenario: Watch separator

- **WHEN** the user runs `outfit remote metrics -w`
- **THEN** each refresh after the first is preceded by a separator line

#### Scenario: Watch with JSON format

- **WHEN** the user runs `outfit remote metrics --watch --format=json`
- **THEN** each refresh outputs a separate JSON object on its own line

#### Scenario: Watch with cost

- **WHEN** the user runs `outfit remote metrics --watch --cost`
- **THEN** each refresh includes the cost estimate

#### Scenario: Watch stops on interrupt

- **WHEN** the user runs `outfit remote metrics -w` and presses Ctrl+C
- **THEN** the command exits cleanly without error
