## Why

With the shared, account-level infrastructure deployed once by
[`outfit remote bootstrap`](../add-remote-bootstrap/proposal.md), the CLI needs a
way to create and run individual endpoints against it. That is what
`outfit remote deploy` should become: the step that creates an **environment** —
its own Elastic IP and EC2 instance — on top of the shared layer, and registers
it so the other `remote` commands can drive it. One account, bootstrapped once,
then as many environments as the user wants, each an isolated instance.

Today `deploy` only sets what a single pre-existing endpoint serves. This change
makes it own the environment's lifecycle boundary: allocate the environment,
seed its weights, register it, and let the shared lifecycle Lambdas start, stop
and monitor every environment in the account.

## What Changes

- `outfit remote deploy <env>` **creates an environment**: it discovers the
  shared layer from the bootstrap stack's CloudFormation outputs, then provisions
  the environment's own **Elastic IP**, **EC2 instance** configuration, a
  **per-environment API key**, a **per-environment allowed ingress CIDR** (who
  can reach this instance, auto-detected from the caller's public IP or given by
  a flag), and per-environment SSM state (deploy-config and idle-state), tagged by
  environment name.
- It sets what that environment serves from the Outfit and its preset (as
  `deploy` does today), and seeds the model weights into the shared bucket under
  the model's prefix if they are not already there.
- It **registers the environment** in the per-user registry
  (`~/.config/outfit/remotes/<env>/remote.json`, owner-only), carrying the shared
  Lambda URLs, region, the environment's base URL (its EIP), and an
  **environment identifier** the Lambdas key on. This is where the registry
  entries the `add-remote-environments` work introduced actually get written.
- The lifecycle Lambdas become **environment-aware**: `start`, `stop`, `status`
  and the scheduled idle sweep operate on a named environment's instance (by tag
  and per-environment SSM state), so one shared set manages **all** environments
  in a bootstrapped account. Control requests carry the environment identifier.
- **Overwrite guard**: creating over an already-registered environment, or one
  whose instance is live, shows a warning and requires explicit `--overwrite`
  (which `--yes` does not imply), so a redeploy cannot silently clobber a running
  instance.

## Capabilities

### New Capabilities

- `environment-deployment`: `outfit remote deploy` creating an environment on the
  shared layer — discovering it via stack outputs, provisioning the per-env EIP /
  instance / API key / SSM state, seeding weights, registering the environment,
  and the overwrite guard.

### Modified Capabilities

- `remote-endpoint`: "Deploying what the endpoint serves" becomes "deploy creates
  and configures an environment" — allocating its EIP and registering it, not
  only setting a single endpoint's SSM config.
- `endpoint-lifecycle`: start / stop / idle teardown operate **per environment**;
  the shared Lambdas manage every environment's instance in the account, each
  with its own stable address, rather than a single global instance.
- `remote-environments`: an environment's `remote.json` also carries the
  environment identifier the shared Lambdas key on (the control URLs are shared
  across environments; the identifier selects the instance).

## Impact

- **Depends on `add-remote-bootstrap`** (the shared layer and its discoverable
  outputs) and on the `remote/` CDK restructure that splits per-environment
  resources out of the shared stack and makes the Lambdas environment-aware.
- **Code**: `cmd/outfit/remote.go` `cmdRemoteDeploy` gains environment creation +
  registration + the overwrite guard; `internal/remote` gains
  shared-layer discovery (CloudFormation outputs), the per-environment control
  identifier on `Config`, and `SaveEnvironment`; the lifecycle client passes the
  environment identifier.
- **CDK/Lambdas (`remote/`)**: per-environment EIP/instance/secret/SSM; Lambdas
  keyed by environment; idle sweep across all instances.
- **Docs**: `docs/commands/remote.md` (deploy creates environments; the
  bootstrap → deploy → start flow) and `remote/README.md`.
