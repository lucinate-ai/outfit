## MODIFIED Requirements

### Requirement: Control endpoints

The API SHALL provide JSON endpoints to: report status (engine state, what is
being served, the engine log path, and when the engine was last active); start
the engine; stop the engine; return collected metrics; and accept a deploy
config. Start SHALL accept an optional deploy config in its request body —
validated and persisted exactly as a config push, then started — so a client
can say what to run and run it in one call; without a body, start uses the
stored config or the Outfit. Start SHALL fail when an engine is already
running, changing nothing — a body sent with a rejected start SHALL NOT be
stored. Stop SHALL succeed when nothing is running (idempotent), and stopping
the engine SHALL never terminate `outfit daemon` — the API keeps answering.
Errors SHALL be returned as JSON with a message and a meaningful HTTP status.

Status SHALL report the engine's last-active time as an RFC 3339 timestamp and
the idle duration derived from it in seconds, so a caller can judge idleness
from a decision the daemon has already made rather than from raw counters it
would have to compare itself. Both SHALL be omitted when no engine has ever
run, and neither SHALL be inferred by the caller from any other field.

#### Scenario: Status reports the supervised state

- **WHEN** a status request is made
- **THEN** the response reports the engine state, the model/runner being
  served (when known), and the engine log path

#### Scenario: Status reports engine activity

- **WHEN** a status request is made while an engine is running that has served
  work
- **THEN** the response reports the last-active timestamp and the number of
  seconds since it

#### Scenario: Status omits activity when there is none to report

- **WHEN** a status request is made on a daemon that has never started an
  engine
- **THEN** the response carries no last-active timestamp and no idle duration

#### Scenario: Start and stop drive the engine

- **WHEN** a start request is made while the engine is `idle`, `stopped`, or
  `crashed`
- **THEN** the daemon starts the engine and the response reports the new state

#### Scenario: Start carries its own deploy config

- **WHEN** a start request carries a deploy config naming a servable runner
  and model, and no engine is running
- **THEN** the config is validated, persisted, and the engine starts serving
  it

#### Scenario: Start body is rejected while running

- **WHEN** a start request carrying a deploy config arrives while an engine
  is running
- **THEN** the request fails as already-running, the running engine is
  untouched, and the carried config is not stored

#### Scenario: Stop is idempotent

- **WHEN** a stop request is made while no engine is running
- **THEN** the response succeeds, reporting the engine as not running

#### Scenario: Stop never ends the daemon

- **WHEN** a stop request stops the engine under `outfit daemon`
- **THEN** the daemon and its API keep running, and a later start request
  succeeds

#### Scenario: Metrics endpoint returns collected stats

- **WHEN** a metrics request is made
- **THEN** the response is the in-process collected metrics in the
  rendering-compatible stats shape
