## MODIFIED Requirements

### Requirement: Reporting a start in progress

Because the endpoint blocks until the model is serving, `start` SHALL report
that it is waiting rather than appear to hang: it SHALL say what is happening
before the first attempt and repeat at intervals with the elapsed time, and
SHALL report how long it took once ready. Progress SHALL be written to standard
error and the resulting exports to standard output, so the command's output can
be evaluated directly while a person watching still sees progress.

The periodic progress line SHALL reflect the situation the start is actually in
so it does not misdescribe what is happening. When the most recent reply
reported that no capacity is available anywhere — so no instance is booting —
and no newer attempt is in flight, the line SHALL say it is waiting for
capacity rather than that the instance is starting. Once a newer attempt has
been issued and has not yet returned, that no-capacity reply no longer
describes the situation — a refusal comes back within seconds of trying each
zone, whereas a successful attempt holds its request while the instance boots —
so the line SHALL say it is starting. Before any attempt has returned, the
line SHALL also say it is starting. Each per-poll retry notice (reporting the
state and the wait before the next attempt) SHALL continue to be reported as it
happens, independently of the periodic line.

#### Scenario: A cold start is not silent

- **WHEN** the endpoint takes minutes to become ready
- **THEN** the command explains what it is waiting for and continues to report
  the elapsed time until it succeeds

#### Scenario: Waiting for capacity is not reported as booting

- **WHEN** the most recent reply reports no capacity in any zone and no newer
  attempt is in flight
- **THEN** the periodic progress line says it is waiting for capacity, not that
  the instance is still starting

#### Scenario: Booting is reported as starting

- **WHEN** the most recent reply reports the instance is booting, or no attempt
  has returned yet
- **THEN** the periodic progress line says it is still starting

#### Scenario: Booting after a capacity wait is not reported as waiting

- **WHEN** the most recent reply reported no capacity in any zone and the
  client has since issued another attempt that finds capacity and is booting
- **THEN** the periodic progress line says it is starting, not that it is still
  waiting for capacity

#### Scenario: Only the result is on standard output

- **WHEN** a start succeeds and its output is captured
- **THEN** standard output holds exactly the environment exports, with every
  progress line on standard error
