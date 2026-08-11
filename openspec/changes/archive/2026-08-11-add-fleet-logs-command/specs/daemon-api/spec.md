## ADDED Requirements

### Requirement: Engine log endpoint

The API SHALL provide a read-only JSON endpoint returning the supervised
engine's captured output — the same output whose path status reports — so a
caller can read what the engine said without shell access to the machine.
Reading logs SHALL NOT change the engine's state, and SHALL be reachable
whether the engine is running, stopped or crashed: the output of a crash is
wanted precisely when the engine is no longer there to ask.

#### Scenario: Reading a running engine's output

- **WHEN** a logs request is made while the engine is running and has produced
  output
- **THEN** the response carries that output
- **AND** the engine's state is unchanged

#### Scenario: Reading after a crash

- **WHEN** the engine has exited and a logs request is made
- **THEN** the response still carries the output it produced before exiting

### Requirement: Log reads are bounded and resumable

A logs response SHALL be bounded: the endpoint SHALL never return the whole
file merely because no bound was asked for, since an engine's log grows without
limit and the daemon does not rotate it. A caller SHALL be able to state how
much it wants, and the endpoint SHALL cap that at a limit of its own so a
client cannot ask a node to read an unbounded amount into memory.

With no position stated, the response SHALL carry the **end** of the log — the
most recent output — rather than its beginning, because the recent end is what
diagnosis needs. Every response SHALL carry the position immediately after what
it returned, and a caller supplying that position SHALL receive only what has
been appended since. Following a log SHALL therefore be exact: no overlap
window, no duplicate suppression, no reliance on timestamps the output may not
carry.

#### Scenario: An unbounded request is still bounded

- **WHEN** a logs request states no bound
- **THEN** the response carries at most the endpoint's own maximum
- **AND** it is taken from the end of the log, not the beginning

#### Scenario: A cursor returns only what is new

- **WHEN** a caller repeats a logs request with the position from its previous
  response
- **THEN** the response carries only the output appended since that position
- **AND** it carries a new position for the next request

#### Scenario: Nothing new yields nothing

- **WHEN** a caller supplies the current end position and the engine has
  written nothing since
- **THEN** the response carries no output and the same position

### Requirement: A log that cannot be read is reported, not faked

The endpoint SHALL distinguish the states a caller can act on rather than
returning an empty log for all of them. When no log file exists — no engine has
ever run on this node, or the daemon was configured to forward output to its own
stdio instead of a file — the response SHALL say so distinctly from a log that
exists and is empty. When a supplied position no longer makes sense because the
log is shorter than it — the file was truncated or replaced underneath the
caller — the response SHALL report that rather than silently returning nothing,
so the caller can resume from the end instead of waiting forever on a position
that will never be reached.

#### Scenario: No engine has ever run

- **WHEN** a logs request is made on a node whose engine has never started
- **THEN** the response reports that there is no log to read
- **AND** it is distinguishable from an engine that ran and logged nothing

#### Scenario: The log was truncated under the caller

- **WHEN** a caller supplies a position beyond the log's current end
- **THEN** the response reports that the position is no longer valid
- **AND** it carries the log's current end so the caller can resume
