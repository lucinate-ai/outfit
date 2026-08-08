# Local Serving Specification

## Purpose

Define `outfit serve`: launching `llama-server` for the model an Outfit
describes — from the Outfit's own instructions, or from a llama.cpp preset
`.ini` it points at — so the same file that dresses the harness can also start
the server behind it. `serve` is harness-agnostic and never touches harness
config.
## Requirements
### Requirement: Serve basics

`outfit serve [path]` SHALL read an Outfit (default `./Outfit`, aliases and
directories accepted like every Outfit command), build the command for the
engine its `PROVIDER` names, print it in copy-pasteable shell form, and run it
with stdio forwarded. `--dry-run`/`-n` SHALL print the command without
launching. A missing binary SHALL produce an install hint naming **that** engine
rather than a raw exec error.

#### Scenario: Dry run

- **WHEN** the user runs `outfit serve --dry-run`
- **THEN** the resolved command is printed and no server starts

#### Scenario: Engine not installed

- **WHEN** the selected engine's binary cannot be found
- **THEN** the error suggests installing that engine, not another one

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

### Requirement: Preset-less serving

Without a `PRESET`, the Outfit SHALL supply the command directly and MUST name
a `MODEL`: a value that looks like a local file (ending `.gguf`, or starting
with `/`, `./`, `../`, or `~`) becomes the model path, anything else is
treated as a Hugging Face repo reference. `ALIAS` sets the served model's
reported name, `CONTEXT` (parsed with the standard lenient size format) sets
the context size, and `BASEURL`'s host and port set the server's bind address.

#### Scenario: Hugging Face repo

- **WHEN** the Outfit has `MODEL unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_XL`,
  `ALIAS qwen3.6`, and `CONTEXT 32768`
- **THEN** the command carries the repo reference, the alias, and a context
  size of 32768

#### Scenario: Local file

- **WHEN** the Outfit has `MODEL ./models/qwen.gguf`
- **THEN** the command loads that file as a model path rather than a repo

#### Scenario: No model to serve

- **WHEN** the Outfit has neither `PRESET` nor `MODEL`
- **THEN** the command fails explaining serve needs one of them

### Requirement: Serving a multi-model engine

An engine that loads a directory of models and selects one per request SHALL be
servable with neither a `PRESET` nor a `MODEL`, since it needs nothing at launch
to know what to load. For such an engine the Outfit's `MODEL` and `ALIAS` SHALL
keep their usual meaning — the id the harness requests — rather than becoming
launch flags, and `CONTEXT` SHALL size the harness's window only. `BASEURL`'s
host and port SHALL still set the server's bind address, and SHALL be omitted
entirely when the Outfit states no `BASEURL`, so the engine's own defaults stand.

#### Scenario: Bare Outfit starts the server

- **WHEN** the Outfit states only `PROVIDER omlx`
- **THEN** the server is launched with no bind flags and no model flags, rather
  than failing for want of a model

#### Scenario: Bind address from BASEURL

- **WHEN** the Outfit adds `BASEURL http://127.0.0.1:9100/v1`
- **THEN** the command binds the server to that host and port

### Requirement: Preset-based serving

With a `PRESET`, the referenced `.ini` SHALL supply the command: a relative
preset path resolves against the Outfit's own directory, so the pair can travel
together. The preset's `[*]`/`[global]` section holds shared defaults; each named
section is one model whose keys are server arguments with dashes stripped. The
served section is chosen by the Outfit's `ALIAS`, matched case-insensitively; a
preset with exactly one section is always served; no sections is an error;
several sections with no matching name SHALL fail listing the available
sections. Values the Outfit itself states SHALL override the preset's.

A preset SHALL be read in the flag vocabulary of the engine the `PROVIDER` names,
never in a vocabulary inferred from the file. A preset written for one engine is
therefore not portable to another: read in the wrong vocabulary it would parse
cleanly and produce a command with silently rewritten or dropped flags.

#### Scenario: Outfit overrides the preset

- **WHEN** the preset section sets `ctx-size = 4096` and the Outfit says
  `CONTEXT 32768`
- **THEN** the command carries a context size of 32768

#### Scenario: Ambiguous preset

- **WHEN** the preset defines several sections and the Outfit's `ALIAS` matches
  none
- **THEN** the command fails listing the section names to choose from

#### Scenario: Another engine's preset keys are left alone

- **WHEN** an oMLX preset contains a key that llama.cpp would treat as a short
  alias
- **THEN** the key is rendered as written, not rewritten to llama.cpp's
  long-form flag

### Requirement: Flag rendering

Preset keys SHALL render as the engine's flags. Where an engine defines short
aliases, they SHALL be canonicalised to their long form so the same flag written
different ways collapses to one when layers merge, later layers overriding
earlier ones in place; an engine that defines none SHALL pass its keys through
unchanged. Flags the engine accepts bare SHALL render bare when truthy and be
dropped when falsy (`0`, `false`, `off`, `no`). Printed commands SHALL quote only
the tokens that need it, and SHALL pass values to the server verbatim.

#### Scenario: Layer override by canonical name

- **WHEN** the global section sets `c = 4096` and the model section sets
  `ctx-size = 8192` for llama.cpp
- **THEN** the command carries a single context-size flag with value 8192

#### Scenario: Boolean flags

- **WHEN** a llama.cpp preset sets `jinja = 1` and `mmap = 0`
- **THEN** the command includes a bare `--jinja` and no mmap flag

#### Scenario: An engine with no short aliases

- **WHEN** an oMLX preset sets `model-dir` and `memory-guard`
- **THEN** both render as their long-form flags with their values unchanged

### Requirement: Secrets never reach the server command

`serve` SHALL NOT resolve or pass an API key to the engine it launches. Because
the command is printed before it runs, and an engine may accept its key as a
command-line flag, passing one would expose the secret on screen and to any local
process. Configuring authentication on the server is the engine's own concern.

#### Scenario: A set key is not passed through

- **WHEN** the provider's API key variable is set in the environment
- **THEN** neither the printed command nor the launched process carries the key

### Requirement: Control API flag

`outfit serve` SHALL accept `-a`/`--api` to expose the control API over the
foreground engine, as defined by the `daemon-api` capability. Serve SHALL
remain a foreground command with no daemon flag — long-lived supervision is
`outfit daemon`'s job. Without `--api`, serve's foreground stdio-forwarded
behaviour SHALL be unchanged.

#### Scenario: Plain serve is unchanged

- **WHEN** the user runs `outfit serve` without `--api`
- **THEN** the engine runs in the foreground with stdio forwarded, exactly as
  before

#### Scenario: Serve with the API stays foreground

- **WHEN** the user runs `outfit serve -a`
- **THEN** the engine runs in the foreground with the control API listening
  beside it

