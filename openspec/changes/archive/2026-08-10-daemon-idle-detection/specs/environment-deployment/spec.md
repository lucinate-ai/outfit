## MODIFIED Requirements

### Requirement: Deploy creates an environment on the control plane

`outfit remote deploy` SHALL create a named environment on top of the control
plane: it SHALL discover it, then provision the
environment's own Elastic IP, EC2 instance configuration, per-environment API
key, per-environment allowed-ingress rule, and per-environment SSM state
(the deploy-config), all tagged by the environment name. It SHALL set
what the environment serves from the Outfit and its preset, and SHALL register
the environment so the other `remote` commands can drive it. Deploying SHALL NOT
start the instance.

Deploy SHALL NOT provision any activity-tracking state. Engine activity is
recorded on the instance by its daemon, so there is nothing for the control
plane to seed, read or write.

#### Scenario: Deploying stands up and registers an environment

- **WHEN** `outfit remote deploy` runs for an environment against a bootstrapped
  account
- **THEN** the environment's Elastic IP, instance configuration, API key,
  ingress rule, and SSM state are provisioned, and the environment is registered

#### Scenario: Deploying provisions no activity state

- **WHEN** a deploy succeeds
- **THEN** no idle- or activity-tracking parameter is created for the
  environment

#### Scenario: Deploying is not starting

- **WHEN** a deploy succeeds
- **THEN** the environment is configured and registered but no instance is
  running until `outfit remote start`
