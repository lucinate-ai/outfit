# remote-version-reporting Specification

## Purpose
Reports the spinloop version running on a remote instance or fleet node so the operator can answer "is this node on the release I expect?" without SSH access.

## Requirements

### Requirement: An environment's status shows version

`spinloop status --env <name>` SHALL display the spinloop version running on the remote instance alongside its existing state, health, and base URL fields.

#### Scenario: Version is shown when the instance is running

- **WHEN** the user runs `spinloop status --env <name>` against a running instance
- **THEN** the output includes a `version` line with the spinloop version string (e.g. `version: 1.16.0`)

#### Scenario: Version is unavailable when the instance is stopped

- **WHEN** the user runs `spinloop status --env <name>` against a stopped instance
- **THEN** the output omits the version line, since the daemon is not reachable

### Requirement: An environment's metrics show version

`spinloop metrics --env <name>` SHALL display the spinloop version in its output, as the stats Lambda already reads the daemon and can carry the version alongside its existing fields.

#### Scenario: Version is shown in table format

- **WHEN** the user runs `spinloop metrics --env <name> --format=table` against a running instance
- **THEN** the table output includes a `version` line

#### Scenario: Version is shown in JSON format

- **WHEN** the user runs `spinloop metrics --env <name> --format=json` against a running instance
- **THEN** the JSON output includes a `version` field

#### Scenario: Version is omitted from bar header when unavailable

- **WHEN** the user runs `spinloop metrics --env <name> --format=bar` and the version is not available
- **THEN** the bar header omits the version without error

### Requirement: Daemon status endpoint reports version

The daemon's `GET /v1/status` response SHALL include a `version` field containing the spinloop binary's build-time version string.

#### Scenario: Version is the build-time string

- **WHEN** the daemon responds to a status request
- **THEN** the JSON response includes `"version": "<string>"` set from the build-time `main.version` variable

#### Scenario: Version defaults to dev

- **WHEN** the daemon binary was built without `-ldflags` version override
- **THEN** the version field is `"dev"`

### Requirement: Fleet status shows version

`spinloop fleet status` SHALL display the spinloop version for each node alongside its existing state and serving columns, read from the daemon's `/v1/status` response.

#### Scenario: Version is shown per node

- **WHEN** `spinloop fleet status` runs against a fleet of running nodes
- **THEN** each node's row includes the spinloop version string

#### Scenario: Version is omitted for unreachable nodes

- **WHEN** a node's daemon is unreachable
- **THEN** that node's row shows its failure outcome without a version

#### Scenario: Versions differ across nodes

- **WHEN** nodes in the fleet run different spinloop versions
- **THEN** each node's row shows its own version, making the difference visible

### Requirement: Control plane responses carry the control plane version

Every response from a control plane Lambda SHALL include the header `x-spinloop-control-plane-version`, whose value is the spinloop version the control plane was deployed with. The header SHALL be present on error responses as well as successful ones. When the control plane was deployed without a version, the value SHALL be `dev`.

#### Scenario: A successful response carries the header

- **WHEN** the start, stop, deploy, env, stats, seed or update Lambda returns a 200 response
- **THEN** the response includes `x-spinloop-control-plane-version` set to the deployed version

#### Scenario: An error response carries the header

- **WHEN** a control plane Lambda returns a 4xx or 5xx response
- **THEN** the response still includes `x-spinloop-control-plane-version`

### Requirement: The CLI warns when the control plane version differs

When a `spinloop remote` command receives a control plane response whose `x-spinloop-control-plane-version` differs from the CLI's own version, the CLI SHALL print a single warning to stderr naming both versions and suggesting `spinloop remote bootstrap`. The version SHALL be read from the response to the command's own request, with no extra call. The warning SHALL be printed at most once per process and SHALL NOT change the command's exit status or stdout.

#### Scenario: Versions differ

- **WHEN** the CLI is `1.30.0` and a control plane call returns the header `1.28.0`
- **THEN** stderr gets one warning naming `1.30.0` and `1.28.0` and suggesting `spinloop remote bootstrap`
- **AND** the command carries on and exits as it would have without the warning

#### Scenario: Several calls in one command

- **WHEN** one command makes several control plane calls that all return a differing version
- **THEN** the warning is printed once

#### Scenario: Versions match

- **WHEN** the header equals the CLI's version, ignoring a leading `v`
- **THEN** no warning is printed

#### Scenario: The control plane predates the header

- **WHEN** a control plane response has no `x-spinloop-control-plane-version` header
- **THEN** no warning is printed

#### Scenario: A development build is on either side

- **WHEN** the CLI version or the header value is `dev` or empty
- **THEN** no warning is printed

#### Scenario: Machine-readable output is untouched

- **WHEN** a command run with JSON output prints a version warning
- **THEN** the warning goes to stderr and stdout holds only the JSON
