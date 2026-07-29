## Context

Until now every command has been offline and local: read a catalogue, write a
harness config, or launch a server on this machine. Controlling a remote
endpoint breaks both properties — it makes network calls and it authenticates —
so the main design pressure is keeping that blast radius small enough that the
rest of the tool is unaffected.

The endpoint itself is this repository's `remote/` subproject: a scale-to-zero
GPU instance fronted by control Lambdas. It is scale-to-zero precisely because
a GPU costs money by the hour, which is why starting and stopping are
first-class commands rather than something the user is trusted to remember.

## Goals / Non-Goals

**Goals:**

- One declaration. The Outfit that describes a model locally describes the same
  model remotely, so the name a coding agent asks for and the name the endpoint
  serves cannot drift apart.
- Keep the AWS dependency confined to one package and one command group.
- Require no credentials of outfit's own: use the caller's AWS chain, and the
  narrowest permission that works.
- Fail loudly and early. A misconfigured deployment should be a clear error
  before anything is launched, not a server that boots and then cannot serve.

**Non-Goals:**

- Provisioning. This creates no infrastructure; the endpoint is deployed by its
  own project, and outfit only tells it what to serve.
- Being a general AWS or cloud client. It calls three URLs.
- Modelling the endpoint's storage. Where weights live and how they get there
  is the endpoint's business.
- Supporting arbitrary providers remotely. Only a self-hostable engine can be
  deployed, because only those have something to deploy.

## Decisions

**`PROVIDER` names the engine; there is no `RUNNER` keyword.** An Outfit
already says `llamacpp`, and `outfit serve` already runs llama.cpp from it. The
remote case is the same statement pointed elsewhere, so the engine is read from
`PROVIDER` and the command decides local or remote. The alternative — a new
keyword — would let an Outfit say `PROVIDER llamacpp` with `RUNNER vllm`, a
contradiction worth making unrepresentable. The cost is that a provider which
is not a self-hosted engine cannot be deployed; that is an error, not a gap.

**The deployment describes intent, not storage.** The request carries the
engine, model, quantisation, context, served name and engine flags — and
deliberately no storage location. The endpoint derives its own layout and
fetches the weights when they are missing. This keeps a storage-layout change
on the endpoint's side from becoming an outfit release, and means a caller
cannot point a deployment at the wrong place.

**The preset is the single source of serving detail.** Rather than a second
list of remote flags, the existing preset is reused and the settings the
endpoint owns (host, port, model location, API key, context, alias, metrics)
are dropped from it. So one file serves locally and deploys unchanged. The
subtlety is that flags must be matched after alias resolution — `ngl` and
`n-gpu-layers` are the same setting — and that a preset's `[*]` defaults are a
separate layer that must be merged, or settings written once for every model
would silently vanish.

**Signing covers the body.** The control URLs use IAM auth, so requests are
SigV4-signed. `deploy` is the first request with a payload, and the signature
covers the payload hash and `Content-Length`; both are therefore computed from
the exact bytes sent rather than assumed empty.

**`deploy_url` is optional.** A configuration written before `deploy` existed
still works for `start`/`stop`/`status`; only `deploy` complains, and it says
what to add. Validating it up front would break working setups for a command
they do not use.

**The command and the deployment ship together.** They are one feature — a
change to what the deploy request carries touches both sides — so they live in
one repository and are reviewed as one unit. The cost is a polyglot repository,
a TypeScript CDK project beside a Go CLI, handled by giving the deployment its
own workflow rather than bending the Go one around it.

**Terminate, do not stop.** A stopped instance still bills for its disk, and
the endpoint is stateless — the weights are fetched at start — so there is
nothing worth keeping between sessions. Terminating means the resting cost is
storage and an address, and a forgotten session costs minutes rather than a
night. The price is a slower start, which the lifecycle bounds are designed
around rather than against.

**Nothing identifying a deployment may be committed.** This repository is
public, so it is enforced by a check rather than a convention: the build fails
on an account id, ARN, endpoint hostname, public address, bucket name or
resource id. Documentation and tests use the reserved ranges instead. Logical
stack names and tag keys are deliberately *not* covered — they are chosen in
source and identify the running stack, so changing them would orphan it.

**Progress belongs on stderr, results on stdout.** `start` blocks for minutes
while the model loads, so it has to report; but its useful output is two shell
exports. Splitting them means `eval "$(outfit remote start)"` works while the
person watching still sees progress. The alternative — progress on stdout, as it
was — forced a `grep` in the documented idiom and threw the progress away.

**An optional API key is a catalogue fact, not a special case in code.** The
same provider covers a keyless local server and an authenticated remote one, so
the catalogue marks the key optional and the harness builders read that. The
alternative — inferring it from a localhost base URL, or always falling back to
a placeholder — would either be a guess or would break providers whose key is
genuinely resolved later.

## Risks / Trade-offs

- **A network- and credential-bearing dependency in an otherwise offline tool.**
  → It is confined to `internal/remote` and reached only by `outfit remote`;
  every other command still runs with no credentials and no network.
- **Deploying is not starting, so a deployment can be "live" while its weights
  are still being fetched.** → The response says whether a fetch was started,
  and the command tells the user to wait before starting.
- **The Outfit's context wins over the preset's, and human-friendly sizes are
  decimal — `128k` is 128000, not 131072.** → Consistent with `outfit serve`,
  where the same precedence already applies, so the two cannot disagree; the
  exact value can always be written out in full.
- **Only one endpoint per Outfit.** → Deliberate: an Outfit describes one
  selection, and a second endpoint is a second Outfit.
- **The catalogue is embedded at build time**, so a stale binary applies an old
  catalogue and appears to ignore a fix. → Documented as a trap in AGENTS.md;
  the runtime override reads a file instead.
- **A public repository now holds the deployment's source**, so a careless
  example could disclose a real account. → The identifier check runs first in
  CI, and its patterns were verified against real samples of each class.
- **The keys the launched agent receives are wider than one provider's.** →
  Only variables outfit can already resolve are passed, never invented, and
  anything already in the environment is left as it is.

## Migration Plan

Additive throughout: an Outfit with no `REMOTE` behaves exactly as before, and
no existing command changes behaviour. Rollback is dropping the command group;
nothing persists on the user's machine beyond the configuration file they
wrote.

## Open Questions

- Whether `status` should also report the deployed configuration (the endpoint
  can return it) or stay a liveness check.
- Whether a future harness-facing command should read the served model name
  back from the endpoint rather than trusting `ALIAS`, once more than one
  Outfit can target the same endpoint.
