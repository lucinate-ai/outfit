# remote-env Specification

## Purpose

How a caller gets the credentials for a running cloud endpoint. The API key
is not in the local `cloud.json` — it lives in AWS Secrets Manager, and the
base URL comes from the environment's Elastic IP — so it can only be fetched,
never read from disk. This covers `spinloop cloud env`, which fetches it from an
already-running endpoint without booting one, and the `--print-env` flag that
opts `spinloop cloud start` into printing the same exports.

## Requirements

### Requirement: cloud env subcommand
The CLI SHALL provide `spinloop cloud env` as a subcommand that returns the cloud endpoint's environment variables from an already-running instance.

It SHALL select which environment it reads using the same rule as the other
`cloud` subcommands: the `--env <name>` flag naming a registered environment,
falling back to the `default` environment when the flag is absent. A Spinloop
given as an argument SHALL be read only for its `ENV` instructions and adjacent
`.env`, never to select an environment.

Its stdout SHALL carry nothing but the `export` lines, so `eval "$(spinloop cloud env)"` is safe in a shell. Anything the command has to say for itself — notably the note reporting which alias resolved the Spinloop — SHALL go to stderr, where it is still visible in a terminal but outside what the shell evaluates.

#### Scenario: env returns exports for a running endpoint
- **WHEN** the user runs `spinloop cloud env` and the cloud instance is running
- **THEN** stdout contains `export OPENAI_BASE_URL=<url>` and `export OPENAI_API_KEY=<key>`

#### Scenario: env fails when endpoint is stopped
- **WHEN** the user runs `spinloop cloud env` and the cloud instance is stopped
- **THEN** the command fails with an error telling the user to run `spinloop cloud start` first

#### Scenario: env names the environment with the flag
- **WHEN** the user runs `spinloop cloud env --env dev-2`
- **THEN** the command reads `dev-2`'s control configuration from the registry
  and reports on that environment's endpoint

#### Scenario: env falls back to the default environment
- **WHEN** the user runs `spinloop cloud env` with no `--env` flag
- **THEN** the command uses the `default` environment, whether or not a
  `./Spinloop` is present

#### Scenario: env outputs nothing to stderr on success
- **WHEN** the user runs `spinloop cloud env` with no alias to report, and the endpoint is running
- **THEN** stderr is empty (only export lines on stdout)

#### Scenario: env stdout is eval-safe for an aliased Spinloop
- **WHEN** the user runs `eval "$(spinloop cloud env <alias>)"` and the endpoint is running
- **THEN** every line on stdout is an `export` line, the alias note having gone to stderr, and the shell evaluates it without error

### Requirement: env Lambda is fast (no boot)
The `spinloop cloud env` command SHALL NOT trigger an instance boot. It reads the API key from Secrets Manager, the base URL from the environment's Elastic IP, and — best-effort — the environment's deploy-config from SSM; none of these require the instance to be running or booting.

#### Scenario: env does not start a stopped instance
- **WHEN** the user runs `spinloop cloud env` and the instance is stopped
- **THEN** the command returns quickly with an error (not after minutes of booting)

### Requirement: env Lambda reports what is deployed
The `env` Lambda SHALL read the named environment's deploy-config from SSM (the same state `deploy` writes and `start`/`stats` already relay) and, when one is present and parses, include it in its reply alongside `base_url` and `api_key`: a `deployed` flag, the `runner`, the `modelId`, the `servedName` (the name the engine answers to — an `ALIAS` at deploy time, falling back to the model id), and the `contextSize`.

When no deploy-config is registered for the environment, or the stored value fails to parse, the reply SHALL omit these fields rather than fail the request: `base_url` and `api_key` remain valid and useful without them, exactly as they were before this requirement existed.

#### Scenario: env reports the deployed model
- **WHEN** the user runs `spinloop cloud env --env dev-3` and a model has been deployed to `dev-3`
- **THEN** the reply includes `deployed: true`, the `runner`, `modelId`, `servedName` and `contextSize` the deploy recorded, alongside `base_url` and `api_key`

#### Scenario: env degrades gracefully with nothing deployed
- **WHEN** the user runs `spinloop cloud env --env dev-3` and `dev-3` is registered but nothing has been deployed to it
- **THEN** the reply carries `base_url` and `api_key` as it always has, with no `deployed`, `runner`, `modelId`, `servedName` or `contextSize` fields, and the command does not fail because of their absence

### Requirement: start --print-env flag prints exports

The `spinloop cloud start` command SHALL accept a `--print-env` flag, with no
short form, that when present prints the export lines to stdout after a
successful start. The flag's former `-e`/`--env` spelling is removed, since
`--env` on `start` now selects the environment like on every other `cloud`
subcommand; the eval-safe export lines remain available from
`spinloop cloud env`.

#### Scenario: start with --print-env prints exports

- **WHEN** the user runs `spinloop cloud start --print-env` and the instance
  starts successfully
- **THEN** stdout contains the `export OPENAI_BASE_URL` and
  `export OPENAI_API_KEY` lines

#### Scenario: start without the flag suppresses exports

- **WHEN** the user runs `spinloop cloud start` without `--print-env`
- **THEN** stdout does not contain export lines (only stderr progress)

### Requirement: start default behaviour changed
By default (no flags), `spinloop cloud start` SHALL NOT print export lines to stdout.

#### Scenario: bare start produces no stdout exports
- **WHEN** the user runs `spinloop cloud start` and the instance starts successfully
- **THEN** stdout is empty (progress goes to stderr only)
