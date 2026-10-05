# Gemma-4-E2B on oMLX (Apple Silicon)

Run LM Studio's MLX build of Gemma-4-E2B-it on your Mac with
[oMLX](https://omlx.ai), then point opencode at it with the [`Spinloop`](Spinloop) in
this directory.

`E2B` is the small member of the Gemma-4 family: an *effective* ~2B parameters,
so it loads in seconds and leaves plenty of memory headroom — a good first model
to prove the setup end to end before reaching for something larger (see the
[Qwen3.6-27B example](../qwen3.6/README.md) for a heavier one).

## Prerequisites

- An **Apple Silicon** Mac (M1 or later). oMLX is Apple Silicon only, so
  [`spinloop cloud`](../../../docs/commands/cloud.md) cannot deploy it.
- [oMLX](https://omlx.ai), installed from its DMG or from source.
- The 6-bit build is only a few GB, so memory is not a concern here.

## 1. Pull the model

oMLX's admin panel (`http://localhost:8000/admin`) has a Hugging Face browser
that downloads a model in one click, which is the easiest route.

To do it from the shell instead, put the model in the directory oMLX serves
from:

```sh
hf download lmstudio-community/gemma-4-E2B-it-MLX-6bit \
  --local-dir ~/models/gemma-4-E2B-it-MLX-6bit
```

The **directory name** is what the model is called over the API —
`gemma-4-E2B-it-MLX-6bit` here — unless you set an alias for it in the admin
panel. That is the name the `Spinloop` uses as its `MODEL`.

## 2. Start oMLX

```sh
omlx-cli serve \
  --model-dir ~/models \
  --memory-guard safe \
  --max-concurrent-requests 8 \
  --host 127.0.0.1 --port 8000
```

- `--model-dir` — the directory of models to serve. oMLX loads what it needs and
  picks per request, so there is no "the model" flag here.
- `--host`/`--port` — the OpenAI-compatible API is served at
  `http://127.0.0.1:8000/v1`.

Installed from the DMG and haven't put it on your `PATH`? The binary lives at
`/Applications/oMLX.app/Contents/MacOS/omlx-cli`.

Rather than remember those flags, this directory keeps them in a
[`preset.ini`](preset.ini) (with the paths spelled out in full — preset values
reach the server verbatim, so `~` is not expanded) and lets `spinloop` build and
run the command:

```sh
spinloop serve              # from this directory; reads ./Spinloop and its PRESET
spinloop serve --dry-run    # print the omlx-cli command without running it
```

`serve` finds `omlx-cli` on your `PATH`, falling back to the app bundle.

### Check it's up

```sh
curl -H "Authorization: Bearer $OPENAI_API_KEY" http://localhost:8000/v1/models
```

This is also how to confirm what your model is actually called — the response
lists the names requests must use. (Drop the header if you have not enabled
oMLX's API-key auth.)

## 3. Point opencode at it

oMLX speaks the OpenAI-compatible API, which is what the `omlx` provider targets
(default base URL `http://localhost:8000/v1`). Apply the [`Spinloop`](Spinloop) in
this directory:

```sh
spinloop harness apply examples/omlx/gemma-4-e2b/Spinloop
# or, from this directory:
spinloop harness apply
```

The Spinloop is:

```dockerfile
PROVIDER omlx
MODEL    gemma-4-E2B-it-MLX-6bit
CONTEXT  32768
PRESET   ./preset.ini
```

`MODEL` must match a model oMLX actually has — its directory name, or the alias
you set in the admin panel. This Spinloop names the model downloaded above; **if
you downloaded something else, change `MODEL` to match** (list yours with the
`/v1/models` call above). A name oMLX does not have comes back as
`model not found`.

`CONTEXT` sets opencode's context window. Unlike llama.cpp there is no
`--ctx-size` to keep it in step with — oMLX sizes its cache dynamically — so
this is purely about what opencode will send.

## A note on API keys

By default `spinloop` writes no key for a localhost oMLX: the `omlx` provider is
marked `apiKeyOptional`, so a local endpoint with no `OPENAI_API_KEY` set gets no
`apiKey` field at all.

**But oMLX can require a key even on localhost** — it is a per-install setting in
the admin panel, and some builds turn it on by default. If yours does, set
`OPENAI_API_KEY` **before** you apply the Spinloop:

```sh
OPENAI_API_KEY=your-omlx-key spinloop harness apply     # writes an {env:OPENAI_API_KEY} reference
OPENAI_API_KEY=your-omlx-key opencode         # or `spinloop harness open`, which forwards it
```

The *before* matters: with the key unset at apply time, `spinloop` omits the
`apiKey` field, and exporting the key afterwards does nothing — there is no
reference in the config to resolve, so you would re-run `apply`. See the
[Qwen3.6 example](../qwen3.6/README.md#a-note-on-api-keys) for the full
explanation.

## See also

- [`spinloop serve`](../../../docs/commands/serve.md) — the full command reference
- [The `Spinloop` file](../../../docs/spinloop-file.md) — full syntax
- A heavier model on oMLX: [`examples/omlx/qwen3.6`](../qwen3.6/README.md)
