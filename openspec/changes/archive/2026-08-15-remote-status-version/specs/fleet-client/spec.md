## MODIFIED Requirements

### Requirement: Fleet status

`outfit fleet status` SHALL query every node's daemon status endpoint and render one row per node: the node name, its engine state (`idle`/`running`/`stopped`/`crashed`), what it is serving (runner and model when known), the outfit version of the daemon on that node, and its reachability. Nodes SHALL be queried concurrently so the command's latency is that of the slowest reachable node, not their sum.

A node SHALL also report how long it has been since its engine last did work, taken from the activity its daemon tracks — "which of my nodes is doing nothing?" is a question a fleet view exists to answer, and the daemon already knows. That figure SHALL NOT be labelled in a way that collides with the `idle` engine state, which means something different. A node whose daemon reports no activity yet SHALL omit the figure rather than imply an engine has sat unused since it started.

#### Scenario: Mixed fleet renders every node

- **WHEN** `outfit fleet status` runs against a fleet of several nodes
- **THEN** the output has one row per node showing its state, version, and what it serves

#### Scenario: A node reports how long since it last did work

- **WHEN** `outfit fleet status` runs against a node whose daemon reports a last-active time
- **THEN** that node's row shows how long ago that was, labelled so it is not confused with the `idle` engine state

#### Scenario: A node with no recorded activity omits the figure

- **WHEN** a node's daemon reports no last-active time, because its engine has done no work yet
- **THEN** that node's row shows no activity figure rather than a misleading one

#### Scenario: Version is shown per node

- **WHEN** `outfit fleet status` runs against a fleet of running nodes
- **THEN** each node's row includes the outfit version string from its daemon

#### Scenario: Version is omitted for unreachable nodes

- **WHEN** a node's daemon is unreachable
- **THEN** that node's row shows its failure outcome without a version
