# harness-remote-env Specification

## Purpose

Making a cloud endpoint's credentials reach the launched agent without the
user doing anything. `spinloop harness --env <name>` fetches the named
environment's endpoint environment variables and injects them into the child
process, so the agent authenticates against a key that only ever existed in
Secrets Manager. The failure mode this replaces is silent: a user who forgets
to `eval` the exports gets an agent that cannot connect, so no way this can
fail is allowed to pass unremarked — and when it leaves the agent with no key
at all, it fails before the harness launches.

## Requirements

### Requirement: harness injects the endpoint's credentials
When `spinloop harness` is given an `--env/-e <name>` flag, it SHALL automatically obtain the named environment's endpoint environment variables and inject them into the child process environment, so the agent authenticates against a key that only ever existed in Secrets Manager. The failure mode this replaces is silent: a user who forgets to `eval` the exports gets an agent that cannot connect, so no way this can fail is allowed to pass unremarked — and when it leaves the agent with no key at all, it fails before the harness launches.

The fetch SHALL happen before the Spinloop is applied, and the apply SHALL resolve the provider's API key variable against the fetched key as well as the local environment. The config the apply writes is therefore complete, and the apply SHALL NOT warn that no API key is set when the launch is about to supply one. The fetched key SHALL be used only where nothing local supplies a value, so an exported key or one in the adjacent `.env` still wins, and it SHALL satisfy only the API key variable — no other lookup.

The key SHALL reach the agent through its environment alone, and SHALL NOT be written into any harness config. Where a harness reads the key under its own name — lucinate's `LUCINATE_OPENAI_API_KEY` — the fetched key SHALL satisfy that too.

The flag SHALL name a registered environment (see the Cloud Environments
specification), and its environment name SHALL key the applied provider and
supply the applied base URL exactly as an apply given the flag does on its
own. A launch given `--env` alongside a fleet — a `./fleet.yaml` in force or a
`--fleet` flag — SHALL fail naming both, since each is an answer to where the
model is served from (see the `fleet-routing` specification).

#### Scenario: harness injects OPENAI_BASE_URL and OPENAI_API_KEY
- **WHEN** the user runs `spinloop harness --env dev-2` and the endpoint is running
- **THEN** the launched harness process receives `OPENAI_BASE_URL` and `OPENAI_API_KEY` in its environment

#### Scenario: harness calls env Lambda to fetch key
- **WHEN** the user runs `spinloop harness --env dev-2`
- **THEN** the command calls the cloud env Lambda (not Start) for `dev-2` to obtain the `api_key` and `base_url` from the response

#### Scenario: harness fails when endpoint is stopped
- **WHEN** the cloud instance is stopped, no API key is set locally, and the user runs `spinloop harness --env dev-2`
- **THEN** the command fails with an error telling the user to run `spinloop cloud start` first

#### Scenario: the apply does not warn about a key the launch supplies
- **WHEN** the user runs `spinloop harness --env dev-2`, no `OPENAI_API_KEY` is set locally, and the endpoint is running
- **THEN** the apply reports that the key is read from the environment when the harness runs, and does not warn that no key was set

#### Scenario: harness informs user it is fetching cloud env
- **WHEN** the user runs `spinloop harness --env dev-2`
- **THEN** a message is printed to stderr naming the environment, so the user knows a network call is happening

#### Scenario: harness without --env is unaffected
- **WHEN** the user runs `spinloop harness` with no `--env` flag
- **THEN** the command behaves as before with no cloud Lambda calls

#### Scenario: existing env vars are not overridden
- **WHEN** `OPENAI_BASE_URL` or `OPENAI_API_KEY` is already set in the user's shell environment
- **THEN** the existing value is preserved (the cloud value is only injected when the variable is not already set)

#### Scenario: lucinate receives the fetched key under its own name
- **WHEN** the user runs `spinloop harness -H lucinate --env dev-2` and the endpoint is running
- **THEN** the launched lucinate process receives the fetched key as `LUCINATE_OPENAI_API_KEY`

#### Scenario: --env and a fleet conflict
- **WHEN** the user runs `spinloop harness --env dev-2` in a directory holding a `fleet.yaml` (or `--fleet` is given)
- **THEN** the launch fails naming both the `--env` flag and the fleet, rather than choosing one

### Requirement: harness cloud error is loud
When the named environment's endpoint cannot be fetched during `spinloop harness`, the command SHALL report the failure on stderr rather than discarding it, and SHALL bound the attempt with a timeout so an unresponsive control plane cannot block the launch indefinitely.

The failure SHALL be fatal — before the harness is launched and before its config is written — when no API key is otherwise available to the launched agent, because the endpoint refuses every request without one. The error SHALL name the environment, carry the underlying cause, and say how to resolve it: start the endpoint, or set the key.

When an API key is already available — exported, in the `.env` beside the Spinloop, or set by an `ENV` instruction, which overrides both in the launched agent's environment — the fetch was only a convenience, so the command SHALL warn and carry on.

#### Scenario: AWS credentials failure surfaces early
- **WHEN** AWS credentials are not available, no API key is set locally, and the user runs `spinloop harness --env dev-2`
- **THEN** the command fails with a clear error before attempting to launch the harness

#### Scenario: cloud not deployed surfaces early
- **WHEN** the environment named by `--env` has no registered configuration (no deploy yet) and the user runs `spinloop harness --env dev-2`
- **THEN** the command fails with a clear error naming the environment and saying to deploy it

#### Scenario: missing env_url in config surfaces early
- **WHEN** the environment's configuration lacks an `env_url` field and the user runs `spinloop harness` for it
- **THEN** the command fails with an error indicating the cloud deployment needs to be updated

#### Scenario: a fatal fetch leaves the harness config untouched
- **WHEN** the fetch fails with no API key available and the user runs `spinloop harness --env dev-2`
- **THEN** the harness config is not written

#### Scenario: an available key downgrades the failure to a warning
- **WHEN** the fetch fails but `OPENAI_API_KEY` is already set in the environment, in the `.env` beside the Spinloop, or by an `ENV` instruction
- **THEN** the failure is reported on stderr and the harness is launched with the key that is available

### Requirement: harness auto-configures from a deployed environment
When `spinloop harness --env <name>` is run with no Spinloop applied — no leading alias or path, and no `--spinloop`/`-O` — the command SHALL fetch the named environment's live environment response (the same fetch that supplies `OPENAI_BASE_URL`/`OPENAI_API_KEY`) and, when it carries a deploy-config, synthesise a provider selection from it rather than doing nothing with the flag.

The deploy-config's runner SHALL become the catalogue provider, by the same mapping `spinloop cloud deploy` uses in reverse (a runner is a catalogue provider's engine kind). The deploy-config's served model name SHALL become the model key. The deploy-config's context size, when present, SHALL set the context window. The harness SHALL then be configured and launched exactly as it would be for a Spinloop stating the same `PROVIDER`, `ALIAS` and `CONTEXT` with the same `--env <name>` — the same environment labelling, base URL, and injected credentials as the existing `--env` behaviour.

A Spinloop applied alongside `--env` — a leading alias or path, or `--spinloop`/`-O` — SHALL continue to use its own `PROVIDER`, `ALIAS`, `MODEL` and `CONTEXT` exactly as today; the deploy-config's fields SHALL NOT override a value the Spinloop states.

#### Scenario: bare --env configures and launches the harness
- **WHEN** the user runs `spinloop harness --env dev-3` with no Spinloop applied, and a model is deployed to `dev-3`
- **THEN** the harness is configured with `dev-3` as the provider, the deployed served model name as the model, the deployed context size as the window, and is launched with the fetched base URL and API key in its environment

#### Scenario: an applied Spinloop still wins
- **WHEN** the user runs `spinloop harness some-alias --env dev-3` (or `--spinloop=<path> --env dev-3`) and the Spinloop states its own `PROVIDER` and `ALIAS`
- **THEN** the Spinloop's values configure the harness; the environment's deploy-config is not consulted for them

#### Scenario: trailing args are still forwarded
- **WHEN** the user runs `spinloop harness --env dev-3 --prompt "hello"` with no Spinloop applied, and a model is deployed to `dev-3`
- **THEN** the harness is auto-configured from `dev-3` and launched with `--prompt hello` forwarded to it

### Requirement: harness fails clearly with nothing to auto-configure from
When `spinloop harness --env <name>` is run with no Spinloop applied, and the environment's fetched response carries no deploy-config — because nothing has been deployed to it, or because its `env` Lambda predates this behaviour and the reply simply omits the fields — the command SHALL fail before launching, rather than launch an unconfigured or misconfigured harness.

The error SHALL name the environment and say what to do: deploy a model to it (`spinloop cloud deploy <spinloop> --env <name>`), or, when a redeployed model is plausible but the reply still lacks the fields, run `spinloop cloud bootstrap` to update the control plane to a version whose `env` Lambda reports what is deployed.

#### Scenario: nothing deployed to the environment
- **WHEN** the user runs `spinloop harness --env dev-3` with no Spinloop applied, and nothing has been deployed to `dev-3`
- **THEN** the command fails saying nothing is deployed to `dev-3` and how to deploy one, and the harness is not launched

#### Scenario: an env Lambda predating this behaviour
- **WHEN** the user runs `spinloop harness --env dev-3` with no Spinloop applied, and `dev-3`'s `env` Lambda reply carries no deploy-config fields
- **THEN** the command fails the same way as when nothing is deployed, naming `spinloop cloud bootstrap` as a way to update the control plane, and the harness is not launched
