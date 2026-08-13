## MODIFIED Requirements

### Requirement: Bearer-token authentication

The API SHALL authenticate requests with a bearer token compared against the
token configured for the process. The token MAY be supplied three ways: a file
naming it (`--api-token-file`), the environment (`OUTFIT_API_TOKEN`), or
literally on the command line (`--api-token`). Giving more than one SHALL fail
naming the conflict rather than picking one. Requests without the correct token
SHALL be rejected with `401` and no state change. When no token is configured,
the API SHALL refuse to listen on a non-loopback address and SHALL say why;
listening on loopback without a token SHALL be allowed.

The three are peers. That this token may be given on a command line while the
engine's key may not is a distinction rather than an inconsistency: this token
is configured locally, by whoever runs the daemon, on a machine they have
already decided to trust with it, whereas the engine's key is set remotely by a
client and persists on the node afterwards. A single "never on a command line"
rule covered both and was too broad for the first.

The trade-off SHALL be documented with the command: a command line is readable
by every local user, so a token given that way is disclosed to anyone with a
shell on that machine, while the file and environment forms carry no such
exposure. The documentation SHALL name the file form as the one to use from a
service manager, where a literal would otherwise sit in a unit file *and* in
the process list.

#### Scenario: Wrong token is rejected

- **WHEN** an API request carries a missing or incorrect bearer token
- **THEN** the response is `401` and no engine state changes

#### Scenario: A token file is read

- **WHEN** the daemon is given a file naming its token
- **THEN** requests carrying that token are accepted, and the file's
  surrounding whitespace is not part of it

#### Scenario: A missing token file fails at startup

- **WHEN** the daemon is given a token file that cannot be read
- **THEN** it fails at startup naming the path, rather than listening with no
  token

#### Scenario: Two sources are a conflict

- **WHEN** the daemon is given both a literal token and a token file
- **THEN** it fails naming both, rather than choosing one

#### Scenario: Tokenless non-loopback listen refuses to start

- **WHEN** the API would listen on a non-loopback address and no token is
  configured
- **THEN** startup fails with an error explaining a token is required for
  non-loopback exposure, naming every way one can be supplied

#### Scenario: Tokenless loopback is permitted

- **WHEN** the API listens on a loopback address with no token configured
- **THEN** the API serves requests without authentication

## ADDED Requirements

### Requirement: Start carries the engine's key

The start endpoint SHALL accept an engine API key alongside the optional deploy
config it already takes, so a caller says what to run and how it is gated in the
one request that runs it. The key SHALL be validated and stored exactly as the
config is: a start that is refused SHALL store neither.

The key SHALL NOT appear in any reply, any log line, or any error message the
API returns. It is the one field of a start request that must not come back out.

#### Scenario: A start carries a config and a key together

- **WHEN** a start request carries a deploy config and an engine key, and no
  engine is running
- **THEN** the config is stored, the engine starts gated with that key, and the
  reply reports the new state

#### Scenario: A refused start stores neither

- **WHEN** a start request carrying a config and a key arrives while an engine
  is running
- **THEN** the request fails as already-running, and neither the config nor the
  key is stored

#### Scenario: The key does not come back

- **WHEN** a start request carrying a key succeeds or fails
- **THEN** no part of the reply, and no error it produces, contains the key
