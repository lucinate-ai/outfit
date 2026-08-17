## MODIFIED Requirements

### Requirement: Outfit file format

An Outfit SHALL be a flat, line-oriented text file of `KEYWORD value`
instructions. The keywords are `PROVIDER`, `MODEL`, `ALIAS`, `CONTEXT`,
`OUTPUT`, `PARALLEL`, `BASEURL` (also accepted as `BASE-URL`, `BASE_URL`, or
`URL`), `PRESET`, `REMOTE`, `FLEET`, and `ENV`. Keywords SHALL match
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

#### Scenario: Naming a fleet

- **WHEN** an Outfit contains `FLEET ./fleet.yaml`
- **THEN** it parses, and the value is available to the launch as the fleet to
  route through

#### Scenario: Declaring local environment variables

- **WHEN** an Outfit contains `ENV AWS_PROFILE=dev` and `ENV AWS_REGION=eu-west-2`
  on separate lines
- **THEN** it parses, yielding both key/value pairs in the selection, and the
  repetition is not treated as a duplicate-instruction error

#### Scenario: Malformed ENV value

- **WHEN** an Outfit contains an `ENV` instruction whose value has no `=` or an
  empty key
- **THEN** parsing fails, naming the offending line

#### Scenario: Setting the number of parallel slots

- **WHEN** an Outfit contains `PARALLEL 2`
- **THEN** it parses, yielding a parallel count of 2 in the selection

#### Scenario: A non-numeric or non-positive PARALLEL is caught on use

- **WHEN** an Outfit contains `PARALLEL 0`, `PARALLEL -1`, or `PARALLEL abc`
- **THEN** parsing accepts the raw value, exactly as it does for `CONTEXT`, and
  the command that goes on to use it (`serve`, `remote deploy`, a fleet wake)
  fails naming the value, rather than silently treating it as a slot count
