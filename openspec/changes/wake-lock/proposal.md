## Why

The start Lambda decides whether to launch an instance by looking one up by tag. That lookup is eventually consistent, so two starts for the same environment that arrive close together can each miss the other's new instance and each launch one: two GPU instances for one environment, both billed (issue 224).

The gateway already coalesces concurrent wakes inside one gateway process, but the control plane is open to every other caller: a second gateway, `spinloop remote start` racing a gateway, and a scheduled start (issue 178) firing while someone starts the environment by hand.

## What Changes

- The start Lambda takes a per-environment lock before it looks anything up and releases it on every way a start can end.
- A start that finds the lock held does not look up, launch or re-wake anything. It replies with a retryable 503 saying another start is in progress, which the CLI and gateway already retry.
- A lock left behind by a start that never finished (the Lambda was killed) expires when that start would have timed out, and the next start takes it over.
- The lock is an SSM parameter created only if absent, `/cloud-vm-llm/<env>/wake-lock`. The start Lambda's role gains `ssm:DeleteParameter` on that parameter name only.
- Locks are per environment: starts for different environments do not wait for each other.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `endpoint-lifecycle`: adds a requirement that concurrent starts of one environment are serialised and that a held or abandoned lock is handled as above. The existing "Starting on demand" behaviour is otherwise unchanged.

## Impact

- `remote/lambda/start/index.ts`: `wake()` becomes a lock wrapper around the existing body; a new shared module holds the lock.
- `remote/lib/llm-stack.ts`: one IAM statement on the start Lambda's role.
- `remote/test/`: new tests for the lock and for `wake()` under contention.
- `docs/maintainer/internals.md` and `remote/README.md`: a short note on the lock.
- No CLI, config or API change. Existing deployments need a `spinloop remote bootstrap` re-run (or `pnpm run deploy`) to get the new grant; until then a start fails at the lock instead of launching unlocked.
