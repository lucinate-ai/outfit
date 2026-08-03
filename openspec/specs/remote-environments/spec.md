# Remote Environments Specification

## Purpose

Define the per-user registry of named remote environments: how an Outfit's
`REMOTE` value selects a deployment's control config by name (or still by path),
where that config lives, how environments are listed, and the storage rules that
keep per-user, per-instance deployment state out of committed Outfits.

## Requirements

### Requirement: Named environment registry

A remote environment SHALL be a directory under the per-user config,
`${XDG_CONFIG_HOME:-~/.config}/outfit/remotes/<name>/`, whose canonical file is
`remote.json` — the control URLs, region, base URL, and the environment
identifier of one deployed instance. Because the lifecycle Lambda URLs are shared
across environments, the identifier is what selects this environment's instance;
the `remote` client SHALL send it with each control request. The directory form
SHALL be used so that other per-environment state may live alongside `remote.json`
later, and so that distinct environments never share a file. The registry SHALL
hold as many environments as the user has instances.

#### Scenario: An environment is a directory holding remote.json

- **WHEN** an environment named `qwen3.6-27b-prod` is registered
- **THEN** its configuration is `~/.config/outfit/remotes/qwen3.6-27b-prod/remote.json`

#### Scenario: Two environments do not collide

- **WHEN** two environments `a` and `b` both exist
- **THEN** each has its own `~/.config/outfit/remotes/<name>/` directory and
  neither overwrites the other

#### Scenario: The identifier selects the instance

- **WHEN** two environments share the same lifecycle Lambda URLs and a control
  command runs for one of them
- **THEN** the environment identifier in its `remote.json` is sent so the shared
  Lambda acts on that environment's instance

#### Scenario: A control call without an environment is rejected

- **WHEN** a control request reaches a lifecycle Lambda naming no environment
- **THEN** it is rejected with an error saying how to name one, rather than a
  default being silently assumed — defaults are a CLI affordance, not part of
  the control API

### Requirement: Resolving a REMOTE value to an environment or a file

A `REMOTE` value that is a bare name — a plain identifier with no path separator
and no `.json` suffix — SHALL resolve to that environment's `remote.json` in the
registry. A `REMOTE` value that is a path — containing a separator, absolute, or
ending in `.json` — SHALL resolve as a file, relative to the Outfit's directory
when not absolute, exactly as before. This resolution SHALL apply wherever a
`REMOTE` value is read, including both the `remote` control commands and the
base-URL lookup performed when applying an Outfit.

#### Scenario: A bare name resolves through the registry

- **WHEN** an Outfit states `REMOTE qwen3.6-27b-prod`
- **THEN** its remote configuration is read from
  `~/.config/outfit/remotes/qwen3.6-27b-prod/remote.json`

#### Scenario: A path still resolves as a file

- **WHEN** an Outfit states `REMOTE ./remote.json`
- **THEN** the configuration is read from that file beside the Outfit, as before

#### Scenario: The same resolution feeds the base URL

- **WHEN** an Outfit states `REMOTE qwen3.6-27b-prod`, no `BASEURL`, and is
  applied
- **THEN** the base URL is taken from that environment's `remote.json`

### Requirement: Environment name validity

An environment name SHALL be a plain name, not a path: it SHALL NOT contain a
path separator, and a value that looks like a path SHALL be rejected as a name
(and treated as a file path instead). Invalid names SHALL be reported saying an
environment name is a plain identifier.

#### Scenario: A path-like value is not a name

- **WHEN** a `REMOTE` value contains a `/`
- **THEN** it is treated as a file path, not looked up as an environment name

### Requirement: Listing environments

`outfit remote ls` SHALL print every registered environment with its base URL
and region, and SHALL mark an environment whose `remote.json` is missing or
unreadable rather than failing. With no environments registered it SHALL say so
plainly rather than printing nothing.

#### Scenario: Listing shows each environment

- **WHEN** two environments are registered and the user runs `outfit remote ls`
- **THEN** both are listed with their base URL and region

#### Scenario: A missing configuration is marked, not fatal

- **WHEN** an environment directory exists without a readable `remote.json` and
  the user runs `outfit remote ls`
- **THEN** that environment is listed with a missing/unreadable marker and the
  command still succeeds

#### Scenario: No environments registered

- **WHEN** the registry is empty and the user runs `outfit remote ls`
- **THEN** the command says there are none, rather than printing empty output

### Requirement: Registry storage and isolation

Environment state SHALL live only under the per-user config directory, never in
an Outfit, so Outfits stay portable and committable — an Outfit carries only the
environment *name*. Each environment's `remote.json` SHALL be written with
owner-only permissions, since it holds a deployment's URLs and address. Because
state is per-user and keyed by name, two users sharing a repo SHALL each drive
their own instance under the same committed name without either seeing the
other's URLs.

#### Scenario: The Outfit carries only the name

- **WHEN** an Outfit names an environment and is committed to a shared repo
- **THEN** no deployment URLs or addresses are committed, only the environment
  name

#### Scenario: Owner-only configuration

- **WHEN** an environment's `remote.json` is written
- **THEN** it is created with owner-only permissions

### Requirement: A REMOTE names the harness provider

When an Outfit that has a `REMOTE` is applied, the harness provider SHALL be
keyed on the remote environment name rather than the `PROVIDER` value, and the
default model SHALL read as `<environment>/<model>`. The environment name SHALL
be the bare `REMOTE` value when `REMOTE` is a name, or the `environment` field of
the `remote.json` it names when `REMOTE` is a path; if neither yields a name, the
`PROVIDER` value SHALL remain the provider name. The `PROVIDER` entry SHALL still
supply the engine configuration (its options, API-key environment variable, and
base URL). Unapplying the same Outfit SHALL remove the provider that apply wrote.

#### Scenario: A bare name becomes the provider name

- **WHEN** an Outfit states `PROVIDER llamacpp`, `ALIAS qwen`, and `REMOTE dev-1`,
  and is applied
- **THEN** the harness config holds a provider keyed `dev-1` whose default model
  is `dev-1/qwen`, configured from the `llamacpp` catalogue entry

#### Scenario: A path form takes the name from its config

- **WHEN** an Outfit states `PROVIDER llamacpp` and `REMOTE ./remote.json`, and
  that `remote.json` sets `"environment": "dev-1"`, and is applied
- **THEN** the harness config holds a provider keyed `dev-1`

#### Scenario: A path form without an environment keeps the PROVIDER name

- **WHEN** an Outfit states `PROVIDER llamacpp` and `REMOTE ./remote.json`, and
  that `remote.json` has no `environment` field, and is applied
- **THEN** the harness config holds a provider keyed `llamacpp`, as before

#### Scenario: Unapply removes the environment-named provider

- **WHEN** an Outfit with `REMOTE dev-1` is applied and then unapplied
- **THEN** the provider keyed `dev-1` is removed from the harness config
