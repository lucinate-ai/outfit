# daemon-api Specification

## Purpose

The daemon's control HTTP API: which endpoints exist, what they accept and
return, how they authenticate, and the rules for exposing them. This is the
surface every remote caller speaks — the control-plane Lambdas that curl it
over SSM, and a fleet client across the network.

Two things shape it. It can start and stop processes, so exposure is guarded:
a non-loopback listen without a bearer token refuses to start outright rather
than serving unauthenticated. And it answers questions rather than handing out
raw material — status reports how long the engine has been idle, not the
counters a caller would have to compare for itself.
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

### Requirement: Control endpoints

The API SHALL provide JSON endpoints to: report status (engine state, what is being served, the engine log path, when the engine was last active, and the daemon's outfit version); start the engine; stop the engine; return collected metrics; and accept a deploy config. Start SHALL accept an optional deploy config in its request body — validated and persisted exactly as a config push, then started — so a client can say what to run and run it in one call; without a body, start uses the stored config or the Outfit. Start SHALL fail when an engine is already running, changing nothing — a body sent with a rejected start SHALL NOT be stored. Stop SHALL succeed when nothing is running (idempotent), and stopping the engine SHALL never terminate `outfit daemon` — the API keeps answering. Errors SHALL be returned as JSON with a message and a meaningful HTTP status.

Status SHALL report the engine's last-active time as an RFC 3339 timestamp and the idle duration derived from it in seconds, so a caller can judge idleness from a decision the daemon has already made rather than from raw counters it would have to compare itself. Both SHALL be omitted when no engine has ever run, and neither SHALL be inferred by the caller from any other field.

Status SHALL also report the daemon's outfit version as a string, set from the binary's build-time version variable. This enables remote callers to verify which outfit release the node is running without SSH access.

#### Scenario: Status reports the supervised state

- **WHEN** a status request is made
- **THEN** the response reports the engine state, the model/runner being served (when known), the engine log path, and the outfit version

#### Scenario: Status reports engine activity

- **WHEN** a status request is made while an engine is running that has served work
- **THEN** the response reports the last-active timestamp and the number of seconds since it

#### Scenario: Status omits activity when there is none to report

- **WHEN** a status request is made on a daemon that has never started an engine
- **THEN** the response carries no last-active timestamp and no idle duration

#### Scenario: Status reports version from build time

- **WHEN** a status request is made
- **THEN** the response includes the `version` field set to the binary's build-time version string

#### Scenario: Start and stop drive the engine

- **WHEN** a start request is made while the engine is `idle`, `stopped`, or `crashed`
- **THEN** the daemon starts the engine and the response reports the new state

#### Scenario: Start carries its own deploy config

- **WHEN** a start request carries a deploy config naming a servable runner and model, and no engine is running
- **THEN** the config is validated, persisted, and the engine starts serving it

#### Scenario: Start body is rejected while running

- **WHEN** a start request carrying a deploy config arrives while an engine is running
- **THEN** the request fails as already-running, the running engine is untouched, and the carried config is not stored

#### Scenario: Stop is idempotent

- **WHEN** a stop request is made while no engine is running
- **THEN** the response succeeds, reporting the engine as not running

#### Scenario: Stop never ends the daemon

- **WHEN** a stop request stops the engine under `outfit daemon`
- **THEN** the daemon and its API keep running, and a later start request succeeds

#### Scenario: Metrics endpoint returns collected stats

- **WHEN** a metrics request is made
- **THEN** the response is the in-process collected metrics in the rendering-compatible stats shape

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

### Requirement: Metrics reports engine activity

The metrics endpoint SHALL report the engine's last-active time as an RFC 3339
timestamp and the idle duration derived from it in seconds, on exactly the
terms the status endpoint already uses: both present or both absent, and both
absent until an engine has run.

The two endpoints SHALL draw on the same activity record rather than each
keeping its own, so a caller cannot see one answer from status and a different
answer from metrics at the same moment. A metrics request that scrapes the
engine's counters SHALL feed that reading into the record exactly as the
background sampler does, so polling metrics refreshes the shared answer rather
than racing it.

The activity fields SHALL be reported whatever the engine's state. Where the
metrics endpoint omits the running-engine figures — the token counters and the
host's GPU, CPU and memory readings — for an engine that is not running, it
SHALL still report last-active and idle, because the record survives a stop
precisely so it can answer after one.

#### Scenario: Metrics reports activity for a running engine

- **WHEN** a metrics request is made while an engine is running that has
  served work
- **THEN** the response reports the last-active timestamp and the number of
  seconds since it, alongside the token and system figures

#### Scenario: Metrics and status agree

- **WHEN** a metrics request and a status request are made against the same
  daemon
- **THEN** both report the same last-active time

#### Scenario: Metrics omits activity when there is none

- **WHEN** a metrics request is made on a daemon that has never started an
  engine
- **THEN** the response carries no last-active timestamp and no idle duration

#### Scenario: A stopped engine still reports its last activity

- **WHEN** a metrics request is made after the engine has been stopped
- **THEN** the response reports the last-active time and idle duration, even
  though it reports no token or system figures

### Requirement: Engine log endpoint

The API SHALL provide a read-only JSON endpoint returning the supervised
engine's captured output — the same output whose path status reports — so a
caller can read what the engine said without shell access to the machine.
Reading logs SHALL NOT change the engine's state, and SHALL be reachable
whether the engine is running, stopped or crashed: the output of a crash is
wanted precisely when the engine is no longer there to ask.

#### Scenario: Reading a running engine's output

- **WHEN** a logs request is made while the engine is running and has produced
  output
- **THEN** the response carries that output
- **AND** the engine's state is unchanged

#### Scenario: Reading after a crash

- **WHEN** the engine has exited and a logs request is made
- **THEN** the response still carries the output it produced before exiting

### Requirement: Log reads are bounded and resumable

A logs response SHALL be bounded: the endpoint SHALL never return the whole
file merely because no bound was asked for, since an engine's log grows without
limit and the daemon does not rotate it. A caller SHALL be able to state how
much it wants, and the endpoint SHALL cap that at a limit of its own so a
client cannot ask a node to read an unbounded amount into memory.

With no position stated, the response SHALL carry the **end** of the log — the
most recent output — rather than its beginning, because the recent end is what
diagnosis needs. Every response SHALL carry the position immediately after what
it returned, and a caller supplying that position SHALL receive only what has
been appended since. Following a log SHALL therefore be exact: no overlap
window, no duplicate suppression, no reliance on timestamps the output may not
carry.

#### Scenario: An unbounded request is still bounded

- **WHEN** a logs request states no bound
- **THEN** the response carries at most the endpoint's own maximum
- **AND** it is taken from the end of the log, not the beginning

#### Scenario: A cursor returns only what is new

- **WHEN** a caller repeats a logs request with the position from its previous
  response
- **THEN** the response carries only the output appended since that position
- **AND** it carries a new position for the next request

#### Scenario: Nothing new yields nothing

- **WHEN** a caller supplies the current end position and the engine has
  written nothing since
- **THEN** the response carries no output and the same position

### Requirement: A log that cannot be read is reported, not faked

The endpoint SHALL distinguish the states a caller can act on rather than
returning an empty log for all of them. When no log file exists — no engine has
ever run on this node, or the daemon was configured to forward output to its own
stdio instead of a file — the response SHALL say so distinctly from a log that
exists and is empty. When a supplied position no longer makes sense because the
log is shorter than it — the file was truncated or replaced underneath the
caller — the response SHALL report that rather than silently returning nothing,
so the caller can resume from the end instead of waiting forever on a position
that will never be reached.

#### Scenario: No engine has ever run

- **WHEN** a logs request is made on a node whose engine has never started
- **THEN** the response reports that there is no log to read
- **AND** it is distinguishable from an engine that ran and logged nothing

#### Scenario: The log was truncated under the caller

- **WHEN** a caller supplies a position beyond the log's current end
- **THEN** the response reports that the position is no longer valid
- **AND** it carries the log's current end so the caller can resume

### Requirement: Status reports the engine's endpoint

Status SHALL report where the supervised engine serves: the port it listens on,
the OpenAI-compatible path prefix under it when that is not the default, whether
the bind is loopback-only, and whether the engine requires an API key. This is
the one thing about a node a remote caller cannot work out for itself — the
engine's port is not the control API's, and nothing in the reply implies it —
so a router asking "where do I send inference?" gets an answer rather than a
guess.

The engine endpoint SHALL be omitted when no engine is running: an address for
a process that does not exist is worse than no address.

The reported key requirement SHALL say only whether a key is needed. The API
SHALL NOT return the engine's key under any endpoint: a caller authorised to
drive a node is not thereby authorised to be handed its engine's credential.

#### Scenario: A running engine reports where it serves

- **WHEN** a status request is made while an engine is running
- **THEN** the response reports the engine's port, whether the bind is
  loopback-only, and whether it requires a key

#### Scenario: The engine port is distinct from the API port

- **WHEN** a status request is made on a daemon whose control API and engine
  listen on different ports
- **THEN** the reported engine port is the engine's, not the API's

#### Scenario: A gated engine says so without saying what

- **WHEN** a status request is made while an engine started with an API key is
  running
- **THEN** the response says a key is required and carries no key value

#### Scenario: No engine, no endpoint

- **WHEN** a status request is made while the engine is idle, stopped, or
  crashed
- **THEN** the response carries no engine endpoint

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

