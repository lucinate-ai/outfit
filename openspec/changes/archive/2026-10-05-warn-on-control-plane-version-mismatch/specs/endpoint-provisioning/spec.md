## ADDED Requirements

### Requirement: Bootstrap records the deploying CLI's version

`spinloop remote bootstrap` SHALL pass the running binary's version to the control plane deploy, so every control plane Lambda reports it in the `x-spinloop-control-plane-version` response header. A binary built without a version SHALL record `dev`.

#### Scenario: A release build bootstraps

- **WHEN** a CLI at version `1.30.0` runs `spinloop remote bootstrap`
- **THEN** the deployed Lambdas report `1.30.0` as the control plane version

#### Scenario: A development build bootstraps

- **WHEN** a CLI built without a version override runs `spinloop remote bootstrap`
- **THEN** the deployed Lambdas report `dev`
