## ADDED Requirements

### Requirement: Configurable start timeout

`start` SHALL wait for the endpoint up to an overall timeout that the user can
set, defaulting to 15 minutes when not given. The timeout SHALL be accepted as a
Go duration on a `--timeout` flag with a `-t` short alias, so a user may shorten
or lengthen the wait, e.g. `-t 5m`. When the timeout passes before the endpoint
is ready, `start` SHALL stop waiting and fail rather than block indefinitely.

#### Scenario: Shortening the wait

- **WHEN** the user runs `outfit remote start` with `-t 5m` (or `--timeout 5m`)
- **THEN** the command waits at most five minutes for the endpoint before
  giving up

#### Scenario: Default wait when unset

- **WHEN** the user runs `outfit remote start` without a timeout flag
- **THEN** the command waits up to fifteen minutes

## MODIFIED Requirements

### Requirement: Reporting a start in progress

Because the endpoint blocks until the model is serving, `start` SHALL report
that it is waiting rather than appear to hang: it SHALL say what is happening
before the first attempt and repeat at intervals with the elapsed time, and
SHALL report how long it took once ready. Progress SHALL be written to standard
error and the resulting exports to standard output, so the command's output can
be evaluated directly while a person watching still sees progress.

The periodic progress line SHALL reflect the endpoint's most recently reported
state so it does not misdescribe what is happening. When the latest poll reports
that no capacity is available anywhere — so no instance is booting — the line
SHALL say it is waiting for capacity rather than that the instance is starting.
When the latest poll reports that an instance is booting, or before any poll has
returned, the line SHALL say it is starting. Each per-poll retry notice
(reporting the state and the wait before the next attempt) SHALL continue to be
reported as it happens, independently of the periodic line.

#### Scenario: A cold start is not silent

- **WHEN** the endpoint takes minutes to become ready
- **THEN** the command explains what it is waiting for and continues to report
  the elapsed time until it succeeds

#### Scenario: Waiting for capacity is not reported as booting

- **WHEN** the most recent poll reports no capacity in any zone
- **THEN** the periodic progress line says it is waiting for capacity, not that
  the instance is still starting

#### Scenario: Booting is reported as starting

- **WHEN** the most recent poll reports the instance is booting, or no poll has
  returned yet
- **THEN** the periodic progress line says it is still starting

#### Scenario: Only the result is on standard output

- **WHEN** a start succeeds and its output is captured
- **THEN** standard output holds exactly the environment exports, with every
  progress line on standard error
