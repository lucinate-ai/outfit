# Bar Metrics Format Specification

## Purpose

Define the bar graph output format for `outfit remote metrics` with colour-coded resource utilization indicators.
## Requirements
### Requirement: Bar format output

The system SHALL support a `--format=bar` option that renders resource metrics as horizontal progress bars. Each bar SHALL consist of a left-aligned label, a filled portion using block characters, an unfilled portion using light shade characters, and a right-aligned percentage value.

#### Scenario: Bar format displays CPU utilization

- **WHEN** the user runs `outfit remote metrics --format=bar` with a running instance that has CPU data
- **THEN** the output includes a bar labelled "CPU" with filled and unfilled segments proportional to the utilization percentage

#### Scenario: Bar format displays RAM utilization

- **WHEN** the user runs `outfit remote metrics --format=bar` with a running instance that has memory data
- **THEN** the output includes a bar labelled "RAM" with filled and unfilled segments proportional to the used/total memory ratio

#### Scenario: Bar format displays GPU utilization

- **WHEN** the user runs `outfit remote metrics --format=bar` with a running instance that has GPU data
- **THEN** the output includes bars labelled "GPU util" and "GPU mem" (or "GPU N util"/"GPU N mem" for multiple GPUs) with filled and unfilled segments proportional to their respective utilization

#### Scenario: Bar format header line

- **WHEN** the user runs `outfit remote metrics --format=bar` with a running instance
- **THEN** the first line shows the environment, state, instance type, and model ID separated by double spaces

### Requirement: Colour thresholds

The bar fill SHALL be colour-coded based on utilization: green for values at or below 80%, yellow for values from 80% to 90%, and red for values above 90%. The colour SHALL be reset after the filled portion so the unfilled characters and percentage appear in the terminal's default colour.

#### Scenario: Green bar for low utilization

- **WHEN** a metric value is 70%
- **THEN** the bar fill appears in green

#### Scenario: Yellow bar for high utilization

- **WHEN** a metric value is 85%
- **THEN** the bar fill appears in yellow

#### Scenario: Red bar for critical utilization

- **WHEN** a metric value is 95%
- **THEN** the bar fill appears in red

### Requirement: Bar format is default

The system SHALL use bar format as the default output when no `--format` flag is specified.

#### Scenario: Default format is bar

- **WHEN** the user runs `outfit remote metrics` without `--format`
- **THEN** the output is in bar format

### Requirement: Bar format with stopped instance

When the instance is not running, bar format SHALL show the header line with environment, state, instance type, and model — but SHALL NOT display resource bars. When a last-active time is known it SHALL still be shown, in the same
place it occupies for a running instance: the resource bars describe a running
engine and have nothing to say about a stopped one, but when work last
happened is exactly what a stopped endpoint is worth asking about.

#### Scenario: Stopped instance shows header only

- **WHEN** the user runs `outfit remote metrics --format=bar` and the instance is stopped with no recorded activity
- **THEN** the output shows the header with state "stopped" and no resource bars

#### Scenario: Stopped instance still reports its last activity

- **WHEN** the user runs `outfit remote metrics --format=bar`, the instance is
  stopped, and a last-active time is known
- **THEN** the output shows the header, the last-active line, and no resource
  bars

### Requirement: Last-active line in bar format

Bar format SHALL show the last-active figure on its own line, immediately
below the header line and above the resource bars, so it reads as a fact about
the endpoint rather than as another utilisation reading. It SHALL NOT be drawn
as a bar: it is an elapsed time with no ceiling to fill against, and a bar
would imply one.

The line SHALL be omitted entirely when no last-active time is known, rather
than shown empty or zeroed.

#### Scenario: The figure sits under the header

- **WHEN** the user runs `outfit remote metrics --format=bar` against a
  running endpoint whose engine has served work
- **THEN** the line after the header shows how long ago that was, and the
  resource bars follow it

#### Scenario: No activity, no line

- **WHEN** bar format renders an endpoint with no known last-active time
- **THEN** the output goes straight from the header to the resource bars

