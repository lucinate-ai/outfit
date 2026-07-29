# Remote Endpoint Specification

## Purpose

Define how a remote inference endpoint is discovered, controlled and told
what to serve from an Outfit: the `outfit remote` command group.

## Requirements

### Requirement: Remote command group

The system SHALL provide a `remote` command group with the subcommands `start`,
`stop`, `status` and `deploy`, each taking an optional Outfit path. `start`
SHALL boot the endpoint and block until it is serving, printing the base URL
and API key as shell exports; `stop` SHALL stop it immediately rather than
waiting for its idle timer; `status` SHALL report instance state and endpoint
health without side effects; `deploy` SHALL set what the endpoint serves. An
unrecognised subcommand SHALL fail naming the accepted ones.

#### Scenario: Starting the endpoint

- **WHEN** the user runs `outfit remote start` and the endpoint reports ready
- **THEN** the base URL and API key are printed as `export` lines

#### Scenario: Waiting through a cold start

- **WHEN** the endpoint reports that it is still starting
- **THEN** the command waits and retries until it is ready or the timeout
  passes, rather than failing on the first attempt

#### Scenario: Unknown subcommand

- **WHEN** the user runs `outfit remote frobnicate`
- **THEN** the command fails listing the accepted subcommands

### Requirement: Reporting a start in progress

Because the endpoint blocks until the model is serving, `start` SHALL report
that it is waiting rather than appear to hang: it SHALL say what is happening
before the first attempt and repeat at intervals with the elapsed time, and
SHALL report how long it took once ready. Progress SHALL be written to standard
error and the resulting exports to standard output, so the command's output can
be evaluated directly while a person watching still sees progress.

#### Scenario: A cold start is not silent

- **WHEN** the endpoint takes minutes to become ready
- **THEN** the command explains what it is waiting for and continues to report
  the elapsed time until it succeeds

#### Scenario: Only the result is on standard output

- **WHEN** a start succeeds and its output is captured
- **THEN** standard output holds exactly the environment exports, with every
  progress line on standard error

### Requirement: Remote configuration discovery

The endpoint's control URLs SHALL come from a JSON configuration naming a start
URL, a stop URL, an optional deploy URL, and a region. An Outfit's `REMOTE`
instruction SHALL select that file, resolved relative to the Outfit when the
value is not absolute. When no Outfit names one, the per-user configuration
SHALL be used, so the command works outside any project. Environment variables
SHALL override individual values, and the region SHALL fall back to the
standard AWS region variable and then to the region named in the URL. A missing
or incomplete configuration SHALL fail saying where to put it.

#### Scenario: Outfit names the configuration

- **WHEN** an Outfit sets `REMOTE ./remote.json` and a `remote` subcommand runs
  with that Outfit
- **THEN** the URLs come from that file, resolved beside the Outfit

#### Scenario: Explicit Outfit without a REMOTE instruction

- **WHEN** a `remote` subcommand is given an Outfit that has no `REMOTE`
- **THEN** it fails saying that Outfit has no `REMOTE` instruction, rather than
  silently using the per-user configuration

#### Scenario: No Outfit in play

- **WHEN** a `remote` subcommand runs outside a project
- **THEN** the per-user configuration is used

### Requirement: Authenticated control requests

Requests to the control URLs SHALL be signed with the caller's own AWS
credentials, resolved from the standard credential chain, and SHALL carry the
hash of the request body so that a request with a payload is signed over that
payload. Outfit SHALL NOT store AWS credentials of its own. A rejected request
SHALL report that the credentials may lack permission to invoke the endpoint.

#### Scenario: A request carrying a body is signed over it

- **WHEN** `outfit remote deploy` sends a configuration
- **THEN** the request is signed including the body's hash, not as an empty
  payload

#### Scenario: Credentials are missing

- **WHEN** no AWS credentials can be resolved
- **THEN** the command fails saying how to configure them

### Requirement: Deploying what the endpoint serves

`outfit remote deploy` SHALL derive the deployment from the Outfit and its
preset: `PROVIDER` SHALL select the inference engine, `MODEL` or the preset's
Hugging Face reference SHALL name the weights as a repository and optional
quantisation, `CONTEXT` or the preset's context size SHALL set the window,
`ALIAS` SHALL set the name the endpoint serves under (defaulting to the
repository), and the preset's remaining settings SHALL become the engine's
arguments. Settings the endpoint owns — host, port, model location, API key,
context size, alias, and metrics — SHALL be dropped, so one preset can both
serve locally and deploy unchanged. The request SHALL describe only what to
serve, never where the weights are stored. A `--dry-run` SHALL print the
derived deployment without sending it.

#### Scenario: A preset drives both serving and deploying

- **WHEN** an Outfit with a preset is deployed
- **THEN** the engine's arguments are the preset's, minus the settings the
  endpoint sets itself

#### Scenario: The Outfit overrides its preset

- **WHEN** the Outfit states a `MODEL` and `CONTEXT` that differ from the
  preset's
- **THEN** the Outfit's values are deployed

#### Scenario: A provider that is not a self-hosted engine

- **WHEN** an Outfit naming a hosted provider is deployed
- **THEN** the command fails saying that only a self-hosted engine can be
  deployed

#### Scenario: A local model file

- **WHEN** an Outfit naming a local model file is deployed
- **THEN** the command fails saying to name a repository instead, because the
  endpoint fetches its own weights

#### Scenario: Deploying is not starting

- **WHEN** a deployment succeeds
- **THEN** the endpoint is configured but not started, and the report says
  whether the weights still have to be fetched before it can serve
