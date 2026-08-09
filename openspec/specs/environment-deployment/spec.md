# Environment Deployment Specification

## Purpose

Define how `outfit remote deploy` creates a named environment on the
account-level control plane: discovering it, provisioning per-environment resources, registering the environment, and guarding against accidental overwrites.

## Requirements

### Requirement: Deploy creates an environment on the control plane

`outfit remote deploy` SHALL create a named environment on top of the control
plane: it SHALL discover it, then provision the
environment's own Elastic IP, EC2 instance configuration, per-environment API
key, per-environment allowed-ingress rule, and per-environment SSM state
(deploy-config and idle-state), all tagged by the environment name. It SHALL set
what the environment serves from the Outfit and its preset, and SHALL register
the environment so the other `remote` commands can drive it. Deploying SHALL NOT
start the instance.

#### Scenario: Deploying stands up and registers an environment

- **WHEN** `outfit remote deploy` runs for an environment against a bootstrapped
  account
- **THEN** the environment's Elastic IP, instance configuration, API key,
  ingress rule, and SSM state are provisioned, and the environment is registered

#### Scenario: Deploying is not starting

- **WHEN** a deploy succeeds
- **THEN** the environment is configured and registered but no instance is
  running until `outfit remote start`

### Requirement: Discovering the control plane

Deploy SHALL discover the control plane from the bootstrap stack's
CloudFormation outputs (a well-known stack name) — the lifecycle Lambda URLs, the
weights bucket, the shared roles, and the region — rather than from any local
file. When the control-plane stack is absent, deploy SHALL fail telling the user to run
`outfit remote bootstrap` first, rather than attempting to create an environment.

#### Scenario: The control plane is discovered

- **WHEN** deploy runs against a bootstrapped account
- **THEN** it reads the Lambda URLs, bucket, roles and region from the stack
  outputs

#### Scenario: Not bootstrapped

- **WHEN** deploy runs against an account with no control-plane stack
- **THEN** it fails saying to run `outfit remote bootstrap` first, and creates
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
Remote Environments specification — `~/.config/outfit/remotes/<env>/remote.json`,
written owner-only — carrying the shared lifecycle Lambda URLs, the region, the
environment's base URL (its Elastic IP), and the environment identifier the
shared Lambdas use to select this environment's instance.

#### Scenario: The environment is registered and resolvable

- **WHEN** `outfit remote deploy` for environment `prod` succeeds
- **THEN** `~/.config/outfit/remotes/prod/remote.json` exists (owner-only) and an
  Outfit stating `REMOTE prod` resolves to it

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
