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
directories accepted like every Outfit command), build a `llama-server`
command, print it in copy-pasteable shell form, and run it with stdio
forwarded. `--dry-run`/`-n` SHALL print the command without launching. A
missing `llama-server` binary SHALL produce an install hint rather than a raw
exec error.

#### Scenario: Dry run

- **WHEN** the user runs `outfit serve --dry-run`
- **THEN** the resolved command is printed and no server starts

#### Scenario: llama-server not installed

- **WHEN** the binary cannot be found on the PATH
- **THEN** the error suggests installing llama.cpp

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

### Requirement: Preset-based serving

With a `PRESET`, the referenced llama.cpp `.ini` SHALL supply the command: a
relative preset path resolves against the Outfit's own directory, so the pair
can travel together. The preset's `[*]`/`[global]` section holds shared
defaults; each named section is one model whose keys are `llama-server` flags
with dashes stripped. The served section is chosen by the Outfit's `ALIAS`,
matched case-insensitively; a preset with exactly one section is always served;
no sections is an error; several sections with no matching name SHALL fail
listing the available sections. Values the Outfit itself states (model, alias,
context, host/port) SHALL override the preset's.

#### Scenario: Outfit overrides the preset

- **WHEN** the preset section sets `ctx-size = 4096` and the Outfit says
  `CONTEXT 32768`
- **THEN** the command carries a context size of 32768

#### Scenario: Ambiguous preset

- **WHEN** the preset defines several sections and the Outfit's `ALIAS` matches
  none
- **THEN** the command fails listing the section names to choose from

### Requirement: Flag rendering

Preset keys SHALL render as `llama-server` flags: known short aliases (like
`hf`, `ngl`, `c`) are canonicalised to their long form so the same flag written
different ways collapses to one when layers merge, later layers overriding
earlier ones in place. Known boolean flags render bare when truthy and are
dropped when falsy (`0`, `false`, `off`, `no`). Printed commands SHALL quote
only the tokens that need it.

#### Scenario: Layer override by canonical name

- **WHEN** the global section sets `c = 4096` and the model section sets
  `ctx-size = 8192`
- **THEN** the command carries a single context-size flag with value 8192

#### Scenario: Boolean flags

- **WHEN** a preset sets `jinja = 1` and `mmap = 0`
- **THEN** the command includes a bare `--jinja` and no mmap flag
