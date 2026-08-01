## Context

Two engines now have to coexist behind one command. They differ in three ways that the
design has to absorb, and one way it deliberately does not.

- **Binary**: `llama-server` is a `PATH` install; oMLX ships as a signed macOS app whose
  CLI lives at `/Applications/oMLX.app/Contents/MacOS/omlx-cli`.
- **Shape**: `llama-server` takes flags directly; `omlx-cli` takes a `serve` subcommand
  first.
- **Model**: `llama-server` is launched with one model. oMLX loads a whole `--model-dir`
  and selects per request, so it has nothing to be told at launch and no context flag.
- **Preset format**: identical. Both are INI files of `key = value` under `[section]`
  headers. Only the *vocabulary* differs.

## Goals / Non-Goals

- Goals: one engine-selection rule shared with the cloud path; no silent misrendering of
  one engine's preset in another's vocabulary; existing llama.cpp behaviour byte-identical.
- Non-goals: a plugin system for engines; a generic engine descriptor in the catalogue;
  cloud deployment of oMLX.

## Decisions

### Engine selection comes from PROVIDER

The alternatives were a new `ENGINE` Outfit keyword or a `--engine` flag on `serve`.

`PROVIDER` wins because the codebase already committed to it: `runnerFor` maps `PROVIDER`
to a cloud runner and documents that `serve` does the same locally. Adding a keyword would
create a second source of truth for a question already answered, and `internal/outfit`
errors on unknown keywords, so every Outfit stating `ENGINE` would break on older
binaries. A flag would be worse still — it is per-invocation, so it could not travel with
the Outfit.

The cost is the breaking change: `serve` now rejects providers that are not local engines.
That is a fix, not a regression. The previous behaviour ran `llama-server` for an Outfit
saying `PROVIDER ollama`, which is a command the Outfit never asked for.

`engineFor` mirrors `runnerFor` deliberately: a hard-coded switch in Go, not a catalogue
field. Putting engine metadata in `providers.yaml` would be a single source of truth
across `serve`/`deploy`/`list`, but it would also mean a user-supplied `--providers` file
could name an arbitrary binary for `outfit serve` to execute. The allow-list stays in the
binary.

### Dialects, not a second parser

`internal/preset` splits cleanly: `Parse`, `Select`, `SectionNames`, the layer-merge loop
and `FormatCommand` never look at key names, while `canonical`, `boolean` and `flagFor`
are entirely llama.cpp's. So the engine-specific part becomes a value — `Dialect{Aliases,
Boolean}` — rather than a fork of the package.

The zero `Dialect` passes keys through unchanged, which is exactly right for oMLX: every
`omlx-cli serve` flag is long-form, so there is nothing to alias.

The package-level `Flags`, `CanonicalKey`, `Preset.Args` and `Preset.Command` are kept as
`LlamaCpp` wrappers rather than removed. This is not politeness to callers: `remote.go`'s
`isCloudOwned` compares preset keys through `preset.CanonicalKey` to decide which flags
the cloud owns, so changing that function's meaning would silently change what
`outfit remote deploy` strips from a preset.

**The trap this closes**: an oMLX preset read in llama.cpp's dialect parses without error
and produces a wrong command — `m` becomes `--model`, `c` becomes `--ctx-size`, and a
`key = 0` for a name in llama.cpp's boolean table vanishes. The dialect therefore comes
from the engine `PROVIDER` names, never from inspecting the file.

### What an Outfit means for oMLX

Only `BASEURL` becomes a launch flag, via the already engine-neutral `hostPortFromURL`.
`MODEL` and `ALIAS` keep their existing meaning — the id the harness requests — because
that is what they mean for every other provider, and for oMLX it is load-bearing rather
than a label: the server really does dispatch on it. `CONTEXT` sizes the harness's window;
oMLX has no context flag to keep in step with.

`isModelPath`'s `.gguf` heuristic is not reached on the oMLX path. MLX models are
directories of safetensors, so the predicate would be wrong for them.

`serveEngine.needsModel` keeps llama.cpp's "needs a PRESET or a MODEL" rule from applying
to an engine that needs neither.

### No API key to the server

`serve` prints the command it runs, and oMLX takes `--api-key` on the command line. Passing
a resolved key would put it on the terminal and in `ps` output — a sharper failure than
the config-file secrets the rest of the codebase is careful to avoid, since those at least
stay `0600` on disk. Auth on the server is configured in oMLX. The catalogue path is
unaffected: `outfit add`/`apply` still writes an `{env:OPENAI_API_KEY}` reference for the
harness when a key is set.

### Serve moves to its own file

`cmd/outfit/serve.go` is partly cohesion and partly a constraint:
`TestCompletionCoversDispatch` scans `main.go` for `case "…":` at one tab of indentation to
prove every dispatched command is completable. `engineFor`'s switch matches that shape, so
leaving it in `main.go` reports `llamacpp` and `omlx` as uncompletable commands.

## Risks / Trade-offs

- **Breaking `serve` for non-engine providers** — mitigated by an error that names the
  valid values, and by the fact that the previous behaviour was already wrong.
- **oMLX cannot be verified in CI** — it is Apple Silicon only. Mitigated by the stubbed
  binary seam the llama.cpp tests already use, which pins the whole argv without running
  anything real.
- **Port 8000 collides with the `vllm` default** — harmless (nothing assumes base URLs are
  unique), and documented so anyone running both knows to set `OMLX_BASE_URL`.
