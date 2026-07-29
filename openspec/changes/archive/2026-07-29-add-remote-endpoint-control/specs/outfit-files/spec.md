## MODIFIED Requirements

### Requirement: Outfit file format

An Outfit SHALL be a flat, line-oriented text file of `KEYWORD value`
instructions. The keywords are `PROVIDER`, `FAMILY`, `MODEL`, `ALIAS`,
`CONTEXT`, `OUTPUT`, `BASEURL` (also accepted as `BASE-URL`, `BASE_URL`, or
`URL`), `PRESET`, and `REMOTE`. Keywords SHALL match case-insensitively, with
UPPERCASE as the canonical form. Blank lines, full-line `#` comments, and
trailing comments introduced by whitespace-then-`#` SHALL be ignored. Each
instruction SHALL take exactly one value and SHALL appear at most once.
`PROVIDER` is required. Parse errors SHALL name the offending line.

#### Scenario: A minimal Outfit

- **WHEN** a file containing only `PROVIDER openrouter` and
  `FAMILY deepseek-v4` is parsed
- **THEN** it yields a selection of that provider and family

#### Scenario: Duplicate instruction

- **WHEN** an Outfit sets `MODEL` on two lines
- **THEN** parsing fails, citing both line numbers

#### Scenario: Unknown keyword

- **WHEN** an Outfit contains `HARNESS pi`
- **THEN** parsing fails listing the accepted keywords

#### Scenario: Missing provider

- **WHEN** an Outfit has no `PROVIDER` instruction
- **THEN** parsing fails saying the PROVIDER instruction is missing

#### Scenario: Naming a remote endpoint

- **WHEN** an Outfit contains `REMOTE ./remote.json`
- **THEN** it parses, and the value is available to the `remote` command group

### Requirement: Outfit path resolution

Commands that take an Outfit path (`apply`, `unapply`, `serve`, `alias`,
`harness --outfit`, and the `remote` subcommands) SHALL default to `./Outfit`
when no path is given, SHALL accept a directory and use the `Outfit` file
inside it, and SHALL accept a registered alias name in place of a path. When
the default `./Outfit` is missing, the error SHALL suggest passing a path or an
alias.

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
