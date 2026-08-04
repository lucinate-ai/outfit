# Remote Stats Delta Specification

## MODIFIED Requirements

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
