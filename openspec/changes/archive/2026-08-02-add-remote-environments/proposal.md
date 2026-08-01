## Why

An Outfit's `REMOTE` currently resolves to a single file — either one beside the
Outfit or the per-user `~/.config/outfit/remote.json` fallback. That doesn't
scale to the way people actually use remote endpoints:

- **Two projects, two instances.** A user with a repo per model, each deploying
  its own cloud VM, has both Outfits fall back to the *same* per-user
  `remote.json`, which clobbers.
- **Multi-user on one repo.** `remote.json` is deployment state (the Lambda URLs
  and the endpoint address of one user's stack), so it can't be committed — each
  user runs their own instance — yet a committed `REMOTE ./remote.json` implies
  a file every user must produce, and there's nowhere to keep more than one.

The mistake is treating per-user, per-instance *deployment state* as if it were
committable project config. The fix is to give the Outfit a stable *name* for
its environment and keep the state in a per-user registry that holds as many
environments as the user has instances.

## What Changes

- Introduce a **per-user registry of named remote environments**. An environment
  is a directory `~/.config/outfit/remotes/<name>/` whose canonical file is
  `remote.json` (the control URLs, region, and base URL of one deployed
  instance). The directory form leaves room for other per-environment state
  later.
- Extend `REMOTE` resolution: a **bare name** (e.g. `REMOTE qwen3.6-27b-prod`)
  resolves to `~/.config/outfit/remotes/qwen3.6-27b-prod/remote.json`; a **path**
  (`./remote.json`, an absolute path, anything with a separator or a `.json`
  suffix) resolves as a file relative to the Outfit or absolutely, exactly as
  today. This is backward compatible.
- Replace the single-file per-user fallback: when no Outfit names a `REMOTE`, the
  `remote` commands use a `default` environment
  (`~/.config/outfit/remotes/default/remote.json`) so the "works from anywhere"
  convenience survives without a shared file that clobbers.
- Add `outfit remote ls` to list the registered environments — each with its
  base URL and region, marking any whose `remote.json` is missing — so the user
  can see what instances they have.
- Because only the environment *name* lives in the (committable) Outfit and all
  state lives per-user, two users sharing a repo each deploy and drive their own
  instance under the same name with no leakage, and one user's two projects map
  to two names with no clobber.

## Capabilities

### New Capabilities

- `remote-environments`: the per-user registry of named remote environments —
  the `~/.config/outfit/remotes/<name>/` layout and its canonical `remote.json`,
  how a bare `REMOTE` name resolves to it (and how a path value still resolves as
  a file), environment-name validity, listing via `outfit remote ls`, and the
  storage/isolation rules that keep deployment state per-user and out of Outfits.

### Modified Capabilities

- `remote-endpoint`: "Remote configuration discovery" resolves a `REMOTE` value
  through the environments registry (name) or as a file (path), with the
  no-`REMOTE` fallback moving from a single `remote.json` to the `default`
  environment; the "Remote command group" gains the `ls` subcommand.

## Impact

- **Config layout**: new `~/.config/outfit/remotes/<name>/` tree; the old
  single-file `~/.config/outfit/remote.json` fallback is superseded by the
  `default` environment. (A one-line migration note/behaviour for an existing
  `~/.config/outfit/remote.json` is covered in design.)
- **Code**: `internal/remote` gains environment-name resolution (name → registry
  dir → `remote.json`) reused by `resolveRemoteConfig` and `remoteBaseURL`
  (`cmd/outfit/remote.go`); a new `cmdRemoteList` (`outfit remote ls`); the
  `remote` sub-dispatch and usage/help text; the completion scripts.
- **Depended on by**: `add-remote-bootstrap`, which writes a deployed instance's
  state into `~/.config/outfit/remotes/<name>/remote.json` rather than a single
  shared file.
- **Docs**: `docs/commands/remote.md`, `docs/outfit-file.md`, and `remote/`
  guidance gain the environment-name form and `outfit remote ls`.
