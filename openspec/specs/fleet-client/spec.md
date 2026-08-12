# fleet-client Specification

## Purpose
Define the `outfit fleet` command family: the client that reads `fleet.yaml`,
polls each node's daemon control API, and renders the cluster — observing every
engine and driving individual ones, degrading gracefully when a node cannot be
reached.
## Requirements
### Requirement: Fleet status

`outfit fleet status` SHALL query every node's daemon status endpoint and
render one row per node: the node name, its engine state
(`idle`/`running`/`stopped`/`crashed`), what it is serving (runner and model
when known), and its reachability. Nodes SHALL be queried concurrently so the
command's latency is that of the slowest reachable node, not their sum.

A node SHALL also report how long it has been since its engine last did work,
taken from the activity its daemon tracks — "which of my nodes is doing
nothing?" is a question a fleet view exists to answer, and the daemon already
knows. That figure SHALL NOT be labelled in a way that collides with the
`idle` engine state, which means something different. A node whose daemon
reports no activity yet SHALL omit the figure rather than imply an engine has
sat unused since it started.

#### Scenario: Mixed fleet renders every node

- **WHEN** `outfit fleet status` runs against a fleet of several nodes
- **THEN** the output has one row per node showing its state and what it serves

#### Scenario: A node reports how long since it last did work

- **WHEN** `outfit fleet status` runs against a node whose daemon reports a
  last-active time
- **THEN** that node's row shows how long ago that was, labelled so it is not
  confused with the `idle` engine state

#### Scenario: A node with no recorded activity omits the figure

- **WHEN** a node's daemon reports no last-active time, because its engine has
  done no work yet
- **THEN** that node's row shows no activity figure rather than a misleading
  one

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

### Requirement: Fleet logs

`outfit fleet logs` SHALL read the engine output of the fleet's nodes through
each node's daemon, so "what did that engine say?" is answerable from the same
place as "what is it doing?" — without shell access to any machine. With no node
named it SHALL read every node in the fleet; naming a node SHALL restrict it to
that one. Nodes SHALL be read concurrently, so the command's latency is that of
the slowest reachable node rather than their sum.

#### Scenario: Reading the whole fleet

- **WHEN** the operator runs `outfit fleet logs` with no node named
- **THEN** every node's engine output is read and printed

#### Scenario: Reading one node

- **WHEN** the operator names a node
- **THEN** only that node's output is printed, and the other nodes are not
  contacted

#### Scenario: A crashed node's output is readable

- **WHEN** a node's engine has crashed, as `outfit fleet status` reports
- **THEN** its output up to the crash is printed, explaining what status can
  only report

### Requirement: Fleet log lines are attributed to their node

When output from more than one node is printed, every line SHALL identify the
node it came from, since interleaved output from several machines is misleading
otherwise. When the output is from a single node — because the fleet holds one,
or because a node was named — that attribution SHALL be omitted, so the common
case of reading one node reads like that node's own log.

Lines SHALL NOT be interleaved across nodes by time: the daemon returns captured
output as the engine wrote it, and an engine's output carries no timestamp the
client can rely on, so each node's output SHALL be kept in its own order rather
than merged into a false chronology.

#### Scenario: Several nodes are labelled

- **WHEN** output from more than one node is printed
- **THEN** each line identifies its node

#### Scenario: A single node is not labelled

- **WHEN** every printed line comes from one node
- **THEN** the lines carry no node prefix

### Requirement: Fleet logs can be followed

`outfit fleet logs` SHALL be able to keep running and print output as nodes
produce it, rather than exiting after one read. Following SHALL resume each node
from the position that node last returned, so a line already printed is never
printed twice. Interrupting SHALL exit cleanly, without reporting an error.

#### Scenario: New output appears

- **WHEN** the operator follows the fleet's logs and a node's engine writes more
  output
- **THEN** that output is printed as it arrives, attributed to its node

#### Scenario: No duplicates across polls

- **WHEN** following continues across several polls
- **THEN** no line that has already been printed is printed again

#### Scenario: Interrupting stops cleanly

- **WHEN** the operator interrupts a follow
- **THEN** the command exits without reporting an error

### Requirement: A node that cannot supply logs does not fail the command

Reading logs SHALL degrade per node in the same way the rest of the fleet
commands do: a node that cannot be reached, that rejects the client's
credentials, that has never run an engine, or whose daemon is too old to serve
logs SHALL be reported against that node while every other node's output is
still printed. The command SHALL NOT fail as a whole because one node could not
answer.

#### Scenario: One node is unreachable

- **WHEN** one node cannot be reached and the others can
- **THEN** the reachable nodes' output is printed
- **AND** the unreachable node is reported as such

#### Scenario: A node has never run an engine

- **WHEN** a node has no engine log because nothing has ever run there
- **THEN** that is reported for that node, distinctly from a node that failed
  to answer

#### Scenario: A node's daemon predates the endpoint

- **WHEN** a node's daemon does not serve the logs endpoint
- **THEN** that node is reported as needing an upgrade, naming what is missing
- **AND** the other nodes' output is unaffected

### Requirement: Explaining a route

`outfit fleet route` SHALL report the node a harness launch would choose for a
given Outfit, the endpoint that node resolves to, and why it was chosen — and
SHALL change nothing: it SHALL never push a config, start an engine, or write a
harness config. It is how a routing decision is checked before an agent depends
on it, and how an unexpected choice is diagnosed after one.

When no node would be chosen, it SHALL report each node's state and the reason
it was passed over, and SHALL name what would happen on a real launch: which
node would be woken, or that none could serve it.

The Outfit and the fleet file SHALL resolve as they do for a launch: the Outfit
path defaults to `./Outfit`, and `--fleet` overrides the Outfit's `FLEET`. It
SHALL accept `--prefer` and `--node` as a launch does, and SHALL name the
activity preference in force — comparing the two preferences on a live fleet is
the cheapest way to decide which one a fleet should be run with.

#### Scenario: The chosen node is explained

- **WHEN** `outfit fleet route` runs against a fleet with a node serving the
  Outfit's model
- **THEN** it prints that node, its resolved engine endpoint, and why it was
  chosen

#### Scenario: Routing changes nothing

- **WHEN** `outfit fleet route` runs against a fleet where no node is serving
  the Outfit's model
- **THEN** no engine is started, no config is pushed, and no harness config is
  written

#### Scenario: The two preferences can be compared

- **WHEN** `outfit fleet route --prefer active` runs against a fleet whose file
  declares `prefer: idle`
- **THEN** it reports the node `active` would choose and names that preference,
  without changing the fleet file

#### Scenario: A launch that would wake a node says so

- **WHEN** `outfit fleet route` runs and no node is serving the model but one
  could
- **THEN** it names the node a launch would wake, and does not wake it

