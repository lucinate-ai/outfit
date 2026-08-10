## ADDED Requirements

### Requirement: Metrics reports engine activity

The metrics endpoint SHALL report the engine's last-active time as an RFC 3339
timestamp and the idle duration derived from it in seconds, on exactly the
terms the status endpoint already uses: both present or both absent, and both
absent until an engine has run.

The two endpoints SHALL draw on the same activity record rather than each
keeping its own, so a caller cannot see one answer from status and a different
answer from metrics at the same moment. A metrics request that scrapes the
engine's counters SHALL feed that reading into the record exactly as the
background sampler does, so polling metrics refreshes the shared answer rather
than racing it.

The activity fields SHALL be reported whatever the engine's state. Where the
metrics endpoint omits the running-engine figures — the token counters and the
host's GPU, CPU and memory readings — for an engine that is not running, it
SHALL still report last-active and idle, because the record survives a stop
precisely so it can answer after one.

#### Scenario: Metrics reports activity for a running engine

- **WHEN** a metrics request is made while an engine is running that has
  served work
- **THEN** the response reports the last-active timestamp and the number of
  seconds since it, alongside the token and system figures

#### Scenario: Metrics and status agree

- **WHEN** a metrics request and a status request are made against the same
  daemon
- **THEN** both report the same last-active time

#### Scenario: Metrics omits activity when there is none

- **WHEN** a metrics request is made on a daemon that has never started an
  engine
- **THEN** the response carries no last-active timestamp and no idle duration

#### Scenario: A stopped engine still reports its last activity

- **WHEN** a metrics request is made after the engine has been stopped
- **THEN** the response reports the last-active time and idle duration, even
  though it reports no token or system figures
