# Qwen3.6-35B-A3B on oMLX (Apple Silicon)

Run an MLX build of Qwen3.6-35B-A3B on your Mac with [oMLX](https://omlx.ai),
then point opencode at it with the [`Outfit`](Outfit) in this directory.

`A3B` means it's a mixture-of-experts model: ~35B total parameters but only ~3B
active per token, so it's far lighter to run than its size suggests — which is
what makes it practical on a laptop's unified memory.

oMLX is built on [MLX](https://github.com/ml-explore/mlx), Apple's array
framework, so it runs on the GPU and Neural Engine rather than through a CUDA
path. Its headline feature is **paged SSD caching**: KV-cache blocks are
persisted to disk, so returning to a long prompt restores instead of recomputing
it. For a coding agent, which re-sends a large and mostly-unchanged context on
every turn, that is the difference between a usable and an unusable setup.

## Prerequisites

- An **Apple Silicon** Mac (M1 or later). oMLX is Apple Silicon only — there is
  no Intel or Linux build, and no cloud equivalent, so
  [`outfit remote`](../../../docs/commands/remote.md) cannot deploy it.
- [oMLX](https://omlx.ai), installed from its DMG or from source.
- Enough unified memory for the weights. The 4-bit build is roughly 20 GB, so
  plan for a 32 GB machine or better.

## 1. Pull the model

oMLX's admin panel (`http://localhost:8000/admin`) has a Hugging Face browser
that downloads a model in one click, which is the easiest route.

To do it from the shell instead, put the model in the directory oMLX serves
from:

```sh
hf download mlx-community/Qwen3.6-35B-A3B-4bit \
  --local-dir ~/models/Qwen3.6-35B-A3B-4bit
```

The **directory name** is what the model is called over the API —
`Qwen3.6-35B-A3B-4bit` here — unless you set an alias for it in the admin panel.
That is the name the `Outfit` uses as its `MODEL`.

## 2. Start oMLX

```sh
omlx-cli serve \
  --model-dir ~/models \
  --memory-guard safe \
  --paged-ssd-cache-dir ~/.omlx/cache \
  --max-concurrent-requests 8 \
  --host 127.0.0.1 --port 8000
```

What the flags do:

- `--model-dir` — the directory of models to serve. oMLX loads *all* of them and
  picks per request, which is why there's no "the model" flag here.
- `--memory-guard safe` — keep memory headroom for the rest of the machine.
  `balanced` gives the model more; `--memory-guard-gb` sets a ceiling directly.
- `--paged-ssd-cache-dir` — persist KV-cache blocks to SSD. This is the setting
  that makes long agent contexts fast on repeat turns.
- `--max-concurrent-requests` — continuous batching width (default 8).
- `--host`/`--port` — the OpenAI-compatible API is served at
  `http://127.0.0.1:8000/v1`.

Installed from the DMG and haven't put it on your `PATH`? The binary lives at
`/Applications/oMLX.app/Contents/MacOS/omlx-cli`.

Rather than remember those flags, this directory keeps them in a
[`preset.ini`](preset.ini) and lets `outfit` build and run the command (with the
paths spelled out in full — preset values reach the server verbatim, so `~` is
not expanded):

```sh
outfit serve              # from this directory; reads ./Outfit and its PRESET
outfit serve --dry-run    # print the omlx-cli command without running it
```

`serve` finds `omlx-cli` on your `PATH`, falling back to the app bundle — so
this works even if you've only ever launched oMLX from the menu bar.

Note the preset holds `omlx-cli` flags, **not** llama.cpp's. Each engine's
preset is read in its own vocabulary, so this file and the ones under
[`examples/llamacpp/`](../../llamacpp/) are not interchangeable.

### Check it's up

```sh
curl http://127.0.0.1:8000/v1/models
```

This is also how to confirm what your model is actually called — the response
lists the names requests must use.

## 3. Point opencode at it

oMLX speaks the OpenAI-compatible API, which is what the `omlx` provider targets
(default base URL `http://localhost:8000/v1`). Apply the [`Outfit`](Outfit) in
this directory:

```sh
outfit apply examples/omlx/qwen3.6/Outfit
# or, from this directory:
outfit apply
```

The Outfit is:

```dockerfile
PROVIDER omlx
MODEL    Qwen3.6-35B-A3B-4bit   # the directory name under the preset's model-dir
CONTEXT  32768
PRESET   ./preset.ini
```

`MODEL` matters more here than it does for a single-model llama.cpp server:
oMLX serves everything in its model directory, so the name is what actually
selects the model per request. It must match the directory name (or the alias
you set in the admin panel).

`CONTEXT` sets opencode's context window. Unlike llama.cpp there is no
`--ctx-size` to keep it in step with — oMLX sizes its cache dynamically — so
this is purely about what opencode will send.

Running on a different host or port? Add a `BASEURL` line (the file ships one
commented out):

```dockerfile
BASEURL http://127.0.0.1:9100/v1
```

That sets both what opencode calls and what `serve` binds to.

## A note on API keys

A local oMLX server needs no key, and `outfit` writes none: the `omlx` provider
is marked `apiKeyOptional`, so a localhost endpoint gets no `apiKey` at all.

Serving to another machine on your network? Start oMLX with `--api-key`, export
the same value as `OPENAI_API_KEY`, and point `OMLX_BASE_URL` at the Mac —
`outfit` then writes an environment *reference* into the agent's config, never
the secret itself.

`outfit serve` never passes `--api-key`: it prints the command it runs, so a key
there would land on your screen and in the process table. Configure auth in oMLX
itself.

## See also

- [`outfit serve`](../../../docs/commands/serve.md) — the full command reference
- [The `Outfit` file](../../../docs/outfit-file.md) — full syntax
- The same model on llama.cpp: [`examples/llamacpp/qwen3.6`](../../llamacpp/qwen3.6/README.md)
