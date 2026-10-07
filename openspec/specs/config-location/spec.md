# config-location Specification

## Purpose

How spinloop resolves its own config directory, and why that resolution is
explicit rather than inferred.

Everything spinloop owns lives under one directory — `config.json`, `cloud.json`,
the environment registry, the daemon state dir, the CDK source cache — and the
obvious way to find it leans on `$HOME`. A systemd service does not get one. On
the cloud instance that meant the boot script wrote to `/root/.config/spinloop`
while the daemon read a relative `.config/spinloop`, so every boot start returned
"nothing to serve" against a perfectly healthy box. This covers the
`SPINLOOP_CONFIG_DIR` override, its precedence over `XDG_CONFIG_HOME` and
`~/.config`, and the rule that an unresolvable home fails loudly instead of
silently resolving to a bogus relative path.

## Requirements
### Requirement: Single resolved config directory

spinloop SHALL resolve one config directory and place every file it owns under
it: its own `config.json` (default-harness preference and alias registry), the
`clouds/<name>/` environment registry, the daemon state directory, and the CDK
source directory. There SHALL be one resolver; the location SHALL NOT be
computed independently in more than one place.

#### Scenario: All spinloop-owned state shares one root

- **WHEN** the config directory resolves to a given path
- **THEN** `config.json`, the `clouds/<name>/` registry, and the daemon state
  directory all resolve beneath that same path

### Requirement: SPINLOOP_CONFIG_DIR override

When `SPINLOOP_CONFIG_DIR` is set, its value SHALL be spinloop's config directory
verbatim — used as-is, with no `spinloop` segment appended. It SHALL take
precedence over `XDG_CONFIG_HOME` and over the home-directory default.

#### Scenario: Override is used verbatim

- **WHEN** `SPINLOOP_CONFIG_DIR=/var/lib/spinloop` is set
- **THEN** spinloop's config directory is `/var/lib/spinloop` and, for example, the
  daemon reads its deploy config from `/var/lib/spinloop/daemon/`

#### Scenario: Override wins over XDG and home

- **WHEN** both `SPINLOOP_CONFIG_DIR` and `XDG_CONFIG_HOME` are set
- **THEN** the config directory is `SPINLOOP_CONFIG_DIR`'s value, ignoring
  `XDG_CONFIG_HOME`

### Requirement: Default resolution unchanged

With `SPINLOOP_CONFIG_DIR` unset, the config directory SHALL be
`${XDG_CONFIG_HOME}/spinloop` when `XDG_CONFIG_HOME` is set, otherwise
`~/.config/spinloop`. This is the existing behaviour and SHALL be preserved so
existing installs are unaffected.

#### Scenario: XDG default

- **WHEN** `SPINLOOP_CONFIG_DIR` is unset and `XDG_CONFIG_HOME` is set
- **THEN** the config directory is `${XDG_CONFIG_HOME}/spinloop`

#### Scenario: Home default

- **WHEN** neither `SPINLOOP_CONFIG_DIR` nor `XDG_CONFIG_HOME` is set and the
  home directory resolves
- **THEN** the config directory is `~/.config/spinloop`

### Requirement: Unresolvable home fails loudly

When `SPINLOOP_CONFIG_DIR` is unset, `XDG_CONFIG_HOME` is unset, and the home
directory cannot be resolved (no `$HOME`, as under a bare systemd service),
spinloop SHALL fail with an error that names `SPINLOOP_CONFIG_DIR` as the fix,
rather than silently resolving to a relative or root-anchored `.config`
directory.

#### Scenario: No home, no override

- **WHEN** none of `SPINLOOP_CONFIG_DIR`, `XDG_CONFIG_HOME`, or a resolvable
  home directory is available
- **THEN** the operation fails with an error naming `SPINLOOP_CONFIG_DIR`, and
  does not read or write a bogus relative `.config` path

