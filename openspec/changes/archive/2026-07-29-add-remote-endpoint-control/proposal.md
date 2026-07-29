## Why

A model too large for a laptop has to run somewhere else, but the moment it
does, the Outfit stops being the whole story: the endpoint has to be started
before use and stopped after, and something has to tell it which model to load.
Doing that by hand — cloud console, then a config edit, then remembering to
shut it down — is exactly the fiddling `outfit` exists to remove, and it drifts:
the model the endpoint serves and the model the harness asks for are maintained
in two places and quietly disagree.

An Outfit already describes a model precisely enough to serve it, which
`outfit serve` proves locally. The same description can drive a remote engine,
making local and cloud the same declaration pointed at a different machine — so
this change delivers both halves: the command, and the deployment it commands.

## What Changes

- A new `outfit remote` command group — the CLI's first nested subcommand —
  with `start`, `stop`, `status` and `deploy`. It targets the scale-to-zero
  endpoint defined by this repository's `remote/` subproject, calling its
  control Lambdas over SigV4-signed Function URLs.
- A new `REMOTE` Outfit instruction naming the file that holds those URLs,
  resolved relative to the Outfit like `PRESET`, so an Outfit and the endpoint
  it belongs to travel together. Without it, a per-user config is used, so
  `outfit remote` still works outside a project.
- `outfit remote deploy` derives what to serve from the Outfit and its preset:
  `PROVIDER` selects the engine, `MODEL` or the preset's `hf` the weights,
  `CONTEXT` or the preset's `ctx-size` the window, `ALIAS` the served name, and
  the preset's remaining flags the engine's own arguments. Settings the remote
  owns (host, port, model path, API key, context, alias, metrics) are dropped,
  so one preset serves locally and deploys unchanged.
- A `vllm` provider in the catalogue, so both self-hostable engines can be named
  by `PROVIDER` and swapping between them is an edit to one line.
- An `apiKeyOptional` flag on a catalogue provider, for one that works both
  unauthenticated (a local server) and authenticated (the same engine deployed
  remotely). `llamacpp` becomes such a provider.
- Tab completion for a nested command: `outfit remote <TAB>` offers the
  subcommands, and the argument after one completes as an Outfit.
- `start` reports progress. The endpoint blocks until the model is serving, so a
  cold start previously sat silent for minutes; it now says what it is doing and
  repeats with the elapsed time. Progress goes to stderr and only the exports to
  stdout, so the output stays pipeable.
- Applying a config that cannot authenticate is called out. A remote endpoint
  with no resolvable key succeeded silently and failed later as a rejected
  request; it now warns, naming the variable to set.
- **A deployment that runs the model**, under `remote/`: an endpoint that holds
  no instance until asked for one, launches into whichever zone has GPU
  capacity, and terminates itself once unused — so it costs nothing at rest and
  cannot quietly run all night. A retention marker, a maximum runtime and a
  grace period bound it in that order.
- **A choice of inference engine**, llama.cpp or vLLM, made per deployment
  rather than baked in. What to serve — engine, model, quantisation, context,
  engine flags — is a single stored configuration the endpoint reads when it
  starts, so changing model is a deploy, not a rebuild.
- **Weights kept outside the machine image**, in object storage, at a location
  derived from the engine, model and quantisation. Deploying a model whose
  weights are absent fetches them first, so nothing has to be staged by hand.
- Nothing identifying a deployment may be committed: account ids, ARNs,
  endpoint hostnames, allocated addresses, bucket names, resource ids. This
  repository is public, so `scripts/check-no-cloud-identifiers.sh` fails the
  build on any of them, and a deployment's own state stays in files that are
  never committed.
- **BREAKING** for opencode users who relied on the config carrying the key:
  the key is now referenced as `{env:VAR}` and resolved when opencode runs, so
  the variable must be set — `outfit harness` passes on whatever outfit can
  resolve, and an explicit export always wins.

Everything else is additive: every new instruction and command is new surface,
and an Outfit without `REMOTE` behaves exactly as before. The one behavioural
change is the opencode key above — a deliberate reversal of the previous choice
to embed the secret, which was justified by a global config being unable to rely
on a project-local `.env`. Passing resolved keys to the launched agent removes
that justification, and stops writing a secret to disk.

## Capabilities

### New Capabilities

- `remote-endpoint`: controlling a remote inference endpoint from an Outfit —
  discovering its configuration, starting and stopping it, reporting its state,
  and deploying what it serves.
- `endpoint-lifecycle`: an endpoint that exists only while it is used —
  starting on demand, judging whether it is still wanted, and the bounds that
  decide when it is torn down.
- `inference-runners`: which engine serves a deployment and how it is
  configured, including the stored description of what to serve.
- `model-weights`: where a model's weights live, how their location is derived,
  and how they get there.

### Modified Capabilities

- `outfit-files`: the instruction set gains `REMOTE`, and Outfit path
  resolution now also covers the `remote` subcommands.
- `provider-catalog`: a provider entry may declare that its API key is
  optional.
- `pi-integration`: the keyless-provider placeholder is now conditioned on the
  endpoint being local, so a remote endpoint keeps the reference Pi resolves at
  run time.
- `opencode-integration`: the API key is referenced as `{env:VAR}` rather than
  embedded, so no secret is written to disk.
- `harness-management`: the launched agent inherits the API keys outfit can
  resolve, since neither harness stores the secret itself.
- `provider-selection`: a key's `.env` is resolved beside the Outfit being
  applied rather than relative to the tool.
- `shell-completion`: completion covers a command's subcommands, not only its
  flags and positionals.

## Impact

- **Code**: new `internal/remote` (the only package making network calls or
  using the AWS SDK) and `cmd/outfit/remote.go`; the Outfit parser, the
  catalogue schema, both harness adapters, the key resolver, and the completion
  table. The deployment under `remote/` — a CDK application, and the
  repository's only non-Go code.
- **CI**: a `No cloud identifiers` step ahead of everything else, and a separate
  `Remote deployment` workflow that typechecks, tests and synthesizes `remote/`
  when it changes, so the deployment cannot rot untested.
- **Dependencies**: `aws-sdk-go-v2` (config + SigV4 signer) — the project's
  first non-stdlib runtime dependency outside YAML parsing. It is reached only
  by `outfit remote`; every other command stays offline.
- **Credentials**: `outfit remote` uses the caller's own AWS credential chain
  and needs only `lambda:InvokeFunctionUrl`. No AWS permissions are needed by
  any other command, and none are stored by outfit.
- **Internal contract**: the deploy payload is consumed by the deployment's own
  deploy function, which owns the storage layout and fetching the weights. The
  CLI states intent; where anything is stored is not its business, which is why
  the payload names no location.
- **Cost**: the deployment bills for GPU time only while an instance is up, so
  the lifecycle bounds are the difference between minutes and hours of it.
- The implementation for this change already exists on the `feat/remote`
  branch; this change records the specification it introduced.
