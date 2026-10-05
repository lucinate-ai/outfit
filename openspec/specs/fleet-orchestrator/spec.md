# fleet-orchestrator Specification

## Purpose

Let a backlog of work run against a fleet at a pace the fleet can absorb: the
`spinloop orchestrator` command holds the work items, reads the fleet's
topology from the fleet's gateway, admits items while the fleet's declared
concurrency limits allow, and runs each admitted item as a one-shot agent
whose inference goes through that gateway.

## Requirements

### Requirement: The orchestrator command

`spinloop orchestrator` SHALL run as a long-running foreground process, the
way `spinloop gateway` and `spinloop serve` do: its lifecycle is the
process's, and it is a top-level command beside them.

It SHALL take the address of a fleet's gateway and the path of its work items
file. The gateway's address comes from an explicit flag, or — where no flag
is given — from the `gateway` section of a fleet file, the one an explicit
fleet flag names or the file in the working directory; the flag, where
given, wins. The items file is a flag with a default of `./work.yaml` in the
working directory. It SHALL authenticate to the gateway with a bearer token
resolved the way the fleet file's gateway section resolves one: a flag
naming the environment variable, defaulting to `OPENAI_API_KEY` — the
section's own variable where the gateway comes from the file and the flag
was not given — and, where the gateway comes from the file, the value read
from the process environment first, then the `.env` beside the fleet file.

The fleet file, where the command reads it, is read for the gateway's
address and the token's variable and nothing else: no node fact in it
reaches the run, and the gateway is the run's only view of the fleet. Where
neither a flag nor a readable fleet file's section names a gateway, the
command SHALL fail before it works an item, naming the flag and the file. A
gateway it cannot reach, or will not authenticate it to, SHALL stop the
command with a message naming the gateway.

The command SHALL take a `--create-item-dirs` flag, defaulting to false:
where it is set, the orchestrator creates an item's missing directory before
launching its agent; where it is not, a missing directory fails the item.

The command SHALL take a `--listen` flag, defaulting to loopback port 4010:
where it is set, the orchestrator serves the work list API on that address
for the life of the run. The command SHALL take a `--loopback` flag that
selects loopback port 4010: it SHALL refuse to run the API on an address
that is not loopback unless an API token is set, and the API token is a flag
or a flag naming the file that holds the token — it SHALL resolve the flag
first, then the file, and SHALL NOT write it anywhere. The API token is the
orchestrator's own: it is distinct from the gateway's token and is not
inherited from any environment variable the command itself uses.

Once the work list API is ready and before the run's first admission pass,
the command SHALL print the startup banner — the items file, the gateway,
the backlog count, and the work list API's address — followed by the work
list itself, in the form `spinloop work list` prints it: a table where
standard output is a terminal, plain tab-separated lines otherwise. The
list SHALL be the run's own view of the items, the one the work list API
would answer at that moment, so a restart's recovered state — an item
already recorded done, failed, or running — shows the way it would show to
a caller of the API.

#### Scenario: A gateway and an items file drive the command

- **WHEN** the operator runs the command naming a reachable gateway and an
  items file, with the gateway's token in the environment
- **THEN** it reads the fleet's topology from the gateway and works the
  items file's backlog

#### Scenario: A fleet file names the gateway

- **WHEN** the operator runs the command naming no gateway, and the fleet
  file — the one the fleet flag names, or the file in the working
  directory — names a gateway in its section
- **THEN** the command works against that gateway, authenticating under the
  section's token variable where the token flag was not given, the token's
  value read from the environment first, then the `.env` beside the file

#### Scenario: No gateway anywhere stops the command

- **WHEN** the operator names no gateway, and no readable fleet file — or
  none whose section names a gateway — is to hand
- **THEN** the command fails before it works an item, naming the flag and
  the file

#### Scenario: An unreachable gateway stops the command

- **WHEN** the operator runs the command naming a gateway that does not
  answer, or that refuses its token
- **THEN** the command fails, naming the gateway and what went wrong, and
  works no item

#### Scenario: A loopback API takes no token

- **WHEN** the operator runs the command with a loopback listen address, and
  no API token set
- **THEN** the work list API serves on that address, and its requests take
  no token

#### Scenario: A non-loopback API without a token refuses

- **WHEN** the operator runs the command with a non-loopback listen address,
  and no API token set
- **THEN** the command fails before it works an item, naming the token

#### Scenario: The API token is its own

- **WHEN** the operator runs the command against a gateway that has a
  token, with the API token set
- **THEN** the API authenticates on its own token, and the gateway's token
  is not what its requests present

#### Scenario: A listen address that conflicts is refused

- **WHEN** the operator gives both a loopback flag and a listen address, or
  an address that is not loopback with no token
- **THEN** the command fails before it works an item, naming the conflict

#### Scenario: Startup prints the work list on a terminal

- **WHEN** the command starts with standard output a terminal, and the
  items file carries items
- **THEN** after the startup banner, the command prints the work list as a
  table — a heading row and one aligned row per item, id, state, node,
  started and ended — the same table `spinloop work list` prints

#### Scenario: Startup prints the work list on a pipe

- **WHEN** the command's standard output is piped into another program
- **THEN** after the startup banner, the command prints one plain
  tab-separated line per item, in the items file's order, with no
  decoration

#### Scenario: A restart's recovered state shows at startup

- **WHEN** the command starts against an items file whose state beside it
  already records an item done, failed, or running from a prior run
- **THEN** the startup work list shows that item in its recorded state, not
  backlog

### Requirement: Serving the work list API

The orchestrator SHALL serve a work list API for the life of the run, on
the address its `--listen` flag names. The API's paths SHALL be:

- `GET /` — the list of the items file's items as the run holds them: each
  item's id, state, and the node it runs on, where it is running.
- `GET /log/<id>` — the output of the item named, where the run keeps it.
- `POST /` — a new item: the run admits it to the backlog, writes it to the
  items file, and the items file stays the source of truth.
- `DELETE /<id>` — an item out of the backlog: the run removes it from the
  items file and its state.
- `POST /abort/<id>` — an item out of the running: the run stops its agent,
  puts the item back in the backlog, and writes that to the items file and
  its state.

The API SHALL answer from the run's in-memory view of the items — the same
view the run's own loop reads and writes — and SHALL NOT re-read the items
file to answer a request. Where the run writes the items file, the write
SHALL carry the run's view, and SHALL NOT drop an item the API added or
remove one it removed. The run's loop and the API SHALL share one view of
the items: an item the API adds SHALL be eligible for admission on the
loop's next pass, and an item the loop finishes SHALL show finished to the
API.

The API SHALL authenticate its requests with a bearer token: where the run
listens on an address that is not loopback, every request SHALL present the
run's API token or be refused; where it listens on loopback, a request MAY
present no token and be served.

The orchestrator SHALL log every call the API answers, once the answer is
complete: the method, the path, the status the caller was given, and how
long the call took. A refusal or a fault SHALL be visible in the run's own
log at its default level, not only in the caller's reply.

#### Scenario: The run's view answers the list

- **WHEN** the run's loop admits an item and a request reads the list
- **THEN** the list shows the item running, on the node it was matched to

#### Scenario: A new item enters through the API

- **WHEN** a request posts an item to the API
- **THEN** the run's view holds it, the items file holds it, and the loop's
  next pass may admit it

#### Scenario: An item comes out through the API

- **WHEN** a request removes an item from the backlog, or aborts one that is
  running
- **THEN** the run's view no longer holds it in that state, the items file
  no longer holds it where it is removed, and an aborted item's agent is
  stopped and its item back in the backlog

#### Scenario: A stopped run's API is gone

- **WHEN** the run stops, whatever its cause
- **THEN** the API no longer answers

#### Scenario: A request without a token is refused off loopback

- **WHEN** the run listens on an address that is not loopback, and a request
  presents no token, or the wrong one
- **THEN** the request is refused, and no item is read or changed

#### Scenario: The loop and the API see one view

- **WHEN** the API adds an item while the loop's next pass runs
- **THEN** the pass sees the item, and an item the loop finishes shows
  finished to a request that reads it

### Requirement: Work items

A work items file SHALL declare a list of items. Each item SHALL carry a
unique `id`, the instructions its agent is given, and the directory the agent
works in. An item MAY carry tags — a list of `key=value` pairs, the way node
tags are named — describing the nodes it can run on, and a numeric priority,
higher first, defaulting to last among equals by file order.

An item's id SHALL be unique within the file; a duplicate SHALL be an error
naming the collision. An item whose instructions or working directory is
missing SHALL be an error naming the item. An item that names no tags SHALL
match any node. A file the orchestrator cannot parse SHALL stop the command
naming the file, working no item.

#### Scenario: A minimal item

- **WHEN** an items file lists an item with an id, instructions, and a
  working directory and nothing else
- **THEN** the item enters the backlog, matching any node, at the lowest
  priority

#### Scenario: Duplicate ids are rejected

- **WHEN** an items file lists two items with the same id
- **THEN** the command fails, naming the duplicated id, and works no item

#### Scenario: A new item in the file enters the backlog

- **WHEN** the items file gains an item the orchestrator has not seen, while
  it runs
- **THEN** the item enters the backlog and is eligible for admission on the
  next pass

### Requirement: Reading the fleet's topology

The orchestrator SHALL learn the fleet's nodes, tags, states, serving facts,
and concurrency limits only from the gateway's topology: the run itself SHALL
NOT hold a fleet file, a node's token, or an engine key, and it SHALL NOT
contact a node's control API directly. The command may read a fleet file to
find the gateway, and nothing else from it reaches the run. It SHALL re-read
the topology as the fleet changes, so a node that starts, stops, or changes
what it serves is seen without the orchestrator being restarted.

#### Scenario: The gateway is the only fleet view

- **WHEN** the orchestrator runs
- **THEN** every fact it uses about the fleet — a node's tags, state, model,
  and the limits — came from the gateway's topology

#### Scenario: A changing fleet is seen

- **WHEN** a node the orchestrator last saw stopped comes back serving,
  without the orchestrator being restarted
- **THEN** the orchestrator's next pass sees it serving, and items that
  match it become eligible

### Requirement: Matching an item to a node

An item SHALL match a node only where every tag the item carries is a tag the
node carries: an item carrying `gpu=a100` and `os=linux` matches only a node
carrying both. Among matching nodes, a node that is running and whose engine
has answered SHALL be preferred over a node that would have to be started,
and matching nodes SHALL be ranked the way the fleet file's preference
ranks them. A node that is not running SHALL be eligible only where the
fleet's wake policy allows starting it, and it SHALL be offered the model a
request would start it with. An item no node matches SHALL wait in the
backlog, not fail: a node may come up, or be tagged, later.

#### Scenario: An item takes a node carrying all its tags

- **WHEN** an item carries two tags and two nodes each carry one of them,
  while a third node carries both
- **THEN** the item matches only the third node

#### Scenario: A running node is offered before a stopped one

- **WHEN** an item matches one running node and one stopped node, and the
  fleet wakes
- **THEN** the running node is offered first

#### Scenario: A stopped node is offered only where the fleet wakes

- **WHEN** an item matches only a stopped node, and the fleet's wake policy
  refuses to start
- **THEN** the item waits in the backlog

#### Scenario: An item nothing matches waits

- **WHEN** an item's tags match no node
- **THEN** the item stays in the backlog, and is tried again as the fleet
  changes

### Requirement: Admitting items under the limits

The orchestrator SHALL admit an item to run only while every limit the item
counts against has room: the fleet's total, where declared, against the
items it has in flight, and each per-tag limit, against the items it has in
flight that carry that tag. An item counts against every tag it carries, and
against the total. Where the fleet declares no limits, the orchestrator
admits as many items as nodes will take. Admitted items run no further than
the limits allow at once: when an item finishes, another may be admitted on
the next pass.

#### Scenario: The total bounds the in-flight

- **WHEN** the fleet's total is three and three items are in flight
- **THEN** no further item is admitted, whatever its tags

#### Scenario: A tag limit bounds its own items

- **WHEN** the limit on `gpu=a100` is two and two in-flight items carry it
- **THEN** a third item carrying it waits, while an item carrying no such
  tag may still be admitted

#### Scenario: Finishing frees a slot

- **WHEN** an in-flight item finishes while others wait
- **THEN** the next eligible item is admitted on the next pass, within the
  limits

#### Scenario: No limits, no bounding

- **WHEN** the fleet declares no concurrency limits
- **THEN** items are admitted as nodes will take them

### Requirement: Running an item

An admitted item SHALL run as a one-shot agent: the active harness, in its
non-interactive single-task form, given the item's instructions, working in
a `workspace` subdirectory of the item's own directory, with its inference
pointed at the gateway and the model of the node the item was matched to.
The agent's output SHALL be kept, per item, beside the items file, so a
finished or failed item can be read after the fact. The orchestrator SHALL
verify the item's directory exists before it launches the agent; an item
whose directory is missing SHALL be failed, naming the item, and the rest
of the backlog SHALL go on. Where the command was given
`--create-item-dirs`, the orchestrator SHALL create the missing directory
instead, and the item SHALL go on to launch; a directory it cannot create
SHALL fail the item, naming the item and the cause.

#### Scenario: An admitted item runs against the gateway

- **WHEN** the orchestrator admits an item matched to a node
- **THEN** its agent runs in a `workspace` subdirectory of the item's
  directory, one-shot, with its inference pointed at the gateway and the
  node's model

#### Scenario: A missing directory fails the item alone

- **WHEN** an admitted item's directory does not exist
- **THEN** the item is failed, naming it, and other items still run

#### Scenario: A missing directory is created where the command says to

- **WHEN** the command is given `--create-item-dirs` and an admitted item's
  directory does not exist
- **THEN** the orchestrator creates the directory, and the item's agent
  runs in its workspace subdirectory

#### Scenario: An item's output is kept

- **WHEN** an item's agent finishes, whatever its outcome
- **THEN** what the agent said is kept, per item, beside the items file

### Requirement: Item lifecycle and state

Each item SHALL move from the backlog to running to a finished end: done,
where its agent ended successfully, or failed, where it did not, or where it
could not be launched. The orchestrator SHALL keep this state beside the
items file, so that on starting it sees which items are done and which are
failed, and treats an item it last left running as failed, naming the
interruption, rather than re-running it. An item that fails SHALL NOT be
retried on its own: it stays failed until the operator acts. On a clean
interrupt, the orchestrator SHALL stop the agents it launched and put their
items back in the backlog, so no work is lost and none runs twice.

#### Scenario: An item's ends are recorded

- **WHEN** an item's agent ends successfully, and another's ends in error
- **THEN** the first is recorded done and the second failed, and both stay
  recorded across an orchestrator restart

#### Scenario: A restart does not re-run an item

- **WHEN** the orchestrator starts again and its state says an item was
  running when it last stopped
- **THEN** that item is recorded failed, naming the interruption, and is not
  re-run

#### Scenario: A clean interrupt loses no work

- **WHEN** the operator interrupts a running orchestrator
- **THEN** its agents are stopped, their items return to the backlog, and the
  next start picks them up again

### Requirement: What the orchestrator does not do

The orchestrator SHALL NOT start an engine on a node: the only thing that
ever does that is the gateway's behaviour for a request it holds. It SHALL
NOT publish an item's result anywhere — no pull request, no comment on the
work the item came from — beyond keeping the agent's output beside the items
file. It SHALL NOT hold a fleet file or any credential but the gateway's
token. Its limits SHALL bound how many items it admits, not which node the
fleet's gateway serves each request to.

#### Scenario: The orchestrator never starts a node

- **WHEN** an item is matched to a stopped node and admitted
- **THEN** the orchestrator launches the agent and nothing else, and it is
  the gateway, for the agent's first request, that starts the node

#### Scenario: A finished item is not published

- **WHEN** an item's agent ends successfully
- **THEN** the record of it is the item's state and kept output, and nothing
  is posted, opened, or committed on the item's behalf

### Requirement: The run drops a record the file no longer carries

The items file is the set of items the run works, so a record the file no
longer carries SHALL NOT stand: where an item has gone out of the file — by
an external edit, or by `spinloop work remove` — and is not in flight, the
run SHALL drop its record from the state on its pass, so the state holds no
record of an item the file has let go, and an id the file has let go can be
worked again where its record has stood ended. A record of an item still in
flight SHALL stand until the item ends, even where the file has let it go in
the meantime: the agent that is running runs to its end, and its record goes
with it out of the state once it is no longer in flight.

#### Scenario: A removed item's record goes

- **WHEN** an item that is backlog, done or failed goes out of the items file
  while the run works it
- **THEN** the run's pass drops its record from the state, and the state
  holds no record of it

#### Scenario: An in-flight item the file has let go

- **WHEN** an item that is running goes out of the items file while the run
  works it
- **THEN** the agent runs to its end, and the record is dropped from the
  state once the item is no longer in flight

#### Scenario: A let-go id can be worked again

- **WHEN** the state records an item failed, the item goes out of the file,
  and the run drops the record
- **THEN** the id can enter the file again and be worked

### Requirement: An abort marker stops a running item

Where a marker for an item stands beside the items file, asking the run to
abort it, the run SHALL take it up on its pass: the item's agent stopped the
way a clean interrupt stops it — the polite signal, the grace, then the hard
end — the item's record removed from the state, and the marker taken up, so
the item is back in the backlog. The run SHALL NOT admit the item again on
the pass that took the marker up: the item is eligible on the next pass. A
marker for an item that is not in flight SHALL be taken up without further
action — the marker goes, and nothing else changes.

#### Scenario: A marker stops the item

- **WHEN** a marker for a running item stands beside the file, and the run's
  pass comes
- **THEN** the item's agent is stopped, its record is gone from the state,
  and the marker is taken up

#### Scenario: The pass that aborts does not re-admit

- **WHEN** the run takes up a marker for an item on a pass
- **THEN** the item is not admitted on that pass, and is eligible on the next

#### Scenario: A marker for an item not in flight

- **WHEN** a marker stands beside the file for an item that is not running
- **THEN** the marker is taken up, and nothing else changes

### Requirement: Retrying a failed item through the work list API

The work list API SHALL serve `POST /v1/items/{id}/retry`, which puts a
failed item back in the backlog: the item's record is removed from the run's
view and the state, so the item is backlog, and the run's loop admits it on
a later pass the way it admits any backlog item. Only a failed item SHALL be
retried: an item recorded done, running, or with no record SHALL be refused
with a conflict naming the item and its state, and an id the items file does
not carry SHALL be refused as not found, naming it. The item's fields in the
items file SHALL NOT change. The pass that handles the request SHALL NOT
admit the item before the record is gone, and a retried item SHALL be
subject to the same matching and limits as any backlog item.

#### Scenario: A failed item returns to the backlog

- **WHEN** a request retries an item the run records failed
- **THEN** the item shows backlog to a request that reads the list, and the
  loop's next pass may admit it

#### Scenario: An item that has not failed is refused

- **WHEN** a request retries an item that is running, done, or backlog
- **THEN** the API answers a conflict naming the item and its state, and
  the run's view is unchanged

#### Scenario: An unknown id is refused

- **WHEN** a request retries an id the items file does not carry
- **THEN** the API answers not found, naming the id

#### Scenario: The items file is untouched

- **WHEN** a failed item is retried
- **THEN** the items file is byte-for-byte what it was before
