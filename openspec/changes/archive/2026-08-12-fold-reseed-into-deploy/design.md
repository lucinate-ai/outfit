## Context

See proposal.md — Why. The relevant shape:

- `deploy/index.ts:99` is the entire seed decision:
  `if (!(await weightsPresent(...))) { launchSeedInstance(...) }`.
- `launchSeedInstance` already picks the seed-tooling AMI (with the
  pre-runner-tag fallback), builds the user-data via `buildSeedUserData`, and
  launches through `runInstance` — which passes a `ClientToken`.
- The script re-implements the AMI lookup, the user-data assembly and the
  launch, and omits the `ClientToken`.
- The Go client posts `{...DeployConfig, allowedCidr}` as one flat body;
  `allowedCidr` is already an example of a request-only field that is not part
  of `DeployConfig`.

## Goals / Non-Goals

**Goals:**

- One seed code path, reached the same way whether the weights are missing or
  the caller asked for a re-fetch.
- Remove the `cdk-outputs.json` dependency from the re-seed workflow.

**Non-Goals:**

- Changing what a seed *does*. The user-data, the AMI choice and the S3 layout
  are untouched.
- Any new authorisation model. `--reseed` rides the existing SigV4 Function URL
  with the same permission as any other deploy.
- Re-seeding without deploying. A re-fetch is requested *as part of* a deploy,
  not as a separate verb — see below.

## Decisions

### A request field, not part of `DeployConfig`

`reseed` is a property of *this request*, not of what the environment serves,
so it must not enter `DeployConfig` — that type is persisted verbatim to the
SSM deploy-config, and a stored `reseed: true` would re-seed on every
subsequent wake that re-read it.

It therefore sits beside `allowedCidr`, which is already exactly this: a
request-only field parsed from the raw body rather than by
`parseDeployConfig`. On the Go side it joins the same anonymous struct.

*Alternative — a separate `reseed` Lambda or a `?reseed=1` query parameter.*
Rejected: a re-seed is only meaningful against a specific config, and deploy
already resolves, validates and stores that config. A separate entry point
would have to duplicate that resolution — the same duplication being removed.

### The condition, not the mechanism

The whole change at the Lambda is the guard:

```ts
if (reseed || !(await weightsPresent(WEIGHTS_BUCKET, config))) {
```

`weightsPresent` is skipped rather than overridden when `reseed` is set — no
point paying HEAD requests whose answer is ignored. Everything downstream
(`launchSeedInstance`, the 502 on failure, the `seeding`/`seedInstanceId` in
the reply) is already correct for both cases and is not touched.

### Deleting the script rather than thinning it

The alternative considered was keeping `seed-model.mts` as a thin wrapper over
`launchSeedInstance`. Rejected: even thinned it still needs `cdk-outputs.json`
to build a `SeedEnv`, which means a local `remote/` checkout and a prior
`pnpm deploy` — for an operation the control plane can already do from
anywhere with nothing but AWS credentials. Keeping it would preserve the split
that caused the drift.

This also settles a live question: `--reseed` re-seeds *the config being
deployed*, whereas the script re-seeded whatever the SSM parameter already
held. That is a behaviour change, and the better one — the script's version
could re-seed weights for a config nobody had deployed yet.

### An overwrite, not a new location

A re-fetch writes to the same derived prefix, and the seed's `aws s3 sync`
overwrites in place. Nothing needs to change for this; it is recorded because
the spec now states it.

## Risks / Trade-offs

- **A re-seed is now reachable through the normal deploy API** → It costs a
  ~20-minute seed instance and re-downloads the weights, so an accidental
  `--reseed` is real money. Mitigated by it being an explicit opt-in flag,
  never a default, and by deploy printing that a seed was started.
- **A re-seed while an instance is running** → The running engine already has
  its weights on local disk from boot, so an in-flight re-sync does not disturb
  it; the next wake picks up the new copy. Unchanged from the script's
  behaviour, which had the same property.
- **Losing the "re-seed exactly what SSM holds" behaviour** → Deliberate, per
  the decision above. Anyone wanting the old semantics deploys the same Outfit
  with `--reseed`, which is the same thing whenever the Outfit is the source of
  truth — which the deploy flow already assumes everywhere else.

## Migration Plan

No data migration. `pnpm seed-model <env>` becomes
`outfit remote deploy --reseed [outfit]`, documented in `remote/README.md`
where the script is currently described.

Rollback is a Lambda redeploy: an older Lambda ignores the unknown `reseed`
field and simply does not force the seed.

Ship order: Lambda first (accepting the field is inert until something sends
it), then the Go flag, then delete the script and its docs.
