## Context

`outfit remote bootstrap` deploys the shared, account-level layer (Image Builder
+ AMIs, the lifecycle Lambdas, the shared bucket/roles/VPC) and publishes it as
CloudFormation stack outputs. This change makes `outfit remote deploy` the step
that stands up an actual endpoint on top: an **environment** with its own Elastic
IP and EC2 instance, registered so the other `remote` commands can drive it.

Today `deploy` derives what to serve from the Outfit + preset and sends it to a
single pre-existing endpoint. That derivation is kept; what is added is the
environment's lifecycle boundary — discover the shared layer, provision the
per-environment resources, register the environment, and let the shared,
environment-aware Lambdas manage it. This depends on the `remote/` CDK
restructure that splits per-environment resources out of the shared stack and
keys the Lambdas by environment.

## Goals / Non-Goals

**Goals:**

- `outfit remote deploy <env>` creates a named environment on the shared layer:
  its own EIP, instance config, per-env API key, per-env allowed CIDR, and per-env
  SSM state.
- Discover the shared layer from stack outputs, so nothing per-account is
  hard-coded and it works from any machine with account access.
- Register the environment in the per-user registry (owner-only), completing the
  `REMOTE <name>` → registry flow the environments work set up.
- One shared set of Lambdas manages every environment; refuse to clobber a live
  one without explicit consent.

**Non-Goals:**

- Deploying the shared layer (that is `bootstrap`).
- Changing how what-to-serve is derived from the Outfit/preset — that logic is
  unchanged; this change wraps environment creation around it.
- A Go rewrite of the CDK.

## Decisions

### Discover the shared layer from CloudFormation outputs

Deploy reads the bootstrap stack's outputs (well-known stack name) with
`DescribeStacks` to get the lifecycle Lambda URLs, weights bucket, shared roles
and region. If the stack is absent, deploy fails telling the user to run
`outfit remote bootstrap` first. This reuses the `service/cloudformation` client
the bootstrap change adds.

### Per-environment resources, keyed by name

Creating an environment provisions, tagged by the environment name: an Elastic
IP; the EC2 instance configuration (launched later by `start`); a per-environment
API-key secret; a per-environment allowed-CIDR security-group rule; and
per-environment SSM parameters for deploy-config and idle-state (e.g.
`/cloud-vm-llm/<env>/…`). The engine comes from the Outfit's `PROVIDER`, selecting
the matching baked AMI.

### The environment identifier travels in remote.json and control calls

Because the lifecycle Lambda URLs are shared across environments, a control call
must name which environment's instance to act on. The environment's `remote.json`
carries an `environment` identifier alongside the shared URLs, region, and its
own base URL (the EIP); the `remote` client sends it with each `start`/`stop`/
`status`/`deploy` request, and the Lambdas select the instance by it (tag + the
per-env SSM state).

### Registration via an owner-only helper

Deploy writes the environment's `remote.json` via `remote.SaveEnvironment(name,
data)` (`MkdirAll` `0700` + `WriteFile` `0600`), so the registry's owner-only rule
holds. The file carries the shared control URLs, region, base URL, and the
environment identifier.

### Per-environment allowed CIDR

Who may reach an environment's instance is scoped per environment, not per
account: `--allowed-cidr` on `deploy`, defaulting to the caller's detected public
IP as a `/32`, applied to that environment's security-group rule.

### Overwrite guard

If the target environment is already registered, or its instance is live, deploy
shows a prominent warning and requires explicit `--overwrite` (which `--yes` does
not imply), so a redeploy cannot silently clobber a running instance. A fresh
environment needs neither.

### Environment-aware lifecycle Lambdas (CDK/TS work)

The shared Lambdas take an environment identifier: `start` launches that
environment's instance and associates its EIP; `stop`/`status` act on it; the
scheduled idle sweep iterates every environment's instance rather than one global
one. This is the per-environment half of the `remote/` CDK restructure.

## Risks / Trade-offs

- **Depends on the CDK restructure and the shared layer** → sequenced after
  `add-remote-bootstrap`; deploy fails helpfully when the account is not
  bootstrapped.
- **Accidental clobber of a live instance** → the overwrite guard (warning +
  `--overwrite`, not implied by `--yes`).
- **Per-environment cost** → each environment holds an EIP (small at-rest) and,
  while running, its own GPU instance; the deploy report and `remote ls` make
  environments visible so stale ones can be found.
- **Stale registry vs reality** → `remote.json` reflects what deploy wrote;
  teardown/removal of an environment is future work (see the bootstrap change's
  `outfit remote destroy` note).

## Open Questions

- Whether weights seeding stays shared (one model seeded once into the shared
  bucket, reused by every environment serving it) — assumed yes, keyed by model
  prefix; deploy triggers a seed only when the model's weights are absent.
- Whether `deploy` should optionally `start` the environment in one step, or
  always leave starting to `outfit remote start` (kept separate here, matching
  today's "deploying is not starting").
