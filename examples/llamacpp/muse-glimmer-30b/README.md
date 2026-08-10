# Muse-Glimmer-30B on llama.cpp

Run Meta's `meta-models/Muse-Glimmer-30B` (Apache 2.0, ~29.6B params, 131k
context) via `llama-server`, either locally or on this repo's [scale-to-zero
cloud GPU stack](../../../remote/). This example targets the **K-Quant-Dynamic**
quant tier (~32 GB VRAM per the model card) — well inside a single L40S's
48 GB, so it fits the `g6e.xlarge` the `remote/` stack boots by default.

## Before you start

- **This is a very new model.** Confirm your llama.cpp build actually supports
  its architecture before betting an evening on it — check the
  [llama.cpp releases/issues](https://github.com/ggml-org/llama.cpp) for
  "Muse-Glimmer" or its architecture name. Day-one HF releases routinely land
  before inference-engine support does.
- **This path is text-only.** The model card describes an integrated vision
  encoder, but no separate projector file is distributed, and this repo's
  remote seed script (`remote/scripts/seed-model.mjs`) explicitly excludes
  `mmproj` files when it fetches GGUF weights. Don't expect image input to
  work through llama.cpp here — vLLM would be the multimodal path, at a much
  higher VRAM cost (64 GB for full precision).
- **The exact quant repo/tag isn't filled in below.** Open the model page's
  ["Quantizations" tab](https://huggingface.co/meta-models/Muse-Glimmer-30B)
  and find the GGUF repo + tag for the **K-Quant-Dynamic** (~32 GB) tier, then
  replace `REPLACE_WITH_DYNAMIC_QUANT_TAG` in [`preset.ini`](preset.ini) and
  below. (The smaller K-Quant-17GB tier, ~24 GB VRAM, is a safer fallback if
  Dynamic runs tight.)

## Prerequisites

- A recent build of [llama.cpp](https://github.com/ggml-org/llama.cpp) that
  includes `llama-server` (e.g. `brew install llama.cpp`, or build from
  source) — see the architecture-support caveat above first.
- A GPU with headroom above ~32 GB VRAM for this tier (less for the 17GB
  tier). The model is not gated, so no Hugging Face license acceptance is
  needed to download it.

## 1. Test it locally first

Before deploying to the cloud, confirm the quant actually loads and serves
correctly:

```sh
llama-server -hf meta-models/Muse-Glimmer-30B-GGUF:REPLACE_WITH_DYNAMIC_QUANT_TAG \
  --jinja -ngl 99 --ctx-size 32768 --host 127.0.0.1 --port 8080
```

Or, from this directory, let `outfit` build the command from
[`preset.ini`](preset.ini):

```sh
outfit serve --dry-run   # print the llama-server command without running it
outfit serve             # run it
```

Check it's up:

```sh
curl http://127.0.0.1:8080/v1/models
```

Then point opencode at it:

```sh
outfit apply examples/llamacpp/muse-glimmer-30b/Outfit
```

## 2. Deploy to the cloud

Once the quant checks out locally, put the same model on this repo's
[remote GPU stack](../../../docs/commands/remote.md) so it's reachable without
keeping your own machine on:

```sh
outfit remote bootstrap          # once per AWS account — skip if already done
outfit remote deploy examples/llamacpp/muse-glimmer-30b/Outfit --dry-run
outfit remote deploy examples/llamacpp/muse-glimmer-30b/Outfit
```

`deploy` reads this Outfit and its preset — `PROVIDER llamacpp` picks the
engine, `PRESET` names the model and its flags, `REMOTE muse-glimmer-30b`
names the environment it registers under `~/.config/outfit/remotes/`. If the
weights aren't in S3 yet, the deploy Lambda seeds them automatically
(~15–20 min for a ~32 GB download).

```sh
eval "$(outfit remote start)"    # boots the instance, waits for the model to
                                  # load, exports OPENAI_BASE_URL/OPENAI_API_KEY
outfit apply examples/llamacpp/muse-glimmer-30b/Outfit
outfit remote status             # confirm it's healthy
outfit remote stop               # when you're done — otherwise it idles out
                                  # on its own after ~15-20 min
```

See [`remote/docs/costs.md`](../../../remote/docs/costs.md) for what this
costs per hour and how the idle/max-runtime timers bound it.

## See also

- [`examples/llamacpp/qwen3.6-27b`](../qwen3.6-27b/README.md) — the example
  this one is modelled on, for a smaller dense model.
- [`docs/commands/remote.md`](../../../docs/commands/remote.md) — full
  `outfit remote` reference.
