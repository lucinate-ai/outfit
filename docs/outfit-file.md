# The `Outfit` file

An **Outfit** is a small, declarative file that captures one opencode provider
selection — which provider, and which model family and/or model — so you can
apply it with a single command instead of remembering flags. Think of it like a
`Dockerfile`, but for pointing opencode at a model.

```dockerfile
# Outfit — point opencode at one provider
PROVIDER openrouter
FAMILY   deepseek-v4
MODEL    deepseek/deepseek-v4-pro   # optional; the provider-native model ref
ALIAS    deepseek                   # optional; friendly name for the model
CONTEXT  128k                       # optional; context window
OUTPUT   32k                        # optional; max output tokens
BASEURL  https://gateway/v1         # optional; API base URL override
PRESET   ./preset.ini               # optional; llama.cpp preset for `outfit serve`
```

Applying it is the same as running the equivalent `outfit add`, so everything
you already have in your opencode config is preserved.

## Applying an Outfit

```sh
outfit apply            # reads ./Outfit in the current directory
outfit apply path/to/Outfit
```

Run `outfit apply` with no arguments and it looks for a file named `Outfit`
in the current directory. Point it at any path to apply a different file.

After applying, just run `opencode`.

## Serving a llama.cpp model

`outfit serve` launches `llama-server` for an Outfit, so the same file that
points opencode at a model can also start it. It works two ways.

```sh
outfit serve              # reads ./Outfit and runs llama-server
outfit serve path/to/Outfit
outfit serve --dry-run    # print the command without launching the server
```

It prints the command before running it, and does not touch your opencode
config — pair it with `outfit apply` to point opencode at the server.

### Simple case — straight from the Outfit

With no `PRESET`, `serve` builds the command from the Outfit itself:

```dockerfile
PROVIDER llamacpp
MODEL    unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_XL   # an HF repo, or a .gguf path
ALIAS    qwen3.6                                    # llama-server --alias
CONTEXT  32768                                      # llama-server --ctx-size
BASEURL  http://127.0.0.1:8080/v1                   # llama-server --host/--port
```

`MODEL` becomes `-hf` (a Hugging Face repo) or `-m` (anything that looks like a
path or ends in `.gguf`); `ALIAS`, `CONTEXT`, and `BASEURL` fill in the rest.

### Full control — a llama.cpp preset

For flags an Outfit doesn't model — `-ngl`, `--jinja`, KV-cache types, draft
models — point at a llama.cpp
[preset `.ini`](https://github.com/ggml-org/llama.cpp/blob/master/docs/preset.md):
a set of `llama-server` flags grouped under named `[model]` sections, with a
`[*]` section for shared defaults. Presets are built for the server's router
(multi-model) mode, so there's no clean way to launch a single model from one —
which is exactly what `serve` does.

```dockerfile
PROVIDER llamacpp
ALIAS    qwen3.6-35b-a3b   # selects the preset's [qwen3.6-35b-a3b] section
PRESET   ./preset.ini
```

`serve` flattens the `[*]` defaults and the matching section into explicit
`llama-server` flags (the section wins on any clash). Keys map straight to flags
— `ctx-size = 262144` becomes `--ctx-size 262144`, `hf` becomes `--hf-repo`, and
boolean toggles like `mmap = 1` become a bare `--mmap`. Which section runs:

- `ALIAS` names the section.
- With no `ALIAS`, a preset holding exactly one section serves that one.
- Several sections and no `ALIAS` is an error — name one.

## Syntax

One instruction per line: a keyword followed by a single value.

| Keyword    | Required?                  | Maps to        | Example                        |
| ---------- | -------------------------- | -------------- | ------------------------------ |
| `PROVIDER` | yes                              | `--provider`   | `PROVIDER openrouter`          |
| `FAMILY`   | one of `FAMILY`/`MODEL`/`ALIAS`  | `--model-family` | `FAMILY deepseek-v4`         |
| `MODEL`    | one of `FAMILY`/`MODEL`/`ALIAS`  | `--model`      | `MODEL deepseek/deepseek-v4-pro` |
| `ALIAS`    | one of `FAMILY`/`MODEL`/`ALIAS`  | `--alias`      | `ALIAS deepseek`               |
| `CONTEXT`  | no                               | `--context`    | `CONTEXT 128k`                 |
| `OUTPUT`   | no                               | `--output`     | `OUTPUT 32k`                   |
| `BASEURL`  | no                               | `--base-url`   | `BASEURL https://gateway/v1`   |
| `PRESET`   | no                               | `outfit serve` | `PRESET ./preset.ini`          |

Rules:

- An Outfit describes **exactly one provider**. `PROVIDER` is required and may
  appear only once; so may every other keyword.
- You need **at least one** of `FAMILY`, `MODEL`, or `ALIAS`. Give a `FAMILY` to
  add all of that family's models; give a `MODEL` to add or pin a specific one;
  give both to add the family but make `MODEL` the default.
- `MODEL` is the reference the **provider itself** understands: an
  OpenRouter/Bedrock model id, an Ollama name, or — for llama.cpp — a Hugging
  Face repo (`org/model:quant`) or a path to a `.gguf`. `outfit serve` derives
  the `llama-server` model source from it.
- `ALIAS` is the friendly name the harness shows for the model (and, under
  `serve`, the name `llama-server` reports and the preset section to run). It
  defaults to `MODEL`. For a llama.cpp server the model key is only a label, so
  an `ALIAS` keeps it readable; an `ALIAS` on its own is enough to select one.
- `CONTEXT` sets the context window for the model(s). It accepts human suffixes
  (`128k`, `1m`) or an absolute count (`200000`).
- `OUTPUT` caps the max output tokens, in the same format as `CONTEXT`. opencode
  requires one whenever a context is set, so if you omit it `outfit` records a
  quarter of the context. It cannot exceed the context window. A command-line
  `--output`/`-o` on `outfit apply` overrides whatever the Outfit specifies.
- `BASEURL` overrides the provider's API base URL — handy for a gateway or a
  llama.cpp server on a non-default port. `URL`, `BASE-URL`, and `BASE_URL` are
  accepted as aliases.
- `PRESET` points at a llama.cpp [preset `.ini`](https://github.com/ggml-org/llama.cpp/blob/master/docs/preset.md)
  and is only used by [`outfit serve`](#serving-a-llamacpp-model); `apply`
  ignores it. When set it overrides the simple `MODEL`-based command. A relative
  path is resolved against the Outfit's own directory.
- Keywords are **case-insensitive** — `provider`, `Provider`, and `PROVIDER` are
  all accepted — but **UPPERCASE is canonical** and is what `outfit export`
  writes.
- **Comments** start with `#`, either on their own line or at the end of a line.
  Blank lines are ignored.

To see the available providers, families, and models, run `outfit list`.

## Examples

A local model served by llama.cpp (no API key needed). `ALIAS` is the name
opencode shows; add a `MODEL` (an HF repo or `.gguf` path) or a `PRESET` if you
also want `outfit serve` to launch it:

```dockerfile
PROVIDER llamacpp
ALIAS    qwen3.6-35b-a3b
```

A whole model family from OpenRouter (its key comes from your `.env` or
environment, exactly as with `outfit add`):

```dockerfile
PROVIDER openrouter
FAMILY   deepseek-v4
```

Any OpenAI-compatible endpoint, with a single pinned model:

```dockerfile
PROVIDER openai-compatible
MODEL    my-model
```

Ready-to-use Outfits live under [`examples/`](../examples/).

## Capturing your current setup

`outfit export` prints your current opencode configuration as an Outfit, so
you can save a setup you built by hand:

```sh
outfit export > Outfit
```

By default it exports the provider behind your default model (or the only
configured provider). If you have several, choose one with `-p`:

```sh
outfit export -p openrouter > Outfit
```

Where the configured models match a known family, export names the `FAMILY`;
otherwise it writes the specific `MODEL`.
