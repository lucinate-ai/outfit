## MODIFIED Requirements

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

#### Scenario: Dangling local alias

- **WHEN** a registered name points at a local file that has been deleted
- **THEN** the command fails suggesting `outfit alias -n <name> <path>` or
  `outfit unalias <name>`

#### Scenario: A URL alias is not probed before use

- **WHEN** a registered name points at a URL and the user runs
  `outfit apply <name>`
- **THEN** resolution proceeds without a preliminary network check; the
  Outfit is fetched directly, and a failure there (unreachable host, non-2xx
  status) is reported as an ordinary fetch error

### Requirement: Listing and removing aliases

`outfit alias --list`/`-l` SHALL print every registered name with the Outfit
it points at, marking entries whose local-path target is missing; a
URL-valued entry SHALL be printed as-is, with no liveness check performed
(listing SHALL NOT make a network call). The same listing SHALL appear in
`outfit show`. `outfit unalias <name>` SHALL take exactly one registered
name and drop it, leaving the aliased Outfit untouched, and SHALL fail on an
unknown name.

#### Scenario: Listing with a missing local target

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
