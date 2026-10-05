# Environment Deployment Specification

## Purpose

Define how `spinloop cloud deploy` creates a named environment on the
account-level control plane: discovering it, provisioning per-environment resources, registering the environment, and guarding against accidental overwrites.

## Requirements

### Requirement: Deploy creates an environment on the control plane

`spinloop cloud deploy` SHALL create a named environment on top of the control
plane: it SHALL discover it, then provision the
environment's own Elastic IP, EC2 instance configuration, per-environment API
key, per-environment allowed-ingress rule, and per-environment SSM state
(the deploy-config), all tagged by the environment name. It SHALL set
what the environment serves from the Spinloop and its preset, and SHALL register
the environment so the other `cloud` commands can drive it. Deploying SHALL NOT
start the instance.

The environment name SHALL come from the command, never from the Spinloop:
`spinloop cloud deploy` SHALL take it from its required `--env <name>` flag,
and `spinloop fleet deploy` SHALL take it from the name of the node being
deployed, which is the registered environment that node drives. A deploy given
no name by either route SHALL fail saying the environment must be named. The
Spinloop SHALL be read only for what the environment serves and for its local
environment (`ENV` instructions and adjacent `.env`), and the same Spinloop
SHALL be deployable under more than one environment name.

Deploy SHALL NOT provision any activity-tracking state. Engine activity is
recorded on the instance by its daemon, so there is nothing for the control
plane to seed, read or write.

#### Scenario: Deploying stands up and registers an environment

- **WHEN** `spinloop cloud deploy --env prod` runs against a bootstrapped
  account
- **THEN** the environment's Elastic IP, instance configuration, API key,
  ingress rule, and SSM state are provisioned, and the environment is
  registered under the name `prod`

#### Scenario: A deploy without a name fails

- **WHEN** `spinloop cloud deploy` runs with no `--env` flag
- **THEN** it fails saying the environment must be named with `--env <name>`

#### Scenario: One Spinloop deploys to two environments

- **WHEN** the same Spinloop is deployed with `--env dev` and later with
  `--env prod`
- **THEN** two distinct environments are created and registered, each serving
  what the Spinloop describes

#### Scenario: A fleet node deploys under its own name

- **WHEN** `spinloop fleet deploy` targets a `kind: cloud` node named `qwen`
- **THEN** the environment created and registered is named `qwen`, from the
  node's name in the fleet file, and the node's Spinloop is read only for what
  it serves

#### Scenario: Deploying provisions no activity state

- **WHEN** a deploy succeeds
- **THEN** no idle- or activity-tracking parameter is created for the
  environment

#### Scenario: Deploying is not starting

- **WHEN** a deploy succeeds
- **THEN** the environment is configured and registered but no instance is
  running until `spinloop cloud start`
### Requirement: Discovering the control plane

Deploy SHALL discover the control plane from the bootstrap stack's
CloudFormation outputs (a well-known stack name) — the lifecycle Lambda URLs, the
weights bucket, the shared roles, and the region — rather than from any local
file. When the control-plane stack is absent, deploy SHALL fail telling the user to run
`spinloop cloud bootstrap` first, rather than attempting to create an environment.

#### Scenario: The control plane is discovered

- **WHEN** deploy runs against a bootstrapped account
- **THEN** it reads the Lambda URLs, bucket, roles and region from the stack
  outputs

#### Scenario: Not bootstrapped

- **WHEN** deploy runs against an account with no control-plane stack
- **THEN** it fails saying to run `spinloop cloud bootstrap` first, and creates
  nothing

### Requirement: Per-environment allowed ingress

Who may reach an environment's instance SHALL be scoped per environment. Deploy
SHALL take the allowed CIDR from a flag, defaulting to the caller's detected
public address as a `/32`, and apply it to that environment's security-group
rule. It SHALL NOT be an account-wide setting.

#### Scenario: Ingress defaults to the caller's address

- **WHEN** deploy runs without an explicit CIDR
- **THEN** the environment's ingress is the caller's public IP as a `/32`

#### Scenario: Ingress is per environment

- **WHEN** two environments are deployed with different CIDRs
- **THEN** each instance admits only its own CIDR

### Requirement: Registering the environment

Deploy SHALL register the environment in the per-user registry defined by the
Cloud Environments specification — `~/.config/spinloop/clouds/<env>/cloud.json`,
written owner-only — carrying the shared lifecycle Lambda URLs, the region, the
environment's base URL (its Elastic IP), and the environment identifier the
shared Lambdas use to select this environment's instance.

#### Scenario: The environment is registered and resolvable

- **WHEN** `spinloop cloud deploy --env prod` succeeds
- **THEN** `~/.config/spinloop/clouds/prod/cloud.json` exists (owner-only)
  and `spinloop cloud status --env prod` resolves the environment from it

### Requirement: Refuse to overwrite a live environment

Before creating, deploy SHALL detect whether the target environment is already
registered or its instance is currently live. When it is, deploy SHALL show a
prominent warning and SHALL require explicit override consent — an `--overwrite`
flag, which `--yes` alone SHALL NOT satisfy — so a redeploy cannot silently
clobber a running instance. When neither holds, deploy SHALL proceed without a
warning and without requiring `--overwrite`.

#### Scenario: An existing environment warns

- **WHEN** deploy targets an environment that is already registered or live
- **THEN** it warns and does not overwrite unless `--overwrite` is given

#### Scenario: --yes does not imply override

- **WHEN** deploy is run with `--yes` but not `--overwrite` against an existing
  environment
- **THEN** it aborts telling the user to pass `--overwrite`

#### Scenario: A fresh environment needs no override

- **WHEN** deploy targets an environment that is neither registered nor live
- **THEN** it proceeds without an overwrite warning

### Requirement: Deploy accepts an optional spinloop version pin

`spinloop cloud deploy` SHALL accept an optional flag pinning the exact spinloop
release the environment's instances install at boot. When the flag is given,
deploy SHALL record that version in the environment's stored deploy config so
the next fresh boot installs it; when it is absent, deploy SHALL record no pin
and the environment's boots install the latest published release (the boot's
default, defined by the `remote-engine-host` capability). An empty or
whitespace-only value SHALL be treated as if the flag were not given.

#### Scenario: A pin is recorded in the deploy config

- **WHEN** `spinloop cloud deploy` runs with an spinloop version pin
- **THEN** the environment's stored deploy config carries that version, and
  the environment's next fresh boot installs exactly that release

#### Scenario: No pin leaves the boot on its default

- **WHEN** `spinloop cloud deploy` runs without an spinloop version pin
- **THEN** the stored deploy config carries no spinloop version, and the
  environment's boots install the latest published release

#### Scenario: An empty pin value is ignored

- **WHEN** `spinloop cloud deploy` is given an spinloop version pin whose value is
  empty or whitespace only
- **THEN** it is treated as if no pin were given

### Requirement: The deploy plan shows the resolved spinloop version

The plan `spinloop cloud deploy` prints — including under `--dry-run`, before
any AWS work or send — SHALL state the spinloop version the environment's boots
will install: the pinned version when a pin is given, otherwise `latest`. It
SHALL appear alongside the runner and model the plan already prints.

#### Scenario: A pinned deploy prints the pinned version

- **WHEN** `spinloop cloud deploy --dry-run` runs with an spinloop version pin
- **THEN** the printed plan names that pinned version as the spinloop the
  environment will run

#### Scenario: An unpinned deploy prints latest

- **WHEN** `spinloop cloud deploy --dry-run` runs without an spinloop version pin
- **THEN** the printed plan names `latest` as the spinloop the environment will
  run

### Requirement: Deploy accepts an optional instance type

`spinloop cloud deploy` SHALL accept an optional `--instance-type` flag naming
the EC2 instance type the environment's instances launch as. When the flag is
given, deploy SHALL record that type in the environment's stored deploy config
so the environment's next fresh launch uses it; when it is absent, deploy SHALL
record no type and the environment launches as the control plane's default. An
empty or whitespace-only value SHALL be treated as if the flag were not given.
A value that is not shaped like an EC2 instance type SHALL be refused before
anything is sent, naming the value.

The instance type is a property of the environment's deployment, recorded in
the stored deploy config the way the spinloop version pin is — not a property
of a single start, and never derived from the Spinloop.

#### Scenario: A type is recorded in the deploy config

- **WHEN** `spinloop cloud deploy` runs with `--instance-type g6e.2xlarge`
- **THEN** the environment's stored deploy config carries that type, and the
  environment's next fresh launch uses it

#### Scenario: No type leaves the launch on its default

- **WHEN** `spinloop cloud deploy` runs without `--instance-type`
- **THEN** the stored deploy config carries no instance type, and the
  environment's launches use the control plane's default type

#### Scenario: An empty type value is ignored

- **WHEN** `spinloop cloud deploy` is given an `--instance-type` whose value
  is empty or whitespace only
- **THEN** it is treated as if no type were given

#### Scenario: A malformed type is refused before sending

- **WHEN** `spinloop cloud deploy` is given an `--instance-type` that is not
  shaped like an EC2 instance type
- **THEN** the command fails, naming the value, and nothing is sent to the
  control plane

### Requirement: The deploy plan shows the resolved instance type

The plan `spinloop cloud deploy` prints — including under `--dry-run`, before
any AWS work or send — SHALL state the instance type the environment will
launch as: the type named by `--instance-type` when one is given, otherwise a
statement that the environment launches as the control plane's default. It
SHALL appear alongside the runner and model the plan already prints.

#### Scenario: A typed deploy prints the type

- **WHEN** `spinloop cloud deploy --dry-run` runs with `--instance-type
  g6e.2xlarge`
- **THEN** the printed plan names `g6e.2xlarge` as the instance type the
  environment will launch as

#### Scenario: An untyped deploy prints the default

- **WHEN** `spinloop cloud deploy --dry-run` runs without `--instance-type`
- **THEN** the printed plan says the environment launches as the control
  plane's default instance type

### Requirement: Externally provided API key

`spinloop cloud deploy` SHALL accept an externally provided API key as a
reference to an environment variable, and pass it to the control plane to store
as the environment's API key. The value SHALL NOT be written on the command line
or in any file the CLI owns: the flag names a variable, and the CLI resolves it
from the process environment — which the Spinloop's local environment has already
populated — before sending it. A named variable that is set nowhere SHALL fail
the deploy, naming the variable, before anything is sent.

The key SHALL travel in the signed deploy request as a **request-scoped** field
— riding beside the allowed CIDR and the reseed choice — and SHALL NOT be part
of the persisted deploy-config and SHALL NOT appear in any reply. The control
plane SHALL store a supplied key in the environment's existing API-key secret:
creating the secret when it is absent, and setting its value when the secret
already exists.

Deploying with a key is a rotation: it SHALL replace the environment's secret,
instantly invalidating the previous key. Deploying without a key SHALL leave any
existing secret untouched and SHALL NOT regenerate one. A deploy that supplies a
key SHALL report that the key was applied — or, when it replaced an existing
one, that it was rotated — without printing the value, so an operator who
rotates a key out from under a live agent sees it happen rather than discovering
it as 401s.

#### Scenario: A deploy stores a supplied key

- **WHEN** `spinloop cloud deploy` is given a key variable that is set, for an
  environment whose API-key secret does not yet exist
- **THEN** the environment's API-key secret is created holding that value, and
  the report says the key was applied

#### Scenario: A deploy rotates an existing key

- **WHEN** `spinloop cloud deploy` is given a key for an environment that already
  has an API-key secret
- **THEN** the secret is set to the new value, the old key is no longer valid,
  and the report says the key was rotated

#### Scenario: A deploy without a key keeps the existing one

- **WHEN** `spinloop cloud deploy` runs with no key for an environment that
  already has an API-key secret
- **THEN** the secret is left unchanged and no new key is generated

#### Scenario: The key is not persisted or echoed

- **WHEN** a deploy supplies a key
- **THEN** the value is not written to the environment's deploy-config, is not
  in the registered cloud configuration, and is not printed in any reply or in
  the deploy report

#### Scenario: A named variable that is unset fails early

- **WHEN** `spinloop cloud deploy` names a key variable that is set nowhere
- **THEN** the deploy fails naming the variable, before anything is sent
