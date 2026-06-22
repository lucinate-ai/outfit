# Qwen3.6-35B-A3B on llama.cpp

Run Unsloth's GGUF build of Qwen3.6-35B-A3B locally with `llama-server`, then
point opencode at it with `oc-config`.

`A3B` means it's a mixture-of-experts model: ~35B total parameters but only ~3B
active per token, so it's far lighter to run than its size suggests.

## Prerequisites

- A recent build of [llama.cpp](https://github.com/ggml-org/llama.cpp) that
  includes `llama-server` (e.g. `brew install llama.cpp`, or build from source).
- A GPU is strongly recommended. The `UD-Q4_K_XL` quant is roughly 20 GB on
  disk; for comfortable headroom plan for ~24 GB of VRAM (less if you offload
  fewer layers to the GPU).

## 1. Pull the model

`llama-server` can fetch GGUFs straight from Hugging Face. The quant is selected
with the `:TAG` suffix:

```sh
llama-server -hf unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_XL
```

On first run this downloads the `UD-Q4_K_XL` weights into the llama.cpp cache
(`~/.cache/llama.cpp`) and then starts serving. Subsequent runs reuse the cache.

Prefer to download ahead of time? Use the Hugging Face CLI:

```sh
hf download unsloth/Qwen3.6-35B-A3B-GGUF --include "*UD-Q4_K_XL*"
```

(`huggingface-cli download ...` works too on older installs.)

## 2. Start llama-server

```sh
llama-server \
  -hf unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_XL \
  --jinja \
  -ngl 99 \
  --ctx-size 32768 \
  --host 127.0.0.1 --port 8080
```

What the flags do:

- `-hf …:UD-Q4_K_XL` — model repository and quant tag.
- `--jinja` — use the model's built-in chat template. Required for Qwen3 tool
  calling to work correctly.
- `-ngl 99` — offload all layers to the GPU. Lower it (or drop it) for CPU-only
  or limited VRAM.
- `--ctx-size 32768` — context window in tokens. Raise or lower to taste.
- `--host`/`--port` — the OpenAI-compatible API is served at
  `http://127.0.0.1:8080/v1`.

### Optional: quantise the KV cache

For long contexts the K/V cache can dominate memory. Quantising it to `q8_0`
roughly halves that cost. KV-cache quantisation requires flash attention:

```sh
llama-server \
  -hf unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_XL \
  --jinja -ngl 99 --ctx-size 32768 --host 127.0.0.1 --port 8080 \
  -fa on \
  --cache-type-k q8_0 \
  --cache-type-v q8_0
```

- `-fa on` — enable flash attention (on older builds this is just `-fa`).
- `--cache-type-k q8_0` / `--cache-type-v q8_0` — 8-bit K and V caches.

### Check it's up

```sh
curl http://127.0.0.1:8080/v1/models
```

## 3. Point opencode at it

`llama-server` speaks the OpenAI-compatible API, which is exactly what the
`llamacpp` provider targets (default base URL `http://localhost:8080/v1`):

```sh
oc-config add --provider llamacpp --model qwen3.6-35b-a3b
# short form:
oc-config add -p llamacpp -m qwen3.6-35b-a3b
```

The model name is just a label — `llama-server` serves whichever model it has
loaded regardless of what's requested — so call it whatever you find readable.

Running on a non-default host or port? Point the provider at it with the
`--base-url`/`-b` flag (or `LLAMACPP_BASE_URL`):

```sh
oc-config add -p llamacpp -m qwen3.6-35b-a3b --base-url http://127.0.0.1:9090/v1
```

Now start `opencode` and select `llamacpp/qwen3.6-35b-a3b`.
