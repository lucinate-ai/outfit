## MODIFIED Requirements

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

## REMOVED Requirements

### Requirement: Selection validation

**Reason**: The requirement admitted a model family as a selector and carried a
scenario about an unknown provider *or family*. Families are removed, so the
selector set and that scenario change; the family-free rules are restated by the
"Validating a selection" requirement below.

**Migration**: Select with `--provider` plus a `--model` and/or `--alias`; there
is no `--model-family`.

### Requirement: Family expansion and default model

**Reason**: Model families are removed. A selection now configures exactly the
one model the user names (`MODEL`/`ALIAS`); there is no family to expand and no
per-family default to pick.

**Migration**: Replace `-f <family>` with an explicit `-m <model>` (or `ALIAS`).
The pinned model becomes the default, exactly as before when both a family and a
`--model` were given. The alias-as-model-key behaviour is preserved by the
"Selection model key" requirement below.

### Requirement: Removing a selection

**Reason**: The requirement expanded a named family to its catalogue models when
removing, and carried a scenario about removing one family's models. Families are
removed, so removal no longer expands anything; the family-free rules are
restated by the "Removing a provider or model" requirement below.

**Migration**: `outfit remove -p <provider>` still removes the whole provider;
name a `--model` or `--alias` to remove exactly that one key.

## ADDED Requirements

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
