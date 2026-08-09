## Purpose

Define the `outfit fleet` command family: the client that reads `fleet.yaml`,
polls each node's daemon control API, and renders the cluster — observing every
engine and driving individual ones, degrading gracefully when a node cannot be
reached.

## ADDED Requirements

### Requirement: Fleet status

`outfit fleet status` SHALL query every node's daemon status endpoint and
render one row per node: the node name, its engine state
(`idle`/`running`/`stopped`/`crashed`), what it is serving (runner and model
when known), and its reachability. Nodes SHALL be queried concurrently so the
command's latency is that of the slowest reachable node, not their sum.

#### Scenario: Mixed fleet renders every node

- **WHEN** `outfit fleet status` runs against a fleet of several nodes
- **THEN** the output has one row per node showing its state and what it serves

### Requirement: Unreachable nodes degrade

A node that does not yield a result SHALL be shown with a typed outcome and a
short reason, distinguishing what went wrong:

- `unreachable` — the daemon could not be contacted at all (connection
  refused, timeout, DNS failure);
- `unauthorized` — the daemon rejected the client's bearer token;
- `config-error` — the node could not be called, typically a token reference
  that resolves to nothing;
- `failed` — the daemon answered with an error (a refused start, an
  unservable config): the node is healthy, the request was not.

Such a node SHALL NOT abort the command: every other node's result SHALL still
render, and the command SHALL succeed.

#### Scenario: One node down, the rest still shown

- **WHEN** `outfit fleet status` runs and one node's daemon is unreachable
- **THEN** that node's row reads `unreachable` with its reason, the other nodes
  render normally, and the command exits successfully

#### Scenario: Bad token is distinguished from unreachable

- **WHEN** a node's daemon rejects the client's bearer token
- **THEN** that node's row reads `unauthorized`, not `unreachable`

#### Scenario: A refused request is distinguished from an unreachable node

- **WHEN** a node's daemon answers a request with an error, such as a start
  while its engine is already running
- **THEN** that node's outcome reads `failed` with the daemon's own message,
  not `unreachable` — the node was reached, the request was refused

### Requirement: Fleet metrics

`outfit fleet metrics` SHALL query every node's metrics endpoint and render
each node's engine and system metrics using the same bar, table, and json
formats `outfit remote metrics` provides, selected by `--format`. Unreachable
nodes SHALL be reported as in status rather than omitted. The command SHALL
support a `--watch`/`-w` mode that refreshes on an interval, clearing and
redrawing the screen in place with no scrollback accumulation, and exiting
cleanly on interrupt.

#### Scenario: Bar format per node

- **WHEN** `outfit fleet metrics` runs without `--format`
- **THEN** each reachable node's metrics render in bar format under its name

#### Scenario: JSON aggregates the fleet

- **WHEN** `outfit fleet metrics --format=json` runs
- **THEN** the output is valid JSON keyed or labelled by node, including
  unreachable nodes with their error

#### Scenario: Watch redraws in place

- **WHEN** `outfit fleet metrics --watch` runs
- **THEN** each refresh clears the screen and redraws the fleet, and Ctrl+C
  exits cleanly

### Requirement: Driving one node

`outfit fleet start <node>` and `outfit fleet stop <node>` SHALL call the named
node's daemon start and stop endpoints. Start and stop SHALL require a node
name: invoked without one they SHALL fail and list the available nodes, rather
than acting on the whole fleet. An unknown node name SHALL fail, naming the
known nodes. The daemon's own rules still hold — a start while that node's
engine is running is reported as the daemon's conflict, and a stop is
idempotent.

#### Scenario: Start a named node

- **WHEN** `outfit fleet start gpu-box` runs and that node is idle
- **THEN** the client calls that node's daemon start endpoint and reports the
  resulting state

#### Scenario: Start with no node names the fleet

- **WHEN** `outfit fleet start` runs with no node argument
- **THEN** it fails, listing the nodes, and starts nothing

#### Scenario: Unknown node

- **WHEN** `outfit fleet stop nope` runs and no node is named `nope`
- **THEN** it fails, naming the known nodes, and stops nothing

### Requirement: Authenticated fan-out

Every request the client makes to a node SHALL carry that node's resolved
bearer token when one is configured, as the daemon control API requires. A
node configured with a token whose env var is unset SHALL be reported as a
configuration error for that node (distinct from an unreachable node), without
aborting the rest of the fleet.

#### Scenario: Missing token env var is a per-node error

- **WHEN** a node references a token env var that is not set
- **THEN** that node reports a configuration error and the other nodes still
  render
