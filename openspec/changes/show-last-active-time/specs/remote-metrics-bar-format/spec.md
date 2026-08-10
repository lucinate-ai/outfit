## ADDED Requirements

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

## MODIFIED Requirements

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
