# fleet-client Specification

## Purpose
Define the `spinloop fleet` command family: the client that reads `fleet.yaml`,
polls each node's daemon control API, and renders the cluster — observing every
engine and driving individual ones, degrading gracefully when a node cannot be
reached.

## Requirements

### Requirement: Fleet status

`spinloop status` SHALL query every node in the resolved target and render one row per node: the node name, its engine state (`idle`/`running`/`stopped`/`crashed`), what it is serving (runner and model when known), the spinloop version of the daemon on that node, and its reachability. Nodes SHALL be queried concurrently so the command's latency is that of the slowest reachable node, not their sum.

The target SHALL resolve as it does for every other command that acts on a fleet — a named environment, a named fleet file, or the working directory's fleet file — so one environment, a fleet, and a fleet holding that same environment are all read by the one command, rendered identically however the target was named.

A node's health SHALL be reported as the readiness mark every node carries, so a cloud environment whose endpoint is unhealthy reads the same as a daemon node whose engine has not answered its health check. A field only one node kind fills — an endpoint's address, a retention deadline — SHALL NOT be a column of a table meant to be scanned one row per node.

A node SHALL also report how long it has been since its engine last did work, taken from the activity its daemon tracks — "which of my nodes is doing nothing?" is a question a fleet view exists to answer, and the daemon already knows. That figure SHALL NOT be labelled in a way that collides with the `idle` engine state, which means something different. A node whose daemon reports no activity yet SHALL omit the figure rather than imply an engine has sat unused since it started.

#### Scenario: Mixed fleet renders every node

- **WHEN** `spinloop status` runs against a fleet of several nodes
- **THEN** the output has one row per node showing its state, version, and what it serves

#### Scenario: A named environment is read by the same command

- **WHEN** `spinloop status --env prod` runs
- **THEN** that environment's row is rendered with no fleet file required, and
  it reads the same as the row for the same environment named in a fleet file

#### Scenario: An unhealthy endpoint reads as not ready

- **WHEN** `spinloop status --env prod` runs and the control plane reports the
  endpoint unhealthy
- **THEN** the row carries the same not-ready mark a daemon node carries when
  its engine has not answered its health check

#### Scenario: A node reports how long since it last did work

- **WHEN** `spinloop status` runs against a node whose daemon reports a last-active time
- **THEN** that node's row shows how long ago that was, labelled so it is not confused with the `idle` engine state

#### Scenario: A node with no recorded activity omits the figure

- **WHEN** a node's daemon reports no last-active time, because its engine has done no work yet
- **THEN** that node's row shows no activity figure rather than a misleading one

#### Scenario: Version is shown per node

- **WHEN** `spinloop status` runs against a fleet of running nodes
- **THEN** each node's row includes the spinloop version string from its daemon

#### Scenario: Version is omitted for unreachable nodes

- **WHEN** a node's daemon is unreachable
- **THEN** that node's row shows its failure outcome without a version

#### Scenario: The fleet-scoped spelling names its replacement

- **WHEN** the operator runs `spinloop fleet status`
- **THEN** it fails naming `spinloop status` as the command that replaced it

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

- **WHEN** `spinloop fleet status` runs and one node's daemon is unreachable
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

`spinloop metrics` SHALL query every node in the resolved target and render
each node's engine and system metrics using the same bar, gauge, table and
json formats, selected by `--format`.
Unreachable nodes SHALL be reported as in status rather than omitted. The
command SHALL support a `--watch`/`-w` mode that refreshes on an interval,
clearing and redrawing the screen in place with no scrollback accumulation,
and exiting cleanly on interrupt.

#### Scenario: Gauge format per node

- **WHEN** `spinloop metrics` runs without `--format`
- **THEN** each reachable node's resource series render in gauge format under
  its name

#### Scenario: Bar format per node

- **WHEN** `spinloop fleet metrics --format=bar` runs
- **THEN** each reachable node's metrics render in bar format under its name

#### Scenario: JSON aggregates the fleet

- **WHEN** `spinloop fleet metrics --format=json` runs
- **THEN** the output is valid JSON keyed or labelled by node, including
  unreachable nodes with their error

#### Scenario: Watch redraws in place

- **WHEN** `spinloop fleet metrics --watch` runs
- **THEN** each refresh clears the screen and redraws the fleet, and Ctrl+C
  exits cleanly

The target SHALL resolve as it does for every other command that acts on a
fleet — a named environment, a named fleet file, or the working directory's
fleet file — so one environment and a fleet holding it are read by the one
command.

#### Scenario: A named environment is read by the same command

- **WHEN** `spinloop metrics --env prod` runs
- **THEN** that environment's metrics are rendered with no fleet file
  required, in the same format a fleet file naming it would produce

#### Scenario: The fleet-scoped spelling names its replacement

- **WHEN** the operator runs `spinloop fleet metrics`
- **THEN** it fails naming `spinloop metrics` as the command that replaced it

### Requirement: Driving one node

`spinloop fleet start <node...>` SHALL call each named node's daemon start
endpoint (or push a resolved deploy config, for a `kind: daemon` node — see
below); `spinloop fleet stop <node...>` SHALL call each named node's daemon
stop endpoint. `spinloop fleet start --all`/`spinloop fleet stop --all`
SHALL target every node in the file instead, of either kind. Either command
invoked with neither a node name nor `--all` SHALL fail and list the
available nodes, rather than acting on the whole fleet by default. `--all`
combined with one or more node names SHALL fail as ambiguous, for either
command. An unknown node name SHALL fail the command, naming the known
nodes, before anything is started or stopped. The daemon's own rules still
hold — a start while that node's engine is running is reported as the
daemon's conflict for that node, and a stop is idempotent. Multiple targeted
nodes SHALL be driven independently, for either command: one node's failure
(including, for start, an unresolved Spinloop source, see below) SHALL be
reported against that node alone and SHALL NOT stop the others; the command
SHALL exit non-zero when any targeted node failed.

For a `kind: daemon` node, `fleet start` SHALL first resolve that node's
Spinloop source (see fleet-config's "Node Spinloop source" and "...falls
back to name-based lookup" requirements), and SHALL fail that node's start,
naming all three ways a source could have been given, when none resolves.
When one resolves, the client SHALL derive a deploy config from it — the
same node-owned derivation a routed wake already uses
(`deployConfigForNode`) — report the resolved source and derived config
alongside the node's name, and start the node's engine with that config
(`StartWith`) rather than a plain start, exactly as a routed wake tells a
node what to serve. A `kind: cloud` node's start is unaffected regardless
of whether a source resolves for it: what it serves is fixed at deploy time,
not pushed at start time, so it always uses a plain start.

#### Scenario: Start a named node

- **WHEN** `spinloop fleet start gpu-box` runs, that node is idle, and its
  Spinloop source resolves
- **THEN** the client derives a deploy config from the resolved source and
  calls that node's daemon start endpoint with it, reporting the resulting
  state

#### Scenario: Start several named nodes

- **WHEN** `spinloop fleet start gpu-a gpu-b` runs and both nodes' Spinloop
  sources resolve
- **THEN** both nodes start, independently, whatever else the file lists

#### Scenario: Start every node

- **WHEN** `spinloop fleet start --all` runs against a file mixing `kind:
  cloud` and `kind: daemon` nodes, and every `kind: daemon` node's Spinloop
  source resolves
- **THEN** every node in the file starts — the daemon nodes with their
  resolved config, the cloud nodes with a plain start

#### Scenario: Start with no node names the fleet

- **WHEN** `spinloop fleet start` runs with no node argument and no `--all`
- **THEN** it fails, listing the nodes, and starts nothing

#### Scenario: Combining --all with node names is an error

- **WHEN** `spinloop fleet start --all gpu-a` or `spinloop fleet stop --all
  gpu-a` runs
- **THEN** it fails as ambiguous and neither starts nor stops anything

#### Scenario: Unknown node

- **WHEN** `spinloop fleet stop nope` runs and no node is named `nope`
- **THEN** it fails, naming the known nodes, and stops nothing

#### Scenario: An unknown name among several fails before starting any

- **WHEN** `spinloop fleet start gpu-a nope` runs and no node is named `nope`
- **THEN** the command fails, naming the known nodes, and starts neither node

#### Scenario: Stop several named nodes

- **WHEN** `spinloop fleet stop gpu-a gpu-b` runs
- **THEN** both nodes are stopped, independently, whatever else the file
  lists

#### Scenario: Stop every node

- **WHEN** `spinloop fleet stop --all` runs against a file mixing `kind:
  cloud` and `kind: daemon` nodes
- **THEN** every node in the file is stopped

#### Scenario: Stop with no node names the fleet

- **WHEN** `spinloop fleet stop` runs with no node argument and no `--all`
- **THEN** it fails, listing the nodes, and stops nothing

#### Scenario: An unknown name among several fails before stopping any

- **WHEN** `spinloop fleet stop gpu-a nope` runs and no node is named `nope`
- **THEN** the command fails, naming the known nodes, and stops neither node

#### Scenario: Starting a daemon node with a resolved source pushes it

- **WHEN** `spinloop fleet start dev-1` runs, `dev-1` is a `kind: daemon`
  node, and its Spinloop source resolves (by `file`, alias, or subdirectory)
- **THEN** the client derives a deploy config from the resolved Spinloop,
  reports the resolved source, and starts `dev-1`'s engine with that config

#### Scenario: Starting a daemon node with no resolvable source fails

- **WHEN** `spinloop fleet start studio` runs, `studio` is a `kind: daemon`
  node, and no `file` field, alias, or subdirectory resolves for it
- **THEN** the command fails for `studio`, naming the `file` field, the
  alias registry, and the subdirectory convention as the three ways a source
  could have been given, and nothing is started

#### Scenario: One unresolved node among several fails only that node

- **WHEN** `spinloop fleet start --all` runs, and one `kind: daemon` node in
  the file has no resolvable Spinloop source while the rest do
- **THEN** every other targeted node starts, the unresolved node is reported
  as failed naming the three ways a source could have been given, and the
  command exits non-zero

#### Scenario: Starting a cloud node is unaffected by a resolved source

- **WHEN** `spinloop fleet start gpu-env` runs, `gpu-env` is a `kind: cloud`
  node, and a Spinloop source resolves for it
- **THEN** the client starts it with a plain start; the resolved source is
  not used, since a `kind: cloud` node's `StartWith` always refuses a
  deploy config

### Requirement: Fleet deploy targets cloud nodes

`spinloop fleet deploy <node...>` SHALL deploy the AWS environment for one or
more `kind: cloud` nodes in the fleet file, named explicitly.
`spinloop fleet deploy --all` SHALL target every `kind: cloud` node in the
file instead. Invoked with neither a node name nor `--all`, it SHALL fail,
listing the fleet's `kind: cloud` nodes, and deploy nothing — mutating
however many cloud environments a fleet file lists SHALL NOT happen by
default. `--all` combined with one or more node names SHALL fail as
ambiguous. An unknown node name SHALL fail the command, naming the known
nodes, without deploying anything. A named `kind: daemon` node SHALL fail
the command, explaining that `fleet deploy` provisions cloud environments
and that node is not one; `--all` SHALL only ever select `kind: cloud`
nodes, so a `kind: daemon` node is never targeted by it and is not reported
at all.

#### Scenario: Deploy every cloud node

- **WHEN** `spinloop fleet deploy --all` runs against a file mixing `kind:
  cloud` and `kind: daemon` nodes
- **THEN** every `kind: cloud` node is deployed and no `kind: daemon` node
  is touched or mentioned

#### Scenario: Deploy named nodes

- **WHEN** `spinloop fleet deploy gpu-a gpu-b` runs and both are `kind:
  cloud` nodes in the file
- **THEN** only those two are deployed, whatever else the file lists

#### Scenario: No target is an error

- **WHEN** `spinloop fleet deploy` runs with no node arguments and no `--all`
- **THEN** it fails, listing the fleet's `kind: cloud` nodes, and deploys
  nothing

#### Scenario: Combining --all with node names is an error

- **WHEN** `spinloop fleet deploy --all gpu-a` runs
- **THEN** it fails as ambiguous and deploys nothing

#### Scenario: An unknown node name fails the command

- **WHEN** `spinloop fleet deploy nope` runs and no node is named `nope`
- **THEN** the command fails, naming the known nodes, and deploys nothing

#### Scenario: Naming a daemon node explicitly fails

- **WHEN** `spinloop fleet deploy studio` runs and `studio` is a `kind:
  daemon` node
- **THEN** the command fails, explaining that `fleet deploy` provisions cloud
  environments and `studio` is not one

### Requirement: Fleet deploy derives and applies each node's config

Each targeted node SHALL be deployed from the Spinloop file its deploy
source resolves to (see fleet-config's "Node Spinloop source" and "...falls
back to name-based lookup" requirements: its `file` field, else an alias
registered under its name, else a `<name>/` subdirectory beside the fleet
file), deriving the deploy config and registering the resulting environment
exactly as `spinloop cloud deploy <file>` does for that same file — the two
SHALL NOT be able to disagree about what a given Spinloop file deploys. A
targeted node for which no source resolves SHALL fail for that node alone,
naming all three ways one could have been given, without touching the other
targeted nodes. The resolved source (the path used, or the alias name when
one was used) SHALL be reported alongside that node's plan, so which of the
three supplied it is never left to be inferred.

Where a node declares an `instance-type` in the fleet file, the deploy config
derived for it SHALL carry that type, so the node's environment launches as
named — the same value a standalone `spinloop cloud deploy --instance-type`
would record for the environment — and a node declaring none SHALL deploy an
environment on the control plane's default type. This keeps `fleet deploy` and
a matching standalone deploy in agreement about what a node's environment
launches as.

Nodes SHALL be deployed independently: one node already registered or live
SHALL require `--overwrite` for that node exactly as a standalone `cloud
deploy` does, and refusing it SHALL NOT stop the other targeted nodes from
deploying. A node whose deploy fails for any other reason SHALL likewise be
reported against that node without aborting the rest. The command SHALL exit
non-zero when any targeted node failed to deploy, having still attempted
every other targeted node.

`--dry-run` SHALL print the plan for every targeted node without deploying
any of them, exactly as a standalone `cloud deploy --dry-run` does for one.
`--overwrite` SHALL apply to every targeted node that needs it.

#### Scenario: A node deploys from its own Spinloop file

- **WHEN** `fleet deploy` targets a node declaring `file:
  ./envs/gpu.Spinloop`
- **THEN** that node's environment is created and registered from that file,
  the same as `spinloop cloud deploy ./envs/gpu.Spinloop` would produce, and
  the resolved path is reported against that node

#### Scenario: A node's declared instance type is deployed

- **WHEN** `fleet deploy` targets a `kind: cloud` node declaring
  `instance-type: g6e.2xlarge`
- **THEN** the environment it deploys launches as `g6e.2xlarge`, the same
  value a standalone `spinloop cloud deploy --instance-type g6e.2xlarge` of
  the node's source would record

#### Scenario: A node with no instance type deploys the default

- **WHEN** `fleet deploy` targets a `kind: cloud` node declaring no
  `instance-type`
- **THEN** the environment it deploys launches as the control plane's default
  instance type

#### Scenario: A node with no resolvable source fails only that node

- **WHEN** `fleet deploy` targets two cloud nodes and one declares no `file`
  field, has no alias registered under its name, and has no same-named
  subdirectory beside the fleet file
- **THEN** the other node still deploys, and the command reports against the
  unresolved node that none of the `file` field, a matching alias, or a
  matching subdirectory was found

#### Scenario: One node's guard does not block the others

- **WHEN** `fleet deploy` targets two cloud nodes and one is already
  registered while the other is not, and `--overwrite` is not given
- **THEN** the unregistered node deploys, the registered node is refused with
  the same message a standalone `cloud deploy` gives, and the command exits
  non-zero

#### Scenario: Dry run previews every targeted node

- **WHEN** `spinloop fleet deploy --dry-run --all` runs
- **THEN** the plan for every `kind: cloud` node in the file is printed and
  no environment is created or registered

### Requirement: Fleet deploy reports progress and results legibly

`fleet deploy` SHALL indicate that a targeted node's deploy is still in
progress for as long as it is running, on an output capable of an in-place
update, so a run against several nodes — which can take AWS-call minutes per
node — is never silently unresponsive. Each targeted node's final report
SHALL be clearly delimited from every other targeted node's, identifying
which node it describes, so one node's report cannot be mistaken for
bleeding into the next. The command SHALL close with a summary stating how
many of the targeted nodes succeeded.

On an output that is not an interactive terminal (piped, redirected, or
otherwise non-interactive), the command SHALL NOT emit an in-place progress
indicator or other terminal-control escape sequences — a downstream consumer
of that output (a log file, a script, CI) SHALL see only the per-node
reports and the summary, in the order the nodes were targeted.

#### Scenario: Progress is shown while nodes are still deploying

- **WHEN** `fleet deploy --all` targets several nodes on an interactive
  terminal, and their deploys are still in progress
- **THEN** each still-deploying node is shown as in progress until it
  finishes

#### Scenario: Node reports are clearly separated

- **WHEN** `fleet deploy` targets two or more nodes
- **THEN** each node's report is headed by something identifying that node,
  so the boundary between one node's report and the next is unambiguous

#### Scenario: A summary closes the report

- **WHEN** `fleet deploy` finishes against several targeted nodes
- **THEN** the command's output ends with a line stating how many of the
  targeted nodes deployed successfully

#### Scenario: Piped output carries no escape sequences

- **WHEN** `fleet deploy`'s output is piped or redirected rather than an
  interactive terminal
- **THEN** the output contains no in-place progress indicator and no
  terminal-control escape sequences, only the per-node reports and the
  summary

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

`spinloop logs` SHALL read the engine output of the target's nodes, so "what did that engine say?" is answerable from the same
place as "what is it doing?" — without shell access to any machine. With no node
named it SHALL read every node in the fleet; naming a node SHALL restrict it to
that one. Nodes SHALL be read concurrently, so the command's latency is that of
the slowest reachable node rather than their sum.

The target SHALL resolve as it does for every other command that acts on a
fleet. The fleet file SHALL be named by the long form `--fleet` only: unlike
the other commands that take one, `logs` SHALL NOT accept `-f` for it, because
`-f` is that command's `--follow` short form and a flag cannot carry two
meanings on one command line.

#### Scenario: Reading the whole fleet

- **WHEN** the operator runs `spinloop logs` with no node named
- **THEN** every node's engine output is read and printed

#### Scenario: Reading one node

- **WHEN** the operator names a node
- **THEN** only that node's output is printed, and the other nodes are not
  contacted

#### Scenario: A crashed node's output is readable

- **WHEN** a node's engine has crashed, as `spinloop status` reports
- **THEN** its output up to the crash is printed, explaining what status can
  only report

#### Scenario: The fleet file has no short flag here

- **WHEN** the operator runs `spinloop fleet logs -f --fleet ./cluster.yaml`
- **THEN** the flag is accepted as follow mode plus the fleet file, with `-f`
  not treated as a fleet-file flag

#### Scenario: A named environment is read by the same command

- **WHEN** `spinloop logs --env prod` runs
- **THEN** that environment's engine output is read with no fleet file
  required

#### Scenario: The fleet-scoped spelling names its replacement

- **WHEN** the operator runs `spinloop fleet logs`
- **THEN** it fails naming `spinloop logs` as the command that replaced it

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

`spinloop fleet logs` SHALL be able to keep running and print output as nodes
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

`spinloop fleet route` SHALL report the node a harness launch would choose for a
given Spinloop, the endpoint that node resolves to, and why it was chosen — and
SHALL change nothing: it SHALL never push a config, start an engine, or write a
harness config. It is how a routing decision is checked before an agent depends
on it, and how an unexpected choice is diagnosed after one.

When no node would be chosen, it SHALL report each node's state and the reason
it was passed over, and SHALL name what would happen on a real launch: which
node would be woken, or that none could serve it.

The Spinloop and the fleet file SHALL resolve as they do for a launch: the
Spinloop path defaults to `./Spinloop`, the fleet file comes from
`--fleet`/`-f`, and a Spinloop the user did not name explicitly picks up a
`./fleet.yaml` in the working directory. It SHALL accept `--prefer` and
`--node` as a launch does, and SHALL name the activity preference in force —
comparing the two preferences on a live fleet is the cheapest way to decide
which one a fleet should be run with. With no fleet file in force, the command
SHALL fail naming `--fleet`.

#### Scenario: The chosen node is explained

- **WHEN** `spinloop fleet route` runs against a fleet with a node serving the
  Spinloop's model
- **THEN** it prints that node, its resolved engine endpoint, and why it was
  chosen

#### Scenario: Routing changes nothing

- **WHEN** `spinloop fleet route` runs against a fleet where no node is serving
  the Spinloop's model
- **THEN** no engine is started, no config is pushed, and no harness config is
  written

#### Scenario: The two preferences can be compared

- **WHEN** `spinloop fleet route --prefer active` runs against a fleet whose file
  declares `prefer: idle`
- **THEN** it reports the node `active` would choose and names that preference,
  without changing the fleet file

#### Scenario: A launch that would wake a node says so

- **WHEN** `spinloop fleet route` runs and no node is serving the model but one
  could
- **THEN** it names the node a launch would wake, and does not wake it

#### Scenario: No fleet file names the flag

- **WHEN** `spinloop fleet route` runs on an explicitly named Spinloop in a
  directory holding no `fleet.yaml`, passing no `--fleet`
- **THEN** it fails saying there is no fleet file to route through, and names
  `--fleet`

### Requirement: Fleet dashboard

`spinloop dashboard` SHALL open an interactive, full-screen view of the resolved target:
one panel per node, arranged in a grid and refreshed
continuously without operator input. It is the one place where what the fleet is
doing and acting on it meet: unlike `fleet metrics --watch`, it takes keyboard
input and drives the node the operator has selected.

The target SHALL resolve as it does for every other command that acts on a
fleet, so a single named environment opens as a board of one panel. The
dashboard SHALL be openable and usable from cold — a fleet where nothing is up:
every panel SHALL show its node's outcome and reason rather than metrics, and a
node SHALL be startable from within the dashboard.

A problem with the target itself (a missing or unparseable fleet file, an
environment that is not registered) SHALL fail the command before the view
opens, as it does for the other commands that act on a fleet; a problem
with any node SHALL NOT.

#### Scenario: A mixed fleet renders every node

- **WHEN** the operator opens the dashboard against a fleet with some nodes
  answering and some not
- **THEN** every node has a panel: answering nodes show their metrics, the others
  show their outcome and reason, and the view keeps running

#### Scenario: A named environment opens as one panel

- **WHEN** the operator runs `spinloop dashboard --env prod` in a directory
  holding no fleet file
- **THEN** the view opens on that environment alone, and the absent file is not
  an error

#### Scenario: Opening on a fleet where nothing is up

- **WHEN** the operator opens the dashboard and no node is reachable
- **THEN** every panel shows its node's outcome and reason
- **AND** the dashboard does not exit, and starting a node from it works

#### Scenario: A fleet-file problem fails before the view

- **WHEN** the named fleet file is missing or unparseable
- **THEN** the command fails naming the problem, and no interactive view opens

#### Scenario: A non-interactive context is refused

- **WHEN** the dashboard is run with its input or output not on a terminal, such
  as through a pipe or in the background
- **THEN** it fails with a message pointing at `fleet metrics --watch`, and it
  does not enter raw terminal mode or emit screen escapes

#### Scenario: The fleet-scoped spelling names its replacement

- **WHEN** the operator runs `spinloop fleet dashboard`
- **THEN** it fails naming `spinloop dashboard` as the command that replaced it

### Requirement: Dashboard panels show the node's metrics

Each panel SHALL show, for a node that answered the last completed refresh, the
same facts the bar format of `fleet metrics` renders for that node: its state,
what it serves (runner and model when known), how long since it last did work
(with the same labelling rules as the rest of the fleet surfaces), its resource
usage, and its token and request counters. A panel SHALL draw the node's
resource series in the board's current format — gauge by default; where the
board is in bar format, from the history the node's daemon reports, falling
back per the bar format's no-history rule where it reports none. A panel SHALL show the answer of the last completed
refresh for that node — not a mix of refreshes and not a stale bar with a fresh
outcome.

A panel SHALL degrade gracefully when a node answers with fewer facts (no system
stats, no GPUs, an engine that is not running) rather than failing to render.
A node whose answer is a failure — unreachable, unauthorised, a configuration
error — SHALL show its typed outcome and reason in its panel instead of metrics.

One node SHALL be selected at a time, and the selected panel SHALL be
distinguishable at a glance from the others. Selection SHALL move according to
the grid the dashboard is currently rendering, not the fleet file's flat
order: the up and down keys SHALL move the selection to the tile directly
above or below it, in the same column of the adjacent row; the left and right
keys SHALL move the selection to the adjacent tile in the same row. None of
the four directions SHALL wrap — a move off the grid's edge SHALL leave the
selection where it was. A resize that changes how many tiles fit per row
SHALL be reflected immediately: the next move follows the new grid, not the
one the dashboard was drawing before the resize.

#### Scenario: A running node's panel updates

- **WHEN** a node's engine is running and its counters or utilisation change
- **THEN** the node's panel shows the new figures on a subsequent refresh,
  without the operator pressing any key

#### Scenario: A node that reports fewer facts still renders

- **WHEN** a node's answer carries no GPU or system statistics
- **THEN** its panel shows the facts it has, with the missing ones absent rather
  than an error

#### Scenario: A failing node's panel shows why

- **WHEN** a node cannot be reached, or its daemon rejects the client
- **THEN** its panel shows the node's outcome and reason, and the other panels
  are unaffected

#### Scenario: Selection moves and is visible

- **WHEN** the operator moves the selection with the navigation keys
- **THEN** the selection moves according to the currently rendered grid and
  the selected panel is visibly distinct from the others

#### Scenario: Up and down move by grid row

- **WHEN** the dashboard renders more than one tile per row and the operator
  presses the down key
- **THEN** the selection moves to the tile directly below it, in the same
  column of the next row, rather than to the next tile in the fleet file's
  order

#### Scenario: Left and right move within a row

- **WHEN** the operator presses the right key
- **THEN** the selection moves to the next tile in the same row
- **AND** pressing the left key from there returns the selection to the tile
  it started on

#### Scenario: Movement clamps at the grid's edges

- **WHEN** the selection is on the top row and the operator presses up, on the
  bottom row and presses down, on the first column and presses left, or on the
  last column and presses right
- **THEN** the selection does not move and does not wrap to the opposite edge

#### Scenario: Down clamps on a short last row

- **WHEN** the selection is in a column that the last, partially-filled grid
  row does not reach, and the operator presses down
- **THEN** the selection moves to the last tile that exists in that row
  instead of moving past the end of the fleet

#### Scenario: A resize changes the grid the arrow keys follow

- **WHEN** the terminal is resized so the dashboard now fits a different
  number of tiles per row, and the operator then presses an arrow key
- **THEN** the selection moves according to the new column count, not the one
  in effect before the resize

### Requirement: The dashboard drives the selected node

The dashboard SHALL let the operator start and stop the engine of the node
currently selected, from the keyboard, through the same node operations the one-
shot `fleet start` and `fleet stop` commands use. An action SHALL reach the
node under the cursor and no other. An action is one per node, not one per
board: while one node is starting, the operator selects another and starts it,
and the wakes run side by side. A node that already has an action in flight
SHALL take no further start or stop; the dashboard says it is still working
and drives nothing.

Start SHALL proceed without confirmation. Stop SHALL require an explicit
confirmation before it is sent, because it ends an engine that may be serving
work: a declined or abandoned confirmation SHALL send nothing.

While an action is in flight, the node's own tile SHALL carry it: the verb, the
action's current situation, and how long the action has been running, beside the
node's last report rather than in place of it — the action's own account says
what the operator asked for, the report says what the node is doing. For a node
whose last completed refresh answered, the tile SHALL show that answer's state,
what it serves, its last-active record, and its resource usage and token and
request counters whenever the answer carries them, whatever the node's state — a
boot half done is already measuring, and that is the truth the tile keeps showing
while the call works. A node whose action reports nothing and whose refresh has
not yet answered SHALL show the verb alone, and a latest refresh that failed
SHALL change nothing on the tile. Each finished action SHALL clear the action
from its node's tile, which is then the node's report alone, and leave its
outcome on the status line, the one-shot wording.

An action SHALL have exactly one current situation at a time, and a new one
SHALL replace its predecessor outright rather than being added to it. A
situation that was true of an attempt the action has moved on from SHALL NOT be
shown: the tile reports what the action is doing now, never what it was doing
before. In particular, a start refused for want of capacity SHALL stop reporting
that refusal once a further attempt is under way — so a tile never shows a
capacity wait beside a refresh reporting the node up and running.

Anything the tile says about time SHALL be computed when the tile is drawn, not
when the situation it describes arose: a wait counts down towards the attempt it
is waiting for, and an action counts up from when the operator issued it. A
start's situation can hold unchanged for minutes — the attempt that succeeds
keeps one request open for the whole boot and reports nothing while it does — so
these are what distinguish an action that is waiting from one that is wedged.

An action in flight SHALL also carry a spinner beside its verb, whose frame is
likewise chosen when the tile is drawn, and the board SHALL redraw often enough
for it to turn. It says the same thing as the elapsed time to an operator
glancing rather than reading, and it says it on a tile whose every other line
can hold still for minutes.

The outcome of an action SHALL be shown inside the dashboard (a status line the
operator can read before the next refresh replaces attention), and a refused or
unreachable action SHALL NOT close the dashboard. What an action changes in state
— an engine coming up after a start, coming down after a stop — SHALL appear
through the normal refresh, without the operator asking for it.

A start in flight SHALL be abortable from the keyboard, on the node under the
cursor. A start carries no deadline — a cold cloud wake takes minutes, and a
deadline would report a failure to a slow success — so the operator's abort is
its exit: an abort SHALL end the dashboard's wait on the start, return the
node's tile to the node's report, free the node to be started or stopped again,
and show its outcome on the status line, without closing the dashboard. An abort
ends the wait, not the work it set in motion: the dashboard SHALL NOT present it
as a cancellation of the wake, and what the wake goes on to do — the node's
state — SHALL appear through the normal refresh.

A stop in flight SHALL NOT be abortable: it targets an engine that is already
running rather than a cold wake with no deadline of its own, and abandoning
the dashboard's wait on it would leave the operator unsure whether the stop
still went ahead. The abort key SHALL drive nothing on a node whose in-flight
action is a stop.

The dashboard's key help SHALL name the abort key only while a start is in
flight on the node it describes — the node under the cursor on the grid, the
node in view in the detail view — and SHALL NOT name it for an idle or
running node, or one whose in-flight action is a stop, so the operator is
never invited to press a key that would do nothing there.

The dashboard's key help SHALL name the start key for the node it describes
only when that node has no action in flight and the board's current read of it
does not report it running, and SHALL name the stop key for that node only when
it has no action in flight and the board's current read of it reports it
running, so the operator is never invited to press a key that would do nothing
there. A read reports its node running when it answered and carries a running
state; a node whose current read reports no state at all — no read has
answered yet, or its newest read failed — SHALL be offered the start key rather
than the stop key, since the board cannot say the node is running and start is
the key that might still do something on it. A node that could not become a
node at all SHALL be offered neither key, since neither would drive anything on
it.

#### Scenario: Starting a cold node

- **WHEN** the operator selects a node with no engine running and issues the
  start
- **THEN** the start is sent without a prompt, its outcome is shown in the
  status line, and the panel shows the node's new state as the refreshes come
  around

#### Scenario: Stopping asks first

- **WHEN** the operator issues the stop on the selected node
- **THEN** the dashboard asks for confirmation and nothing is sent
- **WHEN** the operator declines
- **THEN** the stop is not sent and the node's state is unchanged

#### Scenario: A confirmed stop is sent

- **WHEN** the operator issues the stop and confirms
- **THEN** the stop is sent to the selected node only, its outcome is shown in
  the status line, and the panel follows the node's state on subsequent refreshes

#### Scenario: A start is watched on its own tile

- **WHEN** the operator starts a node whose start reports its situation as it
  works
- **THEN** the node's tile shows the verb and the start's current situation
  while the start is in flight
- **AND** a refresh that answers while the start is in flight shows its state
  and whatever it measures on the same tile, beneath the start's own account
- **AND** when the start finishes, the tile is the node's report alone and the
  outcome is on the status line

#### Scenario: A refused start does not outlive its refusal

- **WHEN** a node's start is refused for want of capacity, and a later attempt
  is issued once capacity is free
- **THEN** the tile reports the capacity wait while it holds, and stops
  reporting it once the next attempt is under way, rather than showing it
  beside a refresh reporting the node running

#### Scenario: A start's elapsed time keeps moving

- **WHEN** a start is in flight and its situation does not change for some time
- **THEN** the elapsed time beside the verb keeps advancing on the tile, so the
  operator can tell the start is still waiting rather than wedged

#### Scenario: A wait counts down and an action counts up

- **WHEN** a start is waiting for its next attempt and the operator watches
  without pressing anything
- **THEN** the time until that attempt counts down on the tile, and the time
  since the operator issued the start counts up, both advancing as the tile is
  redrawn rather than standing at the values they held when the wait began
- **AND** the spinner beside the verb turns while they do

#### Scenario: An in-flight start before any report

- **WHEN** the operator starts a node before any refresh of it has answered
- **THEN** its tile shows the verb and the start's own account alone
- **AND** the first answered refresh appears on the tile beside them

#### Scenario: Two nodes wake at once

- **WHEN** the operator starts one node, selects another, and starts it
- **THEN** both wakes run at the same time, each reported on its own tile
- **AND** each finishing action clears its own node and leaves its outcome on
  the status line without disturbing the other

#### Scenario: A node still starting is not started again

- **WHEN** the operator presses start on a node whose start is in flight
- **THEN** the dashboard drives nothing and says the node is still starting

#### Scenario: An in-flight start can be abandoned

- **WHEN** the operator's start on a node is still in flight — the cloud waiting
  for capacity, the boot slow, or the connection behind it dropping and
  retrying — and the operator issues the abort on that node
- **THEN** the dashboard stops waiting on the start, the node's tile returns to
  the node's report, and the outcome is shown on the status line
- **AND** the node may be started or stopped again, and the dashboard keeps
  running with its refreshes

#### Scenario: An abort is not a cancellation of the wake

- **WHEN** the operator aborts a start whose wake the cloud is already carrying
  on
- **THEN** the dashboard reports that it stopped waiting, not that the wake was
  cancelled
- **AND** the node's state, whatever the wake goes on to do, appears through the
  normal refresh

#### Scenario: A stop in flight cannot be aborted

- **WHEN** the operator issues the abort on a node whose stop is in flight
- **THEN** the dashboard drives nothing: the stop keeps running, the tile keeps
  showing it as stopping, and the node's state comes back through the normal
  refresh

#### Scenario: The key help hides abort when nothing is abortable

- **WHEN** the node under the cursor (or shown in the detail view) is idle,
  running with nothing in flight, or has a stop in flight
- **THEN** the key help does not name the abort key
- **WHEN** that node has a start in flight
- **THEN** the key help names the abort key

#### Scenario: The key help hides start and stop where they would do nothing

- **WHEN** the node under the cursor (or shown in the detail view) has no
  action in flight and the board's current read of it reports it running
- **THEN** the key help names the stop key and does not name the start key
- **WHEN** that node has no action in flight and the board's current read of
  it reports it not running
- **THEN** the key help names the start key and does not name the stop key
- **WHEN** that node has an action in flight, or could not become a node at all
- **THEN** the key help names neither the start key nor the stop key

#### Scenario: The key help offers start when the node's state is unknown

- **WHEN** the node under the cursor has no action in flight, and no read of
  it has answered yet, or its newest read failed
- **THEN** the key help names the start key and does not name the stop key

#### Scenario: An action that fails keeps the dashboard open

- **WHEN** an action is sent to a node that cannot be reached, or the node
  refuses it
- **THEN** the failure is shown in the status line with the daemon's own reason
  and the dashboard keeps running with its refreshes

### Requirement: Node detail view opens and closes

From the grid, `<enter>` on the selected node SHALL open a full-screen detail
view of that node, replacing the grid; `<esc>` SHALL close the detail view and
return to the grid with the same node still selected. Opening or closing the
detail view SHALL NOT interrupt the fleet's refresh or any action in flight on
any node.

The detail view SHALL be available for any node, including one whose last
answer was a failure (unreachable, unauthorised, a configuration error): the
detail view SHALL show what it can for that node rather than refusing to open.

#### Scenario: Opening the detail view

- **WHEN** the operator selects a node in the grid and presses enter
- **THEN** the grid is replaced by a full-screen view of that node

#### Scenario: Closing the detail view

- **WHEN** the operator presses escape while the detail view is open
- **THEN** the grid is shown again with the same node selected as before

#### Scenario: The detail view opens on a failing node

- **WHEN** the operator opens the detail view for a node that is unreachable or
  otherwise failing
- **THEN** the view opens and shows the node's outcome and reason in place of
  metrics, rather than refusing to open

#### Scenario: Background refresh continues behind the detail view

- **WHEN** the detail view is open
- **THEN** every node in the fleet, including nodes other than the one shown,
  keeps refreshing on its normal cadence, and any action already in flight on
  another node keeps running

### Requirement: The detail view shows the node's metrics and log

The detail view SHALL be laid out as three stacked sections: the node's
metrics, the node's engine log, and a line naming the keys the view answers
to. The metrics section SHALL show the same facts, in the same wording, that
the node's tile and `fleet metrics` show for it, without the tile's clipping —
a fact that does not fit the tile SHALL be shown in full here. The metrics
section SHALL update on the node's normal refresh cadence, exactly as the
tile does.

The log section SHALL show the node's engine log, tailing and following it
the same way `spinloop fleet logs -f <node>` follows one node's log, so new
output appears while the view is open without the operator asking for it. A
node whose engine has not run yet, or that cannot supply its log, SHALL show
the same explanation the `fleet logs` command gives for it, rather than an
empty pane.

The operator SHALL be able to pause and resume the log's follow from the
keyboard, independently of the rest of the view: while paused, the log
section SHALL stop picking up new output, and the view SHALL show whether the
log is following or paused. Nothing written while paused is lost: resuming
SHALL fetch and show whatever the engine wrote in the meantime, the same
backlog a poll would have picked up had it never paused. Pausing SHALL NOT
affect the metrics section's own refresh, or any node's start, stop or abort.

#### Scenario: Metrics match the tile

- **WHEN** the detail view is open on a node with a running engine
- **THEN** the metrics section shows the same state, serving facts, resource
  usage and counters the node's tile shows, in full rather than clipped

#### Scenario: The log pane follows new output

- **WHEN** the node's engine writes to its log while the detail view is open
- **THEN** the new lines appear in the log section without the operator
  pressing any key

#### Scenario: A node with no log yet

- **WHEN** the detail view is open on a node whose engine has never run
- **THEN** the log section shows the same explanation `fleet logs` gives for
  that node, not an empty pane

#### Scenario: Pausing the log

- **WHEN** the operator pauses the log's follow
- **THEN** the log section stops picking up new output and the view shows
  that the log is paused
- **AND** the metrics section keeps refreshing on its own cadence

#### Scenario: Resuming the log

- **WHEN** the operator resumes a paused log
- **THEN** whatever the engine wrote while paused appears in the log section,
  and new output continues to appear as it is written

### Requirement: The detail view drives the node like the grid

While the detail view is open, the operator SHALL be able to start, stop and
abort the node it shows, through the same confirmation and in-flight rules
the grid applies to the selected node. The node's in-flight status — the verb
and the action's status lines — SHALL be shown in place of its metrics for
the life of the action, the same as the tile shows it. The quit key and
interrupt SHALL NOT exit the dashboard from inside the detail view: quitting
is a grid-level action, and the detail view SHALL take the operator back to
the grid on escape rather than closing the dashboard out from under it.

#### Scenario: Starting a node from its detail view

- **WHEN** the operator issues the start from the detail view of a stopped node
- **THEN** the start is sent without a prompt and the view shows its progress
  in place of the metrics section until it finishes

#### Scenario: Stopping asks for confirmation from the detail view

- **WHEN** the operator issues the stop from the detail view
- **THEN** the view asks for confirmation before sending it, the same as the
  grid does

#### Scenario: The quit key does nothing from the detail view

- **WHEN** the operator presses the quit key, or interrupts, while the detail
  view is open
- **THEN** the dashboard keeps running and the detail view stays open
- **WHEN** the operator then presses escape and the quit key
- **THEN** the dashboard exits from the grid, with the terminal restored

### Requirement: The fleet refreshes without stalling

The dashboard SHALL refresh the fleet continuously, and SHALL also refresh
immediately on the operator's request. The cadence SHALL be by node kind: a
local daemon machine SHALL refresh on a short interval — seconds, not the
watch mode's minute — and a `kind: cloud` environment SHALL refresh on a
much slower cadence, a 60-second interval, one status call a minute, because
its status is a signed call through the cloud control plane rather than a
local socket, and its state changes on the scale of minutes. A manual refresh
SHALL read every node, whether or not its kind's cadence was due. A refresh of a
group SHALL read that group's nodes concurrently, as fan-out does elsewhere.

A node with an action in flight SHALL refresh on the short interval whatever its
kind, and SHALL return to its kind's own cadence once the action settles. The
reasons a cloud environment is read once a minute — the call is expensive and
its state changes slowly — hold for an environment nobody is touching and hold
for neither one the operator has just started.

Every reading of a node SHALL carry the time it was taken, and the dashboard
SHALL NOT show a reading older than the one it is already showing for that node.
Reads are concurrent and of uneven duration, so a reading can land after one
taken later than it; showing it would replace what the node is doing with what
it was doing.

A node whose newest reading has aged well past its cadence SHALL be shown as
such — its age on the panel, and its health unknown rather than whatever the
aged reading said. A stale reading is not a wrong reading, but presenting it
indistinguishably from a current one is: the operator SHALL be able to tell how
old what they are looking at is.

One node that is slow or unanswerable SHALL NOT hold the rest of the fleet
hostage to it: a refresh SHALL give up on a node that has not answered within
the budget of the node's group, show that node with its outcome for this
round, and the other nodes SHALL keep their cadence. The dashboard SHALL NOT
start a second read of a group while that group's previous one has not
finished; the two groups' reads MAY run at the same time, and a slow cloud
round SHALL NOT stretch the local machines' cadence.

#### Scenario: Continuous refresh without input

- **WHEN** the dashboard is open and no key is pressed
- **THEN** the local panels keep updating on the short interval, and each
  cloud panel is re-read once a minute, on the 60-second cadence

#### Scenario: An immediate refresh

- **WHEN** the operator presses the refresh key
- **THEN** every node is re-read, local and cloud alike, rather than waiting
  for each kind's next cadence

#### Scenario: A node being acted on is read more often

- **WHEN** the operator starts a cloud environment
- **THEN** that environment is re-read on the short interval for as long as the
  start is in flight, and returns to its 60-second cadence once the start
  settles
- **AND** the other cloud environments keep their own cadence throughout

#### Scenario: A late reading does not overwrite a newer one

- **WHEN** a read of a node is issued, a later read of the same node answers
  first, and the earlier read then answers
- **THEN** the panel keeps showing the later read, and the earlier one is
  discarded

#### Scenario: A panel with an out-of-date reading shows its age

- **WHEN** a node stops answering and its newest reading ages well past its
  cadence
- **THEN** its panel shows how old that reading is and its health reads
  unknown, rather than continuing to present the aged state as current
- **WHEN** the node answers again
- **THEN** the panel returns to showing its state without an age

#### Scenario: One hung node does not stall the board

- **WHEN** one node does not answer within its group's budget while the others
  do
- **THEN** that node's panel shows its outcome, the other panels refreshed on
  their normal cadence, and no second read of its group started over the
  in-flight one

#### Scenario: A slow cloud round keeps the local cadence

- **WHEN** a cloud environment is slow to answer while its group's read runs
- **THEN** the local machines keep refreshing on the short interval, and the
  cloud panel shows its outcome on the cloud cadence

### Requirement: The dashboard exits cleanly

The dashboard SHALL quit on its quit key and on interrupt. On every exit path it
SHALL restore the terminal — leaving the alternate screen, showing the cursor
again, and restoring terminal modes — so the operator's shell is immediately
usable, and it SHALL leave no trace in the terminal's scrollback.

#### Scenario: Quitting returns to the shell

- **WHEN** the operator presses the quit key, or interrupts
- **THEN** the dashboard exits without an error and the terminal is in the state
  it was before the dashboard opened, with the shell prompt ready

### Requirement: Dashboard panels show a health indicator

Each panel SHALL show a coloured status glyph alongside its node's name,
distinct from the border colour that marks the selected panel, so a node's
health reads at a glance across a grid of many panels without reading each
panel's text. The glyph SHALL be shown in every panel shape: a settled
answer, an action in flight, a panel awaiting its first refresh, and a panel
showing a failed outcome.

A tile SHALL draw its first line — the glyph, the node's name, and what the
node is doing — as a header bar: light text on one background colour running
the tile's full width, so the name reads as the panel's title rather than as
another line of the panel's body. That background SHALL be a single neutral
colour, the same on every tile, so it never competes with the glyph's own
colour for what a node's health is read from.

A node's health SHALL fall into exactly one of five tiers: healthy,
attention, unhealthy, not serving, and unknown. Healthy is coloured green,
attention yellow, and unhealthy red. Not serving and unknown are both
coloured grey and read as "nothing to watch" rather than "something is
wrong"; the two stay apart by their marks — not serving is the same filled
dot as the other tiers in a faded shade, and unknown keeps its `?` — so a
node known to be undeployed never reads as a node the dashboard has not
heard from yet.

- **Healthy**: the node answered its last refresh, that answer is current, its
  engine is not crashed, not `idle`, not `stopped`, and not `undeployed`, and —
  when the daemon reports readiness for it — the engine is ready. A `running`
  node whose daemon reports no readiness (an older daemon, or a runner with no
  known health check) counts as healthy too, rather than reporting a health tier
  the daemon cannot actually back.
- **Attention**: the node has a start or stop action in flight for it, or is
  `running` with its daemon explicitly reporting the engine not yet ready.
- **Unhealthy**: the node's engine has crashed, or its last refresh's
  outcome was a failure (`unreachable`, `unauthorized`, `config-error`,
  `failed`, or `unsupported`).
- **Not serving**: the node answered its last refresh, that answer is current,
  no action is in flight for it, and its engine is not serving — its state is
  `idle`, the daemon has started nothing, `stopped`, a daemon engine that was
  stopped, or `undeployed`, a cloud environment with no instance at all.
- **Unknown**: no current status can be determined for the node — it has not yet
  answered any refresh, its last refresh answered without reporting an engine
  state, or its newest answer has aged well past its cadence and no longer
  describes the node now.

#### Scenario: A running, ready node reads healthy

- **WHEN** a node's last completed refresh reports its engine `running` and
  its daemon reports the engine ready
- **THEN** its panel's status glyph is green

#### Scenario: A running node still loading reads attention

- **WHEN** a node's last completed refresh reports its engine `running` and
  its daemon reports the engine not yet ready
- **THEN** its panel's status glyph is yellow, even though its engine state
  reads `running`

#### Scenario: A running node with no readiness signal reads healthy

- **WHEN** a node's last completed refresh reports its engine `running` and
  its daemon reports no readiness for it
- **THEN** its panel's status glyph is green, the same as before this
  daemon-side signal existed

#### Scenario: A crashed node reads unhealthy

- **WHEN** a node's last completed refresh reports its engine `crashed`
- **THEN** its panel's status glyph is red

#### Scenario: An unreachable node reads unhealthy

- **WHEN** a node's last refresh could not reach its daemon
- **THEN** its panel's status glyph is red, alongside the outcome and reason
  already shown

#### Scenario: A node awaiting its first refresh reads unknown

- **WHEN** the dashboard opens and a node has not yet answered any refresh
- **THEN** its panel's status glyph is grey, until its first refresh lands

#### Scenario: An answer without a state reads unknown

- **WHEN** a node's last refresh answered but reported no engine state
- **THEN** its panel's status glyph is grey

#### Scenario: A node whose answer has gone stale reads unknown

- **WHEN** a node last answered `running` and has not answered since, long
  enough that the answer no longer describes the node now
- **THEN** its panel's status glyph is grey rather than the green that answer
  earned when it was current

#### Scenario: An action in flight reads attention

- **WHEN** the operator starts or stops a node and that action has not yet
  finished
- **THEN** that node's panel's status glyph is yellow while the action is in
  flight, whatever the node's last completed refresh reported

#### Scenario: An undeployed node reads not serving

- **WHEN** a node's last completed refresh reports its engine `idle` and no
  start or stop is in flight for it
- **THEN** its panel's status glyph is the faded grey dot, not the green of
  a serving node

#### Scenario: A stopped node reads not serving

- **WHEN** a node's last completed refresh reports its engine `stopped` — a
  daemon engine that was stopped — and no start or stop is in flight for it
- **THEN** its panel's status glyph is the faded grey dot, not the green of
  a serving node

#### Scenario: An undeployed cloud environment reads not serving

- **WHEN** a cloud environment's last completed refresh reports it
  `undeployed` — it has no instance at all — and no start or stop is in
  flight for it
- **THEN** its panel's status glyph is the faded grey dot, not the green of
  a serving node

#### Scenario: An undeployed node with a start in flight reads attention

- **WHEN** a node's last completed refresh reports its engine `idle` or
  `stopped` and a start for it has not yet finished
- **THEN** its panel's status glyph is yellow while the start is in flight,
  never the grey of not serving

#### Scenario: Not serving and unknown keep different marks

- **WHEN** a grid holds both a node that has answered with `idle` and a node
  that has not yet answered any refresh
- **THEN** the first shows the faded grey filled dot and the second the grey
  `?`, so the two greys are told apart by their mark

The board SHALL carry a title bar of its own along the top of the frame, on
the same surface a tile's header bar uses: the product's name, the screen in
view, and — pushed to the right — the fleet file and what is on that screen. A
terminal too narrow for both halves SHALL keep the left one rather than wrap
the bar onto a second row. The grid and the detail view SHALL draw their title
bar from one place, so the two screens cannot title themselves differently.

The board SHALL use exactly one accent colour, the mint of the spinloop logo,
and SHALL use it only where nothing about a node is being reported: the title
bar's product name, and the border of the selected panel. A node's own state —
the health glyph, the resource bars — SHALL keep the terminal's green, amber
and red, which say what an engine is doing rather than whose product this is.

In the frame's key help, each entry SHALL be drawn as the key and what that
key does: the key in the terminal's own text colour, and what it does a step
back in the muted ink, so the keys are what a glance over the footer picks out.
The prose beside them — a confirmation's question, an action's outcome — SHALL
be left as it is.

#### Scenario: A key stands out from what it does

- **WHEN** the key help is drawn
- **THEN** each key keeps the terminal's own text colour and what it does is
  drawn in the muted ink beside it
- **AND** the status line and a confirmation's question are not drawn that way

#### Scenario: The selected panel is marked in the accent

- **WHEN** the operator moves the selection onto a panel
- **THEN** that panel's border is drawn in the brand accent, and no colour that
  reports a node's state is used for it

#### Scenario: The title bar names the product and the screen

- **WHEN** the dashboard is open on the grid or on a node's detail view
- **THEN** the top of the frame carries a title bar naming the product and that
  screen, with the fleet file and the screen's own details to the right

#### Scenario: A narrow terminal keeps the left half of the title bar

- **WHEN** the terminal is too narrow for both halves of the title bar
- **THEN** the product and the screen are kept and the right half is dropped,
  and the bar stays one row

#### Scenario: The header bar does not carry the health colour

- **WHEN** two nodes of different health are shown side by side
- **THEN** both tiles' header bars are the same colour, and the two nodes are
  told apart by their glyphs

#### Scenario: The glyph is distinct from the selection border

- **WHEN** the operator moves the selection onto a panel
- **THEN** the selected panel's border colour changes as it does today, and
  every panel's status glyph colour is unaffected by which panel is selected

### Requirement: Dashboard format toggle

The dashboard SHALL provide a key, `g`, that toggles the resource series of
every panel between bar and gauge. The board SHALL open in gauge. The toggle
SHALL be board-wide — one format for every panel — rather than per node, and
the key help line SHALL name it.

#### Scenario: Pressing the key switches every panel

- **WHEN** the operator presses `g` on the grid
- **THEN** every panel's resource series redraws in the other format, and
  pressing `g` again returns them

#### Scenario: The board opens in gauge

- **WHEN** the dashboard opens
- **THEN** the panels draw the resource series in gauge format

#### Scenario: The key help names the toggle

- **WHEN** the dashboard draws its key help line
- **THEN** it names `g` as the format toggle

### Requirement: The dashboard keeps the selected node

The dashboard SHALL let the operator set the retention deadline of the node
currently selected, from the keyboard, through the same node operation the
one-shot keep command uses. Keep applies to a node's instance — the time until
which the cloud's idle sweep leaves it alone — so it SHALL be offered only for
nodes that support retention, namely the fleet's cloud environments; a node
without retention support SHALL take no keep.

The keep key SHALL open a duration prompt rather than send anything. The
prompt SHALL name the node it will apply to, show the duration as the operator
has it, and name its confirm and cancel keys. It SHALL open pre-filled with a
default duration, so that the common case is the key and a confirm, and the
confirm SHALL send the duration as shown, whatever the operator has changed it
to. The duration SHALL be expressed in the same syntax the one-shot keep
command accepts.

A confirm of an entry that is not a positive duration SHALL send nothing: the
prompt SHALL stay open and show why the entry was rejected, so the operator can
correct it and confirm again. A cancel SHALL close the prompt and send nothing.
Once the prompt is open, the navigation and selection keys SHALL drive
nothing: the prompt takes the keyboard until it is confirmed or cancelled.

Keep SHALL not ask for a second confirmation after the prompt: the operator
has typed, or deliberately kept, the duration and chosen to send it, and a
keep overwrites a deadline rather than ending anything. A keep in flight SHALL
be shown on the node's panel like any action — its verb and how long it has
been running — and SHALL NOT be abortable: it is one fast call with no
open-ended wait, and abandoning the wait on it would leave the operator unsure
whether the deadline was set.

A node that already has an action in flight SHALL take no keep; the dashboard
SHALL say it is still working and drive nothing. The key help SHALL name the
keep key only for the node it describes when that node supports retention and
has no action in flight, so the operator is never invited to press a key that
would do nothing there.

The outcome of a keep SHALL be shown on the status line: on success the
deadline the control plane set, on failure the control plane's own reason — a
deployment that predates keep support, an environment with no instance to
retain — and a failed keep SHALL NOT close the dashboard. What the keep
changes — the deadline the node now carries — SHALL appear on the node's panel
through the normal refresh, without the operator asking for it.

The node's detail view SHALL offer keep on the node it shows, through the same
prompt and the same rules the grid applies to the selected node.

#### Scenario: Keep opens a prompt, it does not send

- **WHEN** the operator selects a cloud environment with no action in flight
  and issues keep
- **THEN** a duration prompt opens naming the node, pre-filled with a default
  duration, and nothing has been sent

#### Scenario: A confirmed keep sets the deadline

- **WHEN** the operator changes the prompt's duration and confirms it
- **THEN** the node is kept until now plus the confirmed duration and the
  status line shows the deadline the control plane set

#### Scenario: A pre-filled keep is one key and a confirm

- **WHEN** the operator issues keep on a cloud environment and confirms the
  prompt without changing its duration
- **THEN** the node is kept until now plus the default duration

#### Scenario: A bad duration sends nothing

- **WHEN** the operator confirms an entry that is not a positive duration
- **THEN** nothing is sent, the prompt stays open, and it shows why the entry
  was rejected

#### Scenario: Cancelling sends nothing

- **WHEN** the operator opens the keep prompt and cancels it
- **THEN** the prompt closes and the node's deadline is unchanged

#### Scenario: The prompt takes the keyboard

- **WHEN** the keep prompt is open and the operator presses a navigation key
- **THEN** the selection does not move and the prompt stays open

#### Scenario: A local node takes no keep

- **WHEN** the operator issues keep on a local daemon node
- **THEN** the dashboard drives nothing: a local daemon has no idle sweep to
  hold off, so there is no deadline to set

#### Scenario: The key help hides keep where it would do nothing

- **WHEN** the node under the cursor is a local daemon node, or has an action
  in flight
- **THEN** the key help does not name the keep key
- **WHEN** that node is a cloud environment with no action in flight
- **THEN** the key help names the keep key

#### Scenario: A busy node is not kept again

- **WHEN** the operator issues keep on a node whose start, stop, or keep is
  still in flight
- **THEN** the dashboard drives nothing and says the node is still working

#### Scenario: A keep in flight cannot be aborted

- **WHEN** the operator issues the abort on a node whose keep is in flight
- **THEN** the abort drives nothing: the keep keeps running, the panel keeps
  showing it, and its outcome lands on the status line when the call returns

#### Scenario: A keep that fails keeps the dashboard open

- **WHEN** a keep is sent to an environment whose deployment predates keep
  support, or to one with no instance to retain
- **THEN** the failure is shown on the status line with the control plane's
  own reason and the dashboard keeps running with its refreshes

#### Scenario: Keep from the detail view

- **WHEN** the operator issues keep from the detail view of a cloud
  environment
- **THEN** the same prompt opens with the same rules, and its outcome is
  shown as the grid would show it

### Requirement: The dashboard panels show the retention deadline

A node's panel SHALL show the node's retention deadline — the time until which
the idle sweep leaves the node alone — whenever the node's last answered
refresh carries one, worded the same way the one-shot surfaces report it, and
SHALL omit the line when the answer carries none: a node with no active
retention, a node without retention at all, or a read from a control plane
that predates the field. The deadline SHALL appear on the tile and on the
node's detail screen, which draw the same lines, so the two cannot show the
same read differently, and it SHALL sit among the panel's other time facts
rather than displacing the node's state or what it serves.

A deadline that has passed carries no active retention: a refresh taken after
the deadline SHALL not show the line, and a panel SHALL NOT draw a deadline
the sweep has already moved past as though the node were still held.

#### Scenario: A retained node shows its deadline

- **WHEN** a cloud environment's refresh answer carries a retention deadline
  in the future
- **THEN** its tile shows the deadline, and its detail screen shows the same
  line

#### Scenario: A node with no retention shows no line

- **WHEN** a node's refresh answer carries no retention deadline
- **THEN** its panel shows no deadline line and renders the rest exactly as
  before

#### Scenario: An older control plane degrades to no line

- **WHEN** a cloud environment's control plane predates the deadline in its
  stats reply
- **THEN** its panel shows no deadline line and no error, and the rest of the
  panel is unaffected

#### Scenario: A passed deadline disappears

- **WHEN** a node's retention deadline has passed and a later refresh answers
- **THEN** the panel no longer shows the deadline

#### Scenario: Tile and detail agree

- **WHEN** the operator opens the detail screen of a retained node
- **THEN** the detail screen's deadline line is the same line, worded the
   same, the tile draws for the same read

### Requirement: A node answers only what its kind can

A node kind SHALL answer the operations its kind supports and no more. Where a
read verb offers a flag whose answer only some node kinds carry — an instance's
price, a log store's source, time window or instance id — the flag SHALL apply
to the nodes that can answer it and SHALL leave the rest as they read without
it.

A target holding no node that can answer SHALL NOT be an error. Rendering a
column blank for a node that has no such fact is what these views already do
for every fact only one kind reports, and a flag is not a different case: a
fleet of daemons asked for a cost is a fleet with no cost to show, not a
malformed command.

The capability SHALL be a property of the node kind, asserted by the caller,
not a field on the shared reply. A kind that cannot answer SHALL simply not
implement it, so adding a kind that can requires no change to the callers and
adding one that cannot requires no exception.

Which flags apply to which kinds SHALL be documented on each verb's page, since
a blank column is not self-explaining.

#### Scenario: A priced node and an unpriced one in one table

- **WHEN** `spinloop metrics --cost` runs against a target holding both a cloud
  environment and a daemon node
- **THEN** the environment's row carries its cost and the daemon's does not,
  and the command succeeds

#### Scenario: No node can answer, and that is not an error

- **WHEN** `spinloop metrics --cost` runs against a fleet of daemon nodes only
- **THEN** no cost is shown for any node and the command succeeds

#### Scenario: A log query narrows the nodes that support it

- **WHEN** `spinloop logs --source boot` runs against a target holding both a
  cloud environment and a daemon node
- **THEN** the environment's boot log is read, and the daemon's output is read
  as it would be without the flag

#### Scenario: A kind that cannot answer implements nothing

- **WHEN** a node kind has no answer for a capability
- **THEN** it does not implement that capability, and no caller carries a
  branch naming that kind
