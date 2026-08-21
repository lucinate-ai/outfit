# Add a `remote restart` command

## Why

A running remote endpoint can end up in a state `start` cannot repair: the
engine wedges (a process that answers nothing), and because wake is idempotent
it will not replace a process the daemon already believes is running — the
instance reports "running" and never becomes ready. Today the remedy is the
stop-then-start dance by hand, and when the engine or its daemon does not
answer a polite stop there is no way to say "just take the box down". A
restart — one command, fastest path back to serving, with a force option for
the unresponsive case — closes that gap.

## What Changes

- A new `outfit remote restart` subcommand: stops the environment's instance
  **without terminating it** (the boot disk and its weights survive, so the
  re-wake is fast and the environment's URL does not change), then wakes it
  again immediately, blocking until the model is serving. It reuses `start`'s
  progress reporting and its `--timeout`/`-t` flag (default 15 minutes), and
  takes an optional Outfit path like the other lifecycle subcommands.
- A `--force`/`-F` flag on `restart`: skips the graceful engine stop. The
  control plane stops the instance without first asking the on-instance daemon
  to shut the engine down — the escape hatch for a wedged engine or daemon that
  would not answer the polite stop.
- The control plane's stop endpoint accepts an optional `force=true` query
  parameter, honoured in both of its manual modes (pause and terminate): the
  daemon engine-stop step is skipped; everything else — the stop-time tag, the
  EC2 call, the reply — is unchanged. Without the parameter, behaviour is
  exactly today's.
- `internal/remote` gains `Restart`, which pairs the stop and the wake: the
  stop is made pause-style (with force when requested), and the wake reuses
  the existing `Start` with its retry and deadline behaviour. When the wake
  fails after the stop has already taken effect, the error says the instance
  is stopped and that `outfit remote start` will bring it back.
- Documentation: `restart` joins the `remote` command page and the usage
  text; AGENTS.md's description of the `remote` command group is updated.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `endpoint-lifecycle`: gains an explicit-restart requirement — a restart
  stops the instance in the pause manner (never terminating, so the re-wake is
  fast and the URL is unchanged) and does not report success until the model
  serves again; a forced restart skips the daemon engine-stop step. The
  existing "Engine is stopped before the EC2 instance" requirement changes to
  allow that step to be skipped when the stop request is marked force.
- `remote-endpoint`: the "Remote command group" requirement gains `restart` as
  a named subcommand, with its Outfit path argument, its `--force` flag and
  its block-until-serving semantics.

## Impact

- Go CLI: `cmd/outfit/remote.go` (new command, usage text, test seam),
  `cmd/outfit/commands.go` (registration in the `remote` tree), and
  `internal/remote` (`Pause` takes a force flag; new `Restart`). Tests:
  `cmd/outfit/remote_test.go` and `internal/remote/remote_test.go`.
- Control plane (TypeScript, `remote/`): `remote/lambda/stop/index.ts` honours
  the `force` query parameter; its vitest suite covers both forced modes. No
  CDK or infrastructure change — a query parameter on an existing Function
  URL — and existing configs work unchanged.
- No new dependencies, no Outfit format change, no config-schema change.
  `pause`, `stop` and `start` keep their current behaviour when no force is
  requested.
