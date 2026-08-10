## MODIFIED Requirements

### Requirement: Embedded provider definitions

The system SHALL ship with a built-in catalogue of providers, defined in a YAML
file (`providers.yaml`) embedded into the binary at build time. Each provider
entry declares a description and MAY declare a display name, an npm package, an
API key environment variable (with optional required flag, optional flag, and
expected key prefix), static options (such as `baseURL`), options resolved from
environment variables, a list of option keys that are required, a `pi` block
marking the provider as usable by the Pi harness, and a `lucinate` marker
identifying the provider as usable by the lucinate harness. The `lucinate`
marker SHALL be present only on providers that expose an OpenAI-compatible
endpoint; a provider without it SHALL be reported as unsupported by the lucinate
harness. The catalogue SHALL NOT enumerate models: the model a provider serves
is named by the user's selection, not stored in the catalogue.

A provider whose API key is declared optional is one that also works
unauthenticated — the same engine run as a local server and as an
authenticated remote endpoint — so an unset key variable SHALL mean "no key",
not "a key that is missing".

A provider MAY declare `optionsRequired`: a list of option keys that MUST resolve
to a non-empty value (whether from static options or from an environment-variable
mapping) when the provider is applied. When any listed key is missing or empty,
applying the provider SHALL fail with an error naming the option and the
environment variable that supplies it. This guards cloud providers that
authenticate via ambient credentials and so inject no API key, but still require
a caller-supplied value (such as a GCP project) that has no usable default.

#### Scenario: Catalogue loads without external files

- **WHEN** any command that needs the catalogue runs with no `--providers` flag
  and no `OUTFIT_PROVIDERS` environment variable
- **THEN** the embedded catalogue is used, with no file read from disk

#### Scenario: An optional key is injected only when set

- **WHEN** a provider whose API key is optional is applied, with its key
  variable set
- **THEN** the key is injected into the harness config

#### Scenario: An optional key that is unset is not an error

- **WHEN** the same provider is applied with the key variable unset
- **THEN** the configuration is written with no key, and the command succeeds

#### Scenario: A required option that is unset is an error

- **WHEN** a provider that declares a required option is applied and neither the
  option's static value nor its environment-variable source resolves to a
  non-empty value
- **THEN** the command fails with an error naming the option and its
  environment variable, and no config is written

#### Scenario: A required option resolved from the environment succeeds

- **WHEN** a provider that declares a required option is applied with that
  option's environment variable set to a non-empty value
- **THEN** the option is injected into the harness config and the command
  succeeds

#### Scenario: An OpenAI-compatible provider is lucinate-capable

- **WHEN** a provider that declares the `lucinate` marker is applied under the
  lucinate harness
- **THEN** the provider is accepted and its connection is written

#### Scenario: A provider without the marker is unsupported by lucinate

- **WHEN** a provider that does not declare the `lucinate` marker is applied
  under the lucinate harness
- **THEN** the command fails reporting the provider unsupported by lucinate
