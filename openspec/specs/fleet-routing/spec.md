# fleet-routing Specification

## Purpose
Connecting a harness launch to the fleet: choosing which node serves the agent
spinloop is about to launch, waking that node when nothing is serving yet, and
turning the choice into the base URL and key the launched agent authenticates
with — so a machine that can reach the fleet needs no addresses of its own.

## Requirements

### Requirement: Choosing a node

Selection SHALL query every candidate node concurrently, as `spinloop fleet
status` does, and SHALL prefer a node that is already running what is wanted: a
node whose state is `running` and whose served model matches the Spinloop's
`MODEL` (or its `ALIAS`, against the name the node reports serving). A Spinloop
that names no model SHALL match any running node.

A node whose state is `running` but whose daemon reports the engine not ready is
not running what is wanted: the state turns running when the engine's process
exists, which is before the weights are fetched and loaded, and during that
window nothing is listening on the engine's port. Such a node SHALL NOT be
selected, and a failure that names it SHALL mark it not ready, so a refusal does
not read as if the node were serving the model it was asked for. A node whose
daemon reports no readiness at all — an older build, or a runner with no
health-check convention — SHALL NOT be disqualified: the absence of a reading is
not evidence of not-readiness.

Matching nodes SHALL be ranked by the activity preference in force (see
"Preferring an idle or an active node"). Ties SHALL be broken by fleet-file
order, so the same fleet in the same state chooses the same node.

A node that does not answer — unreachable, unauthorized, or a configuration
error — SHALL be skipped rather than aborting the selection, exactly as it is a
row rather than a failure in `spinloop fleet status`.

`--node <name>` SHALL pin the selection to one node, skipping the search. An
unknown name SHALL fail naming the known nodes, and a pinned node that cannot be
reached SHALL fail rather than falling back to another node — a pin is an
instruction, not a preference. A pinned node that is running the wanted model
but has not answered yet SHALL fail saying it is still starting it — it may be
fetching or loading weights — and naming the command for the node's log, rather
than saying that nothing serves the model or restarting the node: this node is
about to serve it.

A running engine SHALL NEVER be stopped or restarted to make room, including a
pinned one: another person may be using it. A node running a different model is
therefore not a candidate, and pinning one SHALL fail saying what it is serving.

#### Scenario: The preference decides between matching nodes

- **WHEN** two nodes are running the wanted model and one reports a longer time
  since it last did work
- **THEN** the one the activity preference favours is chosen, and the same fleet
  in the same state chooses the same node every time

#### Scenario: An unreachable node is skipped

- **WHEN** one node in the fleet cannot be reached and another is running the
  wanted model
- **THEN** the reachable node is chosen and the launch proceeds

#### Scenario: A pinned node is used as given

- **WHEN** the user runs `spinloop harness --node gpu-box` and that node is
  running the wanted model
- **THEN** `gpu-box` is chosen without regard to what the other nodes are doing

#### Scenario: A pinned node that cannot be reached fails

- **WHEN** the user pins a node whose daemon is unreachable
- **THEN** the command fails naming that node, and no other node is selected

#### Scenario: A not-ready engine is not a match

- **WHEN** the only node running the wanted model reports its engine not ready,
  and no other node is running it
- **THEN** the selection reports that nothing is serving the model, and the
  failure names the node marked not ready

#### Scenario: A missing readiness reading still routes

- **WHEN** a node is running the wanted model and its daemon reports no
  readiness at all
- **THEN** the node is chosen, as if its engine had answered

#### Scenario: A pinned node that is still starting names its state

- **WHEN** the user pins a node whose engine is running the wanted model but has
  not answered yet
- **THEN** the command fails saying the node is still starting the model, that
  it may be fetching or loading weights, and names the command for the node's
  log, without restarting the node

#### Scenario: A busy node is left alone

- **WHEN** every reachable node is running a model other than the one wanted
- **THEN** no running engine is stopped, and selection falls through to waking
  an idle node

#### Scenario: Pinning a node serving something else fails

- **WHEN** the user pins a node that is running a different model
- **THEN** the command fails saying what that node is serving, and the engine is
  untouched

### Requirement: Preferring an idle or an active node

Which of several matching nodes wins SHALL be a setting with two values, because
the right answer depends on the fleet rather than on the code:

- `idle` — the node inactive longest wins, using the idle figure the daemon
  reports; a node reporting no activity counts as the most idle. Work is spread,
  and a node currently mid-request is the last one chosen, since it is the least
  idle of all.
- `active` — the most recently active node wins. Sessions consolidate onto one
  engine, leaving the other nodes free to be woken for another model or left
  alone to save power.

The default SHALL be `idle`: piling a second agent onto the engine that is
already working is the failure worth avoiding by default, and a fleet exists to
have somewhere else to put the work.

The setting SHALL resolve with the precedence `--prefer <idle|active>`, then the
fleet file's own `prefer`, then the default. A value that is neither SHALL fail
naming both accepted values. The preference in force SHALL be named wherever the
choice is explained, so a surprising selection is traceable to the setting that
caused it rather than looking arbitrary.

The setting SHALL rank matching nodes only. It SHALL NOT decide whether to wake
a node, override a pin, or make a node running a different model eligible —
those rules hold whichever value is in force.

#### Scenario: Idle spreads the work

- **WHEN** two nodes are running the wanted model, one active seconds ago and
  one inactive for an hour, and the preference is `idle`
- **THEN** the node inactive for an hour is chosen

#### Scenario: Active consolidates the work

- **WHEN** the same two nodes are available and the preference is `active`
- **THEN** the node active seconds ago is chosen

#### Scenario: A busy engine is the last resort under idle

- **WHEN** one matching node is mid-request and another has been quiet, under
  the default preference
- **THEN** the quiet node is chosen

#### Scenario: The flag beats the fleet file

- **WHEN** a fleet file sets `prefer: active` and the user passes `--prefer idle`
- **THEN** nodes are ranked as `idle`

#### Scenario: The default is idle

- **WHEN** neither the flag nor the fleet file sets a preference
- **THEN** nodes are ranked as `idle`

#### Scenario: An unknown preference is refused

- **WHEN** a preference is set to anything other than `idle` or `active`
- **THEN** the command fails naming both accepted values, and no node is
  selected

#### Scenario: The preference is named in the explanation

- **WHEN** a launch reports the node it chose
- **THEN** the report names the preference that ranked it

### Requirement: Waking a node

When no running node is serving what is wanted, routing SHALL wake one: it SHALL
choose a node that is not running, push what the Spinloop asks for as that node's
deploy config, start it through the daemon's start endpoint, and wait before
launching the agent — not merely until the node reports `running`, which says
only that a process exists, but until its engine endpoint answers. A node whose
stored config already matches the wanted model SHALL be preferred, since it has
the weights.

The pushed config is the node-side counterpart of what `spinloop serve` would run
for that Spinloop, translated per engine. A node may be woken for an engine that
binds its model at launch — `llamacpp`, `vllm`, and `mtplx` — and a `MODEL` that
names a file on the node's own disk is a valid wake for it: the node has the
file, and only a destination that fetches its weights itself refuses a local
path.

A node that refuses the config — a runner or model it cannot serve — SHALL NOT
fail the launch while other candidates remain: the next candidate SHALL be
tried, and the refusals SHALL be reported when none succeeds.

Two clients may wake the same node at once. A start refused because an engine is
already running SHALL NOT fail the launch: the node's state SHALL be re-read,
and a node now serving what was wanted SHALL be used — and the launch SHALL
wait for that node's engine to answer before launching the agent, exactly as it
waits for a node it woken itself: the node that won the race may still be
loading weights, and the wait is bounded by the same timeout. Losing that race
is another route to the same place, not an error.

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
- **THEN** that node is given the Spinloop's model as its deploy config, started,
  and the agent launches against it once its engine answers

#### Scenario: A node is woken for a Mac-only engine

- **WHEN** a fleet-routed launch finds no node serving the wanted model, and an
  idle node's daemon can run MTPLX
- **THEN** that node is woken with a config that runs the wanted model under
  `mtplx serve`, and the agent launches against it once its engine answers

#### Scenario: A local model path wakes the node that has it

- **WHEN** the Spinloop's `MODEL` names a file on the woken node's disk
- **THEN** the wake carries that path as the model to load, rather than
  refusing it as a local file

#### Scenario: A Spinloop that pins a bind wakes a node bound to it

- **WHEN** the Spinloop names a `BASEURL` and a node is woken for it
- **THEN** the engine the node starts binds to the address the `BASEURL` names,
  exactly as `spinloop serve` would bind it, and the node reports that engine
  as reachable rather than on the engine's own default

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
- **THEN** the launch uses that node rather than failing, waiting for its
  engine to answer first if it is still loading

#### Scenario: A node that never comes up

- **WHEN** a woken node does not report running within the timeout
- **THEN** the command fails naming the node, and the engine it started is left
  running rather than stopped

#### Scenario: Waking is refused

- **WHEN** `--no-wake` is passed and no node is serving the wanted model
- **THEN** the command fails, listing the nodes with their states and naming the
  `spinloop fleet start` command that would start one, and nothing is started

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

### Requirement: Routing failures are loud and early

A fleet-routed launch that cannot resolve an endpoint SHALL fail before the
harness config is written and before the agent is launched, so a failed route
never leaves a half-applied config or an agent pointed at nothing. Every failure
SHALL name the fleet file it read, and — where the fleet was reached — the state
of each node it considered.

#### Scenario: A failed route leaves the config untouched

- **WHEN** routing fails because no node can serve the Spinloop's model
- **THEN** the harness config is not written and no agent is launched

#### Scenario: A whole fleet that cannot be reached

- **WHEN** no node in the fleet answers
- **THEN** the command fails naming the fleet file and each node's failure, not
  a single generic error

### Requirement: The engine key the client sets

Routing SHALL resolve the engine key from the variable the node's fleet entry
names, supply it to the node when it wakes one, and place it in the launched
agent's environment as `OPENAI_API_KEY` — and, for a harness that reads the key
under its own name, under that name too, as the cloud path already does. A key
already set in spinloop's environment SHALL win.

The client is therefore the one party that holds the key: it decides what the
engine it starts is gated with, and it knows what to give the agent because it
set it. A daemon node whose fleet entry names no key SHALL wake an ungated
engine, which is correct for a node reached over loopback.

For a cloud node the resolution is the same with a fleet-wide default: the key
is the node's own `engineTokenEnv` when it names one, otherwise the fleet file's
`apiKeyEnv`. A cloud node's engine is always gated by its API key, so a cloud node that
is selected — running or woken — SHALL be reached with a key. When neither the
node nor the fleet names a variable, or the variable it names is set nowhere,
routing SHALL fail before the agent launches, naming the node and what to set: a
a cloud node is never reached ungated.

Routing SHALL NOT ask the daemon for a key, and the daemon SHALL NOT return one:
saying a key is required is a fact a router needs, and handing the key out is
not.

Where routing selects a daemon node that is *already running* — one it did not
wake — the engine was gated by whoever started it. When such a node reports that
it requires a key and the fleet entry names none, routing SHALL fail before the
agent launches, naming the node and the variable to set: an agent that cannot
authenticate is worse than a message that says so.

#### Scenario: A woken node is gated with the client's key

- **WHEN** routing wakes a node and its fleet entry names a variable holding a
  key
- **THEN** the node is started gated with that key, and the launched agent's
  environment carries the same value as `OPENAI_API_KEY`

#### Scenario: No key wakes an ungated engine

- **WHEN** routing wakes a daemon node whose fleet entry names no engine key
- **THEN** the engine starts ungated and the agent launches with no key injected
  for it

#### Scenario: A cloud node takes the fleet-wide key

- **WHEN** routing selects a cloud node whose entry names no `engineTokenEnv`,
  and the fleet file declares an `apiKeyEnv` that is set
- **THEN** the launched agent's environment carries that value as
  `OPENAI_API_KEY`

#### Scenario: A cloud node's own key overrides the fleet-wide one

- **WHEN** routing selects a cloud node that names its own `engineTokenEnv`
  and the fleet file also declares an `apiKeyEnv`
- **THEN** the node's own variable is the key the agent is given, not the
  fleet-wide one

#### Scenario: A cloud node with no key fails early

- **WHEN** routing selects a cloud node whose entry names no `engineTokenEnv`
  and the fleet file declares no `apiKeyEnv`
- **THEN** the command fails naming the node and what to set, and no agent is
  launched

#### Scenario: An already-running gated node with no key fails early

- **WHEN** routing selects a daemon node that is already running a gated engine
  and the node's fleet entry names no key
- **THEN** the command fails naming the node and what to set, and no agent is
  launched

#### Scenario: An unset variable fails before anything starts

- **WHEN** a node's fleet entry names an engine key variable that is set nowhere
- **THEN** the command fails naming the node and the variable, and no engine is
  started

#### Scenario: An exported key wins

- **WHEN** `OPENAI_API_KEY` is already set in the user's environment and a
  fleet-routed launch runs
- **THEN** the existing value reaches the agent unchanged

### Requirement: A fleet file naming a gateway routes the launch at it

A launch whose effective fleet file — one given by `--fleet`/`-f`, or the
`./fleet.yaml` in the working directory where the worn Spinloop was not named
explicitly — declares a `gateway` section SHALL route at that
gateway the way a launch routes at an endpoint: the section's address SHALL be
written as the applied provider's base URL, with the OpenAI-compatible prefix
appended when it carries no path, and SHALL be placed in the launched
agent's environment as `OPENAI_BASE_URL`. No node SHALL be contacted and none
SHALL be woken: the gateway has already done the choosing.

The gateway's token SHALL be resolved from the variable the section names — or
from `OPENAI_API_KEY` where the section names none — through the client's
existing key chain, with a variable already set in spinloop's environment
winning. When the variable is set nowhere, the launch SHALL fail before the
harness config is written, naming the variable to set.

A Spinloop that pins a `BASEURL` SHALL NOT be routed at the section: the
pinned address wins, as it wins over an endpoint.

#### Scenario: A launch is pointed at the fleet's gateway

- **WHEN** the user runs a launch against a fleet file whose `gateway` section
  names an address, and the section's token variable is set
- **THEN** the launched agent's environment carries `OPENAI_BASE_URL` at the
  section's address with the OpenAI-compatible prefix, the applied provider's
  base URL is the same address, and no node is contacted

#### Scenario: A missing gateway token fails early

- **WHEN** a launch routes at a fleet file's `gateway` section and the
  variable the section names is set nowhere
- **THEN** the launch fails before the harness config is written, naming the
  variable to set

#### Scenario: A pinned BASEURL still wins over the section

- **WHEN** a Spinloop pins a `BASEURL` and its fleet file names a gateway
- **THEN** the `BASEURL` is used, the gateway is not, and the launch says it
  is not routing

### Requirement: fleet route answers a fleet file naming a gateway

`spinloop fleet route` on a Spinloop whose fleet file declares a `gateway`
section SHALL report that the gateway has already chosen: it SHALL name the
address the launch will be given, SHALL NOT query any node, and SHALL NOT wake
one.

#### Scenario: A route against a file naming a gateway names it

- **WHEN** the user runs `spinloop fleet route` on a Spinloop whose fleet file
  declares a `gateway` section
- **THEN** the output names the section's address as the one the launch will
  use, no node is queried, and nothing is started

### Requirement: A launch routed through a fleet

`spinloop harness` SHALL route a worn Spinloop through a fleet when
`--fleet <path>` — or its `-f <path>` short form — is given, and, when the
worn Spinloop was not named explicitly, through a `./fleet.yaml` in the
working directory. A worn Spinloop is named explicitly when the user gives its
path — a leading positional argument or a `--spinloop`/`-O` value — or when
`SPINLOOP_ALIAS` is set; a valueless `--spinloop` wears the default Spinloop
and is not named. An explicitly named Spinloop SHALL NOT pick up a fleet file
from the working directory on its own: the user pointed at that file
deliberately, and routing happens only when `--fleet` is also given. A launch
that wears no Spinloop applies nothing and routes nothing, in a directory
holding a `fleet.yaml` or not. Where a Spinloop is worn and neither a flag nor
a directory `./fleet.yaml` is in force, the launch SHALL behave exactly as it
does without routing.

Routing SHALL choose one node and give the launched agent that
node's engine as its OpenAI-compatible endpoint: the chosen base URL SHALL be
written as the applied provider's base URL, in the same place an environment's
address is written, and SHALL also be placed in the launched agent's
environment as `OPENAI_BASE_URL`.

A variable already set in spinloop's environment SHALL win, as it does on the
cloud path — routing fills what is unset, it does not override an explicit
choice.

A Spinloop that pins a `BASEURL` SHALL NOT be routed: the pinned address wins
and spinloop SHALL say it is not routing through the fleet, rather than
silently selecting a node whose address it then discards.

A launch given `--env <name>` — or its `-e <name>` short form — alongside a
fleet, whether the directory's `fleet.yaml` or a `--fleet` flag, SHALL fail
naming both: each is an answer to where the model is served from — one names a
specific environment, the other a set of nodes to choose between — and a launch
stating both is a mistake rather than a precedence to resolve.

The chosen node and the reason it was chosen SHALL be reported on stderr before
the agent launches, so a launch that lands somewhere unexpected says so at the
time rather than at the first request.

#### Scenario: A running node becomes the agent's endpoint

- **WHEN** the user runs `spinloop harness -O` in a directory holding a
  `Spinloop` and a `fleet.yaml`, and a node in that fleet is running the model
  the Spinloop names
- **THEN** the launched agent's environment carries `OPENAI_BASE_URL` pointing
  at that node's engine, and the applied provider's base URL is the same
  address

#### Scenario: The flag overrides the directory's fleet file

- **WHEN** the user runs `spinloop harness -O --fleet=./cluster.yaml` in a
  directory holding a `Spinloop` and a `fleet.yaml`
- **THEN** the nodes in `./cluster.yaml` are the candidates, not the ones in
  the directory's `fleet.yaml`

#### Scenario: The short form overrides the directory's fleet file

- **WHEN** the user runs `spinloop harness -O -f ./cluster.yaml` in a directory
  holding a `Spinloop` and a `fleet.yaml`
- **THEN** the nodes in `./cluster.yaml` are the candidates, not the ones in
  the directory's `fleet.yaml`

#### Scenario: No fleet file in force leaves the launch local

- **WHEN** the user runs `spinloop harness -O` in a directory holding a
  `Spinloop` and no `fleet.yaml`, passing no `--fleet`
- **THEN** no fleet file is read, no node is contacted, and the launch behaves
  as it did before

#### Scenario: An explicitly named Spinloop does not route

- **WHEN** the user runs `spinloop harness ./elsewhere/Spinloop` in a directory
  holding a `fleet.yaml`, and passes no `--fleet`
- **THEN** no fleet file is read, no node is contacted, and the launch applies
  the Spinloop locally

#### Scenario: The flag routes an explicitly named Spinloop

- **WHEN** the user runs `spinloop harness ./elsewhere/Spinloop --fleet
  ./cluster.yaml`
- **THEN** the nodes in `./cluster.yaml` are the candidates

#### Scenario: An aliased Spinloop does not route

- **WHEN** `SPINLOOP_ALIAS` is set and the user runs `spinloop harness -O` in
  a directory holding a `fleet.yaml`, passing no `--fleet`
- **THEN** no fleet file is read, and the launch applies the aliased Spinloop
  locally

#### Scenario: A pinned BASEURL is not routed

- **WHEN** a `Spinloop` beside a `fleet.yaml` names a `BASEURL`, and the user
  runs `spinloop harness -O`
- **THEN** the `BASEURL` is used, no node is selected, and spinloop reports
  that it is not routing through the fleet

#### Scenario: An exported base URL wins

- **WHEN** `OPENAI_BASE_URL` is already set in the user's environment and a
  fleet-routed launch runs
- **THEN** the existing value reaches the agent unchanged

#### Scenario: --env and a fleet conflict

- **WHEN** the user runs `spinloop harness --env dev-2` in a directory holding
  a `fleet.yaml`, or with a `--fleet` flag
- **THEN** the launch fails naming both the `--env` flag and the fleet

#### Scenario: The choice is announced

- **WHEN** a fleet-routed launch selects a node
- **THEN** the node's name, the resolved endpoint, and why it was chosen are
  written to stderr before the harness is launched

### Requirement: A launch through a fleet's gateway needs no Spinloop

A launch routed through a fleet SHALL NOT require a Spinloop when the
effective fleet file declares a `gateway` section. A gateway resolves the model
per request rather than by matching a node against one, so there is no model
for a Spinloop to name; requiring one would ask for a file whose contents the
launch then ignores.

Where the fleet file names no gateway, a launch with no Spinloop SHALL fail
before launching, saying that `--fleet` needs a Spinloop because it is the
Spinloop's model that decides which node can serve you: a node can only be
matched by the model a Spinloop names. Where the named fleet file cannot be
read at all, the failure SHALL name that file rather than the missing
Spinloop — the file is the thing the launch was pointed at.

The same applies to a Spinloop that is given but names neither a model nor an
alias, when routed at a gateway: the launch SHALL NOT fail on that account
either, for the same reason.

Whenever the harness is configured this way — routed at a gateway, with no
model or alias to apply — the launch SHALL query the gateway's `GET /v1/models`
and use the result as the harness's model list, so the harness has something to
choose from instead of an empty one. A failure to complete that query (the
gateway unreachable, timed out, or answering something unusable) SHALL NOT fail
the launch: it SHALL warn and configure the harness with an empty model list,
on the same terms a launch already warns and carries on when it cannot refresh
a cloud endpoint's key. This model-list population is a capability of
harnesses whose config format holds more than one model per provider; a harness
with no such concept is configured with no model or alias.

The provider such a launch configures SHALL be named and keyed by the gateway,
not by the catalogue's shared generic id: its display name SHALL lead with
"Gateway" — not the catalogue engine's own generic label — followed by the
gateway's `name` where the fleet file's `gateway` section gives one, or its
address otherwise (e.g. "Gateway (dev-2)" or "Gateway (localhost:4000)"), the
same "<label> (<qualifier>)" shape a cloud environment already reads as (e.g.
"llama.cpp (dev-2)"). This keeps a second gateway from overwriting the first's
configured block, and — since the word a user actually searches a model picker
for is "gateway" — lets them find it at all.

#### Scenario: No Spinloop, a gateway routes anyway

- **WHEN** the user runs `spinloop code --fleet ./fleet.yaml` in a directory
  holding no Spinloop, and that fleet file names a gateway with its token set
  and models to list
- **THEN** the launch does not fail: the active harness is configured with a
  provider at the gateway's address, its model list populated from the
  gateway's `GET /v1/models`, no single default model set, and the harness
  launches against the gateway

#### Scenario: No Spinloop and no gateway still fails

- **WHEN** the user runs `spinloop code --fleet ./fleet.yaml` in a directory
  holding no Spinloop, and that fleet file names no gateway
- **THEN** the launch fails saying `--fleet` needs a Spinloop, and no harness
  is launched

#### Scenario: A named fleet file that cannot be read names itself

- **WHEN** the user runs `spinloop code --fleet ./fleet.yaml` where that file
  does not exist
- **THEN** the launch fails naming the fleet file it could not read, rather
  than the Spinloop it also lacks

#### Scenario: A Spinloop naming no model routes at a gateway

- **WHEN** a launch routes at a gateway with a Spinloop that names neither a
  model nor an alias
- **THEN** the launch does not fail on that account, and the harness's model
  list is populated from the gateway

#### Scenario: An unreachable gateway warns rather than failing

- **WHEN** a launch is configured against a gateway whose `GET /v1/models`
  cannot be completed
- **THEN** the launch warns, configures the harness with an empty model list,
  and still launches

#### Scenario: The configured provider is named for its gateway

- **WHEN** a launch is configured against a fleet file's gateway
- **THEN** the provider's display name leads with "Gateway" and is qualified by
  the section's `name` where it gives one, or by the gateway's address
  otherwise, so a second gateway does not overwrite the first's block
