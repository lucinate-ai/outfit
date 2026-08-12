## Why

Forcing a re-seed of weights already in S3 is the one seeding operation that
has no place in the control plane. It lives in `remote/scripts/seed-model.mts`,
run by hand, because it predates the deploy Lambda's automatic seed — the
script's own header says so: *"The deploy Lambda **now** seeds automatically…
so this script is only needed to force a re-seed."*

Everything the script does, the Lambda already has. `buildSeedUserData`,
`launchSeedInstance` and `weightsPresent` all live in `lambda/shared/seed.ts`
and the deploy Lambda calls them. What is left in the script is one condition —
seed even when the weights are present — wrapped in a duplicate of machinery
that already exists.

That duplication is not inert. The script hand-rolls its own AMI lookup
(re-implementing `launchSeedInstance`, fallback included) and its own
`RunInstances` call **without** the `ClientToken` that `runInstance` passes, so
an SDK retry of a lost response launches a second seed instance and consumes
the vCPU quota the first one just took. It also reads `cdk-outputs.json` from
disk, so it only works on a machine that has run `pnpm deploy`, and it is the
only remaining consumer of that file.

## What Changes

- A deployment request MAY ask for its weights to be re-fetched even when they
  are already stored. The deploy Lambda then seeds unconditionally rather than
  only on absence.
- `outfit remote deploy --reseed` sends that, and reports the seed it started
  the same way a first deploy does.
- **BREAKING (tooling only)**: `remote/scripts/seed-model.mts` and the
  `pnpm seed-model` script are removed. The replacement is
  `outfit remote deploy --reseed`, which needs no `cdk-outputs.json` and no
  local checkout of `remote/`.
- The `ClientToken` idempotency guard and the runner-tagged AMI selection now
  apply to a forced re-seed too, because it goes through `launchSeedInstance`
  like every other seed.

Not breaking for deployments: without the flag, deploy behaves exactly as it
does today.

## Capabilities

### New Capabilities

None. This narrows an existing capability's behaviour to one code path.

### Modified Capabilities

- `model-weights`: fetching gains a caller-requested form. Presence currently
  decides entirely whether a fetch happens; a deployment may now ask for one
  regardless, and the same completion marker rules apply to it.

## Impact

- `remote/lambda/deploy/index.ts` — the seed decision, and the request field.
- `remote/lambda/shared/seed.ts` — no change expected; `launchSeedInstance`
  already does the work.
- `remote/scripts/seed-model.mts` — deleted.
- `remote/package.json` — the `seed-model` script entry removed.
- `remote/README.md`, `remote/docs/architecture.md` — both document the script.
- `cmd/outfit/remote.go` — the `--reseed` flag and its wiring.
- `internal/remote/remote.go` — the request body gains the field.
- `docs/openapi.yaml` — guarded by a contract test against the Go types.
- No infrastructure change: same Lambda, same seed role, same bucket.
