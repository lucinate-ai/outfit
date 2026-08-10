## Purpose

Connecting a harness launch to the fleet: choosing which node serves the agent
outfit is about to launch, waking that node when nothing is serving yet, and
turning the choice into the base URL and key the launched agent authenticates
with — so a machine that can reach the fleet needs no addresses of its own.

## ADDED Requirements

### Requirement: A fleet-routed launch

`outfit harness` SHALL route through a fleet when the Outfit it wears names one
with a `FLEET` instruction, or when `--fleet <path>` is given; `--fleet` SHALL
override the instruction, and a launch with neither SHALL behave exactly as it
does today. Routing SHALL choose one node and give the launched agent that
node's engine as its OpenAI-compatible endpoint: the chosen base URL SHALL be
written as the applied provider's base URL, in the same place a `REMOTE`
endpoint's address is written, and SHALL also be placed in the launched agent's
environment as `OPENAI_BASE_URL`.

A variable already set in outfit's environment SHALL win, as it does on the
remote path — routing fills what is unset, it does not override an explicit
choice.

An Outfit that pins a `BASEURL` SHALL NOT be routed: the pinned address wins and
outfit SHALL say it is not routing through the fleet, rather than silently
selecting a node whose address it then discards.

The chosen node and the reason it was chosen SHALL be reported on stderr before
the agent launches, so a launch that lands somewhere unexpected says so at the
time rather than at the first request.

#### Scenario: A running node becomes the agent's endpoint

- **WHEN** the user runs `outfit harness` with an Outfit naming a `FLEET`, and a
  node in that fleet is running the model the Outfit names
- **THEN** the launched agent's environment carries `OPENAI_BASE_URL` pointing
  at that node's engine, and the applied provider's base URL is the same address

#### Scenario: The flag overrides the instruction

- **WHEN** the user runs `outfit harness --fleet=./cluster.yaml` with an Outfit
  whose `FLEET` names a different file
- **THEN** the nodes in `./cluster.yaml` are the candidates

#### Scenario: An Outfit with no FLEET is unaffected

- **WHEN** the user runs `outfit harness` with an Outfit naming no `FLEET` and
  passes no `--fleet`
- **THEN** no fleet file is read, no node is contacted, and the launch behaves
  as it did before

#### Scenario: A pinned BASEURL is not routed

- **WHEN** an Outfit names both a `FLEET` and a `BASEURL`
- **THEN** the `BASEURL` is used, no node is selected, and outfit reports that
  it is not routing through the fleet

#### Scenario: An exported base URL wins

- **WHEN** `OPENAI_BASE_URL` is already set in the user's environment and a
  fleet-routed launch runs
- **THEN** the existing value reaches the agent unchanged

#### Scenario: The choice is announced

- **WHEN** a fleet-routed launch selects a node
- **THEN** the node's name, the resolved endpoint, and why it was chosen are
  written to stderr before the harness is launched

### Requirement: Choosing a node

Selection SHALL query every candidate node concurrently, as `outfit fleet
status` does, and SHALL prefer a node that is already running what is wanted: a
node whose state is `running` and whose served model matches the Outfit's
`MODEL` (or its `ALIAS`, against the name the node reports serving). An Outfit
that names no model SHALL match any running node.

Among matching nodes, the one that has been inactive longest SHALL be chosen,
using the idle figure the daemon reports; a node reporting no activity SHALL
count as the most idle. Ties SHALL be broken by fleet-file order, so the same
fleet in the same state chooses the same node.

A node that does not answer — unreachable, unauthorized, or a configuration
error — SHALL be skipped rather than aborting the selection, exactly as it is a
row rather than a failure in `outfit fleet status`.

`--node <name>` SHALL pin the selection to one node, skipping the search. An
unknown name SHALL fail naming the known nodes, and a pinned node that cannot be
reached SHALL fail rather than falling back to another node — a pin is an
instruction, not a preference.

A running engine SHALL NEVER be stopped or restarted to make room, including a
pinned one: another person may be using it. A node running a different model is
therefore not a candidate, and pinning one SHALL fail saying what it is serving.

#### Scenario: The idlest matching node wins

- **WHEN** two nodes are running the wanted model and one reports a longer time
  since it last did work
- **THEN** the longer-idle node is chosen

#### Scenario: An unreachable node is skipped

- **WHEN** one node in the fleet cannot be reached and another is running the
  wanted model
- **THEN** the reachable node is chosen and the launch proceeds

#### Scenario: A pinned node is used as given

- **WHEN** the user runs `outfit harness --node gpu-box` and that node is
  running the wanted model
- **THEN** `gpu-box` is chosen without regard to what the other nodes are doing

#### Scenario: A pinned node that cannot be reached fails

- **WHEN** the user pins a node whose daemon is unreachable
- **THEN** the command fails naming that node, and no other node is selected

#### Scenario: A busy node is left alone

- **WHEN** every reachable node is running a model other than the one wanted
- **THEN** no running engine is stopped, and selection falls through to waking
  an idle node

#### Scenario: Pinning a node serving something else fails

- **WHEN** the user pins a node that is running a different model
- **THEN** the command fails saying what that node is serving, and the engine is
  untouched

### Requirement: Waking a node

When no running node is serving what is wanted, routing SHALL wake one: it SHALL
choose a node that is not running, push what the Outfit asks for as that node's
deploy config, start it through the daemon's start endpoint, and wait before
launching the agent — not merely until the node reports `running`, which says
only that a process exists, but until its engine endpoint answers. A node whose
stored config already matches the wanted model SHALL be preferred, since it has
the weights.

A node that refuses the config — a runner or model it cannot serve — SHALL NOT
fail the launch while other candidates remain: the next candidate SHALL be
tried, and the refusals SHALL be reported when none succeeds.

Two clients may wake the same node at once. A start refused because an engine is
already running SHALL NOT fail the launch: the node's state SHALL be re-read,
and a node now serving what was wanted SHALL be used. Losing that race is
another route to the same place, not an error.

The wait SHALL be bounded by a timeout and SHALL report what it is waiting for,
because a cold node loads weights before it answers. Exceeding the timeout SHALL
fail naming the node and the endpoint that did not come up; the started engine
SHALL be left running rather than stopped, so a slow load is not thrown away.

`--no-wake` SHALL turn waking off: with no running node serving what is wanted
the command SHALL then fail, listing the nodes and their states and naming the
command that would start one.

#### Scenario: An idle node is woken and used

- **WHEN** a fleet-routed launch finds no node serving the wanted model and one
  node is idle and able to serve it
- **THEN** that node is given the Outfit's model as its deploy config, started,
  and the agent launches against it once its engine answers

#### Scenario: A started engine that is not yet loaded is waited for

- **WHEN** a woken node reports `running` while its engine is still loading
  weights and not yet answering
- **THEN** the launch waits for the engine to answer rather than launching the
  agent against an endpoint that refuses connections

#### Scenario: A node that cannot serve the model is passed over

- **WHEN** the first idle candidate rejects the pushed config as unservable and
  a second idle node accepts it
- **THEN** the second node is started and used

#### Scenario: No node can serve it

- **WHEN** every idle node rejects the config
- **THEN** the command fails, naming each node and the reason it refused

#### Scenario: Losing the race to another client

- **WHEN** a start is refused because another client woke the same node first,
  and that node is now serving the wanted model
- **THEN** the launch uses that node rather than failing

#### Scenario: A node that never comes up

- **WHEN** a woken node does not report running within the timeout
- **THEN** the command fails naming the node, and the engine it started is left
  running rather than stopped

#### Scenario: Waking is refused

- **WHEN** `--no-wake` is passed and no node is serving the wanted model
- **THEN** the command fails, listing the nodes with their states and naming the
  `outfit fleet start` command that would start one, and nothing is started

### Requirement: Resolving the chosen node's endpoint

The address the agent is given SHALL be built from what the fleet file says and
what the node reports, in that order: a node's explicit engine override in
`fleet.yaml` SHALL be used as given, otherwise the host is the node's `host`
from the fleet file and the port and path come from the engine endpoint the
node's daemon reports.

The node's daemon host SHALL NOT be assumed to be the engine's: they are
different ports, and one is not derivable from the other.

When a node reports its engine bound to loopback only, and the node is not
reached over loopback, routing SHALL fail with a message saying the engine
answers only on that machine and naming both ways out — bind the engine to a
reachable address, or set the node's engine override — rather than handing the
agent an address that cannot connect.

#### Scenario: The endpoint is the node's host and the engine's port

- **WHEN** a node at `gpu-box` reports its engine serving on port 8080
- **THEN** the agent is given that node's engine at `gpu-box` on port 8080, not
  the daemon's control API port

#### Scenario: An explicit override wins

- **WHEN** a node's fleet entry sets an engine host and port
- **THEN** those are used, whatever the daemon reports

#### Scenario: A node that reports no endpoint is named

- **WHEN** the chosen node is running but its daemon reports no engine endpoint,
  because it predates this field
- **THEN** the command fails naming that node and saying to upgrade its daemon
  or set the node's engine override in the fleet file

#### Scenario: A loopback-bound engine is refused, not guessed

- **WHEN** the chosen node reports its engine bound to loopback and the node is
  reached over the network
- **THEN** the command fails explaining the engine answers only on that machine,
  and names both the engine bind and the fleet-file override as remedies

### Requirement: The chosen node's engine key

When a node's daemon reports that its engine requires a key, routing SHALL
resolve that key from the environment variable the node's fleet entry names, and
place it in the launched agent's environment as `OPENAI_API_KEY` — and, for a
harness that reads the key under its own name, under that name too, as the
remote path already does. A key already set in outfit's environment SHALL win.

The daemon SHALL NOT be asked for the engine's key and SHALL NOT return one:
saying a key is required is a fact a router needs, and handing the key out is
not.

A node whose engine requires a key and whose fleet entry names no variable, or
names one that is set nowhere, SHALL fail before the agent launches, naming the
node and the variable — an agent that cannot authenticate is worse than a
message that says so.

#### Scenario: The node's key reaches the agent

- **WHEN** the chosen node reports its engine requires a key and its fleet entry
  names a variable holding one
- **THEN** the launched agent's environment carries that value as
  `OPENAI_API_KEY`

#### Scenario: A gated engine with no key fails early

- **WHEN** the chosen node's engine requires a key and the node's fleet entry
  names no variable holding one
- **THEN** the command fails naming the node and what to set, and no agent is
  launched

#### Scenario: An ungated engine needs no key

- **WHEN** the chosen node reports its engine requires no key
- **THEN** the agent launches with no key injected for it, and nothing fails

#### Scenario: An exported key wins

- **WHEN** `OPENAI_API_KEY` is already set in the user's environment and a
  fleet-routed launch runs
- **THEN** the existing value reaches the agent unchanged

### Requirement: Routing failures are loud and early

A fleet-routed launch that cannot resolve an endpoint SHALL fail before the
harness config is written and before the agent is launched, so a failed route
never leaves a half-applied config or an agent pointed at nothing. Every failure
SHALL name the fleet file it read, and — where the fleet was reached — the state
of each node it considered.

#### Scenario: A failed route leaves the config untouched

- **WHEN** routing fails because no node can serve the Outfit's model
- **THEN** the harness config is not written and no agent is launched

#### Scenario: A whole fleet that cannot be reached

- **WHEN** no node in the fleet answers
- **THEN** the command fails naming the fleet file and each node's failure, not
  a single generic error
