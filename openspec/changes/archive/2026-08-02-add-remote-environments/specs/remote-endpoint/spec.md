## MODIFIED Requirements

### Requirement: Remote command group

The system SHALL provide a `remote` command group with the subcommands `start`,
`stop`, `status`, `deploy` and `ls`. `start`, `stop`, `status` and `deploy` each
take an optional Outfit path: `start` SHALL boot the endpoint and block until it
is serving, printing the base URL and API key as shell exports; `stop` SHALL stop
it immediately rather than waiting for its idle timer; `status` SHALL report
instance state and endpoint health without side effects; `deploy` SHALL set what
the endpoint serves. `ls` SHALL list the registered remote environments rather
than drive any single endpoint (see the Remote Environments specification). An
unrecognised subcommand SHALL fail naming the accepted ones.

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

#### Scenario: Unknown subcommand

- **WHEN** the user runs `outfit remote frobnicate`
- **THEN** the command fails listing the accepted subcommands

### Requirement: Remote configuration discovery

The endpoint's control URLs SHALL come from a JSON configuration naming a start
URL, a stop URL, an optional deploy URL, and a region. That configuration MAY
also name the endpoint's own base URL; it SHALL be optional, since no control
call needs it, and a configuration without it SHALL remain valid. An Outfit's
`REMOTE` instruction SHALL select that configuration: a bare name selects the
named environment from the per-user registry, and a path selects a file resolved
relative to the Outfit when not absolute (see the Remote Environments
specification). When no Outfit names one, the `default` environment SHALL be
used, so the command works outside any project. Environment variables SHALL
override individual values, and the region SHALL fall back to the standard AWS
region variable and then to the region named in the URL. A missing or incomplete
configuration SHALL fail saying where to put it.

#### Scenario: Outfit names the configuration

- **WHEN** an Outfit sets `REMOTE ./remote.json` and a `remote` subcommand runs
  with that Outfit
- **THEN** the URLs come from that file, resolved beside the Outfit

#### Scenario: Outfit names an environment

- **WHEN** an Outfit sets `REMOTE qwen3.6-27b-prod` and a `remote` subcommand
  runs with that Outfit
- **THEN** the URLs come from that environment's `remote.json` in the registry

#### Scenario: Explicit Outfit without a REMOTE instruction

- **WHEN** a `remote` subcommand is given an Outfit that has no `REMOTE`
- **THEN** it fails saying that Outfit has no `REMOTE` instruction, rather than
  silently using the default environment

#### Scenario: No Outfit in play

- **WHEN** a `remote` subcommand runs outside a project
- **THEN** the `default` environment is used

#### Scenario: Configuration without a base URL

- **WHEN** a remote configuration names the control URLs and region but no base
  URL, and a `remote` subcommand runs
- **THEN** the subcommand works as it always has, since the endpoint reports its
  own address in the replies to `start` and `status`
