# Spinloop Files Specification

## Purpose

Define the `Spinloop` file — a declarative, Dockerfile-style description of one
provider selection — and the commands that consume and produce it:
`spinloop harness apply`, `spinloop harness unapply`, and `spinloop harness export`.

## Requirements

### Requirement: Spinloop file format

A Spinloop SHALL be a flat, line-oriented text file of `KEYWORD value`
instructions. The keywords are `PROVIDER`, `MODEL`, `ALIAS`, `CONTEXT`,
`OUTPUT`, `PARALLEL`, `BASEURL` (also accepted as `BASE-URL`, `BASE_URL`, or
`URL`), `PRESET`, and `ENV`. Keywords SHALL match
case-insensitively, with UPPERCASE as the canonical form. Blank lines, full-line
`#` comments, and trailing comments introduced by whitespace-then-`#` SHALL be
ignored. Each instruction SHALL take exactly one value; every instruction SHALL
appear at most once, except `ENV`, which MAY be repeated. An `ENV` instruction's
value SHALL be a single `KEY=VALUE` token with a non-empty key and no
whitespace. `PARALLEL`'s value SHALL name a count of concurrent request slots;
like `CONTEXT`, its numeric validity (a positive integer) is enforced by the
commands that consume it rather than by parsing itself, and what it does to
the served engine's command is defined by the `local-serving` capability.
`PROVIDER` is required. Parse errors SHALL name the offending line.

#### Scenario: A minimal Spinloop

- **WHEN** a file containing only `PROVIDER openrouter` and
  `MODEL deepseek/deepseek-v4-pro` is parsed
- **THEN** it yields a selection of that provider and model

#### Scenario: Duplicate instruction

- **WHEN** a Spinloop sets `MODEL` on two lines
- **THEN** parsing fails, citing both line numbers

#### Scenario: Unknown keyword

- **WHEN** a Spinloop contains `HARNESS pi`
- **THEN** parsing fails listing the accepted keywords

#### Scenario: Naming a fleet

- **WHEN** a Spinloop contains `FLEET ./fleet.yaml`
- **THEN** parsing fails as an unknown keyword, listing the accepted keywords,
  and no special migration message is printed

#### Scenario: Missing provider

- **WHEN** a Spinloop has no `PROVIDER` instruction
- **THEN** parsing fails saying the PROVIDER instruction is missing

#### Scenario: Naming a cloud endpoint

- **WHEN** a Spinloop contains `REMOTE ./remote.json` on any line
- **THEN** parsing fails on that line, since the `REMOTE` instruction was
  removed: the environment is now named with `cloud deploy --env <name>` at
  deploy time and `--env <name>` at the commands that act on it

#### Scenario: Declaring local environment variables

- **WHEN** a Spinloop contains `ENV AWS_PROFILE=dev` and `ENV AWS_REGION=eu-west-2`
  on separate lines
- **THEN** it parses, yielding both key/value pairs in the selection, and the
  repetition is not treated as a duplicate-instruction error

#### Scenario: Malformed ENV value

- **WHEN** a Spinloop contains an `ENV` instruction whose value has no `=` or an
  empty key
- **THEN** parsing fails, naming the offending line

#### Scenario: Setting the number of parallel slots

- **WHEN** a Spinloop contains `PARALLEL 2`
- **THEN** it parses, yielding a parallel count of 2 in the selection

#### Scenario: A non-numeric or non-positive PARALLEL is caught on use

- **WHEN** a Spinloop contains `PARALLEL 0`, `PARALLEL -1`, or `PARALLEL abc`
- **THEN** parsing accepts the raw value, exactly as it does for `CONTEXT`, and
  the command that goes on to use it (`serve`, `cloud deploy`, a fleet wake)
  fails naming the value, rather than silently treating it as a slot count

### Requirement: REMOTE is a removed keyword

A `REMOTE` instruction SHALL be rejected at parse time with an error that names
the offending line, says the `REMOTE` instruction was removed, and states the
replacement: name the environment with `cloud deploy --env <name>` at deploy
time, and pass `--env <name>` to the commands that act on the environment
(`cloud` subcommands, `apply`, `unapply`, `harness`). The error SHALL NOT be
the generic unknown-keyword message.

#### Scenario: A REMOTE line is rejected with the migration message

- **WHEN** a Spinloop contains `REMOTE ./remote.json` on line 3
- **THEN** parsing fails citing line 3, saying `REMOTE` was removed, and naming
  `--env` as the way to name the environment

#### Scenario: The generic error is not used for REMOTE

- **WHEN** a Spinloop contains a `REMOTE` line
- **THEN** the error names the removal and its replacement rather than merely
  listing the accepted keywords

### Requirement: Harness neutrality

A Spinloop SHALL NOT name a harness, and SHALL NOT name an alias-registry entry:
both are machine-local, runtime choices. The same Spinloop file SHALL be
applicable to any supported harness.

#### Scenario: One Spinloop, two harnesses

- **WHEN** the same Spinloop is applied with the opencode harness active and then
  with `--harness pi`
- **THEN** each harness's own config is updated from the same file with no
  change to the file

### Requirement: Spinloop path resolution

Commands that take a Spinloop path (`apply`, `unapply`, `serve`, `alias`,
`harness --spinloop`, and the `cloud` subcommands) SHALL default to `./Spinloop`
when no path is given, SHALL accept a directory and use the `Spinloop` file
inside it, SHALL accept a registered alias name in place of a path, and SHALL
accept an `http://` or `https://` URL in place of a path, fetched over HTTP
instead of read from local disk. A URL ending in `/` SHALL be treated as a
directory-style reference and have `Spinloop` appended, mirroring the local
directory case. When the default `./Spinloop` is missing, the error SHALL
suggest passing a path or an alias.

When no path is given, the `SPINLOOP_ALIAS` environment variable SHALL be
consulted before falling back to `./Spinloop`, so the resolution order is the
argument, then `SPINLOOP_ALIAS`, then `./Spinloop`. `spinloop alias` SHALL be the one
exception and always use its argument or `./Spinloop`. Where the default
`./Spinloop` is missing and no `SPINLOOP_ALIAS` is set, the error SHALL name the
variable alongside the path and alias it already suggests.

#### Scenario: Bare command in a project directory

- **WHEN** the user runs `spinloop harness apply` in a directory holding a `Spinloop`
- **THEN** that file is applied

#### Scenario: Directory argument

- **WHEN** the user runs `spinloop harness apply path/to/dir` and the directory holds an
  `Spinloop`
- **THEN** `path/to/dir/Spinloop` is applied

#### Scenario: A cloud subcommand resolves the same way

- **WHEN** the user runs `spinloop cloud status` in a directory holding an
  `Spinloop`
- **THEN** that Spinloop is read to find the endpoint's configuration

#### Scenario: The environment names the default Spinloop

- **WHEN** `SPINLOOP_ALIAS` names a registered alias and the user runs
  `spinloop serve` with no argument
- **THEN** that alias's Spinloop is served, whether or not the working directory
  holds one

#### Scenario: Nothing to resolve

- **WHEN** the user runs `spinloop harness apply` with no argument, no `SPINLOOP_ALIAS` set
  and no `./Spinloop` present
- **THEN** the command fails suggesting a path, an alias, or `SPINLOOP_ALIAS`

#### Scenario: A URL argument

- **WHEN** the user runs `spinloop harness apply https://example.com/team/Spinloop`
- **THEN** the Spinloop is fetched from that URL and applied, with no local
  file read

#### Scenario: A directory-style URL argument

- **WHEN** the user runs `spinloop harness apply https://example.com/team/` (a
  trailing `/`)
- **THEN** `https://example.com/team/Spinloop` is fetched and applied

#### Scenario: An unreachable URL

- **WHEN** the user runs `spinloop harness apply` against a URL whose host does not
  respond
- **THEN** the command fails with a clear network error naming the URL,
  rather than a filesystem "not found" error

### Requirement: Applying and unapplying a Spinloop

`spinloop harness apply` SHALL apply the Spinloop's selection exactly as the equivalent
`spinloop harness add` would, and `spinloop harness unapply` SHALL remove what the Spinloop selects
exactly as the equivalent `spinloop harness remove` would. A command-line `--output`/`-o`
on `apply` SHALL override the Spinloop's `OUTPUT` instruction, and `--providers`
SHALL override the catalogue it resolves against (a Spinloop never names a
catalogue). `apply` SHALL ignore a `PRESET` instruction — it is consumed only
by `spinloop serve`.

#### Scenario: Apply equals add

- **WHEN** a Spinloop with `PROVIDER ollama` and `MODEL llama3.2` is applied
- **THEN** the harness config matches what `spinloop harness add -p ollama -m llama3.2`
  would have produced

#### Scenario: Output override

- **WHEN** a Spinloop sets `OUTPUT 32k` and the user runs
  `spinloop harness apply --output 16k`
- **THEN** the applied output limit is 16000 tokens

#### Scenario: Preset is not apply's business

- **WHEN** a Spinloop with a `PRESET` instruction is applied
- **THEN** the harness config is written as if the instruction were absent

### Requirement: Exporting the current config

`spinloop harness export` SHALL reconstruct a canonical Spinloop from the active harness's
config and print it to stdout. The provider exported is chosen by the
`--provider`/`-p` flag, else the default model's provider, else the sole
configured provider; with several providers and no way to choose, the command
SHALL fail listing them. The output SHALL name the configured model with a
`MODEL` instruction, SHALL omit a `BASEURL` that only restates the catalogue's
default, and SHALL record `CONTEXT`/`OUTPUT` only when the exported models agree
on a single value — never inventing one. Rendered output SHALL use canonical
UPPERCASE keywords with aligned values, so `spinloop harness export > Spinloop` round-trips.

#### Scenario: Round-trip through export

- **WHEN** the user applies a Spinloop and then runs `spinloop harness export`
- **THEN** the printed Spinloop selects the same provider, model, and limits

#### Scenario: Ambiguous provider

- **WHEN** several providers are configured, none is the default model's, and
  no `-p` is given
- **THEN** the command fails listing the configured providers to choose from

#### Scenario: Nothing configured

- **WHEN** the harness config has no providers
- **THEN** the command fails naming the config file it read
