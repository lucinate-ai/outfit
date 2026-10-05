## ADDED Requirements

### Requirement: Request body limit

The gateway SHALL read a completion request's body in full or refuse it: a
body larger than the limit SHALL be answered `413` with a message naming the
limit in bytes and the `--max-request-bytes` flag, and SHALL NOT be parsed or
forwarded. The limit SHALL default to 64 MiB and SHALL be set by
`--max-request-bytes`, which SHALL be a positive number of bytes. The gateway
SHALL forward to an engine only a body it read in full, byte for byte.

#### Scenario: A body under the limit is routed

- **WHEN** a completion request whose body is just under the limit reaches the gateway
- **THEN** it is routed and its full body is forwarded to the engine

#### Scenario: A body over the limit is refused with 413

- **WHEN** a completion request whose body is larger than the limit reaches the gateway
- **THEN** the gateway answers `413` naming the limit and `--max-request-bytes`, not `400`, and no engine receives anything

#### Scenario: The limit is set by a flag

- **WHEN** the gateway is started with `--max-request-bytes 2097152`
- **THEN** a 1.5 MiB body is routed and a 3 MiB body is answered `413` naming 2097152

#### Scenario: A non-positive limit fails at startup

- **WHEN** the gateway is started with `--max-request-bytes 0`
- **THEN** it fails at startup naming the flag, and nothing listens
