## MODIFIED Requirements

### Requirement: Choosing the engine

`outfit serve` SHALL launch the inference engine the Outfit's `PROVIDER` names,
the local counterpart of the runner `outfit remote deploy` selects from the same
instruction. `llamacpp` SHALL run `llama-server`; `omlx` SHALL run the oMLX CLI;
`vllm` SHALL run `vllm serve`, with the model passed as its positional
argument, the served name as `--served-model-name`, and the context window as
`--max-model-len`. There SHALL be no default: a `PROVIDER` that is not a
self-hosted engine SHALL fail, naming the providers that can be served, rather
than launching an engine the Outfit did not ask for.

An engine whose executable is normally installed outside the `PATH` SHALL also be
looked for at its conventional install location, so a user who has never put it
on their `PATH` can still serve.

#### Scenario: Provider selects the engine

- **WHEN** the Outfit says `PROVIDER omlx`
- **THEN** the printed command runs the oMLX CLI, not `llama-server`

#### Scenario: vLLM is servable

- **WHEN** the Outfit says `PROVIDER vllm` with a `MODEL`
- **THEN** the printed command runs `vllm serve` with that model as the
  positional argument

#### Scenario: A provider that is not a local engine

- **WHEN** the Outfit names a hosted provider such as `ollama` or `openrouter`
- **THEN** the command fails, listing the providers that can be served locally

#### Scenario: Engine installed outside the PATH

- **WHEN** the oMLX CLI is not on the `PATH` but is present in its macOS app
  bundle
- **THEN** `serve` uses the bundled executable
