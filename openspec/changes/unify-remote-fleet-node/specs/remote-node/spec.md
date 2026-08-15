## Purpose

Let a remote, scale-to-zero inference environment — one driven through its cloud control
plane — be observed and driven the same way a fleet node is, so the two clients share one
driver and one source for its status facts instead of each keeping its own copy.

## ADDED Requirements

### Requirement: A remote environment is a fleet node

A registered remote environment SHALL be representable as one member of the fleet's node
set, answering the same operations a local node answers: its status, its metrics, and
being started, stopped, and read for logs. The control plane's replies SHALL be mapped
onto the same status and metrics shapes a local node yields, so downstream fan-out and
rendering treat the two identically.

A remote environment that cannot be reached, or whose control call is rejected —
including a rejected AWS credential — SHALL be reported as a typed outcome against that
environment, the same way an unreachable or unauthorized node is, rather than failing the
command or being silently dropped.

Because a remote endpoint is provisioned by deployment rather than woken like a node, a
node-level start asked to run on a supplied deploy configuration SHALL be refused with a
message naming the deployment path, rather than attempted.

#### Scenario: A remote environment answers status like a node

- **WHEN** a remote environment is asked for its status as a member of a node set
- **THEN** it returns a status carrying the endpoint's state and, when the engine has done
  work, its last-active time, in the same shape a local node's status carries

#### Scenario: A remote environment answers metrics like a node

- **WHEN** a running remote environment is asked for its metrics as a member of a node set
- **THEN** it returns the token and system figures in the same stats shape a local node
  returns

#### Scenario: A rejected control call is a typed outcome

- **WHEN** a remote environment's status or metrics call is rejected, for example because
  the caller's credentials are not valid
- **THEN** the environment is reported with a failure outcome and the reason, and it does
  not abort or blank the rest of the node set

#### Scenario: Waking a remote environment is refused

- **WHEN** a node-level start is requested for a remote environment, carrying a deploy
  configuration
- **THEN** it is refused with a message naming the deployment path, and the environment
  is not started

### Requirement: Fan-out runs over an explicit node set

Observation fan-out SHALL run over an explicit set of nodes and return one result per
node, in the order the set is given, so a set that mixes local nodes and a remote
environment is observed identically to a set of local nodes alone. Building or reaching
one member that fails SHALL NOT stop the rest of the set from being observed.

#### Scenario: A mixed set is observed in order

- **WHEN** fan-out runs over a set containing a local node and a remote environment
- **THEN** one result is returned per member, in the order given, so the rendering is
  stable between refreshes

#### Scenario: One failing member does not stop the set

- **WHEN** one member of the set cannot be reached or its call is rejected
- **THEN** it is reported with its outcome and reason, and every other member is still
  observed

### Requirement: Status facts are shared between the remote and fleet views

The remote status view and the fleet status view SHALL derive their overlapping facts —
the endpoint's or node's state, what it is serving, how long since it last did work, and
its outfit version — from a single shared source, so no fact is computed differently or
worded differently by the two. Where a view carries facts the other does not, it SHALL
still render them without changing the shared ones: the remote view keeps the endpoint's
address and health, and the fleet view keeps its one-node-per-row table.

#### Scenario: The shared facts agree

- **WHEN** the same endpoint's state, what it serves, its last-active time, and its
  version are shown by both the remote and the fleet status views
- **THEN** the two show the same values with the same wording

#### Scenario: Additional facts do not change the shared ones

- **WHEN** the remote status view shows its endpoint's address and health alongside the
  shared facts
- **THEN** the shared facts render identically to how the fleet view renders them
