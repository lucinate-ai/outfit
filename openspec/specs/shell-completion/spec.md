# Shell Completion Specification

## Purpose

Define tab completion: the `outfit completion <shell>` scripts and the hidden
`outfit __complete` protocol they call, which is the single source of truth for
what completes to what across bash, zsh, and PowerShell.
## Requirements
### Requirement: Completion scripts

`outfit completion <shell>` SHALL print a completion script for `bash`, `zsh`,
or `powershell`, and SHALL fail listing the supported shells when the argument
is missing or unsupported. The scripts SHALL be thin: each hands the words
typed so far to the hidden `outfit __complete` and inserts its results, so the
candidates never differ between shells.

#### Scenario: Unsupported shell

- **WHEN** the user runs `outfit completion fish`
- **THEN** the command fails naming bash, powershell, and zsh as the supported
  shells

### Requirement: Completion protocol

The hidden `outfit __complete <words…>` SHALL print one candidate per line
followed by a final directive line — `:nofile` or `:file` — telling the shell
whether filesystem paths belong alongside the candidates. It SHALL never
return an error and never write to stderr: an unreadable config, an unloadable
catalogue, or nonsense input all mean "no candidates", so a completion attempt
can never spew over the user's prompt.

#### Scenario: Broken config stays quiet

- **WHEN** outfit's own config file is unreadable and completion is attempted
- **THEN** `__complete` exits zero with no candidates and no stderr output

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

