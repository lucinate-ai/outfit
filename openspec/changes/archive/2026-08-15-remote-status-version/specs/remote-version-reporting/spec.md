## Purpose

Reports the outfit version running on a remote instance or fleet node so the operator can answer "is this node on the release I expect?" without SSH access.

## ADDED Requirements

### Requirement: Remote status shows version

`outfit remote status` SHALL display the outfit version running on the remote instance alongside its existing state, health, and base URL fields.

#### Scenario: Version is shown when the instance is running

- **WHEN** the user runs `outfit remote status` against a running instance
- **THEN** the output includes a `version` line with the outfit version string (e.g. `version: 1.16.0`)

#### Scenario: Version is unavailable when the instance is stopped

- **WHEN** the user runs `outfit remote status` against a stopped instance
- **THEN** the output omits the version line, since the daemon is not reachable

### Requirement: Remote metrics shows version

`outfit remote metrics` SHALL display the outfit version in its output, as the stats Lambda already reads the daemon and can carry the version alongside its existing fields.

#### Scenario: Version is shown in table format

- **WHEN** the user runs `outfit remote metrics --format=table` against a running instance
- **THEN** the table output includes a `version` line

#### Scenario: Version is shown in JSON format

- **WHEN** the user runs `outfit remote metrics --format=json` against a running instance
- **THEN** the JSON output includes a `version` field

#### Scenario: Version is omitted from bar header when unavailable

- **WHEN** the user runs `outfit remote metrics --format=bar` and the version is not available
- **THEN** the bar header omits the version without error

### Requirement: Daemon status endpoint reports version

The daemon's `GET /v1/status` response SHALL include a `version` field containing the outfit binary's build-time version string.

#### Scenario: Version is the build-time string

- **WHEN** the daemon responds to a status request
- **THEN** the JSON response includes `"version": "<string>"` set from the build-time `main.version` variable

#### Scenario: Version defaults to dev

- **WHEN** the daemon binary was built without `-ldflags` version override
- **THEN** the version field is `"dev"`

### Requirement: Fleet status shows version

`outfit fleet status` SHALL display the outfit version for each node alongside its existing state and serving columns, read from the daemon's `/v1/status` response.

#### Scenario: Version is shown per node

- **WHEN** `outfit fleet status` runs against a fleet of running nodes
- **THEN** each node's row includes the outfit version string

#### Scenario: Version is omitted for unreachable nodes

- **WHEN** a node's daemon is unreachable
- **THEN** that node's row shows its failure outcome without a version

#### Scenario: Versions differ across nodes

- **WHEN** nodes in the fleet run different outfit versions
- **THEN** each node's row shows its own version, making the difference visible
