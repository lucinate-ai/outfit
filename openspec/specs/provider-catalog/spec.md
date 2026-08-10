# Provider Catalog Specification

## Purpose

Define the catalogue of model providers that `outfit` can configure: where the
catalogue comes from, what it declares (provider connection plumbing, not
models), how users inspect it (`outfit list`), and how they replace it with
their own (`outfit init-providers`, `--providers`, `OUTFIT_PROVIDERS`).
## Requirements
### Requirement: Catalogue listing

`outfit list` SHALL print every provider in the catalogue in stable
(alphabetical) order, showing for each: its id and description, its API key
environment variable (marked `(required)` when the key is mandatory), and the
harnesses that support it (`opencode`, plus `pi` when the provider has a `pi`
block).

#### Scenario: Listing the built-in catalogue

- **WHEN** the user runs `outfit list`
- **THEN** every embedded provider is printed with its key requirements and
  supported harnesses

### Requirement: Runtime catalogue override

The system SHALL let the user substitute their own catalogue file at runtime,
resolved with the precedence: `--providers` flag, then the `OUTFIT_PROVIDERS`
environment variable, then the embedded catalogue.

#### Scenario: Flag wins over environment variable

- **WHEN** both `--providers ./mine.yaml` and `OUTFIT_PROVIDERS=./other.yaml`
  are set
- **THEN** the catalogue is loaded from `./mine.yaml`

#### Scenario: Unreadable override is an error

- **WHEN** the resolved catalogue path cannot be read or parsed as YAML
- **THEN** the command fails with an error naming the file

### Requirement: Catalogue scaffolding

`outfit init-providers [path]` SHALL write a copy of the embedded catalogue to
`./providers.yaml` (or the given path) as a starting point for customisation,
and SHALL refuse to overwrite an existing file unless `--force`/`-F` is given.
On success it SHALL print how to point `outfit` at the written file.

#### Scenario: Refuses to clobber an existing file

- **WHEN** the user runs `outfit init-providers` and `./providers.yaml` already
  exists
- **THEN** the command fails, telling the user to pass a different path or
  `--force`

#### Scenario: Writing the catalogue out

- **WHEN** the user runs `outfit init-providers custom.yaml` and no such file
  exists
- **THEN** the embedded catalogue is written to `custom.yaml` byte-for-byte

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

### Requirement: Vertex AI providers

The built-in catalogue SHALL include two Google Cloud Vertex AI providers,
keyed by the provider ids opencode resolves natively (so neither declares an npm
package):

- `google-vertex` — Gemini models on Vertex AI.
- `google-vertex-anthropic` — Anthropic Claude models on Vertex AI.

Both SHALL authenticate via ambient Google credentials (Application Default
Credentials) and SHALL NOT inject an API key, mirroring `amazon-bedrock`. Both
SHALL declare a `project` option (required, resolved from `GOOGLE_VERTEX_PROJECT`)
and a `location` option (resolved from `GOOGLE_VERTEX_LOCATION`, defaulting to
`global`). Neither SHALL declare a `pi` block: Vertex is not supported by the Pi
harness.

#### Scenario: Applying a Vertex provider with a project set

- **WHEN** the user applies `google-vertex-anthropic` (or `google-vertex`) with
  `GOOGLE_VERTEX_PROJECT` set to a non-empty value
- **THEN** an opencode provider block is written with the `project` and
  `location` options and no `apiKey`, and the command succeeds

#### Scenario: Applying a Vertex provider without a project fails

- **WHEN** the user applies a Vertex provider with `GOOGLE_VERTEX_PROJECT` unset
  and no `project` override
- **THEN** the command fails with an error naming the `project` option and
  `GOOGLE_VERTEX_PROJECT`

#### Scenario: Vertex is not offered to the Pi harness

- **WHEN** the user targets the Pi harness with a Vertex provider
- **THEN** the command reports the provider as unsupported by the Pi harness

### Requirement: Apple Silicon local provider

The catalogue SHALL include an `omlx` provider describing a local
[oMLX](https://omlx.ai) server: an OpenAI-compatible endpoint defaulting to
`http://localhost:8000/v1`, overridable per-provider with `OMLX_BASE_URL`, and
Pi-capable through the `openai-completions` API.

Like the llama.cpp provider it SHALL be optionally authenticated: a local server
needs no key, while the same provider may name an oMLX server started with
`--api-key` on another machine. An unset key at a local endpoint therefore
yields no `apiKey` option for opencode and the keyless placeholder for Pi, while
a set key — or any non-local endpoint — yields the environment reference the
harness resolves at run time.

Its default port coinciding with another provider's is not a conflict: the
catalogue places no uniqueness requirement on base URLs.

#### Scenario: Local server needs no key

- **WHEN** a selection names `omlx` with the default localhost base URL and no
  API key set
- **THEN** the opencode provider block carries no `apiKey` option, and the Pi
  entry carries the keyless placeholder so its models stay selectable

#### Scenario: Remote server keeps the key reference

- **WHEN** the base URL is overridden to a non-local host
- **THEN** the API key is written as an environment reference, never as the
  resolved secret

