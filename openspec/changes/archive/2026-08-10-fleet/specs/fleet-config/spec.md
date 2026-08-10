## Purpose

Define `fleet.yaml`: the file that names the machines an `outfit fleet` client
observes and how to reach each one's daemon control API — the fleet's single
source of what nodes exist, kept separate from the per-node secrets.

## ADDED Requirements

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
