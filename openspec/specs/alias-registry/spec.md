# Alias Registry Specification

## Purpose

Define the alias registry: naming an Outfit once with `outfit alias` so the
name stands in for its path in every command that takes one (`apply`,
`unapply`, `serve`, `harness`), and the rules that keep aliases from ever
changing what an already-working command does.

## Requirements

### Requirement: Registering an alias

`outfit alias [path]` SHALL register the Outfit at `path` (default `./Outfit`)
under a name: the Outfit's own `ALIAS` instruction by default, or the
`--name`/`-n` flag's value. When the file has no `ALIAS` and no name is given,
the command SHALL fail rather than invent a name. The Outfit SHALL be parsed at
registration time so a broken file is caught immediately. The registry SHALL
store the absolute path of the Outfit file itself (never its directory), so a
relative `PRESET` still resolves against the Outfit's own directory later.
Re-registering a name SHALL fail unless `--force`/`-F` is given or the path is
unchanged.

#### Scenario: Name borrowed from the Outfit

- **WHEN** the user runs `outfit alias` beside an Outfit containing
  `ALIAS qwen3.6-27b`
- **THEN** the name `qwen3.6-27b` is registered pointing at that file's
  absolute path

#### Scenario: No name to borrow

- **WHEN** the Outfit has no `ALIAS` and no `--name` is given
- **THEN** the command fails asking for `--name/-n`

#### Scenario: Re-pointing needs force

- **WHEN** a registered name is registered again for a different path without
  `--force`
- **THEN** the command fails naming the existing target

### Requirement: Alias name validity

An alias name SHALL be a plain name usable wherever a path goes: non-empty, no
path separators, not `.` or `..`, no leading `-`, and no whitespace.

#### Scenario: Path-shaped name rejected

- **WHEN** the user runs `outfit alias -n ./qwen`
- **THEN** registration fails explaining the name may not contain a path
  separator

### Requirement: Alias resolution

Wherever an Outfit path is accepted, an argument SHALL be looked up in the
registry only when it is name-shaped — a path-shaped argument never causes a
registry read at all, so commands keep working when outfit's own config is
absent or unreadable. A path on disk SHALL beat a registered name of the same
spelling, and the shadowing SHALL be reported, not silent. A registered name
whose target file no longer exists SHALL fail with instructions to re-point or
drop the alias. When an alias decides the path, the command SHALL say so.

#### Scenario: Alias used from anywhere

- **WHEN** the user runs `outfit apply qwen3.6-27b` in an unrelated directory
- **THEN** the registered Outfit is applied and the output names the alias and
  the resolved path

#### Scenario: Path beats alias

- **WHEN** an argument names both a file on disk and a registered alias
- **THEN** the file wins and a note reports that the path was used

#### Scenario: Dangling alias

- **WHEN** a registered name points at a file that has been deleted
- **THEN** the command fails suggesting `outfit alias -n <name> <path>` or
  `outfit unalias <name>`

### Requirement: Listing and removing aliases

`outfit alias --list`/`-l` SHALL print every registered name with the Outfit it
points at, marking entries whose file is missing; the same listing SHALL appear
in `outfit show`. `outfit unalias <name>` SHALL take exactly one registered
name and drop it, leaving the Outfit file untouched, and SHALL fail on an
unknown name.

#### Scenario: Listing with a missing target

- **WHEN** a registered Outfit has been deleted and the user runs
  `outfit alias --list`
- **THEN** the entry is shown with a `(missing)` marker

#### Scenario: Unalias leaves the file alone

- **WHEN** the user runs `outfit unalias qwen3.6-27b`
- **THEN** the name is removed from the registry and the Outfit file still
  exists

### Requirement: Registry storage

The registry SHALL live in outfit's own config file
(`${XDG_CONFIG_HOME:-~/.config}/outfit/config.json`), never in an Outfit, so
Outfit files stay portable and committable. Every write SHALL be a
read-modify-write of the whole document, so registering an alias cannot clobber
the stored harness preference (or any key a newer version wrote), and the file
SHALL be written with owner-only permissions.

#### Scenario: Alias write preserves the harness preference

- **WHEN** a default harness is stored and the user registers an alias
- **THEN** the stored harness preference survives unchanged
