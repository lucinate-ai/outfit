## Purpose

Define `outfit daemon`: a long-lived, foreground process that supervises a
single engine — starting it on request, capturing its logs, tracking its
state, stopping it on request — and holds the deploy config that says what to
serve. This is the engine host a fleet node runs, and (in a follow-up change)
the cloud instance too.

## ADDED Requirements

### Requirement: The daemon command

`outfit daemon [path]` SHALL run a long-lived foreground process that
supervises the engine and serves the control API — always on, the API being
the command's purpose, under the same listen-address and token rules as any
API exposure. It SHALL NOT start an engine on boot — even when a stored
deploy config or a resolved Outfit is present — and SHALL wait idle for API
requests; the engine starts only on a start request. The daemon SHALL keep
running when the engine exits or is stopped over the API, answering
subsequent requests, and SHALL exit cleanly on `SIGINT`/`SIGTERM`, stopping a
running engine before exiting. Backgrounding the daemon is the user's concern
(tmux, systemd, launchd); the daemon itself SHALL NOT detach.

#### Scenario: Daemon does not auto-start

- **WHEN** `outfit daemon` runs beside an Outfit that names a self-hosted
  engine
- **THEN** no engine starts, and status reports `idle` until a start request
  arrives

#### Scenario: Daemon survives engine exit

- **WHEN** the supervised engine process exits while the daemon runs
- **THEN** the daemon keeps running and records the engine's new state

#### Scenario: Stop keeps the daemon serving

- **WHEN** the engine started via the API is stopped over the API
- **THEN** the engine stops, the daemon keeps running, and subsequent status,
  metrics, and start requests are answered

#### Scenario: Daemon shutdown stops the engine

- **WHEN** the daemon receives `SIGINT` while the engine is running
- **THEN** the engine is stopped, then the daemon exits cleanly

### Requirement: What the daemon serves

When starting an engine, the daemon SHALL determine what to serve in this
order: a deploy config carried by the start request itself; else a deploy
config previously pushed and stored for this daemon; otherwise the resolved
Outfit (the same resolution foreground `serve` uses, including presets —
`outfit daemon` accepts the same optional Outfit path). With no source at
all, a start request SHALL fail saying there is nothing to serve. A pushed or
start-carried deploy config SHALL be persisted so a restarted daemon serves
the same thing on its next start, and SHALL take precedence over the Outfit
thereafter.

#### Scenario: Bare start serves the Outfit

- **WHEN** `outfit daemon` runs beside an Outfit naming a self-hosted engine
  and a start request with no body arrives
- **THEN** the engine starts serving what the Outfit describes

#### Scenario: Nothing to serve fails the start

- **WHEN** a bare start request arrives with no stored deploy config and no
  Outfit
- **THEN** the start fails, saying there is nothing to serve, and the daemon
  keeps running

#### Scenario: Stored deploy config survives restart

- **WHEN** a deploy config was pushed to the daemon and the daemon is
  restarted
- **THEN** a bare start request serves the stored config

### Requirement: Supervised engine lifecycle

The daemon SHALL track the engine's state as one of `idle` (nothing started),
`running`, `stopped` (stopped on request or exited with success), and
`crashed` (exited unexpectedly). A stop request SHALL terminate the engine
gracefully, escalating to a forced kill after a grace period. The daemon SHALL
NOT restart a crashed engine on its own; a crashed engine restarts only on an
explicit start request.

#### Scenario: Crash is reported, not restarted

- **WHEN** the engine process exits with a non-zero status unprompted
- **THEN** the daemon reports state `crashed` and does not start a new engine
  process

#### Scenario: Stop terminates gracefully

- **WHEN** a stop is requested for a running engine
- **THEN** the engine receives a graceful termination signal, and is force
  killed only if it has not exited after the grace period

### Requirement: One engine per daemon

The daemon SHALL supervise at most one engine at a time. A start request while
an engine is running SHALL fail, naming the running engine; it SHALL NOT stop
or replace the running engine implicitly.

#### Scenario: Start while running fails

- **WHEN** a start is requested and an engine is already running
- **THEN** the request fails with an error naming the running engine, which
  keeps running

### Requirement: Engine log capture

The daemon SHALL write the supervised engine's stdout and stderr to a log
file rather than the daemon's own stdio, and SHALL report the log file's path
in its status so the user can find it.

#### Scenario: Engine output lands in the log file

- **WHEN** a supervised engine writes to stdout or stderr
- **THEN** the output is appended to the engine log file named in the daemon's
  status
