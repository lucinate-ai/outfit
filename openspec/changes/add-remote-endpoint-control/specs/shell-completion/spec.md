## MODIFIED Requirements

### Requirement: Completion coverage

Completion SHALL cover the full visible command surface: command names (the
hidden `__complete` excluded), each command's flags, its subcommands where it
has them, and context-aware values — provider names from the resolved catalogue
(honouring a `--providers` override already on the line), family and model
names scoped to the `--provider` (and `--model-family`) already typed, harness
names, registered alias names where an Outfit path is accepted, and the
supported shells for `completion`. For a command with subcommands, the first
positional slot SHALL offer those subcommands and any later slot SHALL fall
through to what the command otherwise accepts. Positional slots beyond a
command's arity SHALL offer nothing.

#### Scenario: Families scoped to the typed provider

- **WHEN** the user completes `outfit add -p openrouter -f <TAB>`
- **THEN** only the families of `openrouter` are offered

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
