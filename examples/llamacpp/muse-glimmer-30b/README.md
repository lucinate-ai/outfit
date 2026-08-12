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

## You need llama.cpp b10355 or newer

Support landed in
[PR #26841](https://github.com/ggml-org/llama.cpp/pull/26841), commit
`62bf73d2`, merged 2026-08-10. The first tagged release carrying it is
**`b10355`** (2026-08-10); `b10344` and earlier sit before the merge, and
Homebrew's formula lagged further behind. Check before you build:

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

**The DFlash drafter needs `--spec-type draft-dflash`, and Meta's model card
doesn't mention it.** The card shows only `-md dflash-kquant.gguf -ngld 99`,
which leaves llama.cpp on its default `draft-simple` path — ordinary
autoregressive drafting. DFlash is a block-diffusion drafter that emits 16
tokens per forward pass, so it needs its own speculative type selected
explicitly. The preset sets it.

The drafter also can't be fetched with `--hf-repo`/`-hfd`. Those resolve a repo
to a single default file, which here is the 17GB text build. Download it once
and point `--spec-draft-model` at the local file:

```sh
hf download meta-models/Muse-Glimmer-30B-GGUF \
  --include "dflash-kquant.gguf" --local-dir ./Muse-Glimmer-30B-GGUF
```

An earlier revision of this file claimed the published drafter was unusable on
upstream llama.cpp, on the strength of a line in the merge commit: *"this breaks
compatibility with Meta's distributed DFlash GGUFs, as the Q/K are stored in
NEOX (rotated half) format"*. That line is one bullet of a squashed PR and does
not describe where the branch landed. In current master
(`src/llama-model.cpp`), a non-DSV4 DFlash backbone resolves to
`LLAMA_ROPE_TYPE_NEOX`, and llama.cpp's own drafter converter
(`conversion/muse_glimmer.py`, `MuseGlimmerAssistantModel.modify_tensors`)
deliberately does **no** permutation — "DFlash defaults to NEOX (rotate_half)
rope, matching transformers HF layout for Q/K". NEOX weights, NEOX rope: Meta's
file matches what master expects, and no self-conversion is needed.

## Running it

```sh
llama-server \
  --hf-repo meta-models/Muse-Glimmer-30B-GGUF \
  --hf-file muse-glimmer-30B-kquant-dynamic.gguf \
  --no-mmproj \
  --spec-type draft-dflash \
  --spec-draft-model ./Muse-Glimmer-30B-GGUF/dflash-kquant.gguf \
  --spec-draft-ngl 999 --spec-draft-n-max 16 --flash-attn on \
  --jinja --ctx-size 524288 --parallel 4 -ngl 99 \
  --chat-template-kwargs '{"reasoning_strength":"high"}' \
  --temp 1.0 --top-p 0.95 --top-k 64 \
  --host 127.0.0.1 --port 8080
```

`--no-mmproj` is what makes this text-only: the repo publishes
`mmproj-kquant.gguf` beside the weights, and `--hf-repo` fetches and loads it
automatically otherwise. Dropping it saves 1.4 GB and the encoder load.

`--jinja` is **mandatory**, not a nicety. The chat template is embedded in the
GGUF and nothing else supplies it — there is no separate template file and
`--chat-template-file` is not needed — but without the flag the multimodal CLI
aborts with `this custom template is not supported, try using --jinja`.

A `[spec] failed to measure draft model memory` warning at startup is expected
and harmless per Meta's card — the drafter loads and serves normally after it.

### `--ctx-size` is a total, and overflow fails silently

`llama-server` divides `--ctx-size` across `--parallel` slots, so **one request
gets `ctx-size / np`**. The startup log's `n_ctx_slot` is the number that
actually bounds a generation.

This bites harder here than it looks, because Muse Glimmer reasons at length
and *nothing errors when a generation runs out of slot context* — the request
simply returns no answer. In an eval that reads as a wrong answer rather than a
failure, with nothing in the logs to explain the lower score.

So scale the total **with** `np` rather than trimming it: `--ctx-size 524288
--parallel 4` gives each of the four slots the full trained 131072. The KV
cache stays cheap — GQA with 2 KV heads, plus sliding-window attention on 3 of
every 4 layers — so this costs a few GB, not tens.

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

This example is deliberately **text-only**. If you do want image input, drop
`no-mmproj` from the preset: `llama-server` then picks up `mmproj-kquant.gguf`
from the same repo, fetches it alongside the weights and logs `loaded
multimodal model`, and `/v1/models` advertises the `multimodal` capability —
budget **21.05 GB** of cache for the pair rather than 19.65 GB.

Cache footprint as configured here: 19.65 GB for the weights plus 1.63 GB for
the drafter.

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

**Reasoning cannot be switched off.** The template opens the thinking channel
unconditionally, so `--reasoning off`, `--reasoning on` and
`"reasoning_effort": "none"` all do nothing. What you control is *how much*, via
the `reasoning_strength` template variable — `low`/`medium`/`high`/`xhigh`,
defaulting to `high`. Server-wide it is a flag, and the preset sets it:

```sh
--chat-template-kwargs '{"reasoning_strength":"xhigh"}'
```

Per request, send the same thing as `chat_template_kwargs`. Use `high` or
`xhigh` for coding and agentic work, and `--reasoning-budget N` to hard-cap
thinking tokens.

(An earlier revision of this file said reasoning depth was set with a
`Reasoning strength:` line in the system prompt. That was wrong — it is a
template variable.)

### Don't stop on `<|eom|>`

The stop tokens are `<|end_of_text|>` (200001) and `<|eot|>` (200008).
`<|eom|>` marks end-of-*message*, not end-of-turn — the turn continues past it,
and stopping there collapses parallel tool calling. Leave it alone if you add
custom stop strings.

llama.cpp's own handling of this was fixed in
[`0b1bad14`](https://github.com/ggml-org/llama.cpp/commit/0b1bad14) ("chat: fix
muse-glimmer detection of tool calls after EOM", #26879, 2026-08-11), which at
the time of writing is **master-only** — `b10362` is 18 commits behind it, and
`b10355` further back. Basic tool calling works without it (see Verified
below); if you lean on *parallel* tool calls, build from master rather than
taking a release.

## Deploying to the cloud

The [`remote/`](../../../remote/) stack can serve this too, but **not without a
re-bake first**: its llama.cpp AMI installs a prebuilt binary from
`ai-dock/llama.cpp-cuda`, pinned to `b10107` in
[`remote/lib/config.ts`](../../../remote/lib/config.ts) — long before the Muse
Glimmer merge.

ai-dock published **`b10355`** on 2026-08-11, and it is the first of their
builds that carries the merge (their previous was `b10333`; upstream tag
`b10355` is 6 commits ahead of `62bf73d2` and 0 behind). The CUDA 12.8 amd64
asset the bake downloads exists for it. So:

```sh
# bump llamacppRelease to b10355 in remote/lib/config.ts and remote/cdk.json
pnpm bake llamacpp     # ~15-25 min
```

Note the cloud path loses the drafter as well as the encoder: the seed script
takes a single GGUF and normalises it to `model.gguf`, so `dflash-kquant.gguf`
is not carried across and speculative decoding is local-only for now.

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

Seeding also excludes `mmproj` files, so a cloud deployment is text-only
regardless of the encoder being published — which is what we want here anyway.

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
