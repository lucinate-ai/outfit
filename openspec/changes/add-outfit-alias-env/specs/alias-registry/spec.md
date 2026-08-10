## ADDED Requirements

### Requirement: Naming an alias in the environment

The `OUTFIT_ALIAS` environment variable SHALL name a registered alias, and that
alias SHALL be used by any command that takes an Outfit path but was given
none. It SHALL rank below an explicit argument and above the `./Outfit`
default, so a command that names a path or an alias is unaffected.

The value SHALL be treated as a registry name only, never as a path: it SHALL
be looked up in the registry directly, and a file of the same name in the
working directory SHALL NOT shadow it. This is the opposite of the rule for an
argument, and deliberate — an argument is usually a path, whereas the variable
can only have been set to name an alias.

An empty or unset `OUTFIT_ALIAS` SHALL have no effect. A value that is not
name-shaped, is not registered, or points at a file that no longer exists SHALL
fail naming `OUTFIT_ALIAS` as the source, so the variable is never mistaken for
a missing file in the current directory.

When `OUTFIT_ALIAS` decides the Outfit, the command SHALL say so on stderr,
naming the variable, the alias and the resolved path.

#### Scenario: The variable supplies the Outfit

- **WHEN** `OUTFIT_ALIAS=qwen3.6-27b` is set and the user runs `outfit apply`
  in a directory with no `Outfit`
- **THEN** the registered Outfit is applied and a note on stderr names the
  variable, the alias and the resolved path

#### Scenario: An argument wins

- **WHEN** `OUTFIT_ALIAS=qwen3.6-27b` is set and the user runs
  `outfit apply path/to/Outfit`
- **THEN** the argument's Outfit is applied and the variable is ignored

#### Scenario: The variable is not shadowed by a file

- **WHEN** `OUTFIT_ALIAS=qwen3.6-27b` is set and a file named `qwen3.6-27b`
  exists in the working directory
- **THEN** the registered Outfit is used and no shadowing note is printed

#### Scenario: Unregistered value

- **WHEN** `OUTFIT_ALIAS` names something that is not in the registry
- **THEN** the command fails saying `OUTFIT_ALIAS` names an unregistered alias
  and pointing at `outfit alias --list`

#### Scenario: Dangling value

- **WHEN** `OUTFIT_ALIAS` names a registered alias whose Outfit has been
  deleted
- **THEN** the command fails naming the variable and suggesting the alias be
  re-pointed or dropped

### Requirement: Commands the environment alias does not reach

`OUTFIT_ALIAS` SHALL change which Outfit is the default, never whether a
command acts on one. A command that applies no Outfit when given no argument
SHALL keep applying none.

`outfit alias [path]` SHALL ignore `OUTFIT_ALIAS` entirely: its bare form means
"register the Outfit in this directory", so honouring the variable there could
only re-register what is already registered.

#### Scenario: A bare harness launch stays bare

- **WHEN** `OUTFIT_ALIAS=qwen3.6-27b` is set and the user runs `outfit harness`
  with no Outfit argument and no `--outfit`/`-O`
- **THEN** the harness launches with its existing configuration and nothing is
  applied

#### Scenario: The default Outfit flag follows the variable

- **WHEN** `OUTFIT_ALIAS=qwen3.6-27b` is set and the user runs
  `outfit harness -O`, which asks for the default Outfit
- **THEN** the registered Outfit is applied before the harness launches

#### Scenario: Registering ignores the variable

- **WHEN** `OUTFIT_ALIAS=qwen3.6-27b` is set and the user runs `outfit alias`
  beside a different `Outfit`
- **THEN** the `Outfit` in the working directory is the one registered
