# daemon-api Specification

## Purpose
TBD - created by archiving change serve-daemon. Update Purpose after archive.
## Requirements
### Requirement: API exposure

`outfit daemon` SHALL always expose the control API — it is the command's
purpose. `outfit serve` SHALL expose it only when `-a`/`--api` is passed, and
remains a foreground command either way; serve SHALL have no daemon flag. The
listen address SHALL default to port 4242 on all interfaces and SHALL be
overridable by flag.

#### Scenario: The daemon exposes the API

- **WHEN** `outfit daemon` runs with no API flags
- **THEN** the control API listens on the default address

#### Scenario: Foreground serve is API-off by default

- **WHEN** `outfit serve` runs without `--api`
- **THEN** no control API listens

#### Scenario: Foreground serve can opt in

- **WHEN** `outfit serve --api` runs
- **THEN** the control API listens while the engine runs in the foreground

### Requirement: Bearer-token authentication

The API SHALL authenticate requests with a bearer token compared against the
token configured for the process. The token SHALL be supplied via the
environment (including the `.env` loading the Outfit resolution already
performs), never as a command-line flag. Requests without the correct token
SHALL be rejected with `401` and no state change. When no token is configured,
the API SHALL refuse to listen on a non-loopback address and SHALL say why;
listening on loopback without a token SHALL be allowed.

#### Scenario: Wrong token is rejected

- **WHEN** an API request carries a missing or incorrect bearer token
- **THEN** the response is `401` and no engine state changes

#### Scenario: Tokenless non-loopback listen refuses to start

- **WHEN** the API would listen on a non-loopback address and no token is
  configured
- **THEN** startup fails with an error explaining a token is required for
  non-loopback exposure

#### Scenario: Tokenless loopback is permitted

- **WHEN** the API listens on a loopback address with no token configured
- **THEN** the API serves requests without authentication

### Requirement: Control endpoints

The API SHALL provide JSON endpoints to: report status (engine state, what is
being served, the engine log path); start the engine; stop the engine; return
collected metrics; and accept a deploy config. Start SHALL accept an optional
deploy config in its request body — validated and persisted exactly as a
config push, then started — so a client can say what to run and run it in one
call; without a body, start uses the stored config or the Outfit. Start SHALL
fail when an engine is already running, changing nothing — a body sent with a
rejected start SHALL NOT be stored. Stop SHALL succeed when nothing is
running (idempotent), and stopping the engine SHALL never terminate
`outfit daemon` — the API keeps answering. Errors SHALL be returned as JSON
with a message and a meaningful HTTP status.

#### Scenario: Status reports the supervised state

- **WHEN** a status request is made
- **THEN** the response reports the engine state, the model/runner being
  served (when known), and the engine log path

#### Scenario: Start and stop drive the engine

- **WHEN** a start request is made while the engine is `idle`, `stopped`, or
  `crashed`
- **THEN** the daemon starts the engine and the response reports the new state

#### Scenario: Start carries its own deploy config

- **WHEN** a start request carries a deploy config naming a servable runner
  and model, and no engine is running
- **THEN** the config is validated, persisted, and the engine starts serving
  it

#### Scenario: Start body is rejected while running

- **WHEN** a start request carrying a deploy config arrives while an engine
  is running
- **THEN** the request fails as already-running, the running engine is
  untouched, and the carried config is not stored

#### Scenario: Stop is idempotent

- **WHEN** a stop request is made while no engine is running
- **THEN** the response succeeds, reporting the engine as not running

#### Scenario: Stop never ends the daemon

- **WHEN** a stop request stops the engine under `outfit daemon`
- **THEN** the daemon and its API keep running, and a later start request
  succeeds

#### Scenario: Metrics endpoint returns collected stats

- **WHEN** a metrics request is made
- **THEN** the response is the in-process collected metrics in the
  rendering-compatible stats shape

### Requirement: Deploy config push

The API SHALL accept a deploy config in the same shape `outfit remote deploy`
derives from an Outfit and its preset (runner, model, context, alias, serve
args — the preset already resolved by the pusher). The daemon SHALL validate
that the runner names an engine it can serve, persist the config, and use it
for subsequent starts. Pushing a config SHALL NOT itself restart a running
engine.

#### Scenario: Pushed config is used on next start

- **WHEN** a deploy config is pushed and a start is then requested
- **THEN** the engine starts with the pushed config's model and serve args

#### Scenario: Push does not disturb a running engine

- **WHEN** a deploy config is pushed while an engine is running
- **THEN** the running engine is untouched and the response says the config
  takes effect on next start

#### Scenario: Unservable runner is rejected

- **WHEN** a pushed deploy config names a runner the daemon cannot serve
  locally
- **THEN** the push is rejected with an error naming the runner

