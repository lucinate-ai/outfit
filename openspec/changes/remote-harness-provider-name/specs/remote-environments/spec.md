## ADDED Requirements

### Requirement: A REMOTE names the harness provider

When an Outfit that has a `REMOTE` is applied, the harness provider SHALL be
keyed on the remote environment name rather than the `PROVIDER` value, and the
default model SHALL read as `<environment>/<model>`. The environment name SHALL
be the bare `REMOTE` value when `REMOTE` is a name, or the `environment` field of
the `remote.json` it names when `REMOTE` is a path; if neither yields a name, the
`PROVIDER` value SHALL remain the provider name. The `PROVIDER` entry SHALL still
supply the engine configuration (its options, API-key environment variable, and
base URL). Unapplying the same Outfit SHALL remove the provider that apply wrote.

#### Scenario: A bare name becomes the provider name

- **WHEN** an Outfit states `PROVIDER llamacpp`, `ALIAS qwen`, and `REMOTE dev-1`,
  and is applied
- **THEN** the harness config holds a provider keyed `dev-1` whose default model
  is `dev-1/qwen`, configured from the `llamacpp` catalogue entry

#### Scenario: A path form takes the name from its config

- **WHEN** an Outfit states `PROVIDER llamacpp` and `REMOTE ./remote.json`, and
  that `remote.json` sets `"environment": "dev-1"`, and is applied
- **THEN** the harness config holds a provider keyed `dev-1`

#### Scenario: A path form without an environment keeps the PROVIDER name

- **WHEN** an Outfit states `PROVIDER llamacpp` and `REMOTE ./remote.json`, and
  that `remote.json` has no `environment` field, and is applied
- **THEN** the harness config holds a provider keyed `llamacpp`, as before

#### Scenario: Unapply removes the environment-named provider

- **WHEN** an Outfit with `REMOTE dev-1` is applied and then unapplied
- **THEN** the provider keyed `dev-1` is removed from the harness config
