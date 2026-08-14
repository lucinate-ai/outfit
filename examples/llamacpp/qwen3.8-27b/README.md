# Qwen3.8-27B on llama.cpp

Run Unsloth's GGUF build of Qwen3.8-27B locally with `llama-server`, then point
opencode at it with the [`Outfit`](Outfit) in this directory. The same file also
deploys it to a GPU in AWS with [`outfit remote`](#running-it-on-aws) — no
infrastructure to hand-write, just this Outfit and one extra line.

Qwen3.8-27B is a dense 27B model built on Qwen's hybrid attention architecture
(mostly linear "Gated DeltaNet" layers with full attention every fourth layer),
which is what lets it carry a native 262144-token context, extensible to 1M,
without the usual KV-cache blowup. It's also vision-language — it can take
images and video — though this Outfit only wires up the text side, which is
all `opencode` needs; see [Vision input](#vision-input-optional) if you want
the rest.

## Prerequisites

- A recent build of [llama.cpp](https://github.com/ggml-org/llama.cpp) that
  includes `llama-server` (e.g. `brew install llama.cpp`, or build from source)
  — new enough to know the `qwen3_5` architecture this model uses.
- A GPU is strongly recommended. The `UD-Q4_K_XL` quant is roughly 17 GB on
  disk; for comfortable headroom plan for ~20 GB of VRAM (less if you offload
  fewer layers to the GPU).

## 1. Pull the model

`llama-server` can fetch GGUFs straight from Hugging Face. The quant is selected
with the `:TAG` suffix:

```sh
llama-server -hf unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_XL
```

On first run this downloads the `UD-Q4_K_XL` weights into the llama.cpp cache
(`~/.cache/llama.cpp`) and then starts serving. Subsequent runs reuse the cache.

Prefer to download ahead of time? Use the Hugging Face CLI:

```sh
hf download unsloth/Qwen3.8-27B-GGUF --include "*UD-Q4_K_XL*"
```

(`huggingface-cli download ...` works too on older installs.)

## 2. Start llama-server

```sh
llama-server \
  -hf unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_XL \
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
- `--ctx-size 32768` — context window in tokens. The model natively supports
  up to 262144 (and 1M with extension), so raise this on a box with the memory
  for it — see [Running it on AWS](#running-it-on-aws) below.
- `--host`/`--port` — the OpenAI-compatible API is served at
  `http://127.0.0.1:8080/v1`.

Rather than remember those flags, this directory keeps them in a
[`preset.ini`](preset.ini) and lets `outfit` build and run the command:

```sh
outfit serve              # from this directory; reads ./Outfit and its PRESET
outfit serve --dry-run    # print the llama-server command without running it
```

### Optional: quantise the KV cache

For long contexts the K/V cache can dominate memory. Quantising it to `q8_0`
roughly halves that cost. KV-cache quantisation requires flash attention:

```sh
llama-server \
  -hf unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_XL \
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
`llamacpp` provider targets (default base URL `http://localhost:8080/v1`). Apply
the [`Outfit`](Outfit) in this directory:

```sh
outfit apply examples/llamacpp/qwen3.8-27b/Outfit
# or, from this directory:
outfit apply
```

The Outfit is:

```dockerfile
PROVIDER llamacpp
ALIAS    qwen3.8-27b
CONTEXT  32768            # match the server's --ctx-size
PRESET   ./preset.ini
```

`ALIAS` is the name opencode shows for the model (and the section `serve` reads
from the preset). For a single-model server it's just a label — `llama-server`
serves whichever model it loaded regardless of what's requested — so call it
whatever you find readable. `CONTEXT` matches opencode's context window to the
`--ctx-size` you launched the server with, so it doesn't overshoot what
`llama-server` will accept.

Running on a non-default host or port? Add a `BASEURL` line to the Outfit (the
file ships one commented out):

```dockerfile
BASEURL http://127.0.0.1:9090/v1
```

Now start `opencode` and select `llamacpp/qwen3.8-27b`.

## Running it on AWS

The same Outfit and preset run this model on a GPU in the cloud — provisioned
by [`outfit remote`](../../../docs/commands/remote.md) — rather than the
machine in front of you, and terminate themselves once you stop using them.
This is real, billed AWS infrastructure (an EC2 GPU instance, an Elastic IP,
image-builder pipelines), so each step below shows you a plan and asks for
confirmation before it creates anything.

### Once per AWS account: bootstrap the control plane

```sh
outfit remote bootstrap                     # shows a plan, then deploys
outfit remote bootstrap --dry-run           # see the plan without deploying
```

This deploys the shared control plane — the AMI-baking pipelines for
`llamacpp` and `vllm`, the lifecycle Lambdas, and the shared weights bucket,
roles and VPC. It needs Node 22, `pnpm` or `npm`, AWS credentials, and GPU
vCPU quota in the target region. It creates no instance and no Elastic IP.

### Deploy this Outfit as an environment

Uncomment the `REMOTE` line in the [`Outfit`](Outfit) — it names the
environment `outfit remote` creates and registers:

```dockerfile
REMOTE qwen3.8-27b
```

Then:

```sh
outfit remote deploy    # from this directory
outfit remote deploy --dry-run   # see what would be sent first
```

`deploy` reads `PROVIDER`, `ALIAS`, `CONTEXT` and `PRESET` from the Outfit — the
same values [`outfit serve`](../../../docs/commands/serve.md) uses locally —
provisions the environment's Elastic IP, API key, ingress rule (defaulting to
your own public IP) and state, and registers it at
`~/.config/outfit/remotes/qwen3.8-27b/remote.json`. If the shared bucket
doesn't have these weights cached yet, deploy fetches them in the background
(15–20 minutes) — wait for that before your first `start`.

### Start it, use it, stop it

```sh
eval "$(outfit remote start)"   # boots the instance (~10 min cold), exports
                                 # OPENAI_BASE_URL / OPENAI_API_KEY
outfit remote status            # is it up, is it healthy
outfit apply                    # point opencode at the running endpoint
outfit harness                  # work
outfit remote stop              # done — shut it down now rather than waiting
                                 # for the idle timer
```

Once deployed, this box has the memory to run past the 32768-token default —
raise `CONTEXT`/`ctx-size` in the Outfit and preset together (up to the
model's native 262144) before your next `deploy`.

See [`outfit remote`](../../../docs/commands/remote.md) for `logs`, `metrics`,
and how to name and switch between multiple deployed environments.

## Vision input (optional)

This Outfit only serves text, which is all a coding agent needs. The GGUF repo
also ships `mmproj-F16.gguf` / `mmproj-BF16.gguf` for the vision tower; passing
one to `llama-server` with `--mmproj` enables image input over the same
OpenAI-compatible API, for anyone driving this server outside opencode.

## See also

- The bigger mixture-of-experts sibling on the same engine:
  [`examples/llamacpp/qwen3.6-35b-a3b`](../qwen3.6-35b-a3b/README.md)
- The previous generation of this dense size:
  [`examples/llamacpp/qwen3.6-27b`](../qwen3.6-27b/README.md)
