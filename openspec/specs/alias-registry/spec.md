# Alias Registry Specification

## Purpose

Define the alias registry: naming an Outfit once with `outfit alias` so the
name stands in for its path in every command that takes one (`apply`,
`unapply`, `serve`, `harness`, and the `remote` control commands `deploy`,
`start`, `stop`, `status`, `stats`), and the rules that keep aliases from ever
changing what an already-working command does.

## Requirements

### Requirement: Registering an alias

`outfit alias [path]` SHALL register the Outfit at `path` (default `./Outfit`)
under a name: the Outfit's own `ALIAS` instruction by default, or the
`--name`/`-n` flag's value. `path` MAY be an `http://` or `https://` URL in
place of a local path or directory. When the file has no `ALIAS` and no name
is given, the command SHALL fail rather than invent a name. The Outfit SHALL
be parsed at registration time (fetched, for a URL) so a broken file is
caught immediately. The registry SHALL store the absolute path of the Outfit
file itself (never its directory) for a local target, or the URL verbatim for
a remote one, so a relative `PRESET` still resolves against the Outfit's own
source later. Re-registering a name SHALL fail unless `--force`/`-F` is given
or the target is unchanged.

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

#### Scenario: Registering a URL

- **WHEN** the user runs `outfit alias -n team-default
  https://example.com/team/Outfit`
- **THEN** the Outfit is fetched and parsed to validate it, and the name
  `team-default` is registered pointing at that URL verbatim

### Requirement: Alias name validity

An alias name SHALL be a plain name usable wherever a path goes: non-empty, no
path separators, not `.` or `..`, no leading `-`, and no whitespace.

#### Scenario: Path-shaped name rejected

- **WHEN** the user runs `outfit alias -n ./qwen`
- **THEN** registration fails explaining the name may not contain a path
  separator

### Requirement: Alias resolution

Wherever an Outfit path is accepted, an argument SHALL be looked up in the
registry only when it is name-shaped — a path-shaped or URL-shaped argument
never causes a registry read at all, so commands keep working when outfit's
own config is absent or unreadable. A path on disk SHALL beat a registered
name of the same spelling, and the shadowing SHALL be reported, not silent. A
registered name whose target is a local file that no longer exists SHALL fail
with instructions to re-point or drop the alias; a registered name whose
target is a URL SHALL NOT be probed for liveness during resolution — a
network failure surfaces normally, at the point the target is actually
fetched. When an alias decides the path, the command SHALL say so.

That report SHALL go to stderr. It is prose about how the command was resolved
rather than the command's result, and the same resolution serves
`outfit remote env`, whose stdout is meant to be evaluated by a shell.

#### Scenario: Alias used from anywhere

- **WHEN** the user runs `outfit apply qwen3.6-27b` in an unrelated directory
- **THEN** the registered Outfit is applied and the output names the alias and
  the resolved path

#### Scenario: The alias note stays out of stdout

- **WHEN** an alias resolves the Outfit for a command whose stdout is consumed
  by a shell, such as `outfit remote env`
- **THEN** the note naming the alias is written to stderr and stdout carries
  only the command's own output

#### Scenario: Path beats alias

- **WHEN** an argument names both a file on disk and a registered alias
- **THEN** the file wins and a note reports that the path was used

#### Scenario: Dangling alias

- **WHEN** a registered name points at a local file that has been deleted
- **THEN** the command fails suggesting `outfit alias -n <name> <path>` or
  `outfit unalias <name>`

#### Scenario: A URL alias is not probed before use

- **WHEN** a registered name points at a URL and the user runs
  `outfit apply <name>`
- **THEN** resolution proceeds without a preliminary network check; the
  Outfit is fetched directly, and a failure there (unreachable host, non-2xx
  status) is reported as an ordinary fetch error

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

A command that consults an Outfit only when there is one to consult — the
`remote` subcommands, which otherwise use the per-user endpoint config, and
`daemon`, which otherwise starts idle — SHALL count `OUTFIT_ALIAS` as naming
one. A set variable SHALL NOT be passed over in favour of that fallback.

#### Scenario: The variable counts as having an Outfit

- **WHEN** `OUTFIT_ALIAS` names an Outfit carrying a `REMOTE` instruction and
  the user runs `outfit remote status` in a directory with no `Outfit`
- **THEN** that Outfit's endpoint is used, rather than the per-user default
  config

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

### Requirement: Listing and removing aliases

`outfit alias --list`/`-l` SHALL print every registered name with the Outfit
it points at, marking entries whose local-path target is missing; a
URL-valued entry SHALL be printed as-is, with no liveness check performed
(listing SHALL NOT make a network call). The same listing SHALL appear in
`outfit show`. `outfit unalias <name>` SHALL take exactly one registered
name and drop it, leaving the aliased Outfit untouched, and SHALL fail on an
unknown name.

#### Scenario: Listing with a missing target

- **WHEN** a registered Outfit has been deleted and the user runs
  `outfit alias --list`
- **THEN** the entry is shown with a `(missing)` marker

#### Scenario: Listing a URL target

- **WHEN** a registered alias points at a URL and the user runs
  `outfit alias --list`
- **THEN** the URL is shown as registered, with no `(missing)` marker either
  way and no network request made

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
