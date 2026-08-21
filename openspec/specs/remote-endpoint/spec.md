# Remote Endpoint Specification

## Purpose

Define how a remote inference endpoint is discovered, controlled and told
what to serve from an Outfit: the `outfit remote` command group.
## Requirements
### Requirement: Remote command group

The system SHALL provide a `remote` command group with the subcommands
`bootstrap`, `start`, `stop`, `restart`, `status`, `deploy`, `ls`, `metrics`,
and `keep`. `start`, `stop`, `restart`, `status`, `metrics` and `deploy` each
take an optional Outfit path:
`start` SHALL boot the endpoint and block until it is serving, then perform a
quick TCP probe of the inference endpoint — if the probe fails, a warning is
printed to stderr explaining the network mismatch (see the Remote Start Probe
specification) — and finally print the base URL and API key as shell exports;
`start` SHALL also accept a `--keep DURATION` flag that sets the instance
retention deadline to `now + DURATION`, preventing the idle sweep from
terminating it before that time (see the Remote Keep specification);
`stop` SHALL stop it immediately rather than waiting for its idle timer;
`restart` SHALL stop the endpoint in the manner of a pause — without
terminating it, so its boot disk, its weights and its stable address are
preserved — and SHALL immediately start it again, blocking until it is serving
and reporting progress as `start` does (see the Reporting a start in progress
specification); `restart` SHALL accept a `--force` flag with a `-F` short form
that, when set, performs the stop without first asking the engine to shut down
(see the Endpoint Lifecycle specification for forced stops);
`status` SHALL report instance state and endpoint health without side effects
and SHALL NOT perform any TCP probe, and SHALL include the `Retain-Until`
deadline when the instance has an active retention tag;
`keep` SHALL set the `Retain-Until` tag on the environment's instance for the
given duration, without starting or stopping the instance (see the Remote Keep
specification); `metrics` SHALL report instance state, token usage, resource
consumption, and GPU information for a running instance; `deploy` SHALL set
what the endpoint serves. `ls` SHALL list the registered remote environments
(see the Remote Environments specification). `bootstrap` SHALL stand up the
account-level AWS control plane (once per account) by obtaining and driving the
CDK project, and takes its own flags rather than an Outfit path (see the
Endpoint Provisioning specification). An unrecognised subcommand SHALL fail
naming the accepted ones.

#### Scenario: Starting the endpoint

- **WHEN** the user runs `outfit remote start` and the endpoint reports ready
- **THEN** the base URL and API key are printed as `export` lines

#### Scenario: Starting warns when the network is not admitted

- **WHEN** the user runs `outfit remote start` and the endpoint reports ready
  but the TCP probe to the inference port fails
- **THEN** a warning is printed to stderr with a remediation command, and the
  command still exits 0

#### Scenario: Starting with a keep flag

- **WHEN** the user runs `outfit remote start --keep 4h` and the endpoint reports ready
- **THEN** the base URL and API key are printed as `export` lines, and the
  instance retention deadline is set to 4 hours from now

#### Scenario: Waiting through a cold start

- **WHEN** the endpoint reports that it is still starting
- **THEN** the command waits and retries until it is ready or the timeout
  passes, rather than failing on the first attempt

#### Scenario: Restarting the endpoint

- **WHEN** the user runs `outfit remote restart` for a running environment and
  the endpoint reports ready again
- **THEN** the instance was stopped and re-woken without being terminated, the
  command blocked until the model was serving again, and the environment's
  address is the one its configuration records

#### Scenario: Forcing a restart skips the engine stop

- **WHEN** the user runs `outfit remote restart --force` (or `-F`)
- **THEN** the instance is stopped without the engine being asked to shut down
  first, and the command then blocks until the model is serving again

#### Scenario: Restarting a stopped endpoint starts it

- **WHEN** the user runs `outfit remote restart` for an environment whose instance is already stopped
- **THEN** the instance is re-woken rather than replaced, and the command blocks
  until the model is serving again, as with a plain start

#### Scenario: A failed re-wake says how to recover

- **WHEN** the stop half of a restart has taken effect but the wake fails
- **THEN** the command fails saying the instance is stopped and that
  `outfit remote start` will bring it back

#### Scenario: Listing environments

- **WHEN** the user runs `outfit remote ls`
- **THEN** the registered environments are listed rather than any endpoint being
  contacted

#### Scenario: Setting a keep deadline

- **WHEN** the user runs `outfit remote keep 2h`
- **THEN** the instance retention tag is set and the deadline is reported

#### Scenario: Metrics reports instance figures

- **WHEN** the user runs `outfit remote metrics` with a running instance
- **THEN** token counts, resource usage, and GPU information are displayed

#### Scenario: Bootstrap is a recognised subcommand

- **WHEN** the user runs `outfit remote bootstrap`
- **THEN** the command is dispatched to the provisioning flow rather than
  reported as unknown

#### Scenario: Unknown subcommand

- **WHEN** the user runs `outfit remote frobnicate`
- **THEN** the command fails listing the accepted subcommands, which include
  `bootstrap`, `metrics`, and `keep`

### Requirement: Reporting a start in progress

Because the endpoint blocks until the model is serving, `start` SHALL report
that it is waiting rather than appear to hang: it SHALL say what is happening
before the first attempt and repeat at intervals with the elapsed time, and
SHALL report how long it took once ready. Progress SHALL be written to standard
error and the resulting exports to standard output, so the command's output can
be evaluated directly while a person watching still sees progress.

The periodic progress line SHALL reflect the situation the start is actually in
so it does not misdescribe what is happening. When the most recent reply
reported that no capacity is available anywhere — so no instance is booting —
and no newer attempt is in flight, the line SHALL say it is waiting for
capacity rather than that the instance is starting. Once a newer attempt has
been issued and has not yet returned, that no-capacity reply no longer
describes the situation — a refusal comes back within seconds of trying each
zone, whereas a successful attempt holds its request while the instance boots —
so the line SHALL say it is starting. Before any attempt has returned, the
line SHALL also say it is starting. Each per-poll retry notice (reporting the
state and the wait before the next attempt) SHALL continue to be reported as it
happens, independently of the periodic line.

#### Scenario: A cold start is not silent

- **WHEN** the endpoint takes minutes to become ready
- **THEN** the command explains what it is waiting for and continues to report
  the elapsed time until it succeeds

#### Scenario: Waiting for capacity is not reported as booting

- **WHEN** the most recent reply reports no capacity in any zone and no newer
  attempt is in flight
- **THEN** the periodic progress line says it is waiting for capacity, not that
  the instance is still starting

#### Scenario: Booting is reported as starting

- **WHEN** the most recent reply reports the instance is booting, or no attempt
  has returned yet
- **THEN** the periodic progress line says it is still starting

#### Scenario: Booting after a capacity wait is not reported as waiting

- **WHEN** the most recent reply reported no capacity in any zone and the
  client has since issued another attempt that finds capacity and is booting
- **THEN** the periodic progress line says it is starting, not that it is still
  waiting for capacity

#### Scenario: Only the result is on standard output

- **WHEN** a start succeeds and its output is captured
- **THEN** standard output holds exactly the environment exports, with every
  progress line on standard error

### Requirement: Configurable start timeout

`start` SHALL wait for the endpoint up to an overall timeout that the user can
set, defaulting to 15 minutes when not given. The timeout SHALL be accepted as a
Go duration on a `--timeout` flag with a `-t` short alias, so a user may shorten
or lengthen the wait, e.g. `-t 5m`. When the timeout passes before the endpoint
is ready, `start` SHALL stop waiting and fail rather than block indefinitely.

#### Scenario: Shortening the wait

- **WHEN** the user runs `outfit remote start` with `-t 5m` (or `--timeout 5m`)
- **THEN** the command waits at most five minutes for the endpoint before
  giving up

#### Scenario: Default wait when unset

- **WHEN** the user runs `outfit remote start` without a timeout flag
- **THEN** the command waits up to fifteen minutes

### Requirement: Remote configuration discovery

The endpoint's control URLs SHALL come from a JSON configuration naming a start
URL, a stop URL, an optional deploy URL, and a region. That configuration MAY
also name the endpoint's own base URL; it SHALL be optional, since no control
call needs it, and a configuration without it SHALL remain valid. An Outfit's
`REMOTE` instruction SHALL select that configuration: a bare name selects the
named environment from the per-user registry (always local; see the Remote
Environments specification), and a path or URL selects a configuration
resolved relative to the Outfit's own source when not itself absolute — a
local directory join when the Outfit was read from disk, URL-relative
resolution when the Outfit was fetched from a URL — and fetched over HTTP when
it resolves to a URL. Fetching a remote `REMOTE` configuration SHALL happen
only at the point a `remote` subcommand, or `outfit apply`'s base-URL
fallback, actually resolves it. When no Outfit names one, the `default`
environment SHALL be used, so the command works outside any project.
Environment variables SHALL override individual values, and the region SHALL
fall back to the standard AWS region variable and then to the region named in
the URL. A missing or incomplete configuration SHALL fail saying where to put
it.

#### Scenario: Outfit names the configuration

- **WHEN** an Outfit sets `REMOTE ./remote.json` and a `remote` subcommand
  runs with that Outfit
- **THEN** the URLs come from that file, resolved beside the Outfit

#### Scenario: Outfit names an environment

- **WHEN** an Outfit sets `REMOTE qwen3.6-27b-prod` and a `remote` subcommand
  runs with that Outfit
- **THEN** the URLs come from that environment's `remote.json` in the registry

#### Scenario: Explicit Outfit without a REMOTE instruction

- **WHEN** a `remote` subcommand is given an Outfit that has no `REMOTE`
- **THEN** it fails saying that Outfit has no `REMOTE` instruction, rather than
  silently using the default environment

#### Scenario: No Outfit in play

- **WHEN** a `remote` subcommand runs outside a project
- **THEN** the `default` environment is used

#### Scenario: Configuration without a base URL

- **WHEN** a remote configuration names the control URLs and region but no base
  URL, and a `remote` subcommand runs
- **THEN** the subcommand works as it always has, since the endpoint reports its
  own address in the replies to `start` and `status`

#### Scenario: A remote configuration fetched over HTTP

- **WHEN** an Outfit sets `REMOTE https://example.com/team/remote.json`
- **THEN** a `remote` subcommand fetches that URL for the control
  configuration

#### Scenario: A REMOTE relative to a URL-sourced Outfit

- **WHEN** an Outfit fetched from `https://example.com/team/Outfit` sets
  `REMOTE ./remote.json`
- **THEN** the configuration resolves to
  `https://example.com/team/remote.json` and is fetched

#### Scenario: A remote REMOTE is fetched only by commands that resolve one

- **WHEN** `outfit serve` runs against an Outfit whose `REMOTE` is a URL
- **THEN** the `REMOTE` URL is never fetched — `serve` has no use for a
  remote endpoint's control configuration

#### Scenario: Applying names the environment even with an explicit BASEURL

- **WHEN** an Outfit with a URL-form `REMOTE` and its own `BASEURL`
  instruction is applied with `outfit apply`
- **THEN** the `REMOTE` URL is still fetched once, to name the harness
  provider after the deployment's environment (the same read a local-path
  `REMOTE` already triggers) — only the redundant base-URL lookup is skipped,
  since `BASEURL` already supplies it

### Requirement: Authenticated control requests

Requests to the control URLs SHALL be signed with the caller's own AWS
credentials, resolved from the standard credential chain, and SHALL carry the
hash of the request body so that a request with a payload is signed over that
payload. Outfit SHALL NOT store AWS credentials of its own.

Every control subcommand — `start`, `stop`, `status`, `deploy`, and `metrics` —
SHALL treat a non-success reply from the control endpoint as a failure: it SHALL
return an error and a non-zero exit, and SHALL NOT print an empty or partial
result as though the call succeeded.

A rejected request SHALL be reported with an actionable cause. When the request
is rejected because the caller's AWS credentials are expired or invalid, the
command SHALL say to refresh them (env credentials, a profile, or an SSO
session), distinct from the case where the credentials are resolvable but may
lack permission to invoke the endpoint.

#### Scenario: A request carrying a body is signed over it

- **WHEN** `outfit remote deploy` sends a configuration
- **THEN** the request is signed including the body's hash, not as an empty
  payload

#### Scenario: Credentials are missing

- **WHEN** no AWS credentials can be resolved
- **THEN** the command fails saying how to configure them

#### Scenario: Credentials are expired

- **WHEN** `outfit remote status` runs with expired or invalid AWS credentials
  and the control endpoint rejects the signed request
- **THEN** the command fails with a non-zero exit and a message saying to
  refresh the AWS credentials, rather than printing a blank state

#### Scenario: The endpoint rejects a control request

- **WHEN** any control subcommand receives a non-success HTTP reply from the
  control endpoint
- **THEN** the command reports the failure with its status and cause, and does
  not present the empty reply as a successful result

### Requirement: Deploying what the endpoint serves

`outfit remote deploy` SHALL derive the deployment from the Outfit and its
preset: `PROVIDER` SHALL select the inference engine, `MODEL` or the preset's
Hugging Face reference SHALL name the weights as a repository and optional
quantisation, `CONTEXT` or the preset's context size SHALL set the window,
`ALIAS` SHALL set the name the endpoint serves under (defaulting to the
repository), and the preset's remaining settings SHALL become the engine's
arguments. Settings the endpoint owns — host, port, model location, API key,
context size, alias, and metrics — SHALL be dropped, so one preset can both
serve locally and deploy unchanged. The request SHALL describe only what to
serve, never where the weights are stored. A `--dry-run` SHALL print the
derived deployment without sending it.

Deploy SHALL target a named environment: in addition to deriving what to serve,
it SHALL create and register that environment on the control plane (its Elastic
IP, instance configuration, per-environment API key and ingress, and SSM state),
as defined by the Environment Deployment specification. Deploying SHALL NOT start
the instance.

#### Scenario: A preset drives both serving and deploying

- **WHEN** an Outfit with a preset is deployed
- **THEN** the engine's arguments are the preset's, minus the settings the
  endpoint sets itself

#### Scenario: The Outfit overrides its preset

- **WHEN** the Outfit states a `MODEL` and `CONTEXT` that differ from the
  preset's
- **THEN** the Outfit's values are deployed

#### Scenario: A provider that is not a self-hosted engine

- **WHEN** an Outfit naming a hosted provider is deployed
- **THEN** the command fails saying that only a self-hosted engine can be
  deployed

#### Scenario: A local model file

- **WHEN** an Outfit naming a local model file is deployed
- **THEN** the command fails saying to name a repository instead, because the
  endpoint fetches its own weights

#### Scenario: Deploying creates and registers the environment

- **WHEN** a deployment succeeds against a bootstrapped account
- **THEN** the named environment is created and registered in the registry, and
  the report says whether the weights still have to be fetched before it can
  serve

#### Scenario: Deploying is not starting

- **WHEN** a deployment succeeds
- **THEN** the environment is configured but not started, and the report says
  whether the weights still have to be fetched before it can serve

### Requirement: Status reports when the endpoint last did work

`outfit remote status` SHALL report how long it has been since the endpoint's
engine last did any work, alongside the instance state and health it reports
already. The figure SHALL come from the activity the on-instance daemon
tracks, not from a measurement the control plane makes itself — one answer,
derived on the box, however it is asked for.

The figure SHALL be labelled "last active", matching the wording and duration
formatting used everywhere else this fact appears, so the same fact reads the
same way in every command.

Collecting it SHALL NOT make `status` slower than its health check already
makes it: the daemon SHALL be asked in parallel with the health check rather
than after it. Nor SHALL it introduce a side effect — `status` SHALL remain a
read, and SHALL still perform no TCP probe.

#### Scenario: A running endpoint reports its last activity

- **WHEN** the user runs `outfit remote status` against a running endpoint
  whose engine has served work
- **THEN** the output reports how long ago that work happened, labelled "last
  active", beside the state and health lines

#### Scenario: Status stays a read

- **WHEN** the user runs `outfit remote status`
- **THEN** nothing is started, stopped or probed in order to obtain the
  last-active figure

### Requirement: Status degrades when activity cannot be read

`outfit remote status` SHALL omit the last-active figure rather than fail,
report zero, or imply inactivity, whenever the figure cannot be obtained. That
covers an endpoint whose engine has not yet done any work, a daemon that
cannot be reached or answers unrecognisably, and an instance that is not
running — reaching the daemon needs a running box, so a stopped or undeployed
environment has nothing to report about its engine.

A failure to read the activity SHALL NOT affect the rest of the report: the
state and health lines SHALL be exactly what they are today, and the command
SHALL still succeed.

#### Scenario: A stopped instance reports no activity figure

- **WHEN** the user runs `outfit remote status` and the instance is stopped or
  undeployed
- **THEN** the output reports the state as it does today and shows no
  last-active figure

#### Scenario: An unreachable daemon does not spoil the report

- **WHEN** the endpoint is running but its daemon cannot be reached
- **THEN** the state and health lines are reported as they are today, no
  last-active figure is shown, and the command succeeds

#### Scenario: An engine that has done nothing yet

- **WHEN** the endpoint is running and its daemon reports no last-active time
- **THEN** no last-active figure is shown, rather than one implying the engine
  has been quiet since it started

