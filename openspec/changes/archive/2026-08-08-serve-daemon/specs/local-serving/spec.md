## ADDED Requirements

### Requirement: Control API flag

`outfit serve` SHALL accept `-a`/`--api` to expose the control API over the
foreground engine, as defined by the `daemon-api` capability. Serve SHALL
remain a foreground command with no daemon flag — long-lived supervision is
`outfit daemon`'s job. Without `--api`, serve's foreground stdio-forwarded
behaviour SHALL be unchanged.

#### Scenario: Plain serve is unchanged

- **WHEN** the user runs `outfit serve` without `--api`
- **THEN** the engine runs in the foreground with stdio forwarded, exactly as
  before

#### Scenario: Serve with the API stays foreground

- **WHEN** the user runs `outfit serve -a`
- **THEN** the engine runs in the foreground with the control API listening
  beside it
