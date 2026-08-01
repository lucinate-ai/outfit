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
oMLX serves everything in its model directory and dispatches on the requested
name, so it must match a model oMLX actually has — its directory name, or the
alias you set in the admin panel. This Outfit names the model downloaded above;
**if you downloaded something else, change `MODEL` to match.** List what yours
exposes with:

```sh
curl -H "Authorization: Bearer $OPENAI_API_KEY" http://localhost:8000/v1/models
```

A name oMLX does not have comes back as `model not found`. And unlike the
llama.cpp examples — where `outfit serve` can fetch the weights from Hugging
Face — oMLX only serves what you have already downloaded.

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

By default `outfit` writes no key for a localhost oMLX: the `omlx` provider is
marked `apiKeyOptional`, so a local endpoint with no `OPENAI_API_KEY` set gets no
`apiKey` field at all.

**But oMLX can require a key even on localhost** — it is a per-install setting in
the admin panel, and some builds turn it on by default. If yours does, requests
without a key come back as `API key required`. To use it, set `OPENAI_API_KEY`
**before** you apply the Outfit:

```sh
OPENAI_API_KEY=your-omlx-key outfit apply     # writes an {env:OPENAI_API_KEY} reference
OPENAI_API_KEY=your-omlx-key opencode         # or `outfit harness`, which forwards it
```

The *before* matters: with the key set at apply time, `outfit` writes an
`{env:OPENAI_API_KEY}` reference into the config (never the secret). With it
unset, `outfit` omits the `apiKey` field entirely — and exporting the key later
does nothing, because there is no reference in the config to resolve. If you hit
`API key required` after the fact, set the variable and re-run `apply`.

Reaching an oMLX on another machine works the same way: start it with
`--api-key`, export `OPENAI_API_KEY`, and point `OMLX_BASE_URL` at the Mac.

`outfit serve` never passes `--api-key`: it prints the command it runs, so a key
there would land on your screen and in the process table. Configure auth in oMLX
itself.

## See also

- [`outfit serve`](../../../docs/commands/serve.md) — the full command reference
- [The `Outfit` file](../../../docs/outfit-file.md) — full syntax
- The same model on llama.cpp: [`examples/llamacpp/qwen3.6`](../../llamacpp/qwen3.6/README.md)
