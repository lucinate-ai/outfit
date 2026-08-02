## MODIFIED Requirements

### Requirement: Remote command group

The system SHALL provide a `remote` command group with the subcommands
`bootstrap`, `start`, `stop`, `status`, `deploy` and `ls`. `start`, `stop`,
`status` and `deploy` each take an optional Outfit path: `start` SHALL boot the
endpoint and block until it is serving, printing the base URL and API key as
shell exports; `stop` SHALL stop it immediately rather than waiting for its idle
timer; `status` SHALL report instance state and endpoint health without side
effects; `deploy` SHALL set what the endpoint serves. `ls` SHALL list the
registered remote environments (see the Remote Environments specification).
`bootstrap` SHALL stand up the shared, account-level AWS infrastructure (once
per account) by obtaining and driving the CDK project, and takes its own flags
rather than an Outfit path (see the Endpoint Provisioning specification). An unrecognised subcommand SHALL fail
naming the accepted ones.

#### Scenario: Starting the endpoint

- **WHEN** the user runs `outfit remote start` and the endpoint reports ready
- **THEN** the base URL and API key are printed as `export` lines

#### Scenario: Waiting through a cold start

- **WHEN** the endpoint reports that it is still starting
- **THEN** the command waits and retries until it is ready or the timeout
  passes, rather than failing on the first attempt

#### Scenario: Listing environments

- **WHEN** the user runs `outfit remote ls`
- **THEN** the registered environments are listed rather than any endpoint being
  contacted

#### Scenario: Bootstrap is a recognised subcommand

- **WHEN** the user runs `outfit remote bootstrap`
- **THEN** the command is dispatched to the provisioning flow rather than
  reported as unknown

#### Scenario: Unknown subcommand

- **WHEN** the user runs `outfit remote frobnicate`
- **THEN** the command fails listing the accepted subcommands, which now
  include `bootstrap`
