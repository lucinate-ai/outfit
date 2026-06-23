# Gemma-4-12B-IT on llama.cpp

Run Unsloth's GGUF build of Gemma-4-12B-IT locally with `llama-server`, using
Multi-Token Prediction (MTP) for faster inference, then point opencode at it
with the [`Outfit`](Outfit) in this directory.

This model uses MTP: a small draft model (`mtp-gemma-4-12b-it.gguf`) runs
ahead of the main model to propose candidate tokens, which the main model
verifies in parallel. This can roughly double throughput on GPU hardware.

## Prerequisites

- A recent build of [llama.cpp](https://github.com/ggml-org/llama.cpp) that
  includes `llama-server` with MTP support (e.g. `brew install llama.cpp`, or
  build from source).
- A GPU is required. The `UD-Q8_K_XL` quant is roughly 13.6 GB on disk; plan
  for ~16 GB of VRAM when accounting for the KV cache and MTP draft model.
- The slim HF repo `Zambizi/slim-rpcache-unsloth-gemma-4-12b-gguf-mtp-mmporj`
  bundles all three files needed: the main GGUF, the MTP draft GGUF, and the
  multimodal projector.

## 1. Pull the model

`llama-server` can fetch all three files straight from Hugging Face. The quant
is selected with the `:TAG` suffix:

```sh
llama-server -hf Zambizi/slim-rpcache-unsloth-gemma-4-12b-gguf-mtp-mmporj:UD-Q8_K_XL
```

On first run this downloads the `UD-Q8_K_XL` main weights, the MTP draft model,
and the multimodal projector into the llama.cpp cache (`~/.cache/llama.cpp`) and
then starts serving. Subsequent runs reuse the cache.

Prefer to download ahead of time? Use the Hugging Face CLI:

```sh
hf download Zambizi/slim-rpcache-unsloth-gemma-4-12b-gguf-mtp-mmporj --include "*UD-Q8_K_XL*" --include "mtp-*" --include "mmproj*"
```

## 2. Start llama-server

```sh
llama-server \
  -hf Zambizi/slim-rpcache-unsloth-gemma-4-12b-gguf-mtp-mmporj:UD-Q8_K_XL \
  --jinja \
  -ngl 99 \
  --ctx-size 32768 \
  --spec-type draft-mtp \
  --spec-draft-n-max 4 \
  --cache-reuse 256 \
  --host 127.0.0.1 --port 8080
```

What the flags do:

- `-hf …:UD-Q8_K_XL` — model repository and quant tag (pulls main GGUF + MTP + mmproj).
- `--jinja` — use the model's built-in chat template. Required for Gemma chat
  formatting to work correctly.
- `-ngl 99` — offload all layers to the GPU. Lower it for limited VRAM.
- `--ctx-size 32768` — context window in tokens. Raise or lower to taste.
- `--spec-type draft-mtp` — enable Multi-Token Prediction using the bundled
  draft model.
- `--spec-draft-n-max 4` — the draft model proposes up to 4 tokens at a time.
- `--cache-reuse 256` — KV-cache reuse window for draft verification, reducing
  redundant computation.
- `--host`/`--port` — the OpenAI-compatible API is served at
  `http://127.0.0.1:8080/v1`.

### Optional: long context with quantised KV cache

For long contexts the K/V cache can dominate memory. Quantising it to `q8_0`
roughly halves that cost. KV-cache quantisation requires flash attention:

```sh
llama-server \
  -hf Zambizi/slim-rpcache-unsloth-gemma-4-12b-gguf-mtp-mmporj:UD-Q8_K_XL \
  --jinja -ngl 99 --ctx-size 32768 --host 127.0.0.1 --port 8080 \
  --spec-type draft-mtp --spec-draft-n-max 4 --cache-reuse 256 \
  -fa on \
  --cache-type-k q8_0 \
  --cache-type-v q8_0
```

### Optional: reasoning mode

Gemma-4 supports structured reasoning with extended chain-of-thought:

```sh
llama-server \
  -hf Zambizi/slim-rpcache-unsloth-gemma-4-12b-gguf-mtp-mmporj:UD-Q8_K_XL \
  --jinja -ngl 99 --ctx-size 262144 --host 127.0.0.1 --port 8080 \
  --spec-type draft-mtp --spec-draft-n-max 4 --cache-reuse 256 \
  --reasoning on --reasoning-budget 1024
```

- `--reasoning on` — enables the model's reasoning mode.
- `--reasoning-budget 1024` — maximum tokens for the thinking/chain-of-thought
  phase (default 1024).

### Check it's up

```sh
curl http://127.0.0.1:8080/v1/models
```

## 3. Point opencode at it

`llama-server` speaks the OpenAI-compatible API, which is exactly what the
`llamacpp` provider targets (default base URL `http://localhost:8080/v1`). Apply
the [`Outfit`](Outfit) in this directory:

```sh
outfit apply examples/llamacpp/gemma4/Outfit
# or, from this directory:
outfit apply
```

The Outfit is:

```dockerfile
PROVIDER llamacpp
MODEL    gemma-4-12b-it
CONTEXT  32768            # match the server's --ctx-size
```

The model name is just a label — `llama-server` serves whichever model it has
loaded regardless of what's requested — so call it whatever you find readable.
`CONTEXT` matches opencode's context window to the `--ctx-size` you launched the
server with, so it doesn't overshoot what `llama-server` will accept.

Running on a non-default host or port? Add a `BASEURL` line to the Outfit (the
file ships one commented out):

```dockerfile
BASEURL http://127.0.0.1:9090/v1
```

Now start `opencode` and select `llamacpp/gemma-4-12b-it`.
