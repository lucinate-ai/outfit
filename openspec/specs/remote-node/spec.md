# remote-node Specification

## Purpose
Let a cloud, scale-to-zero inference environment — one driven through its cloud control
plane — be observed and driven the same way a fleet node is, so the two clients share one
driver and one source for its status facts instead of each keeping its own copy.

## Requirements

### Requirement: A cloud environment is a fleet node

A registered cloud environment SHALL be representable as one member of the fleet's node
set, answering the same operations a local node answers: its status, its metrics, and
being started, stopped, and read for logs. The control plane's replies SHALL be mapped
onto the same status and metrics shapes a local node yields, so downstream fan-out and
rendering treat the two identically. A running environment's status SHALL in particular
carry what its engine is serving — the model it runs, and the served name the deploy gave
it beside the model id when there is one — so a client choosing a node by model matches
it the way it matches a local node, and the fleet view and the cloud view name it the
same. It SHALL also carry where its engine answers — the instance's published address,
which the control plane knows and a daemon on the instance cannot — so a client can
reach the engine, not only name it; a stopped or undeployed environment reports none.

A cloud environment that cannot be reached, or whose control call is rejected —
including a rejected AWS credential — SHALL be reported as a typed outcome against that
environment, the same way an unreachable or unauthorized node is, rather than failing the
command or being silently dropped.

A deployed cloud environment — one whose stored deploy config already describes what to
serve — SHALL answer a node-level start the same way a local node does: the instance is
booted and the call waits for it, without deploying a new configuration. Any deploy
configuration a caller supplies to the start SHALL NOT be pushed onto the environment: it
already knows what to run, and choosing what it runs is `spinloop cloud deploy`'s job, not
a node start's. An undeployed cloud environment — one with no stored deploy config —
SHALL still refuse a node-level start, with a message naming the deployment path, rather
than attempted: starting one would mean choosing what to serve and paying for provisioning
and weights, a heavier decision a node start must not make on a caller's behalf.

#### Scenario: A cloud environment answers status like a node

- **WHEN** a cloud environment is asked for its status as a member of a node set
- **THEN** it returns a status carrying the endpoint's state, what its engine is serving
  (the model, and the served name beside it when the deploy gave one), where its engine
  answers (the instance's published address), and, when the engine has done work, its
  last-active time, in the same shape a local node's status carries

#### Scenario: A freshly loaded engine shows its model before it has done work

- **WHEN** a cloud environment's engine is serving a model but has not yet answered a
  request, so it reports no last-active time
- **THEN** its status still carries the model it is serving, so a router can match a
  request to it before the first request has landed

#### Scenario: A cloud environment answers metrics like a node

- **WHEN** a running cloud environment is asked for its metrics as a member of a node set
- **THEN** it returns the token and system figures in the same stats shape a local node
  returns

#### Scenario: A rejected control call is a typed outcome

- **WHEN** a cloud environment's status or metrics call is rejected, for example because
  the caller's credentials are not valid
- **THEN** the environment is reported with a failure outcome and the reason, and it does
  not abort or blank the rest of the node set

#### Scenario: A deployed cloud environment is started

- **WHEN** a node-level start is requested for a stopped cloud environment whose stored
  deploy config already describes what to serve
- **THEN** the environment's instance is booted, the call waits for it the way a local
  node's start does, and the environment serves what its own stored config names, not
  any config the start call carried

#### Scenario: Waking a cloud environment is refused

- **WHEN** a node-level start is requested for a cloud environment with no stored deploy
  config
- **THEN** it is refused with a message naming the deployment path, and the environment
  is not started

### Requirement: Fan-out runs over an explicit node set

Observation fan-out SHALL run over an explicit set of nodes and return one result per
node, in the order the set is given, so a set that mixes local nodes and a cloud node
environment is observed identically to a set of local nodes alone. Building or reaching
one member that fails SHALL NOT stop the rest of the set from being observed.

#### Scenario: A mixed set is observed in order

- **WHEN** fan-out runs over a set containing a local node and a cloud environment
- **THEN** one result is returned per member, in the order given, so the rendering is
  stable between refreshes

#### Scenario: One failing member does not stop the set

- **WHEN** one member of the set cannot be reached or its call is rejected
- **THEN** it is reported with its outcome and reason, and every other member is still
  observed

### Requirement: Status facts are shared between the cloud and fleet views

The cloud status view and the fleet status view SHALL derive their overlapping facts —
the endpoint's or node's state, what it is serving, how long since it last did work, and
its spinloop version — from a single shared source, so no fact is computed differently or
worded differently by the two. Where a view carries facts the other does not, it SHALL
still render them without changing the shared ones: the cloud view keeps the endpoint's
address and health, and the fleet view keeps its one-node-per-row table.

#### Scenario: The shared facts agree

- **WHEN** the same endpoint's state, what it serves, its last-active time, and its
  version are shown by both the cloud and the fleet status views
- **THEN** the two show the same values with the same wording

#### Scenario: Additional facts do not change the shared ones

- **WHEN** the cloud status view shows its endpoint's address and health alongside the
  shared facts
- **THEN** the shared facts render identically to how the fleet view renders them

### Requirement: The fleet file declares cloud nodes

The fleet file SHALL be able to list a cloud environment as one of its nodes, alongside
daemon nodes. The node's kind SHALL default to daemon. A node of kind `cloud` SHALL be
keyed by its name: the name IS the registered environment it drives (there is no separate
address field, because an environment is already user-named at deployment), and such a
node SHALL need no host, because the environment's control URLs come from that
environment's own config rather than the fleet file. Because the name doubles as the
environment key, it SHALL be constrained to an environment shape — no path separator, no
`.json` suffix — so a path-like name is rejected rather than read as a registry directory.
Building the live node for a `cloud` entry SHALL load that environment's config keyed by
its name, and an environment that is not registered SHALL fail as a per-node
configuration error — naming the environment — rather than failing the command or blanking
the view. A fleet of cloud environments, or of daemons and cloud environments mixed,
SHALL be observable and drivable (status, metrics, start, stop) through the same fan-out
as a fleet of daemons alone.

A node of kind `cloud` MAY declare an optional `instance-type` naming the EC2 instance
type its environment launches as. It is a property of the cloud environment only: a
`kind: daemon` node naming one SHALL be rejected, because a daemon's hardware is the
operator's to choose, not something the fleet file provisions. When present, the value
SHALL be checked for the shape of an EC2 instance type — a lowercase family and size
separated by a single dot, as in `g6e.xlarge` — when the file is read, so a typo is named
at parse rather than at launch. A `kind: cloud` node naming no `instance-type` SHALL
deploy an environment that launches as the control plane's default, unchanged from before
the field existed.

#### Scenario: A fleet file lists a cloud environment as a node

- **WHEN** a fleet file lists a node of kind `cloud` whose name is a registered
  environment
- **THEN** the fan-out builds it as a cloud node and observes it as one row, alongside any
  daemon nodes in the same file

#### Scenario: A fleet file lists a cloud node without its environment

- **WHEN** a fleet file lists a node of kind `cloud` whose name is not a registered
  environment
- **THEN** that node is reported with a configuration error naming the environment, and
  the rest of the fleet is still observed

#### Scenario: A cloud node's name must be env-shaped

- **WHEN** a fleet file lists a node of kind `cloud` whose name contains a path separator
  or a `.json` suffix
- **THEN** the fleet file is rejected, naming the node, because the name is the environment
  key

#### Scenario: A cloud node names its instance type

- **WHEN** a fleet file lists a `kind: cloud` node declaring `instance-type: g6e.2xlarge`
- **THEN** the file parses, and that node's environment is deployed to launch as
  `g6e.2xlarge`

#### Scenario: A daemon node naming an instance type is rejected

- **WHEN** a fleet file lists a `kind: daemon` node declaring an `instance-type`
- **THEN** the file is rejected, naming the node, because instance type is a property of
  a cloud environment only

#### Scenario: A malformed instance type is named at parse

- **WHEN** a fleet file lists a `kind: cloud` node whose `instance-type` is not shaped
  like an EC2 instance type
- **THEN** the file is rejected, naming the node and the value

### Requirement: Reading a cloud environment's logs resumes without duplicating events

A cloud environment's log read SHALL resume from the position it last
returned rather than re-reading its whole tail on every call, so a fleet
node backed by a cloud environment meets the same no-duplicate follow
guarantee a local node meets. It SHALL do so through the same follow cursor
`spinloop cloud logs -f` uses — deduplicating by event id over a shared
overlap window — so the two follows cannot drift into different behavior.

A read that finds nothing SHALL distinguish two states: a from-the-beginning
read that finds nothing, meaning the engine has never logged here, and a
later read that finds nothing new, meaning the log is quiet rather than
missing. Only the former SHALL be reported as a missing log.

Opening a fresh follow of a node — for example, opening the fleet dashboard's
detail view on it — SHALL start the cursor over, so the fresh follow shows
its own tail rather than having it suppressed as already seen by a previous
follow of the same node.

#### Scenario: A follow does not repeat events already shown

- **WHEN** a cloud node's log is polled repeatedly and the engine has
  written no new output between two polls
- **THEN** the second poll returns no content, not the same events again

#### Scenario: New output is shown once

- **WHEN** the engine writes new output between two polls
- **THEN** only the new output is returned by the next poll, not the output
  already returned by a previous one

#### Scenario: A quiet poll is not reported as missing

- **WHEN** a cloud node's log has already shown output, and a later poll
  finds nothing new
- **THEN** the log is not reported as missing

#### Scenario: A log that has never shown output is reported as missing

- **WHEN** a cloud node's log is polled for the first time and the engine
  has never logged anything
- **THEN** the log is reported as missing

#### Scenario: Reopening a follow shows the tail again

- **WHEN** a follow of a cloud node's log is closed and reopened
- **THEN** the reopened follow shows the node's current tail, not an empty
  result because those events were already shown by the previous follow
