## ADDED Requirements

### Requirement: Named environment registry

A remote environment SHALL be a directory under the per-user config,
`${XDG_CONFIG_HOME:-~/.config}/outfit/remotes/<name>/`, whose canonical file is
`remote.json` — the control URLs, region and base URL of one deployed instance.
The directory form SHALL be used so that other per-environment state may live
alongside `remote.json` later, and so that distinct environments never share a
file. The registry SHALL hold as many environments as the user has instances.

#### Scenario: An environment is a directory holding remote.json

- **WHEN** an environment named `qwen3.6-27b-prod` is registered
- **THEN** its configuration is `~/.config/outfit/remotes/qwen3.6-27b-prod/remote.json`

#### Scenario: Two environments do not collide

- **WHEN** two environments `a` and `b` both exist
- **THEN** each has its own `~/.config/outfit/remotes/<name>/` directory and
  neither overwrites the other

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
