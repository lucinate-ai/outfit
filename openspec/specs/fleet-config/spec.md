# fleet-config Specification

## Purpose
Define `fleet.yaml`: the file that names the machines a `spinloop fleet` client
observes and how to reach each one's daemon control API — the fleet's single
source of what nodes exist, kept separate from the per-node secrets.

## Requirements

### Requirement: Fleet file format

A `fleet.yaml` SHALL declare a list of nodes, each with a unique `name`, a
`host` (a hostname or address reachable from the client — a LAN name, a
tailscale name, or an IP), and the daemon control API's port (defaulting to
the daemon's default port when omitted). A node MAY carry an explicit API
scheme/address override for non-default setups. Node names SHALL be unique
within a file; a duplicate name SHALL be an error naming the collision.

#### Scenario: A minimal node

- **WHEN** a `fleet.yaml` lists a node with a name and a host and no port
- **THEN** the client targets that host on the daemon's default API port

#### Scenario: Duplicate node names are rejected

- **WHEN** a `fleet.yaml` lists two nodes with the same name
- **THEN** parsing fails with an error naming the duplicated node

### Requirement: Token references, not secrets

A node MAY reference the bearer token its daemon requires by naming an
environment variable that holds it; the token value SHALL NOT be written in
`fleet.yaml`. The client SHALL resolve the reference from the process
environment, including a `.env` beside the `fleet.yaml`, following the same
precedence spinloop uses elsewhere (environment over `.env`). A node with no
token reference SHALL be contacted without authentication (valid for a
loopback-only daemon).

#### Scenario: Token resolved from the environment

- **WHEN** a node names a token env var that is set (in the environment or the
  adjacent `.env`)
- **THEN** the client sends that value as the node's bearer token

#### Scenario: No secret in the file

- **WHEN** a `fleet.yaml` is parsed
- **THEN** it carries only a token reference, never a literal token value

### Requirement: Fleet file resolution

Every command that acts on a fleet — the `spinloop fleet` group, and the
top-level verbs that read one — SHALL resolve the fleet the same way, from the
flags given: `--env <name>` names a single registered cloud environment,
`--fleet <path>` names a fleet file, and with neither the fleet file is
`./fleet.yaml` in the working directory. One rule serves every such command, so
a target means the same thing wherever it is given.

Where no target resolves — no `--env`, no `--fleet`, and no fleet file at the
expected path — the command SHALL fail naming every way to give one: the
expected path, `--fleet <path>`, and `--env <name>`. It SHALL NOT look for an
engine on the machine it is running on. A local engine is already shown by the
command that runs it, and a machine whose engines are to be read this way names
them in a fleet file like any other.

`--env` and `--fleet` SHALL NOT both be given: each names where the model is
served from, so a command stating both SHALL fail naming both rather than
resolving one by precedence. The same rule governs a launch, so an operator
meets it once.

`--env` SHALL name a registered environment, which is a plain identifier and
never a path. A value that is not a plain identifier, and a name with no
registered configuration, SHALL each fail saying so and how to create the
environment, before any node is contacted.

`--fleet` SHALL carry a `-f` short form on every `spinloop fleet` subcommand
except `fleet logs`, where `-f` is the short form of `--follow`; on `fleet
logs` the fleet file SHALL be named by the long form `--fleet` only. `--env`
SHALL carry no short form on the fleet commands, so that one spelling means
one thing across the group.

#### Scenario: Default resolution

- **WHEN** a `spinloop fleet` command runs in a directory containing
  `fleet.yaml` with no `--fleet` or `--env` flag
- **THEN** that file is used

#### Scenario: Explicit path

- **WHEN** `spinloop fleet status --fleet ./cluster.yaml` runs
- **THEN** that file is used

#### Scenario: Short form

- **WHEN** `spinloop fleet status -f ./cluster.yaml` runs
- **THEN** `./cluster.yaml` is used, exactly as with `--fleet`

#### Scenario: logs keeps -f for follow

- **WHEN** the operator runs `spinloop fleet logs -f`
- **THEN** that is the command's follow flag, not a fleet-file flag, and
  `logs` takes its fleet file only as `--fleet`

#### Scenario: Missing file

- **WHEN** a `spinloop fleet` command runs with no fleet file at the resolved
  path
- **THEN** it fails, naming the expected path

#### Scenario: A named environment is the target

- **WHEN** `spinloop fleet status --env prod` runs
- **THEN** the command acts on the registered environment `prod` alone,
  whether or not a `fleet.yaml` is present

#### Scenario: A named environment needs no fleet file

- **WHEN** `spinloop fleet dashboard --env prod` runs in a directory holding
  no `fleet.yaml`
- **THEN** the view opens on that one environment, and the absent file is not
  an error

#### Scenario: Naming both a fleet and an environment fails

- **WHEN** `spinloop fleet status --env prod --fleet ./cluster.yaml` runs
- **THEN** it fails naming both the environment and the fleet file, and
  contacts nothing

#### Scenario: The directory's fleet file does not conflict with --env

- **WHEN** `spinloop fleet status --env prod` runs in a directory containing
  `fleet.yaml`
- **THEN** the environment is the target and the directory's file is ignored,
  because only a flag states a conflict

#### Scenario: An unregistered environment is reported

- **WHEN** `spinloop fleet status --env nope` runs and no configuration is
  registered under that name
- **THEN** it fails saying the environment is not registered and how to
  create it, and contacts nothing

#### Scenario: A path is not an environment name

- **WHEN** `spinloop fleet status --env ./cloud.json` runs
- **THEN** it fails saying an environment name is a plain identifier with no
  path

#### Scenario: No target at all names every way to give one

- **WHEN** a command that acts on a fleet runs with no `--env`, no `--fleet`,
  and no fleet file at the expected path
- **THEN** it fails naming the expected path, `--fleet <path>` and
  `--env <name>`, and contacts nothing

#### Scenario: No engine on this machine is looked for

- **WHEN** a read verb runs with no target resolvable on a machine that is
  running an engine with its control API up
- **THEN** it fails as above rather than reporting that engine: a local engine
  is read through the command that runs it, or by naming the machine in a fleet
  file

### Requirement: Per-node engine endpoint

A node MAY declare where its engine serves, overriding what its daemon reports:
a host, a port, and a path prefix, each optional and each falling back to what
routing would otherwise derive (the node's own `host`, and the port and path the
daemon reports). This is what covers the setups a daemon cannot describe — an
engine behind a reverse proxy, a container publishing the engine on a different
port than it binds inside, a node reached through a tunnel.

A node that declares no engine block SHALL behave exactly as it does now.

#### Scenario: An override replaces what the daemon reports

- **WHEN** a node's entry declares an engine host and port and routing chooses
  that node
- **THEN** the declared host and port are used, whatever the daemon reports

#### Scenario: A partial override falls back per field

- **WHEN** a node's entry declares only an engine port
- **THEN** that port is used with the node's own host

#### Scenario: No engine block changes nothing

- **WHEN** a fleet file's nodes declare no engine block
- **THEN** they parse and behave as before

### Requirement: Fleet-wide activity preference

A fleet file MAY declare a `prefer` value of `idle` or `active`, setting how
routing ranks several nodes that could all serve a request. It belongs to the
file rather than to each node: it describes how this cluster should be used —
spread the work, or consolidate it — which is a property of the fleet, not of
any one machine in it.

A file declaring nothing SHALL rank as `idle`. A file declaring anything other
than `idle` or `active` SHALL fail to parse, naming both accepted values, in
keeping with the file's other validation.

#### Scenario: A fleet that consolidates

- **WHEN** a fleet file declares `prefer: active`
- **THEN** routing against that fleet ranks the most recently active node first,
  unless a flag overrides it

#### Scenario: A fleet that declares nothing

- **WHEN** a fleet file declares no preference
- **THEN** routing ranks the longest-idle node first

#### Scenario: An unknown value is rejected at parse time

- **WHEN** a fleet file declares `prefer: whatever`
- **THEN** parsing fails naming `idle` and `active`

### Requirement: Engine token references

A node MAY name the environment variable holding the key its engine is to be
gated with, separately from the daemon's own bearer token — the two are
different credentials and a node may need either, both, or neither. The value
SHALL NOT be written in `fleet.yaml`, and SHALL be resolved exactly as the
daemon token reference is: the process environment first, then a `.env` beside
the fleet file.

The reference is the *client's* lookup, not a description of the node. The
client resolves it and supplies the key when it starts an engine there, so the
node holds no key of its own and the two ends cannot disagree about what it is.
It is also the seam for resolving keys from somewhere better than an environment
variable — a keychain, a secret manager — without anything else changing.

A reference that resolves to nothing SHALL be reported against that node, naming
the variable, in the same way a missing daemon token is.

#### Scenario: The engine key is a reference

- **WHEN** a node names an engine token variable that is set
- **THEN** the client resolves that value and gates that node's engine with it

#### Scenario: The fleet file holds no engine key

- **WHEN** a `fleet.yaml` is parsed
- **THEN** it carries only a reference to the engine key, never a literal value

#### Scenario: An unset engine token variable names itself

- **WHEN** a node names an engine token variable that is set nowhere
- **THEN** the failure names that variable and that node

#### Scenario: A node naming no key is not gated

- **WHEN** a node's entry names no engine token variable and the client starts
  an engine there
- **THEN** the engine is started ungated

### Requirement: Fleet-wide API key reference

A `fleet.yaml` MAY declare a top-level `apiKeyEnv` naming the environment
variable that holds the API key shared by the fleet's cloud nodes. The file
SHALL hold the variable's *name*, never the value — the same discipline as the
daemon and engine-token references — and the reference SHALL be resolved exactly
the way those are: the process environment first, then the `.env` beside the
fleet file.

The reference is the default key for a cloud node. A cloud node whose own
entry names no `engineTokenEnv` takes the fleet-wide key. A node's own
`engineTokenEnv` SHALL override the fleet-wide reference, so one cloud node may
carry a distinct key while the rest of the fleet shares one. A daemon node SHALL
NOT take the fleet-wide reference: it is gated only by its own `engineTokenEnv`,
exactly as it is today.

A reference that is named but resolves to nothing SHALL be a configuration error
naming the variable, in the same way a missing engine-token variable is.

#### Scenario: A fleet shares one key across its remotes

- **WHEN** a `fleet.yaml` declares `apiKeyEnv: SHARED_KEY`, that variable is
  set, and it lists two `kind: cloud` nodes that name no `engineTokenEnv`
- **THEN** the value of `SHARED_KEY` is the key both remotes are reached with

#### Scenario: A per-node reference overrides the fleet-wide one

- **WHEN** a `fleet.yaml` declares `apiKeyEnv: SHARED_KEY` and one cloud node
  names `engineTokenEnv: SPECIAL_KEY`
- **THEN** that node is reached with the value of `SPECIAL_KEY` and the other
  remotes with the value of `SHARED_KEY`

#### Scenario: The fleet file holds no key value

- **WHEN** a `fleet.yaml` declaring `apiKeyEnv` is parsed
- **THEN** it carries only the reference, never a literal key value

#### Scenario: An unset fleet-wide variable names itself

- **WHEN** a `fleet.yaml` declares an `apiKeyEnv` that is set nowhere, and a
  cloud node naming no key of its own is reached for its key
- **THEN** the failure names that variable, and no agent is launched without a
  key

#### Scenario: A daemon node is not gated by the fleet-wide key

- **WHEN** a `fleet.yaml` declares `apiKeyEnv` and a daemon node names no
  `engineTokenEnv`
- **THEN** the daemon node is started ungated, as it is today

### Requirement: Node Spinloop source

A fleet-file node, of either kind, MAY declare a `file` field naming the
Spinloop file that describes what it runs — the same file `spinloop fleet
deploy` reads to create a `kind: cloud` node's environment, and the same
file `spinloop fleet start` reads to tell a `kind: daemon` node's engine what
to run. The path SHALL resolve relative to the fleet file's directory, the
same way other Spinloop-relative paths in the project resolve. The field
SHALL NOT be required to parse a fleet file — every fleet command other than
`deploy` and `start` is unaffected by it — but `deploy` and `start` each
SHALL require it (directly or via the fallbacks below) for the nodes they
act on; see fleet-client's "Driving one node" requirement.

#### Scenario: A cloud node names its Spinloop file

- **WHEN** a `kind: cloud` node declares `file: ./envs/gpu.Spinloop`
- **THEN** `spinloop fleet deploy` for that node reads the Spinloop at that
  path, resolved relative to the fleet file's directory, to derive what to
  deploy

#### Scenario: A daemon node names its Spinloop file

- **WHEN** a `kind: daemon` node declares `file: ./envs/gpu.Spinloop`
- **THEN** `spinloop fleet start` for that node reads the Spinloop at that
  path, resolved relative to the fleet file's directory, to derive what to
  start it with

#### Scenario: The field is inert outside deploy and start

- **WHEN** a node declares a `file` field
- **THEN** `fleet status`, `metrics`, `stop`, `route`, and `dashboard` behave
  exactly as they do without it

### Requirement: Node Spinloop source falls back to name-based lookup

A node declaring no `file` field SHALL have its Spinloop source resolved
from its own `name`, tried in order:

1. `name` resolved as a registered `spinloop alias` — the same lookup a bare
   argument to `spinloop cloud deploy <name>` already performs.
2. Failing that, a subdirectory named `<name>` beside the fleet file,
   containing a Spinloop file — the same directory-to-default-file
   resolution an ordinary Spinloop path argument already gets when it names
   a directory.

A node for which neither resolves SHALL fail the command acting on it —
`fleet deploy` for a `kind: cloud` node, `fleet start` for a `kind: daemon`
node — for that node alone, naming all three ways a source could have been
given: the `file` field, a `spinloop alias` named after the node, or a
`<name>/` subdirectory beside the fleet file.

#### Scenario: Resolved through a registered alias

- **WHEN** a node named `gpu-env` declares no `file` field, and `spinloop
  alias` has `gpu-env` registered to a Spinloop path
- **THEN** `fleet deploy` (if `gpu-env` is `kind: cloud`) or `fleet start`
  (if `kind: daemon`) reads the Spinloop the alias names

#### Scenario: Resolved through a named subdirectory

- **WHEN** a node named `dev-1` declares no `file` field, no alias named
  `dev-1` is registered, and a `dev-1/` directory containing a Spinloop file
  sits beside the fleet file
- **THEN** `fleet deploy` (if `dev-1` is `kind: cloud`) or `fleet start` (if
  `kind: daemon`) reads the Spinloop from that subdirectory

#### Scenario: An alias wins over a same-named subdirectory

- **WHEN** a node named `dev-1` declares no `file` field, an alias named
  `dev-1` is registered, and a `dev-1/` subdirectory containing a Spinloop
  file also sits beside the fleet file
- **THEN** the alias is used, not the subdirectory

#### Scenario: None of the three resolve for a cloud node

- **WHEN** a `kind: cloud` node declares no `file` field, no alias is
  registered under its name, and no same-named subdirectory sits beside the
  fleet file
- **THEN** `fleet deploy` fails for that node, naming the `file` field, the
  alias registry, and the subdirectory convention as the three ways a source
  could have been given

#### Scenario: None of the three resolve for a daemon node

- **WHEN** a `kind: daemon` node declares no `file` field, no alias is
  registered under its name, and no same-named subdirectory sits beside the
  fleet file
- **THEN** `fleet start` fails for that node, naming the `file` field, the
  alias registry, and the subdirectory convention as the three ways a source
  could have been given

### Requirement: A fleet file MAY name a gateway

A fleet file MAY declare a top-level `gateway` section, beside `wake:` and
`prefer:`, naming the address the fleet is served under by its gateway: a
`url`, optionally a `tokenEnv` naming the variable that holds the gateway's
token, and optionally a `name` labelling the gateway. The section is how a
machine that holds the fleet file learns how to point a harness at the fleet
without naming the address in a Spinloop.

The section's `url` SHALL carry a scheme, the way an endpoint value does, and
a `gateway` section without one SHALL be refused, naming the missing field. A
`tokenEnv` SHALL name a variable, and where the section names none, the token
SHALL be resolved under `OPENAI_API_KEY`. A `name`, when given, SHALL label
the gateway wherever a launch through it needs to name the gateway rather
than a model — see the fleet-client capability's harness-naming behavior;
where the section names none, the address's host SHALL stand in. A file
without a `gateway` section SHALL behave exactly as it does today.

#### Scenario: A file names its gateway

- **WHEN** a fleet file declares a `gateway` section naming a `url` and a
  `tokenEnv`, and the file is read
- **THEN** the section's address and token variable are available to the
  commands that route a launch at the fleet

#### Scenario: A section without an address is refused

- **WHEN** a fleet file declares a `gateway` section naming no `url`
- **THEN** the file is refused, naming the missing field

#### Scenario: No section, no change

- **WHEN** a fleet file declares no `gateway` section
- **THEN** nothing about the file's behaviour changes

#### Scenario: A section names its gateway

- **WHEN** a fleet file declares a `gateway` section naming a `name`, and the
  file is read
- **THEN** that name is available to the commands that label the gateway,
  ahead of any address-derived fallback

#### Scenario: A section with no name falls back to the address

- **WHEN** a fleet file declares a `gateway` section naming a `url` but no
  `name`, and the file is read
- **THEN** the address's host is available as the gateway's label

### Requirement: Fleet-wide wake policy

A fleet file MAY declare a top-level `wake` value of `on` or `off`, deciding
whether routing starts an engine on a node that is not running one when no
running node serves what is wanted. It applies to every node that declares
no `wake` setting of its own — the same reason `prefer` is fleet-wide: it
describes how this cluster is to be used — may work be started on its
machines on demand, or only used where it is already running — which is a
property of the fleet, not of any one machine in it, unless that machine's
entry says otherwise.

A node entry MAY declare its own `wake` value, `on` or `off`, in the same
shape as the fleet-wide setting. When a node names one, it decides whether
that node may be woken, taking precedence over the fleet-wide setting for
that node alone; a node naming none is governed by the fleet-wide setting as
before. This exists because waking is not free the same way on every node: a
cloud environment's wake boots and pays for a cloud instance, unlike a local
daemon's engine, so an operator may want the fleet's daemons to wake freely
while deciding a cloud node's waking on its own terms — opted in under a
fleet that otherwise does not wake, or opted out under one that does —
without a second fleet-wide flag governing every cloud node in the file
alike.

A file declaring nothing at either level SHALL wake, as routing does when no
setting decides otherwise: waking is the difference between a fleet that
answers a request and one that must be prepared by hand, and the file's
author is the one who owns the machines it names. A fleet-wide or per-node
`wake` declaring anything other than `on` or `off` SHALL fail to parse,
naming both accepted values, in keeping with the file's other validation.

The setting SHALL decide whether to wake only, at whichever level decides it
for a given node. It SHALL NOT change which node is chosen, how matching
nodes are ranked, or what a wake does: a node for which waking is not
allowed still reports, when nothing is running, the node whose source
describes the wanted model and the command that would start it.

#### Scenario: A fleet that declares nothing wakes

- **WHEN** a fleet file declares no `wake` setting at either level and
  routing finds no node serving what is wanted
- **THEN** routing starts an engine on a suitable node, as it does today

#### Scenario: A fleet that refuses to wake

- **WHEN** a fleet file declares `wake: off` and no node overrides it, and
  routing finds no node serving what is wanted
- **THEN** nothing is started, and the failure names the node that would be
  woken and the command that would start it

#### Scenario: A node opts out under a fleet that wakes

- **WHEN** a fleet file declares `wake: on`, one node declares its own
  `wake: off`, and that node is the only one serving what is wanted
- **THEN** that node is not woken, and the failure names it and says waking
  is disabled for it, even though the fleet otherwise wakes

#### Scenario: A node opts in under a fleet that does not wake

- **WHEN** a fleet file declares `wake: off`, one node declares its own
  `wake: on`, and that node is the only one serving what is wanted
- **THEN** that node is woken, though the rest of the fleet still does not
  wake

#### Scenario: An unknown value is rejected at parse time

- **WHEN** a fleet file declares `wake: sometimes`, at the fleet level or on
  a node
- **THEN** parsing fails naming `on` and `off`

### Requirement: Node tags

A node entry MAY declare `tags`: a mapping of key to value, both non-empty
strings. Tags are the operator's description of what work a node can take on —
capability (the model it serves, its context size), hardware, or anything else
the operator wants a dispatcher to match on. A tag is a claim the file makes
about the node: no node reads it, and it never changes what the node's engine
runs.

Tag keys SHALL be unique within a node's mapping; a duplicate key SHALL be
rejected at parse time. A node declaring no tags SHALL behave exactly as it
does today in every command, and SHALL match only work that names no tags.

#### Scenario: A node carries tags

- **WHEN** a fleet file's node entry declares `tags` with two key/value pairs
  and the file is read
- **THEN** both pairs are available, keyed, to the commands that report the
  fleet's topology

#### Scenario: A duplicate tag key is rejected

- **WHEN** a node's `tags` names the same key twice
- **THEN** parsing fails, naming the node and the duplicated key

#### Scenario: No tags, no change

- **WHEN** a fleet file's nodes declare no `tags`
- **THEN** nothing about the file's behaviour changes

### Requirement: Fleet-wide concurrency limits

A fleet file MAY declare a top-level `concurrency` section: how much work the
fleet may take at once. The section MAY set a `total`, the most work items the
fleet may have in flight at once, and a `tags` mapping from a tag (a
`key=value` pair, the way tags are named elsewhere) to the most items carrying
that tag that may be in flight at once, across the fleet.

It belongs to the file for the reason `wake` and `prefer` do: it describes how
this cluster is to be used — how much work its machines will take — which is a
property of the fleet, not of any one machine in it. The limits are the
fleet's declared capacity: a ceiling the operator sets, not a measurement of
the engines' load.

`total`, where declared, SHALL be a positive integer, as SHALL each per-tag
limit; a non-positive or non-integer value SHALL fail to parse, naming the
offending entry. A limit on a tag no node carries is accepted: it simply
bounds items that can match nothing. A file declaring no `concurrency`
section SHALL behave exactly as it does today.

#### Scenario: A fleet declares its limits

- **WHEN** a fleet file declares `concurrency` with a `total` and one per-tag
  limit, and the file is read
- **THEN** both limits are available, with the per-tag limit keyed by the tag
  it bounds

#### Scenario: A non-positive limit is rejected

- **WHEN** a fleet file declares a `concurrency` limit of zero or a negative
  number
- **THEN** parsing fails, naming the entry and what a limit must be

#### Scenario: No section, no change

- **WHEN** a fleet file declares no `concurrency` section
- **THEN** nothing about the file's behaviour changes

### Requirement: A registered environment is a fleet of one

A `--env <name>` target SHALL be a fleet holding exactly one node: a cloud
node named by the flag, whose registered configuration is the one
`clouds/<name>/cloud.json` holds. It SHALL be driven, observed and rendered
exactly as the same node listed in a fleet file is, so a command's output for
one environment does not depend on how that environment was named.

Such a fleet has no file on disk. It SHALL therefore carry none of the
settings a fleet file supplies — no activity preference, no wake policy, no
gateway, no concurrency limits, no fleet-wide key reference — and SHALL take
each of those as its default. Where a command reports which fleet it acted
on, it SHALL name the environment rather than a path.

A token reference SHALL NOT be resolved for such a fleet: a cloud node is
reached through its control plane with the operator's own credentials, and
there is no file for an adjacent `.env` to sit beside. An environment whose
credentials are missing or expired SHALL fail as it does on any other path.

#### Scenario: One environment renders as one node

- **WHEN** `spinloop fleet status --env prod` runs
- **THEN** the output is the one-row status table a single-node fleet file
  produces for the same environment

#### Scenario: An environment that cannot be reached is a row

- **WHEN** `spinloop fleet status --env prod` runs and the environment cannot
  be reached
- **THEN** the row reports why, and the command still succeeds, exactly as an
  unreachable node in a fleet file does

#### Scenario: Fleet-wide settings are absent, not inherited

- **WHEN** a command acts on a `--env` target
- **THEN** no activity preference, wake policy, gateway or concurrency limit
  is in force, and a `fleet.yaml` in the working directory supplies none of
  them

#### Scenario: No .env is consulted

- **WHEN** a command acts on a `--env` target
- **THEN** no adjacent `.env` is read, because the target names no file to sit
  beside
