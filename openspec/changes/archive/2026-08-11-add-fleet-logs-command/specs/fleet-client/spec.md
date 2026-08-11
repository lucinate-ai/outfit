## ADDED Requirements

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
