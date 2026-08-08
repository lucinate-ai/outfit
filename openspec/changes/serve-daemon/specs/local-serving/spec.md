## ADDED Requirements

### Requirement: Daemon and API flags

`outfit serve` SHALL accept `-d`/`--daemon` to run as a supervising daemon and
`-a`/`--api` to expose the control API, as defined by the `serve-daemon` and
`daemon-api` capabilities. Without these flags, serve's foreground
stdio-forwarded behaviour SHALL be unchanged.

#### Scenario: Plain serve is unchanged

- **WHEN** the user runs `outfit serve` with neither `--daemon` nor `--api`
- **THEN** the engine runs in the foreground with stdio forwarded, exactly as
  before

#### Scenario: Serve accepts daemon mode

- **WHEN** the user runs `outfit serve -d`
- **THEN** serve runs as a daemon supervising the engine instead of forwarding
  stdio
