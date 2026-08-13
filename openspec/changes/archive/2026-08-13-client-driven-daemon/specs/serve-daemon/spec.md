## MODIFIED Requirements

### Requirement: The daemon command

`outfit daemon` SHALL run a long-lived foreground process that supervises the
engine and serves the control API — always on, the API being the command's
purpose, under the same listen-address and token rules as any API exposure. It
SHALL take no Outfit path: what it runs comes from its API, not from the
directory it was started in. It SHALL NOT start an engine on boot — even when a
stored deploy config is present — and SHALL wait idle for API requests; the
engine starts only on a start request. The daemon SHALL keep running when the
engine exits or is stopped over the API, answering subsequent requests, and
SHALL exit cleanly on `SIGINT`/`SIGTERM`, stopping a running engine before
exiting. Backgrounding the daemon is the user's concern (tmux, systemd,
launchd); the daemon itself SHALL NOT detach.

An Outfit path given to `outfit daemon` SHALL be rejected with an error saying
the daemon is driven by its API, and naming the start request as the way to say
what to serve — rather than being accepted and quietly ignored.

#### Scenario: Daemon does not auto-start

- **WHEN** `outfit daemon` runs on a machine with a stored deploy config
- **THEN** no engine starts, and status reports `idle` until a start request
  arrives

#### Scenario: An Outfit path is refused

- **WHEN** `outfit daemon ./Outfit` runs
- **THEN** it fails saying the daemon takes no Outfit, and points at the start
  request as the way to say what to serve

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

## REMOVED Requirements

### Requirement: What the daemon serves

**Reason**: The Outfit was one of three sources of what to serve, and it is the
one a node should not have. Removing it changes the requirement's substance
rather than adding to it — including dropping the scenario that asserts a bare
start serves an adjacent Outfit — so it is replaced rather than edited.

**Migration**: Push a deploy config, or carry one on the start request. A daemon
that was started beside an Outfit and relied on a bare start serving it must now
be told what to run: `outfit fleet start <node>` after one push, or a routed
`outfit harness`, which carries the config itself.

## ADDED Requirements

### Requirement: What the daemon runs

When starting an engine, the daemon SHALL determine what to serve in this
order: a deploy config carried by the start request itself; else a deploy config
previously pushed and stored for this daemon. With neither, a start request
SHALL fail saying there is nothing to serve and naming what would supply it.

The daemon SHALL NOT read an Outfit, a preset, or a fleet file for this or any
other purpose. A node runs what it is told to run: workload configuration
belongs to the client that asks, and a fleet file is that client's map of the
fleet, which a node has no use for and should not be handed.

A pushed or start-carried deploy config SHALL be persisted so a restarted daemon
serves the same thing on its next start.

#### Scenario: A bare start serves the stored config

- **WHEN** a deploy config was pushed to the daemon and a start request with no
  body arrives
- **THEN** the engine starts serving the stored config

#### Scenario: Nothing to serve fails the start

- **WHEN** a bare start request arrives with no stored deploy config
- **THEN** the start fails saying there is nothing to serve and how to supply
  it, and the daemon keeps running

#### Scenario: An adjacent Outfit is not a source

- **WHEN** `outfit daemon` runs in a directory holding an `Outfit`, with no
  stored deploy config, and a bare start request arrives
- **THEN** the start fails as having nothing to serve: the adjacent file is not
  read

#### Scenario: Stored deploy config survives restart

- **WHEN** a deploy config was pushed to the daemon and the daemon is
  restarted
- **THEN** a bare start request serves the stored config
