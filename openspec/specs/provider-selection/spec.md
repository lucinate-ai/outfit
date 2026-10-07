# Provider Selection Specification

## Purpose

Define how a single provider selection — a provider plus a model and/or alias,
context/output limits, and base URL — is validated and applied to (or removed
from) the active harness's config. This is the shared core behind the `harness`
config commands (`add`, `remove`) and the Spinloop-file commands (`apply`,
`unapply`), which route through the same logic.
## Requirements
### Requirement: Context and output limits

The system SHALL parse human-friendly token counts for the context window
(`--context`/`-c`) and max output tokens (`--output`/`-o`): surrounding
whitespace, commas, and underscores are ignored; decimal suffixes `k`/`m`/`g`
(and `b`)/`t` are honoured case-insensitively; a fractional value may precede a
suffix; a trailing "tokens"/"tok" word is tolerated. An output limit SHALL
require a context window, SHALL NOT exceed it, and SHALL default to a quarter
of the context (minimum 1) when unset. When a context is set, the resolved
context and output limits SHALL be applied to every model the selection
configures.

#### Scenario: Lenient size parsing

- **WHEN** the user passes `-c "128 K tokens"`, `-c 128k`, `-c 128,000`, or
  `-c 0.128m`
- **THEN** each parses to a context window of 128000 tokens

#### Scenario: Output without context

- **WHEN** the user passes `--output 32k` with no `--context`
- **THEN** the command fails explaining an output limit needs a context window

#### Scenario: Output exceeding context

- **WHEN** the user passes `-c 128k -o 256k`
- **THEN** the command fails because the output limit cannot exceed the context
  window

#### Scenario: Default output

- **WHEN** the user passes `-c 128k` and no output limit
- **THEN** the output limit defaults to 32000 tokens

### Requirement: API key resolution

When a provider declares an API key environment variable, the system SHALL
resolve its value from the process environment first, and only when the variable
is unset there SHALL it fall back to a `.env` file **beside the Spinloop being
applied**. An exported variable therefore always wins; the `.env` only fills a
gap. When no Spinloop is involved — a selection made entirely from flags — the
working directory SHALL be used in place of the Spinloop's directory, so a
project's own `.env` is still found. A missing key SHALL be an error when the
provider marks it required. A resolved key that does not start with the
provider's declared prefix SHALL be rejected. Secrets SHALL never be written into
a Spinloop file.

This precedence — process environment over `.env` — matches the rule the
`cloud` commands follow, so the whole tool resolves local variables the same
way.

The file sits beside the Spinloop for the same reason `PRESET` does: a
Spinloop and the key it needs belong to the same project and travel together,
and a location relative to the tool cannot be resolved by an installed binary.

#### Scenario: The key travels with the Spinloop

- **WHEN** a Spinloop is applied and a `.env` beside it sets the provider's key
  variable, which is unset in the environment
- **THEN** that value is used

#### Scenario: An exported key wins over the .env

- **WHEN** a Spinloop is applied and the provider's key variable is set both in
  the process environment and in the `.env` beside the Spinloop
- **THEN** the process environment's value is used and the `.env` value is
  ignored

#### Scenario: A command with no Spinloop reads the working directory

- **WHEN** a selection made entirely from flags is applied and a `.env` in the
  working directory sets the provider's key variable, unset in the environment
- **THEN** that value is used

#### Scenario: Another project's .env is not consulted

- **WHEN** a Spinloop is applied and the key variable is set only in a `.env`
  belonging to a different directory
- **THEN** the key does not resolve from that file

#### Scenario: Required key missing

- **WHEN** the user adds a provider whose key is required and the variable is
  set in neither `.env` nor the environment
- **THEN** the command fails naming the variable to set

#### Scenario: Malformed key

- **WHEN** the resolved key does not start with the provider's declared prefix
- **THEN** the command fails saying the key does not look right

### Requirement: Base URL resolution

The system SHALL let the user override any provider's API base URL, resolved
with the precedence: the explicit override, then the `SPINLOOP_BASE_URL`
environment variable, then the catalogue's per-provider values. The explicit
override is the `--base-url`/`-u` flag, or the `BASEURL` of the Spinloop being
applied.

When `apply` is given an `--env <name>` flag and the Spinloop states no
`BASEURL` of its own, applying it SHALL take the base URL from the named
environment's registered configuration, so the endpoint's address stays with
the deployment that owns it rather than in the hand-written Spinloop. The
system SHALL report that it did so. When the Spinloop states a `BASEURL` of its
own, that value SHALL be the applied base URL and the environment's own base
URL SHALL NOT be read; the environment still supplies the provider's name and,
for a launch, its key.

#### Scenario: Flag beats environment and catalogue

- **WHEN** `--base-url https://gateway/v1` is given and `SPINLOOP_BASE_URL` is
  also set
- **THEN** the configured base URL is `https://gateway/v1`

#### Scenario: Base URL from the named environment

- **WHEN** a Spinloop with no `BASEURL` is applied with `--env dev-1`, and that
  environment's registered configuration names a base URL
- **THEN** the provider is configured with the base URL from that
  configuration, and the output says where it came from

#### Scenario: A Spinloop's own BASEURL wins

- **WHEN** a Spinloop states a `BASEURL` and is applied with `--env dev-1`
  whose configuration names a different base URL
- **THEN** the provider is configured with the Spinloop's `BASEURL`, and the
  provider is still keyed on the environment `dev-1`

### Requirement: Apply feedback

After applying a selection the system SHALL report the config file written, the
provider configured, the default model when one was set, the resolved context
and output limits when set, and any harness-specific notes (key injection, base
URL, next steps).

#### Scenario: Successful add

- **WHEN** `spinloop harness add -p openrouter -m deepseek/deepseek-v4-pro -c 128k`
  succeeds
- **THEN** the output names the config path, the provider, the default model,
  and the 128000/32000 token limits

### Requirement: Validating a selection

A selection SHALL name a provider (`--provider`/`-p`), and applying one SHALL
additionally require at least one of a model or an alias. The named provider
MUST exist in the resolved catalogue.

#### Scenario: Missing provider

- **WHEN** the user runs `spinloop harness add` without `--provider`
- **THEN** the command fails, pointing at `spinloop provider list`

#### Scenario: Provider alone is not enough to apply

- **WHEN** the user runs `spinloop harness add -p openrouter` with no model or alias
- **THEN** the command fails explaining a selection needs a model or an alias

#### Scenario: Unknown provider

- **WHEN** the selection names a provider not in the catalogue
- **THEN** the command fails naming the unknown id and pointing at
  `spinloop provider list`

### Requirement: Selection model key

The model key a harness stores a selection under SHALL be the alias when one is
given, otherwise the provider-native model id. An explicit `--model` SHALL be
configured and SHALL become the selection's default model.

#### Scenario: Model becomes the default

- **WHEN** the user runs `spinloop harness add -p openrouter -m deepseek/deepseek-v4-pro`
- **THEN** `deepseek/deepseek-v4-pro` is configured and becomes the default
  model

#### Scenario: Alias keys the model

- **WHEN** a selection includes `ALIAS qwen` for model `org/model:quant`
- **THEN** the harness stores the model under the key `qwen`

### Requirement: Removing a provider or model

`spinloop harness remove` (and `unapply`) SHALL remove the whole provider when no model or
alias is given, and otherwise SHALL remove exactly the named model — an alias or
model id each name one key. The command SHALL report how many entries were
removed, and SHALL report "nothing to remove" (not an error) when none matched.

#### Scenario: Removing a whole provider

- **WHEN** the user runs `spinloop harness remove -p ollama`
- **THEN** the provider block is removed from the harness config

#### Scenario: Removing one model

- **WHEN** the user runs `spinloop harness remove -p openrouter -m deepseek/deepseek-v4-pro`
- **THEN** only that model is removed and the provider's other models survive

#### Scenario: Nothing matched

- **WHEN** the removal matches nothing in the harness config
- **THEN** the command reports there was nothing to remove and exits
  successfully

