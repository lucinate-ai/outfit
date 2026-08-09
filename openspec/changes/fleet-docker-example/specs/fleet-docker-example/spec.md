## Purpose

Define the dockerised fleet example: a multi-node fleet anyone can run on one
machine, where each node is a real `outfit daemon` supervising a real engine
process — and the fleet behaviours its test run asserts, so the example is
verified rather than merely documented.

## ADDED Requirements

### Requirement: A runnable multi-node fleet

The repository SHALL provide an example that brings up several nodes on one
machine with a single command, each node running a real `outfit daemon` serving
its control API, addressed by a `fleet.yaml` shipped beside it. Bringing the
example up SHALL require no GPU, no cloud account, and no inference engine
installed on the host.

#### Scenario: A user brings up a fleet

- **WHEN** a user runs the example's compose stack and then `outfit fleet status`
  against its `fleet.yaml`
- **THEN** every node in the file is reported, each showing the state of a real
  daemon

### Requirement: The daemon supervises a real engine process

Each node's engine SHALL be a real process the daemon starts and supervises —
not a stub served by the daemon itself and not a separate container — so that
starting, stopping, log capture, and crash detection are genuinely exercised.
The engine SHALL serve the metrics endpoint outfit scrapes, in the dialect
outfit parses, so token statistics are produced end to end rather than faked.

#### Scenario: The engine is a child of the daemon

- **WHEN** a node's engine is running
- **THEN** it is a child process of that node's daemon, and stopping the engine
  through the control API terminates that process

#### Scenario: Token statistics come from the engine

- **WHEN** `outfit fleet metrics` runs against a node whose engine is running
- **THEN** the reported token and request counters are those the engine's
  metrics endpoint served, parsed by outfit's own collector

### Requirement: Usable from cold

The example SHALL be usable when no engine is running: bringing the stack up
SHALL leave each node's daemon answering with nothing started, and a user SHALL
be able to start a node's engine through `outfit fleet` without editing
configuration or restarting a container.

#### Scenario: Nothing running is still a usable fleet

- **WHEN** the stack is up and no engine has been started
- **THEN** `outfit fleet status` reports every node as `idle` and succeeds

#### Scenario: Starting a node from cold

- **WHEN** `outfit fleet start <node>` runs against a node whose engine has
  never been started
- **THEN** that node's daemon starts its engine and subsequently reports
  `running`

### Requirement: The example is verified, not just documented

The example SHALL ship a test run that drives the same stack non-interactively
and asserts the fleet behaviours below, and that test run SHALL be executed in
continuous integration — so the example cannot silently stop working.

#### Scenario: CI runs the example

- **WHEN** continuous integration runs for the repository
- **THEN** the example's stack is brought up, its test run executes, and a
  failure fails the build

#### Scenario: A stopped node degrades the view without failing it

- **WHEN** one node's container is stopped and `outfit fleet status` runs
- **THEN** that node is reported as `unreachable` with a reason, every other
  node still reports normally, and the command succeeds

#### Scenario: A rejected token is distinguished from an unreachable node

- **WHEN** the client presents a token a node's daemon rejects
- **THEN** that node is reported as `unauthorized`, not `unreachable`

#### Scenario: A crashed engine is reported and recoverable

- **WHEN** a node's engine process is killed abnormally
- **THEN** that node reports `crashed`, and `outfit fleet start <node>` returns
  it to `running`

#### Scenario: Driving one node

- **WHEN** `outfit fleet start <node>` and `outfit fleet stop <node>` run
  against a node in the stack
- **THEN** that node's engine starts and stops accordingly, and no other node
  is affected
