## MODIFIED Requirements

### Requirement: Outfit file format

An Outfit SHALL be a flat, line-oriented text file of `KEYWORD value`
instructions. The keywords are `PROVIDER`, `MODEL`, `ALIAS`, `CONTEXT`,
`OUTPUT`, `BASEURL` (also accepted as `BASE-URL`, `BASE_URL`, or `URL`),
`PRESET`, `REMOTE`, `FLEET`, and `ENV`. Keywords SHALL match
case-insensitively, with UPPERCASE as the canonical form. Blank lines, full-line
`#` comments, and trailing comments introduced by whitespace-then-`#` SHALL be
ignored. Each instruction SHALL take exactly one value; every instruction SHALL
appear at most once, except `ENV`, which MAY be repeated. An `ENV` instruction's
value SHALL be a single `KEY=VALUE` token with a non-empty key and no
whitespace. `PROVIDER` is required. Parse errors SHALL name the offending line.

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

## ADDED Requirements

### Requirement: FLEET and REMOTE are exclusive

An Outfit SHALL NOT name both a `FLEET` and a `REMOTE`: each is a different
answer to where the model is served from — one chooses a machine on your
network, the other a deployed endpoint — and an Outfit stating both is a mistake
rather than a precedence to resolve. Parsing SHALL fail naming both
instructions.

An explicit `BASEURL` is not in conflict: it is the pinned address that already
takes precedence over a `REMOTE`, and it takes precedence over a `FLEET` the
same way.

#### Scenario: Both fail to parse

- **WHEN** an Outfit contains both `FLEET ./fleet.yaml` and `REMOTE ./remote.json`
- **THEN** parsing fails naming both instructions

#### Scenario: A pinned address is allowed

- **WHEN** an Outfit contains both `FLEET ./fleet.yaml` and a `BASEURL`
- **THEN** it parses, and the pinned base URL is what applies

### Requirement: FLEET names a file or an endpoint

A `FLEET` value SHALL be either a path to a fleet file, which routing reads to
choose a node, or a URL, which names an endpoint that has already done the
choosing. A value carrying a scheme SHALL be read as the latter; anything else
as a path. Both SHALL parse, so the two ways of routing are one instruction
rather than two.

Routing through a URL SHALL fail with a message saying it is not implemented
yet, rather than being silently ignored or treated as a filename.

#### Scenario: A path names a fleet file

- **WHEN** an Outfit contains `FLEET ./fleet.yaml`
- **THEN** it parses as a fleet file to route through

#### Scenario: A URL names an endpoint

- **WHEN** an Outfit contains `FLEET http://gateway.internal:4000`
- **THEN** it parses as an endpoint, and a launch against it fails saying that
  routing through a gateway is not implemented yet
