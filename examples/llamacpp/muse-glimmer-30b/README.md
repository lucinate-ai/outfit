# Muse-Glimmer-30B on llama.cpp

Run Meta's [`meta-models/Muse-Glimmer-30B`](https://huggingface.co/meta-models/Muse-Glimmer-30B)
(Apache 2.0, ~29.6B params, 131k context, text + image input) via
`llama-server`, using the [`Outfit`](Outfit) and [`preset.ini`](preset.ini) in
this directory. This example targets Meta's **K-Quant-Dynamic** build.

## Which build this uses

Meta publishes GGUFs at
[`meta-models/Muse-Glimmer-30B-GGUF`](https://huggingface.co/meta-models/Muse-Glimmer-30B-GGUF):

| File | Size | Target | Degradation vs full precision |
|---|---|---|---|
| `muse-glimmer-30B-kquant-dynamic.gguf` | 19.65 GB | 32 GB VRAM | 0.2% |
| `muse-glimmer-30B-kquant-17gb.gguf` | 16.76 GB | 24 GB VRAM | 1.0% |
| `mmproj-kquant.gguf` | 1.40 GB | perception encoder — required for image input | — |
| `dflash-kquant.gguf` | 1.63 GB | DFlash drafter for speculative decoding | — |

Both main builds are **text-only on their own**; `mmproj-kquant.gguf` is what
adds image input.

## You need llama.cpp from master

Support landed in
[PR #26841](https://github.com/ggml-org/llama.cpp/pull/26841), commit
`62bf73d2`, merged 2026-08-10. At the time of writing **no tagged release
contained it** — the newest release, `b10344`, sits 5 commits before that merge,
and Homebrew's formula was further back still. Check before you build:

```sh
llama-server --version    # compare the commit against 62bf73d2
```

If it predates the merge, build from master (Apple Silicon):

```sh
git clone https://github.com/ggml-org/llama.cpp && cd llama.cpp
cmake -B build && cmake --build build --config Release -j
```

The binaries land in `build/bin`. On CUDA add `-DGGML_CUDA=ON` to the configure
step.

## Two things that don't work the way you'd expect

**The `-hf repo:TAG` shorthand can't select these files.** Hugging Face's
manifest endpoint only resolves tags that are standard quantization scheme
names, and Meta's filenames aren't (`kquant-dynamic` is rejected). Bare
`-hf meta-models/Muse-Glimmer-30B-GGUF` resolves to `latest`, which is the
**17GB** build, not the dynamic one. Name the repo and file separately instead —
which is what the preset does, via `hf` (`--hf-repo`) and `hff` (`--hf-file`).

**Meta's published DFlash drafter won't load on upstream llama.cpp.** The merge
reverted deriving the drafter's rope type from its target, and says so plainly:
it "breaks compatibility with Meta's distributed DFlash GGUFs, as the Q/K are
stored in NEOX (rotated half) format". So `dflash-kquant.gguf` is not usable
as-is, and the advertised 3.1x speculative-decoding speedup is off the table
unless you convert a drafter yourself from the transformers checkpoint. The
preset leaves it out.

## Running it

```sh
llama-server \
  --hf-repo meta-models/Muse-Glimmer-30B-GGUF \
  --hf-file muse-glimmer-30B-kquant-dynamic.gguf \
  --jinja -ngl 99 --ctx-size 32768 \
  --temp 1.0 --top-p 0.95 --top-k 64 \
  --host 127.0.0.1 --port 8080
```

21 GB has to go somewhere, and llama.cpp keeps its **own** download cache —
it never reads the Hugging Face cache, so `HF_HOME` and `~/.cache/huggingface`
have no effect here. The location is platform-dependent
(`common/common.cpp:fs_get_cache_directory`): `~/Library/Caches/llama.cpp` on
macOS, `~/.cache/llama.cpp` on Linux. `LLAMA_CACHE` is checked first on both,
so it's the portable way to put the weights on another volume:

```sh
export LLAMA_CACHE=/Volumes/big-disk/llama.cpp
```

Or let `outfit` build that from [`preset.ini`](preset.ini):

```sh
outfit serve --dry-run    # print the command
outfit serve              # run it
curl http://127.0.0.1:8080/v1/models
outfit apply              # point opencode at it
```

Image input needs no extra setup. `llama-server` picks up `mmproj-kquant.gguf`
from the same repo, fetches it alongside the weights and logs `loaded
multimodal model`; `/v1/models` then advertises the `multimodal` capability. So
budget **21.05 GB** of cache for the pair, not 19.65 GB.

### Reasoning comes back on a separate field

This model reasons before answering, and llama.cpp splits that out: the answer
is in `message.content`, the thinking in `message.reasoning_content`. A short
`max_tokens` will be spent entirely on reasoning and return **empty content** —
which looks like a broken model but isn't. Give it room, and read the right
field.

### Memory

19.65 GB of weights (plus 1.4 GB if you load the encoder) has to sit in VRAM
alongside the KV cache. On a 32 GB Apple Silicon machine that fits, but not with
much room spare — the default wired limit leaves roughly 24 GB for the GPU. If
it fails to allocate, either raise the limit:

```sh
sudo sysctl iogpu.wired_limit_mb=28000    # resets on reboot
```

or drop to `muse-glimmer-30B-kquant-17gb.gguf`, which costs 1.0% degradation
instead of 0.2% and leaves considerably more headroom.

### Check the bandwidth before you commit to a machine

Generation speed here is bound by memory bandwidth, not compute: every token
reads the whole model. Roughly, `tokens/s ≈ bandwidth ÷ model size`, and in
practice you get about 75% of that.

Measured on a base M4 (10-core GPU, 32 GB, ~120 GB/s) with the dynamic build:
**4.5–4.7 tok/s** generation, ~40 tok/s prompt eval. That is close to the
hardware ceiling of ~6 tok/s, so tuning won't rescue it — it is usable for
one-off questions and too slow for agentic loops.

Meta's quoted 23.7 tok/s is an M4 **Max**, which has roughly 3.5x the
bandwidth. Check which chip you have before assuming the published figures
apply. On a bandwidth-starved machine the 17GB build is the better trade: about
15% faster for 0.8 percentage points more degradation.

### Verified

Confirmed working on llama.cpp master `030ebb5` (reported as `version: 200`),
built for Metal on macOS 26.5, base M4 / 32 GB, no `iogpu.wired_limit_mb`
change needed: model and encoder load, chat completions return correct answers,
and tool calls come back well-formed with the right arguments.

One benign warning appears at load: `special_eot_id is not in special_eog_ids -
the tokenizer config may be incorrect`. It did not affect generation or tool
calling.

### Model-specific settings

Meta recommends `temperature 1.0`, `top_p 0.95`, `top_k 64` (all in the preset).
Reasoning depth is set **in the system prompt**, not by a flag — add a
`Reasoning strength: <low|medium|high|xhigh>` line, and use `high` or `xhigh`
for coding and agentic work.

## Deploying to the cloud

The [`remote/`](../../../remote/) stack can serve this too, but **not without a
rebuild first**: its llama.cpp AMI installs a prebuilt binary from
`ai-dock/llama.cpp-cuda`, pinned to `b10107` in
[`remote/lib/config.ts`](../../../remote/lib/config.ts). ai-dock's newest build
at the time of writing was `b10333` — still before the Muse Glimmer merge. Once
ai-dock publishes a build that includes it, bump `llamacppRelease` and re-bake
(~30–40 min) before deploying.

Deploy also needs a `MODEL` line, which the [`Outfit`](Outfit) deliberately
leaves out — the cloud seed globs filenames rather than resolving a tag, so it
wants the quant suffix:

```dockerfile
MODEL  meta-models/Muse-Glimmer-30B-GGUF:kquant-dynamic
REMOTE muse-glimmer-30b
```

Be precise with that suffix. The seed script downloads everything matching
`*<quant>*`, drops `mmproj` files, sorts what's left and takes the first — so a
looser `:kquant` would match `dflash-kquant.gguf` and silently serve the 1.6 GB
drafter instead of the model. `kquant-dynamic` matches exactly one file.

Note also that seeding excludes `mmproj` files, so a cloud deployment is
text-only regardless of the encoder being published.

Adding `MODEL` breaks the local `outfit serve` path above, since it becomes
`--hf-repo meta-models/Muse-Glimmer-30B-GGUF:kquant-dynamic` — a tag that
doesn't resolve. Keep separate Outfits if you want both.

```sh
outfit remote deploy --dry-run
outfit remote deploy
eval "$(outfit remote start)"
outfit remote stop
```

Costs and the idle/max-runtime bounds are in
[`remote/docs/costs.md`](../../../remote/docs/costs.md).

## See also

- [`examples/llamacpp/qwen3.6-27b`](../qwen3.6-27b/README.md) — the example this
  one is modelled on.
- [`docs/commands/remote.md`](../../../docs/commands/remote.md) — full
  `outfit remote` reference.
