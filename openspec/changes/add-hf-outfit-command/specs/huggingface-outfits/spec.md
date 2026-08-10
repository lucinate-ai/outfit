## Purpose

Define `outfit hf`: turning a Hugging Face model reference into a working
Outfit — inferring the engine, the quantisation, the context window and the
name from what the repo actually holds, preferring a copy already on disk, and
printing, writing or applying the result.

## ADDED Requirements

### Requirement: Creating an Outfit from a reference

`outfit hf <ref>` SHALL read the named Hugging Face model, derive a provider
selection from it, and render it as an Outfit on stdout. Rendered output SHALL
be the same canonical form `outfit export` produces, so `outfit hf <ref> >
Outfit` yields a file every other command accepts. A missing reference SHALL
fail showing the command's usage.

What was inferred, and from what, SHALL be reported on stderr — the provider
and why, the quantisation chosen and the alternatives available, the context
window and where it came from, and whether a local copy was used — so stdout
stays a clean Outfit while the reasoning is still visible.

#### Scenario: A reference becomes an Outfit

- **WHEN** the user runs `outfit hf unsloth/Qwen3.6-35B-A3B-GGUF`
- **THEN** an Outfit naming a provider, model, alias and context is printed on
  stdout, and the reasoning is printed on stderr

#### Scenario: Redirecting produces a usable file

- **WHEN** the printed output is redirected to `./Outfit` and `outfit apply` is
  run
- **THEN** the harness is configured from it with no editing

#### Scenario: No reference

- **WHEN** the user runs `outfit hf` with no argument
- **THEN** it fails showing how the command is called

### Requirement: The provider is inferred from the repo

The `PROVIDER` SHALL be inferred from what the repo holds: a repo publishing
GGUF files SHALL yield `llamacpp`; a repo published for MLX SHALL yield `omlx`;
a repo publishing plain safetensors weights SHALL yield `vllm`. A repo matching
none of these SHALL fail saying what it appears to hold and which providers
`outfit hf` can infer, rather than guessing one.

`--provider`/`-p` SHALL override the inference, and SHALL be accepted even when
inference would have chosen otherwise, so a repo carrying both GGUF and
safetensors can be pointed at either engine. A `-p` naming a provider that is
not a self-hosted engine SHALL still be honoured — the Outfit describes what a
harness talks to, and only `serve` requires a local engine.

#### Scenario: A GGUF repo

- **WHEN** the reference names a repo whose files are GGUF
- **THEN** the Outfit says `PROVIDER llamacpp`

#### Scenario: An MLX repo

- **WHEN** the reference names a repo published for MLX
- **THEN** the Outfit says `PROVIDER omlx`

#### Scenario: A safetensors repo

- **WHEN** the reference names a repo of plain safetensors weights
- **THEN** the Outfit says `PROVIDER vllm`

#### Scenario: A repo that is not a model

- **WHEN** the reference names a repo holding none of these
- **THEN** the command fails saying what it found and which providers can be
  inferred

#### Scenario: Overriding the inference

- **WHEN** the user passes `-p vllm` for a repo carrying both GGUF and
  safetensors files
- **THEN** the Outfit says `PROVIDER vllm`

### Requirement: Choosing a quantisation

For a repo offering several quantisations, the one served SHALL be chosen in
this order: the reference's own `:QUANT` suffix, then `--quant`/`-q`, then a
deterministic, documented preference order favouring a mid-sized K-quant. The
chosen quantisation SHALL be named in the narration alongside the alternatives
the repo offers, so the default can be seen and overridden.

A named quantisation that the repo does not offer SHALL fail listing the ones
it does, matched case-insensitively so `q4_k_m` finds `Q4_K_M`. A
quantisation split across several files SHALL be treated as one choice, and the
`MODEL` written for it SHALL be the form the engine loads the whole set from,
never a single shard in isolation.

#### Scenario: A default is chosen and explained

- **WHEN** a repo offers `Q4_K_M` and `Q8_0` and neither a suffix nor `-q` was
  given
- **THEN** `Q4_K_M` is chosen, and the narration names it and lists `Q8_0` as
  an alternative

#### Scenario: An explicit quantisation wins

- **WHEN** the reference ends `:Q8_0`
- **THEN** the Outfit's `MODEL` names that quantisation

#### Scenario: A quantisation the repo does not have

- **WHEN** the reference ends `:Q3_K_XXL` and the repo has no such file
- **THEN** the command fails listing the quantisations the repo offers

#### Scenario: A sharded quantisation

- **WHEN** the chosen quantisation is published as several numbered shards
- **THEN** the `MODEL` written loads the whole set

### Requirement: Context and name are derived, never invented

The `CONTEXT` SHALL be the window the model itself declares in its published
configuration, and `--context`/`-c` SHALL override it, parsed by the same
lenient size format every other command uses. Where the model declares no
window, `CONTEXT` SHALL be omitted and the narration SHALL say so, rather than
a plausible number being invented.

The `ALIAS` SHALL be a short, lower-cased name derived from the repo's own
name with a packaging suffix such as `-GGUF` or `-MLX` removed, and
`--alias`/`-a` SHALL override it. `OUTPUT` SHALL NOT be written: it already
defaults to a quarter of the context when an Outfit is applied.

#### Scenario: The declared window is used

- **WHEN** the model's configuration declares a 262144-token window
- **THEN** the Outfit says `CONTEXT 262144`

#### Scenario: An overridden window

- **WHEN** the user passes `-c 32k`
- **THEN** the Outfit says `CONTEXT 32000` whatever the model declares

#### Scenario: No declared window

- **WHEN** the model publishes no configuration stating a window
- **THEN** the Outfit has no `CONTEXT` line and the narration says why

#### Scenario: The alias drops the packaging suffix

- **WHEN** the repo is `unsloth/Qwen3.6-35B-A3B-GGUF`
- **THEN** the `ALIAS` is `qwen3.6-35b-a3b`

### Requirement: A copy already on disk is preferred

Where the chosen model is already in a local cache, the `MODEL` written SHALL
name the file on disk rather than the repo, so the engine loads what is there
instead of downloading a second copy, and the narration SHALL say which cache
it came from. Where it is not cached, the `MODEL` SHALL be the repo reference
in the form the engine understands, leaving the download to the engine as it
happens today.

This SHALL apply only where naming a file is meaningful to the engine: for an
engine that loads a repo or a directory rather than a single weights file, the
`MODEL` SHALL stay the repo reference whether or not a copy is cached.

Because a path names one machine's disk, `--no-cache` SHALL write the repo
reference even when a copy is cached, so an Outfit meant to be committed and
shared can be produced deliberately. The narration SHALL note when a path was
written for exactly this reason.

`outfit hf` SHALL NOT download weights under any circumstances. Reading a repo
is metadata only, and a command that describes a model SHALL never begin a
multi-gigabyte transfer as a side effect.

#### Scenario: A cached GGUF is named directly

- **WHEN** the chosen quantisation is already in a local cache
- **THEN** the `MODEL` is that file's path, and the narration names the cache

#### Scenario: An uncached model keeps the repo reference

- **WHEN** nothing for the reference is cached
- **THEN** the `MODEL` is the repo reference, and nothing is downloaded

#### Scenario: A portable Outfit is asked for

- **WHEN** the chosen quantisation is cached and the user passes `--no-cache`
- **THEN** the `MODEL` is the repo reference rather than the local path

#### Scenario: Describing a model never downloads it

- **WHEN** `outfit hf` runs for a repo holding many gigabytes of weights
- **THEN** only metadata is fetched and no weights file is transferred

### Requirement: Printing, writing and applying

By default the Outfit SHALL be printed to stdout. `--output-file`/`-o` SHALL
write it to the named path instead, reporting where it went; an existing file
SHALL NOT be overwritten unless `--force` is given, so a hand-edited Outfit
cannot be lost to a mistyped command. `--apply` SHALL additionally configure
the active harness from the selection, by the same path `outfit apply` uses and
honouring `--harness`/`-H`, so one command goes from a model page to a dressed
agent.

#### Scenario: Writing to a file

- **WHEN** the user runs `outfit hf <ref> -o ./Outfit` and no such file exists
- **THEN** the Outfit is written there and the path is reported

#### Scenario: An existing file is not clobbered

- **WHEN** `-o` names a file that already exists and `--force` was not given
- **THEN** nothing is written and the command fails saying `--force` overwrites

#### Scenario: Applying directly

- **WHEN** the user runs `outfit hf <ref> --apply`
- **THEN** the active harness is configured exactly as applying the printed
  Outfit would have configured it

#### Scenario: Applying to a named harness

- **WHEN** the user runs `outfit hf <ref> --apply --harness pi`
- **THEN** the Pi harness is configured rather than the active default
