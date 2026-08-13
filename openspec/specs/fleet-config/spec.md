# fleet-config Specification

## Purpose
Define `fleet.yaml`: the file that names the machines an `outfit fleet` client
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
precedence outfit uses elsewhere (environment over `.env`). A node with no
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

The `outfit fleet` commands SHALL resolve the fleet file from an explicit
`--fleet <path>` when given, otherwise `./fleet.yaml` in the working
directory. A missing file when one is required SHALL fail with a message
naming the expected path and how to create one.

#### Scenario: Default resolution

- **WHEN** an `outfit fleet` command runs in a directory containing
  `fleet.yaml` with no `--fleet` flag
- **THEN** that file is used

#### Scenario: Explicit path

- **WHEN** `outfit fleet status --fleet ./cluster.yaml` runs
- **THEN** that file is used

#### Scenario: Missing file

- **WHEN** an `outfit fleet` command runs with no fleet file at the resolved
  path
- **THEN** it fails, naming the expected path

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

