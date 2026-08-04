## MODIFIED Requirements

### Requirement: Remote command group

The system SHALL provide a `remote` command group with the subcommands
`bootstrap`, `start`, `stop`, `status`, `deploy`, `ls`, and `stats`. `start`,
`stop`, `status`, `stats` and `deploy` each take an optional Outfit path:
`start` SHALL boot the endpoint and block until it is serving, then perform a
quick TCP probe of the inference endpoint — if the probe fails, a warning is
printed to stderr explaining the network mismatch (see the Remote Start Probe
specification) — and finally print the base URL and API key as shell exports;
`stop` SHALL stop it immediately rather than waiting for its idle timer;
`status` SHALL report instance state and endpoint health without side effects
and SHALL NOT perform any TCP probe; `stats` SHALL report instance state,
token usage, resource consumption, and GPU information for a running instance;
`deploy` SHALL set what the endpoint serves. `ls` SHALL list the registered
remote environments (see the Remote Environments specification). `bootstrap`
SHALL stand up the shared, account-level AWS infrastructure (once per account)
by obtaining and driving the CDK project, and takes its own flags rather than
an Outfit path (see the Endpoint Provisioning specification). An unrecognised
subcommand SHALL fail naming the accepted ones.

#### Scenario: Starting the endpoint

- **WHEN** the user runs `outfit remote start` and the endpoint reports ready
- **THEN** the base URL and API key are printed as `export` lines

#### Scenario: Starting warns when the network is not admitted

- **WHEN** the user runs `outfit remote start` and the endpoint reports ready
  but the TCP probe to the inference port fails
- **THEN** a warning is printed to stderr with a remediation command, and the
  command still exits 0

#### Scenario: Waiting through a cold start

- **WHEN** the endpoint reports that it is still starting
- **THEN** the command waits and retries until it is ready or the timeout
  passes, rather than failing on the first attempt

#### Scenario: Listing environments

- **WHEN** the user runs `outfit remote ls`
- **THEN** the registered environments are listed rather than any endpoint being
  contacted

#### Scenario: Stats reports instance metrics

- **WHEN** the user runs `outfit remote stats` with a running instance
- **THEN** token counts, resource usage, and GPU information are displayed

#### Scenario: Bootstrap is a recognised subcommand

- **WHEN** the user runs `outfit remote bootstrap`
- **THEN** the command is dispatched to the provisioning flow rather than
  reported as unknown

#### Scenario: Unknown subcommand

- **WHEN** the user runs `outfit remote frobnicate`
- **THEN** the command fails listing the accepted subcommands, which now
  include `bootstrap` and `stats`
