# config-location Specification

## Purpose
TBD - created by archiving change config-dir-override. Update Purpose after archive.
## Requirements
### Requirement: Single resolved config directory

outfit SHALL resolve one config directory and place every file it owns under
it: its own `config.json` (default-harness preference and alias registry), the
legacy `remote.json`, the `remotes/<name>/` environment registry, the daemon
state directory, and the CDK source directory. There SHALL be one resolver;
the location SHALL NOT be computed independently in more than one place.

#### Scenario: All outfit-owned state shares one root

- **WHEN** the config directory resolves to a given path
- **THEN** `config.json`, `remote.json`, the `remotes/<name>/` registry, and
  the daemon state directory all resolve beneath that same path

### Requirement: OUTFIT_CONFIG_DIR override

When `OUTFIT_CONFIG_DIR` is set, its value SHALL be outfit's config directory
verbatim — used as-is, with no `outfit` segment appended. It SHALL take
precedence over `XDG_CONFIG_HOME` and over the home-directory default.

#### Scenario: Override is used verbatim

- **WHEN** `OUTFIT_CONFIG_DIR=/var/lib/outfit` is set
- **THEN** outfit's config directory is `/var/lib/outfit` and, for example, the
  daemon reads its deploy config from `/var/lib/outfit/daemon/`

#### Scenario: Override wins over XDG and home

- **WHEN** both `OUTFIT_CONFIG_DIR` and `XDG_CONFIG_HOME` are set
- **THEN** the config directory is `OUTFIT_CONFIG_DIR`'s value, ignoring
  `XDG_CONFIG_HOME`

### Requirement: Default resolution unchanged

With `OUTFIT_CONFIG_DIR` unset, the config directory SHALL be
`${XDG_CONFIG_HOME}/outfit` when `XDG_CONFIG_HOME` is set, otherwise
`~/.config/outfit`. This is the existing behaviour and SHALL be preserved so
existing installs are unaffected.

#### Scenario: XDG default

- **WHEN** `OUTFIT_CONFIG_DIR` is unset and `XDG_CONFIG_HOME` is set
- **THEN** the config directory is `${XDG_CONFIG_HOME}/outfit`

#### Scenario: Home default

- **WHEN** neither `OUTFIT_CONFIG_DIR` nor `XDG_CONFIG_HOME` is set and the
  home directory resolves
- **THEN** the config directory is `~/.config/outfit`

### Requirement: Unresolvable home fails loudly

When `OUTFIT_CONFIG_DIR` is unset, `XDG_CONFIG_HOME` is unset, and the home
directory cannot be resolved (no `$HOME`, as under a bare systemd service),
outfit SHALL fail with an error that names `OUTFIT_CONFIG_DIR` as the fix,
rather than silently resolving to a relative or root-anchored `.config`
directory.

#### Scenario: No home, no override

- **WHEN** none of `OUTFIT_CONFIG_DIR`, `XDG_CONFIG_HOME`, or a resolvable
  home directory is available
- **THEN** the operation fails with an error naming `OUTFIT_CONFIG_DIR`, and
  does not read or write a bogus relative `.config` path

