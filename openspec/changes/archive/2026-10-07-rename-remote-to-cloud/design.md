## Context

"remote" currently means four things: this cloud GPU feature, a Spinloop
fetched over HTTP, a daemon on another host, and the `remote/` CDK project.
Only the first is being renamed. About 120 Go files and 35 specs mention
"remote"; most of the work is mechanical.

## Goals / Non-Goals

**Goals**
- `cloud` is the only name for this feature in the CLI, help, completion,
  docs, errors, environment variables, fleet files and on-disk files.

**Non-Goals**
- Changing any behaviour of the commands.
- Keeping the old names working (no alias, no fallback, no migration).
- Renaming the `remote/` CDK directory, `remote-spinloop-sources`, or the
  capability directories under `openspec/specs/`.
- Removing the leftover path-form `REMOTE` references (issue 259).

## Decisions

1. **Clean break.** The old command, fleet kind, variables and paths are
   removed outright; there is no deprecation period. The rewrite table in
   `commands.go` (`"remote status": "status --env <name>"`) is re-keyed to
   `cloud`, and the `remote` command is not registered.
2. **Package rename with `git mv`.** `internal/remote` → `internal/cloud`
   and `internal/fleet/remote_node.go` → `cloud_node.go`; identifiers such as
   `KindRemote` → `KindCloud` change in the same commit so the build never has
   two names for one thing.
3. **Environment variables.** `cli_viper.go` binds `SPINLOOP_CLOUD_*`; callers
   ask for `cloud_<x>`.
4. **Storage path.** `remotes/<name>/remote.json` → `clouds/<name>/cloud.json`;
   keystore `remote-<region>.json` → `cloud-<region>.json`. Existing
   environments must be re-registered with `spinloop cloud deploy`.
5. **Keep `remote/` (CDK).** It is referenced by the release asset the
   bootstrap command downloads (`internal/cloud/source.go` strips the
   `remote/` prefix), by CI, and by `scripts/check-no-cloud-identifiers.sh`.
   Renaming it changes the release layout for no user-visible gain. Left as a
   possible follow-up.
6. **Spec directories keep their names.** Renaming 17 capability
   directories would break archive history and links. Main-spec wording is
   updated; the new `cloud-command` capability holds the naming contract.

## Risks / Trade-offs

- A blind find-and-replace would hit the unrelated meanings above.
  Mitigation: rename per package, review each diff, keep the "unchanged"
  requirement as a test (`spinloop apply <url>` still works).
- Tab completion and the root dispatch table are string-keyed; a missed
  entry fails at runtime. Mitigation: `complete_test.go` and
  `root_dispatch_test.go` cases for the new name and for `remote` being unknown.
- Existing users lose access to registered environments and keystore files
  until they re-register. Accepted; called out as **BREAKING** in the proposal.

## Migration Plan

There is no automatic migration. Users rename their scripts and fleet files, set
`SPINLOOP_CLOUD_*`, and re-run `spinloop cloud deploy` for each environment.
