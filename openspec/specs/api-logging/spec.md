# api-logging Specification

## Purpose
What outfit records about its own behaviour while it hosts the control API and
supervises an engine: a summary of every request served, the engine's
lifecycle, the severity each record carries, and the level control that lets an
operator silence routine traffic without losing the failures buried in it.

It exists because the API was previously mute. The engine's output has been
captured and served since the daemon shipped, but nothing recorded what the API
itself did — so a request that was rejected, a start that failed before the
engine existed, or a caller using a stale token left no evidence anywhere on
the machine.
## Requirements
### Requirement: A request summary for every API request

Wherever the control API is exposed — under `outfit daemon`, which always
exposes it, and under `outfit serve --api`, which opts in — outfit SHALL emit
one log record per request it serves, after the response is complete. The
record SHALL identify the request method, the request path, the response
status, how long the request took, how many bytes the response body carried,
and the address the request came from.

The summary SHALL be emitted for every request the API handles, including those
rejected before reaching an endpoint, so an unauthorised request is as visible
as a served one. It SHALL be emitted whatever the outcome: a request that fails
is recorded, not dropped.

Emitting the summary SHALL NOT change what the API returns: the status, headers
and body a caller receives are the same whether or not the record is emitted at
the configured level.

#### Scenario: A served request is summarised

- **WHEN** a status request is served successfully
- **THEN** one log record is emitted naming the method, path, status, duration,
  response size and caller address
- **AND** the response the caller receives is unchanged

#### Scenario: A rejected request is summarised

- **WHEN** a request arrives with a missing or incorrect bearer token
- **THEN** it is rejected as it always was, and a summary recording the
  unauthorised status is emitted

#### Scenario: Every endpoint is covered

- **WHEN** any control endpoint is called — status, start, stop, metrics, logs
  or deploy config
- **THEN** a summary is emitted for it, without the endpoint having to opt in

### Requirement: Records are graded by severity

Each record SHALL carry a severity so that turning the volume down silences
routine traffic first and failures last. A summary of a request that succeeded
SHALL be recorded at informational severity; a summary of a request rejected as
the caller's fault SHALL be recorded at warning severity; a summary of a request
that failed inside outfit SHALL be recorded at error severity.

The consequence SHALL hold in both directions: an operator running at warning
severity sees rejected and failed requests and no successful ones, and an
operator running at error severity sees only outfit's own failures.

#### Scenario: Routine traffic is silenced without silencing failures

- **WHEN** the level is set to warning and a fleet client polls status
  repeatedly, one of those requests carrying a bad token
- **THEN** no record is emitted for the successful polls
- **AND** the rejected request is still recorded

#### Scenario: A server-side failure is recorded at the highest severity

- **WHEN** a request fails with a server error
- **THEN** the summary is recorded at error severity, so it survives every
  level short of silence

### Requirement: No secret and no payload in a record

A log record SHALL NOT contain the bearer token, in any form, from any source —
not the `Authorization` header, not a rendering of the request's headers. It
SHALL NOT contain a request or response body: a pushed deploy config carries
serve arguments that may hold credentials, and engine output served over the
logs endpoint may carry prompts and model output. Neither belongs in a log
that is written to stderr and, in a service manager, forwarded to a shared
journal.

The path SHALL be recorded, including its query parameters, which carry only
cursors and bounds.

#### Scenario: A rejected request does not disclose the token offered

- **WHEN** a request carrying an incorrect bearer token is summarised
- **THEN** the record reports that the request was unauthorised and contains
  neither the offered token nor the expected one

#### Scenario: A pushed deploy config is not logged

- **WHEN** a deploy config is pushed and summarised
- **THEN** the record names the endpoint and its outcome, and contains no part
  of the config body

### Requirement: The engine lifecycle is recorded

The host SHALL record the supervised engine's lifecycle on the same log as the
request summaries, wherever it supervises one: that a start was requested and
what it resolved to serve, that the engine started and the command it started,
that it was stopped, and that it exited. A start that fails before the engine
runs SHALL be recorded with the reason. An engine that exits unexpectedly SHALL
be recorded at error severity, so a crash is visible at any level short of
silence, while an ordinary start or stop is informational.

#### Scenario: A start is traceable end to end

- **WHEN** a start request arrives and the engine starts
- **THEN** the records name the start, what was resolved to serve, and the
  command the engine was started with

#### Scenario: A failed start says why

- **WHEN** a start request arrives with nothing to serve
- **THEN** the failure and its reason are recorded, and the request summary
  records the failed status

#### Scenario: A crash is recorded at error severity

- **WHEN** the supervised engine exits unexpectedly
- **THEN** the exit is recorded at error severity

### Requirement: The level is configurable

The host SHALL let an operator set the severity threshold at or above which
records are emitted, choosing between debug, informational, warning and error.
It SHALL be settable by a command-line flag on both `outfit daemon` and
`outfit serve`, and by an environment variable, with the flag taking precedence
over the variable. With neither set, the threshold SHALL be informational — so
request summaries appear by default and an operator silences them deliberately
rather than discovering them missing.

An unrecognised level SHALL be rejected at startup, naming the accepted values,
rather than being silently treated as the default: a mistyped level that
quietly logged everything anyway would be discovered only when the log was
needed.

#### Scenario: Summaries appear by default

- **WHEN** the API is exposed with no level configured
- **THEN** request summaries are emitted

#### Scenario: Raising the level silences summaries

- **WHEN** the level is set to warning
- **THEN** no summary is emitted for a successfully served request

#### Scenario: The flag beats the environment

- **WHEN** the environment sets one level and the command line sets another
- **THEN** the command line's level applies

#### Scenario: An unrecognised level fails fast

- **WHEN** the configured level is not one of the accepted values
- **THEN** startup fails with an error naming the accepted values, and no API
  is exposed

### Requirement: Records go to the error stream, beside the engine's own output

The host's own records SHALL be written to standard error, so that a foreground
`outfit serve` keeps forwarding the engine's own stdout and stderr exactly as
it does today: outfit's records sit beside the engine's output rather than
replacing it, reformatting it, or being mixed into the stream a caller may be
piping.

The human-facing narration the serve commands already print — the command being
run, the preset selected, the address the API listens on — SHALL remain
readable output rather than becoming records the level control can hide,
except where a line is purely operational and belongs in the log.

#### Scenario: Engine output is untouched

- **WHEN** an engine writes to its stdout under a foreground serve
- **THEN** that output is forwarded as before, with outfit's own records on
  standard error

#### Scenario: A dry run is unaffected

- **WHEN** `outfit serve --dry-run` prints the command it would run
- **THEN** that output is unchanged at every level

