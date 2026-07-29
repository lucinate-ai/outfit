## ADDED Requirements

### Requirement: Per-provider model discovery

The system SHALL be able to fetch the set of models a provider currently serves from that
provider's own HTTP endpoint, using the protocol appropriate to the provider:

- an OpenAI-compatible provider (OpenRouter, vLLM, llama.cpp, generic
  `openai-compatible`) SHALL be queried with `GET {baseURL}/models`, reading model ids
  from the returned `data[].id` list;
- an Ollama provider SHALL be queried with `GET {baseURL}/api/tags`, reading model names
  from the returned `models[].name` list.

The base URL SHALL be resolved with the same precedence a selection uses (`--base-url`,
then `OUTFIT_BASE_URL`, then the provider's catalogue value). When the provider declares an
API key variable and it resolves to a value, that value SHALL be sent as the request's
authorization; a resolved key SHALL NOT be written to disk or logged.

#### Scenario: OpenAI-compatible provider lists its models

- **WHEN** discovery runs for a provider whose endpoint answers `GET {baseURL}/models`
  with a `data` array of objects carrying `id`
- **THEN** those ids are returned as the provider's discovered models

#### Scenario: Ollama lists its local models

- **WHEN** discovery runs for an Ollama provider and `GET {baseURL}/api/tags` returns a
  `models` array of objects carrying `name`
- **THEN** those names are returned as the provider's discovered models

#### Scenario: The resolved key is only sent, never stored

- **WHEN** discovery queries a provider whose key resolves from the environment
- **THEN** the key is sent as a request header and never written to any file or log

### Requirement: Discovery is best-effort and quiet

Model discovery SHALL be best-effort. A network failure, a non-success HTTP status, a
timeout, a missing required key, or an unparseable response SHALL yield an empty model set
rather than an error, and SHALL NOT prevent the surrounding command from succeeding.
Discovery SHALL apply a bounded request timeout so a slow or unreachable endpoint cannot
hang a command.

#### Scenario: Offline discovery does not fail the command

- **WHEN** `outfit list --models <provider>` runs and the provider's endpoint is
  unreachable
- **THEN** the command still prints the provider's plumbing, reports no models were found,
  and exits successfully

#### Scenario: A slow endpoint cannot hang the command

- **WHEN** a provider's endpoint does not respond within the discovery timeout
- **THEN** discovery abandons the request and returns no models

#### Scenario: Completion stays silent on discovery failure

- **WHEN** model completion sources from discovery and the endpoint errors
- **THEN** `__complete` offers no model candidates, exits zero, and writes nothing to
  stderr

### Requirement: Discovery result caching

Within a single process, discovery SHALL cache a provider's result for a short time-to-live
so that repeated lookups (for example, listing then completing) do not re-hit the network.
A cache entry SHALL be keyed by the resolved provider endpoint.

#### Scenario: Repeated lookups reuse the cached result

- **WHEN** discovery for the same provider endpoint is requested twice within the TTL
- **THEN** the second lookup returns the cached models without a second network request

### Requirement: Surfacing discovered models

`outfit list --models <provider>` SHALL print the provider's discovered models beneath its
plumbing. Without `--models`, `outfit list` SHALL behave as before (plumbing only) and
SHALL NOT perform any network request. Shell model completion SHALL offer discovered models
for a provider that supports discovery, scoped to the `--provider` already on the line.

#### Scenario: Listing a provider's live models

- **WHEN** the user runs `outfit list --models openrouter` and discovery succeeds
- **THEN** the provider's currently-served model ids are printed under its entry

#### Scenario: Plain list makes no network call

- **WHEN** the user runs `outfit list` with no `--models` flag
- **THEN** no discovery request is made and only provider plumbing is printed

#### Scenario: Model completion offers discovered ids

- **WHEN** the user completes `outfit add -p openrouter -m <TAB>` and discovery succeeds
- **THEN** the provider's discovered model ids are offered as candidates
