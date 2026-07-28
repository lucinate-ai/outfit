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

### Requirement: Completion coverage

Completion SHALL cover the full visible command surface: command names (the
hidden `__complete` excluded), each command's flags, and context-aware values —
provider names from the resolved catalogue (honouring a `--providers` override
already on the line), family and model names scoped to the `--provider` (and
`--model-family`) already typed, harness names, registered alias names where an
Outfit path is accepted, and the supported shells for `completion`. Positional
slots beyond a command's arity SHALL offer nothing.

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
