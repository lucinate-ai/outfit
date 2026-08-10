# Outfit Files Specification

## Purpose

Define the `Outfit` file — a declarative, Dockerfile-style description of one
provider selection — and the commands that consume and produce it:
`outfit apply`, `outfit unapply`, and `outfit export`.
## Requirements
### Requirement: Outfit file format

An Outfit SHALL be a flat, line-oriented text file of `KEYWORD value`
instructions. The keywords are `PROVIDER`, `MODEL`, `ALIAS`, `CONTEXT`,
`OUTPUT`, `BASEURL` (also accepted as `BASE-URL`, `BASE_URL`, or `URL`),
`PRESET`, `REMOTE`, and `ENV`. Keywords SHALL match case-insensitively, with
UPPERCASE as the canonical form. Blank lines, full-line `#` comments, and
trailing comments introduced by whitespace-then-`#` SHALL be ignored. Each
instruction SHALL take exactly one value; every instruction SHALL appear at most
once, except `ENV`, which MAY be repeated. An `ENV` instruction's value SHALL be
a single `KEY=VALUE` token with a non-empty key and no whitespace. `PROVIDER` is
required. Parse errors SHALL name the offending line.

#### Scenario: A minimal Outfit

- **WHEN** a file containing only `PROVIDER openrouter` and
  `MODEL deepseek/deepseek-v4-pro` is parsed
- **THEN** it yields a selection of that provider and model

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

#### Scenario: Declaring local environment variables

- **WHEN** an Outfit contains `ENV AWS_PROFILE=dev` and `ENV AWS_REGION=eu-west-2`
  on separate lines
- **THEN** it parses, yielding both key/value pairs in the selection, and the
  repetition is not treated as a duplicate-instruction error

#### Scenario: Malformed ENV value

- **WHEN** an Outfit contains an `ENV` instruction whose value has no `=` or an
  empty key
- **THEN** parsing fails, naming the offending line

### Requirement: Harness neutrality

An Outfit SHALL NOT name a harness, and SHALL NOT name an alias-registry entry:
both are machine-local, runtime choices. The same Outfit file SHALL be
applicable to any supported harness.

#### Scenario: One Outfit, two harnesses

- **WHEN** the same Outfit is applied with the opencode harness active and then
  with `--harness pi`
- **THEN** each harness's own config is updated from the same file with no
  change to the file

### Requirement: Outfit path resolution

Commands that take an Outfit path (`apply`, `unapply`, `serve`, `alias`,
`harness --outfit`, and the `remote` subcommands) SHALL default to `./Outfit`
when no path is given, SHALL accept a directory and use the `Outfit` file
inside it, and SHALL accept a registered alias name in place of a path. When
the default `./Outfit` is missing, the error SHALL suggest passing a path or an
alias.

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

### Requirement: Applying and unapplying an Outfit

`outfit apply` SHALL apply the Outfit's selection exactly as the equivalent
`outfit add` would, and `outfit unapply` SHALL remove what the Outfit selects
exactly as the equivalent `outfit remove` would. A command-line `--output`/`-o`
on `apply` SHALL override the Outfit's `OUTPUT` instruction, and `--providers`
SHALL override the catalogue it resolves against (an Outfit never names a
catalogue). `apply` SHALL ignore a `PRESET` instruction — it is consumed only
by `outfit serve`.

#### Scenario: Apply equals add

- **WHEN** an Outfit with `PROVIDER ollama` and `MODEL llama3.2` is applied
- **THEN** the harness config matches what `outfit add -p ollama -m llama3.2`
  would have produced

#### Scenario: Output override

- **WHEN** an Outfit sets `OUTPUT 32k` and the user runs
  `outfit apply --output 16k`
- **THEN** the applied output limit is 16000 tokens

#### Scenario: Preset is not apply's business

- **WHEN** an Outfit with a `PRESET` instruction is applied
- **THEN** the harness config is written as if the instruction were absent

### Requirement: Exporting the current config

`outfit export` SHALL reconstruct a canonical Outfit from the active harness's
config and print it to stdout. The provider exported is chosen by the
`--provider`/`-p` flag, else the default model's provider, else the sole
configured provider; with several providers and no way to choose, the command
SHALL fail listing them. The output SHALL name the configured model with a
`MODEL` instruction, SHALL omit a `BASEURL` that only restates the catalogue's
default, and SHALL record `CONTEXT`/`OUTPUT` only when the exported models agree
on a single value — never inventing one. Rendered output SHALL use canonical
UPPERCASE keywords with aligned values, so `outfit export > Outfit` round-trips.

#### Scenario: Round-trip through export

- **WHEN** the user applies an Outfit and then runs `outfit export`
- **THEN** the printed Outfit selects the same provider, model, and limits

#### Scenario: Ambiguous provider

- **WHEN** several providers are configured, none is the default model's, and
  no `-p` is given
- **THEN** the command fails listing the configured providers to choose from

#### Scenario: Nothing configured

- **WHEN** the harness config has no providers
- **THEN** the command fails naming the config file it read

