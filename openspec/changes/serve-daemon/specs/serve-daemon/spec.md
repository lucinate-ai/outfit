## Purpose

Define daemon mode for `outfit serve`: a long-lived, foreground process that
supervises a single engine — starting it detached, capturing its logs,
tracking its state, stopping it on request — and holds the deploy config that
says what to serve. This is the consistent engine host that fleet nodes and
(in a follow-up change) cloud instances run.

## ADDED Requirements

### Requirement: Daemon mode

`outfit serve -d`/`--daemon` SHALL run serve as a long-lived foreground
process that supervises the engine rather than executing it with stdio
forwarded. The daemon SHALL keep running when the engine exits, and SHALL exit
cleanly on `SIGINT`/`SIGTERM`, stopping a running engine before exiting.
Backgrounding the daemon is the user's concern (tmux, systemd, launchd); the
daemon itself SHALL NOT detach.

#### Scenario: Daemon survives engine exit

- **WHEN** the supervised engine process exits while the daemon runs
- **THEN** the daemon keeps running and records the engine's new state

#### Scenario: Daemon shutdown stops the engine

- **WHEN** the daemon receives `SIGINT` while the engine is running
- **THEN** the engine is stopped, then the daemon exits cleanly

### Requirement: Agent mode

`outfit daemon` SHALL run the same daemon as a pure agent: it SHALL NOT start
an engine on boot — even when a stored deploy config or an adjacent Outfit is
present — and SHALL wait idle for API requests instead. The engine starts
only on a start request, whose deploy config resolves through the same source
order as any start (request payload, then stored config, then Outfit).
Stopping the engine over the API SHALL leave the daemon running and
answering subsequent API calls; only a signal ends `outfit daemon`. The
control API is the command's whole purpose, so it SHALL be on, with the same
listen address and token rules as `serve --daemon`.

#### Scenario: Agent mode does not auto-start

- **WHEN** `outfit daemon` runs beside an Outfit that names a self-hosted
  engine
- **THEN** no engine starts, and status reports `idle` until a start request
  arrives

#### Scenario: Stop keeps the agent serving

- **WHEN** the engine started via `outfit daemon` is stopped over the API
- **THEN** the engine stops, the daemon keeps running, and subsequent status,
  metrics, and start requests are answered

### Requirement: What the daemon serves

When starting an engine, the daemon SHALL determine what to serve in this
order: a deploy config carried by the start request itself; else a deploy
config previously pushed and stored for this daemon; otherwise the resolved
Outfit (the same resolution foreground `serve` uses, including presets).
Under `serve --daemon`, when a stored config or Outfit is available at boot
the daemon SHALL start the engine immediately; with neither, it SHALL start
idle and wait for a deploy config. A pushed or start-carried deploy config
SHALL be persisted so a daemon restart serves the same thing, and SHALL take
precedence over the Outfit thereafter.

#### Scenario: Daemon with an Outfit starts the engine

- **WHEN** `outfit serve -d` runs in a directory whose Outfit names a
  self-hosted engine
- **THEN** the daemon starts that engine on boot

#### Scenario: Daemon with nothing to serve idles

- **WHEN** `outfit serve -d` runs with no Outfit and no stored deploy config
- **THEN** the daemon starts idle, reporting no engine, and does not error

#### Scenario: Stored deploy config survives restart

- **WHEN** a deploy config was pushed to the daemon and the daemon is
  restarted
- **THEN** the restarted daemon starts the engine from the stored config

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
