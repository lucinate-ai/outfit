# Provider Selection Specification

## Purpose

Define how a single provider selection — a provider plus a model and/or alias,
context/output limits, and base URL — is validated and applied to (or removed
from) the active harness's config. This is the shared core behind `outfit
add`/`outfit remove` and the Outfit-file commands `apply`/`unapply`, which route
through the same logic.
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
resolve its value from a `.env` file **beside the Outfit being applied** first,
then from the process environment. When no Outfit is involved — a selection made
entirely from flags — the working directory SHALL be used in its place, so a
project's own `.env` is still found. A missing key SHALL be an error when the
provider marks it required. A resolved key that does not start with the
provider's declared prefix SHALL be rejected.
Secrets SHALL never be written into an Outfit file.

The file sits beside the Outfit for the same reason `PRESET` and `REMOTE` do:
an Outfit and the key it needs belong to the same project and travel together,
and a location relative to the tool cannot be resolved by an installed binary.

#### Scenario: The key travels with the Outfit

- **WHEN** an Outfit is applied and a `.env` beside it sets the provider's key
  variable, which is unset in the environment
- **THEN** that value is used

#### Scenario: A command with no Outfit reads the working directory

- **WHEN** a selection made entirely from flags is applied and a `.env` in the
  working directory sets the provider's key variable
- **THEN** that value is used

#### Scenario: Another project's .env is not consulted

- **WHEN** an Outfit is applied and the key variable is set only in a `.env`
  belonging to a different directory
- **THEN** the key does not resolve from that file

#### Scenario: Required key missing

- **WHEN** the user adds a provider whose key is required and the variable is
  set in neither `.env` nor the environment
- **THEN** the command fails naming the variable to set

#### Scenario: Malformed key

- **WHEN** the resolved key does not start with the provider's declared prefix
- **THEN** the command fails saying the key does not look right

### Requirement: Base URL precedence

The system SHALL let the user override any provider's API base URL, resolved
with the precedence: `--base-url`/`-u` flag, then the `OUTFIT_BASE_URL`
environment variable, then the catalogue's per-provider values.

#### Scenario: Flag beats environment and catalogue

- **WHEN** `--base-url https://gateway/v1` is given and `OUTFIT_BASE_URL` is
  also set
- **THEN** the configured base URL is `https://gateway/v1`

### Requirement: Apply feedback

After applying a selection the system SHALL report the config file written, the
provider configured, the default model when one was set, the resolved context
and output limits when set, and any harness-specific notes (key injection, base
URL, next steps).

#### Scenario: Successful add

- **WHEN** `outfit add -p openrouter -m deepseek/deepseek-v4-pro -c 128k`
  succeeds
- **THEN** the output names the config path, the provider, the default model,
  and the 128000/32000 token limits

### Requirement: Validating a selection

A selection SHALL name a provider (`--provider`/`-p`), and applying one SHALL
additionally require at least one of a model or an alias. The named provider
MUST exist in the resolved catalogue.

#### Scenario: Missing provider

- **WHEN** the user runs `outfit add` without `--provider`
- **THEN** the command fails, pointing at `outfit list`

#### Scenario: Provider alone is not enough to apply

- **WHEN** the user runs `outfit add -p openrouter` with no model or alias
- **THEN** the command fails explaining a selection needs a model or an alias

#### Scenario: Unknown provider

- **WHEN** the selection names a provider not in the catalogue
- **THEN** the command fails naming the unknown id and pointing at
  `outfit list`

### Requirement: Selection model key

The model key a harness stores a selection under SHALL be the alias when one is
given, otherwise the provider-native model id. An explicit `--model` SHALL be
configured and SHALL become the selection's default model.

#### Scenario: Model becomes the default

- **WHEN** the user runs `outfit add -p openrouter -m deepseek/deepseek-v4-pro`
- **THEN** `deepseek/deepseek-v4-pro` is configured and becomes the default
  model

#### Scenario: Alias keys the model

- **WHEN** a selection includes `ALIAS qwen` for model `org/model:quant`
- **THEN** the harness stores the model under the key `qwen`

### Requirement: Removing a provider or model

`outfit remove` (and `unapply`) SHALL remove the whole provider when no model or
alias is given, and otherwise SHALL remove exactly the named model — an alias or
model id each name one key. The command SHALL report how many entries were
removed, and SHALL report "nothing to remove" (not an error) when none matched.

#### Scenario: Removing a whole provider

- **WHEN** the user runs `outfit remove -p ollama`
- **THEN** the provider block is removed from the harness config

#### Scenario: Removing one model

- **WHEN** the user runs `outfit remove -p openrouter -m deepseek/deepseek-v4-pro`
- **THEN** only that model is removed and the provider's other models survive

#### Scenario: Nothing matched

- **WHEN** the removal matches nothing in the harness config
- **THEN** the command reports there was nothing to remove and exits
  successfully

