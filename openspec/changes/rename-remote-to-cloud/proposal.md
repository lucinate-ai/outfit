## Why

The `spinloop remote` command group deploys and drives scale-to-zero GPU
instances in the cloud, but "remote" is also used for other things: a Spinloop
fetched over HTTP, a remote node in a fleet, a remote machine running a
daemon. Naming the group `cloud` says what it does and removes the clash
(GitHub issue 117).

## What Changes

- Rename the `spinloop remote` command group to `spinloop cloud`. The
  subcommands (`bootstrap`, `auth`, `bake`, `deploy`, `start`, `pause`,
  `restart`, `keep`, `stop`, `seed`, `list`, …) keep their names.
- Rename the fleet node kind `remote` to `cloud` (`kind: cloud` in a fleet file).
- Rename the environment variables `SPINLOOP_REMOTE_*` to `SPINLOOP_CLOUD_*`.
- Rename the on-disk names: the `remotes/` environments directory, each
  environment's `remote.json`, and the `remote-<region>.json` keystore files.
- Rename the Go package `internal/remote` to `internal/cloud`, and the fleet
  node type, file names, test names, error messages and help text to match.
- Reword the docs, examples, specs and `AGENTS.md` to say "cloud" wherever
  they mean this feature.
- **BREAKING**: the old names are removed with no fallback. `spinloop remote`,
  `kind: remote`, `SPINLOOP_REMOTE_*` and the old on-disk names stop working;
  environments registered under `remotes/` must be re-registered.
- Not renamed: the `remote/` CDK project directory (see design.md), and
  a Spinloop *source* fetched over HTTP (`remote-spinloop-sources`,
  `internal/spinloopsrc`), which is unrelated. Removing the leftover path-form
  `REMOTE` references is tracked in issue 259.

## Capabilities

### New Capabilities

- `cloud-command`: the `cloud` command group name, the `cloud` fleet node
  kind, the `SPINLOOP_CLOUD_*` variables, the on-disk names, and the removal
  of the old names.

### Modified Capabilities

None as delta specs. The existing `remote-*` capability specs describe the
same behaviour under the old name; their wording is updated in the main specs
as part of the change (see tasks.md), and their directory names are left alone
so that history and links stay valid.

## Impact

- Code: `cmd/spinloop` (command tree, completion, root dispatch, help text,
  `remote*.go` files), `internal/remote` → `internal/cloud`, `internal/fleet`
  (node kind, `remote_node.go`), `internal/harness`, `internal/config`,
  `internal/gateway`, and the Viper env binding.
- Docs: `docs/commands/remote.md`, `docs/guides/remote.md`, fleet, env-vars,
  FAQ, troubleshooting, `mkdocs.yml`, `README.md`, examples (`fleet-remote`).
- Existing users: scripts, fleet files and environment variables using the old
  names must be updated, and environments re-registered.
- `CHANGELOG.md` is not touched (the release process does it).
