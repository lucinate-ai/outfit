# daemon-api (delta)

## MODIFIED Requirements

### Requirement: API exposure

`outfit daemon` SHALL always expose the control API — it is the command's
purpose. `outfit serve` SHALL expose it only when `-a`/`--api` is passed, and
remains a foreground command either way; serve SHALL have no daemon flag. The
listen address SHALL default to port 4242 on all interfaces and SHALL be
overridable by flag; `outfit daemon` SHALL also offer a `--loopback`
(short `-l`) boolean flag that binds the API to loopback on the default port,
identical to giving `--api-addr 127.0.0.1:4242`. The shorthand applies to
`outfit daemon` only — `outfit serve`'s API keeps `--api-addr` alone. Giving
`--loopback` together with an explicit `--api-addr` SHALL fail, naming the
conflict, rather than letting one win.

#### Scenario: The daemon exposes the API

- **WHEN** `outfit daemon` runs with no API flags
- **THEN** the control API listens on the default address

#### Scenario: Loopback shorthand

- **WHEN** `outfit daemon` runs with `--loopback` and no `--api-addr`
- **THEN** the control API listens on `127.0.0.1:4242` and — being loopback —
  needs no bearer token

#### Scenario: Loopback with an explicit address is a conflict

- **WHEN** `outfit daemon` is given both `--loopback` and `--api-addr <addr>`
- **THEN** it fails at startup naming both flags, rather than choosing one

#### Scenario: Foreground serve is API-off by default

- **WHEN** `outfit serve` runs without `--api`
- **THEN** no control API listens

#### Scenario: Foreground serve can opt in

- **WHEN** `outfit serve --api` runs
- **THEN** the control API listens while the engine runs in the foreground
