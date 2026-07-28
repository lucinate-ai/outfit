# opencode Integration Specification

## Purpose

Define how `outfit` reads and writes opencode's global config: which file it
targets, the in-place JSONC merge that preserves everything the user already
has, default-model handling, secret handling, and the state read back for
`show` and `export`.

## Requirements

### Requirement: Config file resolution

The opencode adapter SHALL target the config under
`${XDG_CONFIG_HOME:-~/.config}/opencode`, preferring an existing
`opencode.json` or `opencode.jsonc` (in that order) so a competing file is
never left alongside one the user already has, and falling back to creating
`opencode.json`.

#### Scenario: Existing JSONC file is reused

- **WHEN** the user has only `opencode.jsonc` and runs `outfit add`
- **THEN** that file is updated and no `opencode.json` is created

### Requirement: In-place JSONC merge

Writes SHALL parse the existing config as JSONC — tolerating comments and
trailing commas — and apply edits so that comments and formatting outside the
managed provider block are preserved. The parent `provider` object SHALL only
be created when absent, so sibling providers are never clobbered. The managed
provider block SHALL be deep-merged over any existing block of the same id, so
user-added extras inside it survive. A `$schema` reference SHALL be added when
none exists. Applying the same selection twice SHALL be idempotent.

#### Scenario: Comments survive

- **WHEN** the config holds comments and an unrelated provider, and the user
  runs `outfit add`
- **THEN** after the write the comments and the unrelated provider are intact

#### Scenario: User extras inside the managed block survive

- **WHEN** the user has hand-added a setting inside the managed provider's
  block and re-runs the same `outfit add`
- **THEN** the setting is still there afterwards

#### Scenario: Idempotent apply

- **WHEN** the same selection is applied twice
- **THEN** the second write leaves the file byte-for-byte unchanged

### Requirement: Default model handling

Applying a selection with a chosen model SHALL set opencode's top-level `model`
to `<providerId>/<modelKey>`. Removing SHALL clear the top-level `model` when
it pointed at a removed model or a removed provider's models, and SHALL leave
it alone otherwise.

#### Scenario: Default cleared on removal

- **WHEN** the default model points at a model being removed
- **THEN** the top-level `model` key is removed too

#### Scenario: Unrelated default survives removal

- **WHEN** the default model points at a provider not being touched
- **THEN** the top-level `model` key is unchanged

### Requirement: Secrets and file permissions

The resolved API key SHALL be embedded in the provider block's options (a
global config cannot rely on a project-local `.env`), and the config SHALL be
written with owner-only (`0600`) permissions — enforced even when the file
already existed with looser ones.

#### Scenario: Permissions enforced on existing file

- **WHEN** the config file exists with permissive mode and `outfit add` writes
  it
- **THEN** the file's mode is owner-only afterwards

### Requirement: State read-back

The adapter SHALL read back each configured provider — its model keys
(sorted), `options.baseURL`, and each model's `limit.context`/`limit.output`
when set — plus the top-level default model. This state SHALL be sufficient
for `outfit show` and `outfit export` to reconstruct what is configured.

#### Scenario: Export sees what add wrote

- **WHEN** a selection with context and output limits has been applied
- **THEN** the read-back state reports those limits for each written model
