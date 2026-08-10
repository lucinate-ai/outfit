## MODIFIED Requirements

### Requirement: Outfit path resolution

Commands that take an Outfit path (`apply`, `unapply`, `serve`, `alias`,
`harness --outfit`, and the `remote` subcommands) SHALL default to `./Outfit`
when no path is given, SHALL accept a directory and use the `Outfit` file
inside it, SHALL accept a registered alias name in place of a path, and SHALL
accept an `http://` or `https://` URL in place of a path, fetched over HTTP
instead of read from local disk. A URL ending in `/` SHALL be treated as a
directory-style reference and have `Outfit` appended, mirroring the local
directory case. When the default `./Outfit` is missing, the error SHALL
suggest passing a path or an alias.

When no path is given, the `OUTFIT_ALIAS` environment variable SHALL be
consulted before falling back to `./Outfit`, so the resolution order is the
argument, then `OUTFIT_ALIAS`, then `./Outfit`. `outfit alias` SHALL be the one
exception and always use its argument or `./Outfit`. Where the default
`./Outfit` is missing and no `OUTFIT_ALIAS` is set, the error SHALL name the
variable alongside the path and alias it already suggests.

#### Scenario: Bare command in a project directory

- **WHEN** the user runs `outfit apply` in a directory holding an `Outfit`
- **THEN** that file is applied

#### Scenario: Directory argument

- **WHEN** the user runs `outfit apply path/to/dir` and the directory holds an
  `Outfit`
- **THEN** `path/to/dir/Outfit` is applied

#### Scenario: A remote subcommand resolves the same way

- **WHEN** the user runs `outfit remote status` in a directory holding an
  `Outfit`
- **THEN** that Outfit is read to find the endpoint's configuration

#### Scenario: The environment names the default Outfit

- **WHEN** `OUTFIT_ALIAS` names a registered alias and the user runs
  `outfit serve` with no argument
- **THEN** that alias's Outfit is served, whether or not the working directory
  holds one

#### Scenario: Nothing to resolve

- **WHEN** the user runs `outfit apply` with no argument, no `OUTFIT_ALIAS` set
  and no `./Outfit` present
- **THEN** the command fails suggesting a path, an alias, or `OUTFIT_ALIAS`

#### Scenario: A URL argument

- **WHEN** the user runs `outfit apply https://example.com/team/Outfit`
- **THEN** the Outfit is fetched from that URL and applied, with no local
  file read

#### Scenario: A directory-style URL argument

- **WHEN** the user runs `outfit apply https://example.com/team/` (a
  trailing `/`)
- **THEN** `https://example.com/team/Outfit` is fetched and applied

#### Scenario: An unreachable URL

- **WHEN** the user runs `outfit apply` against a URL whose host does not
  respond
- **THEN** the command fails with a clear network error naming the URL,
  rather than a filesystem "not found" error
