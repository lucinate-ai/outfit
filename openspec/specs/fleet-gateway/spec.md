# fleet-gateway Specification

## Purpose
One OpenAI-compatible endpoint in front of a fleet: a foreground process that
answers agent requests by choosing a node with the fleet's own selector, holds
each node's engine key, and wakes a node when nothing is serving what a request
asks for — so a machine running an agent needs nothing but a URL and one token.

## Requirements

### Requirement: The gateway command

`spinloop gateway` SHALL run in the foreground, the way `spinloop serve` does,
holding the fleet file it serves: `--fleet <path>` (with the `-f <path>` short
form) when given, otherwise `./fleet.yaml` in the working directory, and a
missing file SHALL fail naming the expected path, as the fleet commands do.
The gateway SHALL listen on an address given by `--listen`, defaulting to
port 4000 on all interfaces, and SHALL print the address to name in a fleet
file's `gateway` section when it starts.

The gateway's own startup failures SHALL be the fleet file's: a fleet file that
does not parse, or that names a token variable set nowhere, SHALL fail the
gateway at startup naming the problem, rather than listening and failing per
request.

#### Scenario: The gateway starts and answers

- **WHEN** the user runs `spinloop gateway --listen :4000` in a directory
  holding a `fleet.yaml`
- **THEN** it prints the address to name in the fleet file's `gateway` section
  and serves requests until it is stopped

#### Scenario: An explicit fleet file is served

- **WHEN** the gateway is given a `--fleet` path
- **THEN** that file is the one it serves

#### Scenario: A missing fleet file names itself

- **WHEN** the user runs `spinloop gateway` in a directory holding no
  `fleet.yaml` and passes no `--fleet`
- **THEN** it fails naming `./fleet.yaml`, and nothing listens

#### Scenario: A broken fleet file fails at startup

- **WHEN** the gateway's fleet file names a token variable that is set nowhere
- **THEN** the gateway fails at startup naming the node and the variable,
  rather than listening

### Requirement: Caller authentication

The gateway SHALL authenticate callers with one bearer token, on the same
terms the daemon's control API authenticates: the token MAY be supplied by a
file (`--api-token-file`), the environment (`SPINLOOP_API_TOKEN`), or the
command line (`--api-token`); giving more than one SHALL fail naming the
conflict. Requests without the correct token SHALL be rejected with `401`.
When no token is configured, the gateway SHALL refuse to listen on a
non-loopback address and SHALL say why, and listening on loopback without a
token SHALL be allowed.

The caller's token authorises use of the gateway only. It SHALL NOT be
forwarded to a node's engine or control API: the gateway reaches a node with
the node's own credentials, resolved from the fleet file the way every other
fleet client does.

#### Scenario: A wrong token is rejected

- **WHEN** a request carries a missing or incorrect bearer token
- **THEN** the response is `401` and no node is contacted

#### Scenario: A tokenless non-loopback listen refuses to start

- **WHEN** the gateway would listen on a non-loopback address and no token is
  configured
- **THEN** startup fails saying a token is required for non-loopback exposure,
  naming every way one can be supplied

#### Scenario: A tokenless loopback is permitted

- **WHEN** the gateway listens on a loopback address with no token configured
- **THEN** it serves requests without authentication

#### Scenario: The caller's token stops at the gateway

- **WHEN** an authenticated request is routed to a node
- **THEN** the node is reached with the node's own credentials from the fleet
  file, and the caller's token is not sent to it

### Requirement: Listing the fleet's models

The gateway SHALL serve `GET /v1/models` returning, in the OpenAI list shape,
the union of the models a request can reach. For each node whose state is
`running`, the list SHALL carry the name it reports serving — the served name
when it reports one, otherwise the model id. For each node that is not
running and is a wake candidate — waking is allowed for it (its own `wake`
setting, or the fleet-wide one when it names none) and it names a model to
start with — the list SHALL additionally carry that model, under the same
served-name-first naming a wake would start it with: a daemon node's own
Spinloop source describes it, and a remote node's own stats reply carries it
— the environment's stored deploy config, which the stats reply carries
whether the environment is running or stopped (unlike the status reply,
which only relays it while running). A running node SHALL contribute nothing
but what it reports: a running engine is never displaced, so its source's
model is not a request the gateway would answer from it. An undeployed remote
environment — one whose stats read fails outright, having no deploy config
to read — and a node for which waking is not allowed SHALL contribute
nothing beyond what is running, and duplicates SHALL be listed once. The
model a node would be started with SHALL be resolved at most once in a short
window shared by all models requests, so a burst does not re-read every
node's source or re-fetch every remote node's stats.

#### Scenario: Running models are listed

- **WHEN** two nodes are running, one serving a model under an alias and one
  under its id, and a models request is made
- **THEN** the response lists the alias and the id, each once

#### Scenario: A stopped node's wakeable model is listed

- **WHEN** a node is stopped, its own source describes a model, and waking is
  allowed for it, and a models request is made
- **THEN** the response lists the model the source describes, beside what the
  running nodes serve

#### Scenario: A stopped node's model is not listed when wake is off

- **WHEN** a node is stopped and waking is not allowed for it, and a models
  request is made
- **THEN** the response lists only what the running nodes serve

#### Scenario: A deployed remote environment's model is listed

- **WHEN** a remote environment is stopped, its stats reply reports what its
  stored deploy config would serve, and waking is allowed for it, and a
  models request is made
- **THEN** the response lists that model beside what the running nodes serve

#### Scenario: A stopped remote environment's model is not listed

- **WHEN** a remote environment is stopped and has nothing deployed, and a
  models request is made
- **THEN** the response does not list it: the gateway has nothing stored to
  start it with

#### Scenario: A running node's source adds no second model

- **WHEN** a running node reports one model and its source describes another,
  and a models request is made
- **THEN** the response lists only what the node reports

#### Scenario: A burst of models requests resolves each source once

- **WHEN** several models requests arrive within the window in which a node's
  source or a remote node's status is resolved
- **THEN** each node's source or status is read once for the burst

#### Scenario: Nothing reachable lists nothing

- **WHEN** no node is running and nothing is wakeable — no stopped node
  describes a model, or waking is not allowed for any of them — and a models
  request is made
- **THEN** the response is an empty list, not an error

### Requirement: Routing a request to a node

The gateway SHALL serve `POST /v1/chat/completions` and `POST /v1/completions`
by choosing a node and reverse-proxying the request to that node's engine. The
choice SHALL be the fleet's own selection: every node's state is considered, a
node matches when what it reports serving — its model id or its served name —
equals the model the request names, and matching nodes are ranked by the fleet
file's activity preference, ties broken by fleet-file order. A request naming
no model SHALL be refused saying so, not routed at a guess.

A node whose daemon reports the engine not ready SHALL NOT be selected: the
state turns running when the engine's process exists, which is before the
weights are fetched and loaded, and during that window nothing is listening on
the engine's port. A node whose daemon reports no readiness at all SHALL NOT be
disqualified: the absence of a reading is not evidence of not-readiness.

When nothing is serving the model the request names, a node already running it
whose engine has not answered yet SHALL be waited for rather than passed over:
it is loading the weights the request needs, so it serves sooner than anything
a wake would start from cold, and waking a second node would leave two engines
up for one request. The wait SHALL be bounded by the wake timeout, SHALL name
the node it holds the request for, and a node that does not answer in time
SHALL fail the request naming it, leaving its engine running. Waiting is not
waking: a fleet whose wake is off SHALL still hold a request for a node already
loading the model it named, since no engine is being started.

The request's body SHALL reach the chosen engine unmodified, and the engine's
reply SHALL reach the caller unmodified, including a streamed reply, which the
gateway SHALL pass through without holding it back. The gateway SHALL replace
the caller's authorisation with the engine key it holds for that node — the
value the node's fleet entry names, resolved the way every other fleet client
resolves it — and SHALL send no authorisation at all to an engine that needs
none. The gateway SHALL NOT retry a failed request at another node: the reply
the chosen engine gives is the reply the caller gets.

The engine's address SHALL be resolved the way routing resolves it: the node's
declared engine override as given, otherwise the node's host with the port and
path the engine reports. Where a node reports its engine's host — a remote
environment, whose control plane publishes the instance's address and which the
fleet file names by environment alone — that reported host SHALL be used in
place of the node's host, so the request reaches the instance rather than an
address the fleet file never held. A node that reports its engine bound to
loopback, without an override taking responsibility for reachability, SHALL NOT
be selected: it answers only on its own machine, and where no other node serves
the wanted model the request SHALL fail saying so and naming the fix.

The gateway SHALL reuse node state it has recently read — a reading taken
within the last couple of seconds — rather than query every node on every
request, so a burst of requests does not pay a fan-out each. The freshness of
the reading SHALL NOT matter to the choice: the same fleet in the same state
chooses the same node.

Each routed request SHALL be logged as one line — the model, the node chosen,
and the outcome — and the gateway SHALL log no request body.

#### Scenario: A request goes to the node serving its model

- **WHEN** one node is running the model a request names and another is
  running a different model
- **THEN** the request is proxied to the first node and the engine's reply
  reaches the caller unmodified

#### Scenario: The engine key is swapped, not the caller's token

- **WHEN** a request is routed to a node whose engine is gated, and the node's
  fleet entry names the key
- **THEN** the engine receives the key's value as its authorisation, and the
  caller's token is not sent to the engine

#### Scenario: A streamed reply passes through

- **WHEN** a streamed completion is requested and the chosen engine streams its
  reply
- **THEN** the caller receives the stream as the engine sent it

#### Scenario: An engine error is the caller's error

- **WHEN** the chosen engine refuses the request
- **THEN** the engine's refusal reaches the caller and no other node is tried

#### Scenario: A request naming no model is refused

- **WHEN** a completion request carries no model
- **THEN** it is refused saying the request names no model, and no node is
  contacted

#### Scenario: A loopback-bound engine is not selected

- **WHEN** the only node serving the wanted model reports its engine bound to
  loopback and names no engine override, and the node is not reached over
  loopback
- **THEN** the request fails saying the engine answers only on that machine,
  naming the bind and the override as the fixes

#### Scenario: A not-ready engine is not selected

- **WHEN** the only node running the model a request names reports its engine
  not ready, and the fleet's wake is off
- **THEN** the request fails naming that node marked not ready, and nothing is
  started

#### Scenario: A request waits for an engine that is still starting

- **WHEN** no node is serving the model a request names, one node is running
  it but its engine has not answered yet, and no other node is running it
- **THEN** the request is held for that node and answered when its engine
  answers, and no other node is started

#### Scenario: A still-starting engine that does not answer fails the request

- **WHEN** a node is running the model a request names, its engine has not
  answered yet, and it does not answer within the wake timeout
- **THEN** the request fails saying so, naming the node, and the engine is left
  running

#### Scenario: Wake off still waits for an engine already starting

- **WHEN** the fleet file declares the wake policy off and the only node
  running the model a request names has not answered its engine yet
- **THEN** the request is held for that node and answered when the engine
  answers, since waiting starts nothing

#### Scenario: A burst of requests does not fan out per request

- **WHEN** several requests arrive within a couple of seconds of each other
- **THEN** the fleet is queried once for the burst, and the requests are
  answered from that reading

#### Scenario: A routed request leaves one log line

- **WHEN** a request is routed and answered
- **THEN** the gateway's log gains one line naming the model, the node, and the
  outcome, and carries no part of the request body

### Requirement: Waking a node for a request

When no running node serves the model a request names, and waking is allowed
for at least one candidate, the gateway SHALL start an engine on a node that
is not running one, and SHALL hold the request until the engine answers.
Waking is allowed for a node when its own `wake` setting says so, or,
when it names none, the fleet file's wake policy does. A node is a wake
candidate when it is not running, waking is allowed for it, and it names a
model to start with, matching the one the request asks for:

- A daemon node names one through the Spinloop source it names — its `file`
  field, a registered alias named after it, or a same-named directory beside
  the fleet file, resolved the way `spinloop fleet start` resolves it —
  describing a config whose model or served name is the one the request asks
  for.
- A remote node names one through its own stats reply, which reads the
  environment's stored deploy config directly and so carries its model id
  whether the environment is running or stopped — unlike its status reply,
  which only relays the deploy config while running, and unlike the stats
  reply itself, which carries no served name. An undeployed remote
  environment's stats read fails outright, having no deploy config to read;
  it names nothing and is not a candidate.

A node is started with what it names, never with a config invented for the
request: a daemon node is started with the Spinloop source's config; a remote
node is started as it is — its stored deploy config decides what it serves,
and the gateway pushes it nothing new. Candidates whose stored config already
names the model SHALL be tried first, since they have the weights, and the
rest in fleet-file order. A node that refuses the start — a runner or model it
cannot serve — SHALL NOT fail the request while other candidates remain.

A daemon engine started this way SHALL be gated with the key the node's fleet
entry names, supplied by the gateway: the gateway is the client that starts
the engine, so the key the client sets is the key the engine takes. A remote
environment's engine is gated by its own key, resolved the same way a request
already routed to it resolves one; the gateway does not change it. The wait
SHALL be bounded by a wake timeout, defaulting to five minutes and
overridable by `--wake-timeout`; exceeding it SHALL fail the request saying
the engine did not answer in time, and the started engine SHALL be left
running rather than stopped, so a slow load — or, for a remote node, a slow
boot — is not thrown away.

When several requests ask for a model nothing is serving at once, the gateway
SHALL start at most one engine per node and answer every request from it: the
first request's wait is the wait the rest join, regardless of the node's
kind. A daemon node's own control API refuses a second concurrent start on
its own, but a remote environment's control plane does not, so the gateway
SHALL NOT rely on that alone: two requests racing to wake the same node
SHALL be coalesced before either reaches the node, not just reconciled after
one of them answers. A node another request woke first SHALL be used the
same way, and only once its engine answers.

A request for a model nothing is serving, and for which waking is not allowed
on any node that names it, SHALL fail without starting anything, naming the
nodes and what they could serve, and the command that would start one. A
model no node is running and no node names — no daemon source describes it
and no remote node is deployed with it — SHALL fail the same way regardless
of any wake setting: nothing to wake with, and the failure SHALL say so
rather than trying to start a node with nothing.

#### Scenario: A cold request wakes a node and is served

- **WHEN** no node is running the model a request names, one node's Spinloop
  source describes it, and waking is allowed for it
- **THEN** that node is started with its own config, gated with the key its
  fleet entry names, and the request is answered once the engine answers

#### Scenario: A cold request wakes a deployed remote environment

- **WHEN** no node is running the model a request names, one remote node's
  stats reply reports it is deployed to serve it, and waking is allowed for
  it
- **THEN** that environment's instance is started, its own stored deploy
  config decides what it serves, and the request is answered once its engine
  answers

#### Scenario: An undeployed remote node is not a wake candidate

- **WHEN** the only node whose name could match a request is a remote
  environment with nothing deployed
- **THEN** it is not started, and the failure says nothing is deployed to
  serve the model, naming the deployment path

#### Scenario: The request is held while the engine loads

- **WHEN** the woken node reports running while its engine is still loading
- **THEN** the request waits, and is answered when the engine answers, rather
  than failing against an endpoint that refuses connections

#### Scenario: A wake that does not finish in time fails the request

- **WHEN** a woken node's engine does not answer within the wake timeout
- **THEN** the request fails saying so, naming the node, and the engine is left
  running

#### Scenario: Concurrent cold requests share one wake

- **WHEN** two requests arrive at once for a model nothing is serving, and one
  node's source describes it
- **THEN** that node is started once, and both requests are answered from the
  same engine

#### Scenario: Concurrent cold requests share one remote wake

- **WHEN** two requests arrive at once for a model nothing is serving, and one
  remote node's stats reply reports it is deployed to serve it
- **THEN** that environment's instance is started once, not once per request,
  and both requests are answered once its engine answers

#### Scenario: A woken engine takes the gateway's key

- **WHEN** the gateway starts an engine on a daemon node whose fleet entry
  names an engine key
- **THEN** the engine is gated with that value, the gateway's requests to it
  carry it, and no reply to any caller contains it

#### Scenario: Wake refused by the fleet file

- **WHEN** waking is not allowed for any node that could serve the model a
  request names, and no node is serving it
- **THEN** nothing is started, and the request fails naming the node whose
  source describes the model and the command that would start it

#### Scenario: A remote node opted out is not woken though the fleet wakes

- **WHEN** the fleet file's wake policy is `on`, a stopped remote node
  declares its own `wake: off`, and it is the only node that names the
  model a request asks for
- **THEN** it is not started, and the failure names it and says waking is
  disabled for that node

#### Scenario: Nothing can serve the model

- **WHEN** no node is running the model a request names, no node's Spinloop
  source describes it, and no remote node is deployed with it
- **THEN** the request fails, naming each node and why it cannot serve the
  model, and nothing is started

#### Scenario: A sourceless node is not woken

- **WHEN** the only node that could take a request names no Spinloop source
  that resolves
- **THEN** it is not started, and the failure names it and the ways a source
  could have been given

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

### Requirement: Paths the gateway does not serve

A path other than `/v1/models`, `/v1/chat/completions`, `/v1/completions`, and
the gateway's own health path SHALL be answered with `404` naming the paths the
gateway serves. The health path SHALL answer that the gateway is up without
contacting any node, so an operator can tell the gateway down from the fleet
down.

#### Scenario: An unknown path is named as such

- **WHEN** a request is made to a path the gateway does not serve
- **THEN** the response is `404` and names the paths it does serve

#### Scenario: The health path does not touch the fleet

- **WHEN** the health path is requested and every node is unreachable
- **THEN** it still answers that the gateway is up

### Requirement: Serving the fleet's topology

The gateway SHALL serve the fleet's topology over a read endpoint, behind the
same caller authentication as everything else it serves: a caller with the
token gets it, a caller without does not, exactly as on its other paths.

The reply SHALL describe each node of the fleet the gateway holds: its name,
its kind, its tags as the fleet file declares them, its state, and its
serving facts — the model it serves when it is running, the name it serves
that model under where it reports one, whether its engine has answered, and
when it was last active. For a node that is not running, the reply SHALL name
the model a request would start it with, where the node names one — a daemon
node's own source, or a remote node's own stats reply — and waking is
allowed for it (its own `wake` setting, or the fleet's when it names none); a
node that names no such model, or for which waking is not allowed, SHALL
report none. A node that does not answer SHALL be reported as such in the
fleet's order, not fail the whole reply.

The reply SHALL carry the fleet file's fleet-level settings the way the file
declares them: whether the fleet wakes, how it ranks, and its concurrency
limits where it declares any, with each absent where the file declares it
not. The gateway's fleet file remains the single source of truth for the
topology: the reply is what the file says and what the nodes report, and the
gateway holds no copy of either beyond what it already holds.

#### Scenario: A caller with the token reads the topology

- **WHEN** a caller sends the gateway's token to the topology endpoint
- **THEN** it gets every node with its tags, state, and serving facts, and
  the file's wake policy, ranking, and concurrency limits

#### Scenario: A caller without the token is refused

- **WHEN** a caller sends no token, or the wrong one, to the topology
  endpoint
- **THEN** it is refused the way the gateway's other paths refuse it

#### Scenario: A stopped node reports what it would start

- **WHEN** a node is not running, names a model to start with, and waking is
  allowed for it
- **THEN** the topology names that model as what a request would start the
  node with

#### Scenario: A stopped, deployed remote node reports what it would start

- **WHEN** a remote node is stopped, its stats reply reports its stored
  deploy config, and waking is allowed for it
- **THEN** the topology names that config's model as what a request would
  start it with, the same way a daemon node's is named

#### Scenario: A dead node does not sink the reply

- **WHEN** one of the fleet's nodes does not answer and a caller reads the
  topology
- **THEN** that node is reported as not answering, in the fleet's order, and
  the rest of the fleet is reported as usual

#### Scenario: Absent settings are absent

- **WHEN** the fleet file declares no concurrency limits
- **THEN** the topology carries none, rather than a default the file never
  named
