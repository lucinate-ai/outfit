## MODIFIED Requirements

### Requirement: Rendering-compatible stats shape

The collected metrics SHALL be expressible in the same stats shape the
`outfit remote metrics` formatters render (state, runner, model, GPU, CPU,
RAM, token stats, and the engine's last-active time with the idle duration
derived from it), so the existing bar, table, and JSON formats display
in-process metrics without format-specific changes.

The last-active time SHALL be carried as an RFC 3339 timestamp and the idle
duration as whole seconds. The two SHALL travel together: either both are
present or neither is. They SHALL be absent — rather than zero or empty — when
no engine has run, so a consumer can tell "nothing has happened yet" from
"activity happened at the epoch".

Unlike the system and token figures, which describe a running engine, the
last-active pair SHALL be carried whatever the engine's state, including a
stopped or crashed one — the value of keeping the record across a stop is that
it still answers when work last happened.

#### Scenario: Collected metrics render with existing formats

- **WHEN** in-process metrics are rendered
- **THEN** the bar, table, and JSON formats produce output with the same
  structure as remote metrics for the same data

#### Scenario: Activity travels with the stats

- **WHEN** metrics are collected for an engine that has done work
- **THEN** the result carries the last-active timestamp and the seconds since
  it, alongside the token and system figures

#### Scenario: No activity yet is absent, not zero

- **WHEN** metrics are collected and no engine has ever run
- **THEN** the result carries neither a last-active time nor an idle duration

#### Scenario: A stopped engine still reports when it last worked

- **WHEN** metrics are collected for an engine that has been stopped after
  doing work
- **THEN** the result carries the last-active time and idle duration even
  though the running-engine figures are absent
