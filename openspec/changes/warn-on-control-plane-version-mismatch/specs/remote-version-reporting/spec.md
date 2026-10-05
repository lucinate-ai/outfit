## ADDED Requirements

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
