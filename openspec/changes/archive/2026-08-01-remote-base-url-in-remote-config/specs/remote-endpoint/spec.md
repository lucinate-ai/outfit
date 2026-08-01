## MODIFIED Requirements

### Requirement: Remote configuration discovery

The endpoint's control URLs SHALL come from a JSON configuration naming a start
URL, a stop URL, an optional deploy URL, and a region. That configuration MAY
also name the endpoint's own base URL; it SHALL be optional, since no control
call needs it, and a configuration without it SHALL remain valid. An Outfit's
`REMOTE` instruction SHALL select that file, resolved relative to the Outfit
when the value is not absolute. When no Outfit names one, the per-user
configuration SHALL be used, so the command works outside any project.
Environment variables SHALL override individual values, and the region SHALL
fall back to the standard AWS region variable and then to the region named in
the URL. A missing or incomplete configuration SHALL fail saying where to put
it.

#### Scenario: Outfit names the configuration

- **WHEN** an Outfit sets `REMOTE ./remote.json` and a `remote` subcommand runs
  with that Outfit
- **THEN** the URLs come from that file, resolved beside the Outfit

#### Scenario: Explicit Outfit without a REMOTE instruction

- **WHEN** a `remote` subcommand is given an Outfit that has no `REMOTE`
- **THEN** it fails saying that Outfit has no `REMOTE` instruction, rather than
  silently using the per-user configuration

#### Scenario: No Outfit in play

- **WHEN** a `remote` subcommand runs outside a project
- **THEN** the per-user configuration is used

#### Scenario: Configuration without a base URL

- **WHEN** a remote configuration names the control URLs and region but no base
  URL, and a `remote` subcommand runs
- **THEN** the subcommand works as it always has, since the endpoint reports its
  own address in the replies to `start` and `status`
