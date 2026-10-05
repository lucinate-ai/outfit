# Qwen3.8-27B on MTPLX (Apple Silicon)

Run an MTPLX-optimised build of Qwen3.8-27B on your Mac with
[MTPLX](https://mtplx.com), then point opencode at it with the
[`Spinloop`](Spinloop) in this directory.

MTPLX is a single-model server: you launch it with one model and it serves that
model over an OpenAI-compatible API. That makes it the closest of the local
engines to
[llama.cpp](../../llamacpp/qwen3.8-27b/README.md), but tuned for Apple Silicon
unified memory.

## Prerequisites

- An **Apple Silicon** Mac (M1 or later). MTPLX is Apple Silicon only — there is
  no Intel or Linux build, and **no machine image**, so
  [`spinloop cloud`](../../../docs/commands/cloud.md) cannot deploy it. It
  serves locally, or on a
  [fleet node](../../../docs/commands/fleet.md) you run yourself.
- [MTPLX](https://mtplx.com), installed so that `mtplx` is on your `PATH`.
- Enough unified memory for the weights.

## 1. Start MTPLX

The raw command, for reference:

```sh
mtplx serve \
  --model Youssofal/Qwen3.8-27B-MTPLX-Optimized-Speed \
  --model-id qwen3.8-27b \
  --context-window 32768 \
  --max-active-requests 4 \
  --scheduler-mode ar_batch \
  --download \
  --host 127.0.0.1 --port 8000
```

What the flags do:

- `--model` — the Hugging Face repo to serve, or a local path. Taken verbatim.
- `--download` — fetch the repo if it is not already local, instead of failing
  the launch. `spinloop serve` always passes this.
- `--model-id` — the name the model is served under (the `ALIAS`).
- `--context-window` — the context a single request gets. Never scaled by
  `PARALLEL`.
- `--max-active-requests` — an admission cap on how many requests run at once.
- `--scheduler-mode` — how admitted requests execute. `spinloop` passes the
  value through without checking it, so it must be valid for your `mtplx`
  build (`mtplx serve --help` lists the current modes). This is per-deployment
  tuning, not a Spinloop field, so it lives in the
  [`preset.ini`](preset.ini).
- `--host`/`--port` — the OpenAI-compatible API is served at
  `http://127.0.0.1:8000/v1`.

Rather than remember those flags, this directory keeps them in a
[`preset.ini`](preset.ini) and lets `spinloop` build and run the command:

```sh
spinloop serve              # from this directory; reads ./Spinloop and its PRESET
spinloop serve --dry-run    # print the `mtplx serve` command without running it
```

The preset is read in MTPLX's own long-form vocabulary — its keys pass through
unchanged — so it is not interchangeable with a llama.cpp preset.

### Check it's up

```sh
curl http://127.0.0.1:8000/v1/models
curl http://127.0.0.1:8000/health
```

`/health` answers once the OpenAI server is up, which may be before the weights
finish loading.

## 2. Point opencode at it

MTPLX speaks the OpenAI-compatible API, which is what the `mtplx` provider
targets (default base URL `http://localhost:8000/v1`, overridable with
`MTPLX_BASE_URL`). Apply the [`Spinloop`](Spinloop) in this directory:

```sh
spinloop harness apply examples/mtplx/qwen3.8-27b/Spinloop
# or, from this directory:
spinloop harness apply
```

The Spinloop is:

```dockerfile
PROVIDER mtplx
ALIAS    qwen3.8-27b
CONTEXT  32768
PARALLEL 4
PRESET   ./preset.ini
```

`CONTEXT` maps to `--context-window` and `PARALLEL` to `--max-active-requests`,
each in MTPLX's own vocabulary. Running on a different host or port? Add a
`BASEURL` line (the file ships one commented out) — it sets both what opencode
calls and what `serve` binds to.

## A note on API keys

By default `spinloop` writes no key for a localhost MTPLX: the `mtplx` provider
is marked `apiKeyOptional`, so a local endpoint with no `OPENAI_API_KEY` set
gets no `apiKey` field at all. If you put a key on the engine, set
`OPENAI_API_KEY` **before** you apply the Spinloop, and `spinloop` writes an
`{env:OPENAI_API_KEY}` reference (never the secret).

`spinloop serve` never passes `--api-key`: it prints the command it runs, so a
key there would land on your screen and in the process table. A supervised
engine (`serve --api` or `spinloop daemon`) is gated with a key file the daemon
writes instead.

## See also

- [`spinloop serve`](../../../docs/commands/serve.md) — the full command reference
- [The `Spinloop` file](../../../docs/spinloop-file.md) — full syntax
- The same model on llama.cpp:
  [`examples/llamacpp/qwen3.8-27b`](../../llamacpp/qwen3.8-27b/README.md)
