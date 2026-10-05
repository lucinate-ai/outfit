## ADDED Requirements

### Requirement: The cloud command group

The CLI SHALL provide a `cloud` command group containing every subcommand the
`remote` group provided, with the same arguments, flags, output and exit
codes. Help text, error messages and progress output SHALL say "cloud" where
they previously said "remote" for this feature.

#### Scenario: A subcommand runs under the new name
- **WHEN** the user runs `spinloop cloud deploy staging ./Spinloop`
- **THEN** it behaves exactly as `spinloop remote deploy staging ./Spinloop` did before this change

#### Scenario: Help lists the new name
- **WHEN** the user runs `spinloop --help`
- **THEN** the command list shows `cloud` and does not show `remote`

### Requirement: The remote command group is removed

The CLI SHALL NOT provide a `remote` command group. Running `spinloop remote …`
SHALL fail as any other unknown command does, and shell completion SHALL NOT
offer `remote`.

#### Scenario: Old command is unknown
- **WHEN** the user runs `spinloop remote status`
- **THEN** the CLI exits non-zero with an unknown-command error

#### Scenario: Completion offers only the new name
- **WHEN** shell completion is requested for the root command's subcommands
- **THEN** `cloud` is offered and `remote` is not

### Requirement: The cloud fleet node kind

A fleet file node SHALL accept `kind: cloud` for a registered cloud
environment. `kind: remote` SHALL be rejected with the same error as any other
unknown kind. Fleet output, status and dashboard SHALL label such nodes `cloud`.

#### Scenario: New kind parses
- **WHEN** a fleet file declares a node with `kind: cloud`
- **THEN** it is loaded as a cloud environment node

#### Scenario: Old kind is rejected
- **WHEN** a fleet file declares a node with `kind: remote`
- **THEN** loading fails with an unknown-kind error naming the node

### Requirement: Cloud environment variables

The CLI SHALL read its cloud settings from `SPINLOOP_CLOUD_*` variables
(`SPINLOOP_CLOUD_REGION`, `_START_URL`, `_STOP_URL`, `_STATS_URL`,
`_DEPLOY_URL`, `_ENV_URL`, `_SEED_URL`, `_UPDATE_URL`, `_KEYSTORE`,
`_PACKAGE_MANAGER`). `SPINLOOP_REMOTE_*` variables SHALL be ignored.

#### Scenario: New variable is read
- **WHEN** `SPINLOOP_CLOUD_REGION=us-east-1` is set
- **THEN** the region used is `us-east-1`

#### Scenario: Old variable is ignored
- **WHEN** only `SPINLOOP_REMOTE_REGION=eu-west-2` is set
- **THEN** the region is treated as unset

### Requirement: Cloud environment storage names

Environments SHALL be stored under `~/.config/spinloop/clouds/<name>/cloud.json`
and credentials in `cloud-<region>.json` keystore files, with the same file
modes as before. The CLI SHALL NOT read or write `remotes/<name>/remote.json`
or `remote-<region>.json`.

#### Scenario: Writes use the new path
- **WHEN** `spinloop cloud deploy staging` finishes
- **THEN** `clouds/staging/cloud.json` exists with mode 0600

#### Scenario: Old environment is not found
- **WHEN** `remotes/staging/remote.json` exists and `clouds/staging/` does not
- **THEN** `spinloop status --env staging` reports that environment `staging` is not registered

### Requirement: Unrelated uses of remote are unchanged

Names that mean "fetched over HTTP" or "on another machine" SHALL keep
`remote` in their name: the `remote-spinloop-sources` capability, and daemon
base URLs on other hosts.

#### Scenario: A Spinloop is fetched from a URL
- **WHEN** the user runs `spinloop apply https://example.com/Spinloop`
- **THEN** it behaves as before this change
