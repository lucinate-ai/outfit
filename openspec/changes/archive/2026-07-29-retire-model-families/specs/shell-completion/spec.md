## REMOVED Requirements

### Requirement: Completion coverage

**Reason**: The requirement listed family names among the completable values and
carried a scenario scoping model completion to a typed family. Families are
removed, so that value and scenario go; the family-free coverage rules are
restated by the "Completion surface coverage" requirement below.

**Migration**: Completion offers provider, harness, alias, and shell values;
`--model`/`-m` has no static candidate source now (a follow-up change sources it
live from each provider).

## ADDED Requirements

### Requirement: Completion surface coverage

Completion SHALL cover the full visible command surface: command names (the
hidden `__complete` excluded), each command's flags, its subcommands where it
has them, and context-aware values — provider names from the resolved catalogue
(honouring a `--providers` override already on the line), harness names,
registered alias names where an Outfit path is accepted, and the supported
shells for `completion`. The catalogue no longer enumerates models, so
`--model`/`-m` has no static candidate source; it SHALL still consume its value
so a following flag completes normally. For a command with subcommands, the
first positional slot SHALL offer those subcommands and any later slot SHALL fall
through to what the command otherwise accepts. Positional slots beyond a
command's arity SHALL offer nothing.

#### Scenario: Unalias offers exactly the registered names

- **WHEN** the user completes `outfit unalias <TAB>`
- **THEN** the registered alias names are offered with no file paths

#### Scenario: New commands cannot be forgotten

- **WHEN** a new subcommand is added to the CLI's dispatch
- **THEN** the completion surface must list it too (enforced by a test that
  scans the dispatch)

#### Scenario: A nested command offers its subcommands

- **WHEN** the user completes `outfit remote <TAB>`
- **THEN** its subcommands are offered, with no file paths

#### Scenario: After a subcommand, the Outfit slot completes

- **WHEN** the user completes `outfit remote deploy <TAB>`
- **THEN** registered alias names and paths are offered

#### Scenario: Providers complete from the catalogue

- **WHEN** the user completes `outfit add -p <TAB>`
- **THEN** the catalogue's provider names are offered

#### Scenario: The model flag has no static candidates but consumes its value

- **WHEN** the user completes `outfit add -p openrouter -m <TAB>`
- **THEN** no model candidates are offered and no error occurs
- **AND** a flag typed after `--model <value>` still completes normally
