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

## You need llama.cpp b10355 or newer — b10423 for tool calling

Support landed in
[PR #26841](https://github.com/ggml-org/llama.cpp/pull/26841), commit
`62bf73d2`, merged 2026-08-10. The first tagged release carrying it is
**`b10355`** (2026-08-10); `b10344` and earlier sit before the merge, and
Homebrew's formula lagged further behind.

That is enough to *run* the model. For **parallel tool calling** you want
**`b10423`** or newer, which is where the EOM fix (`0b1bad14`, see below) first
reached a release — the gap between the two is worth knowing about if you are
pointing a coding agent at this. Check before you build:

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

**The DFlash drafter does not work with this repo's GGUF, and the preset leaves
it off.** Enabling it does not merely forfeit the speedup — `llama-server`
crashes at load:

```
vector::_M_range_check: __n (which is 1) >= this->size() (which is 1)
```

Meta's official GGUF encodes `muse-glimmer.attention.sliding_window_pattern` as
an **array**; the DFlash bind path only handles the scalar form. Tracked
upstream as [ggml-org/llama.cpp#26894](https://github.com/ggml-org/llama.cpp/issues/26894),
still open. It is a metadata path, so it is not specific to CUDA or Metal, and
it affects the exact pair published in the same repo: the model creator's own
GGUF cannot bind the model creator's own drafter. PR #26900 is **not** the fix —
its author struck out "Nixes #26894".

Three things follow, none of them obvious:

- **`--spec-type draft-dflash` is required** whenever you do re-enable it.
  Meta's card shows only `-md dflash-kquant.gguf -ngld 99`, which leaves
  llama.cpp on its default `draft-simple` path — ordinary autoregressive
  drafting, the wrong shape for a block-diffusion drafter that emits 16 tokens
  per forward pass.
- **The drafter can't be fetched with `--hf-repo`/`-hfd`.** Those resolve a repo
  to one default file, which here is the 17GB text build. Download it
  explicitly: `hf download meta-models/Muse-Glimmer-30B-GGUF --include
  "dflash-kquant.gguf" --local-dir ./Muse-Glimmer-30B-GGUF`
- **Unsloth's conversion binds the drafter fine**, per the issue — its
  `sliding_window_pattern` is a scalar. Switching to it means different
  filenames throughout (`Muse-Glimmer-30B-UD-Q4_K_XL.gguf`, no
  `kquant-dynamic`), so the Outfit, preset, `MODEL` tag and companion wiring all
  change — and #26900 may since have disallowed the scalar form it relies on.

The rope format, incidentally, is *not* the problem. An earlier revision of this
file claimed the drafter was unusable because the merge commit said it "breaks
compatibility with Meta's distributed DFlash GGUFs, as the Q/K are stored in
NEOX (rotated half) format". That was one bullet of a squashed PR and does not
describe where the branch landed: master resolves a non-DSV4 DFlash backbone to
`LLAMA_ROPE_TYPE_NEOX`, and the drafter converter deliberately does no
permutation to match. Rope lines up; the bind path is what fails.

## Running it

```sh
llama-server \
  --hf-repo meta-models/Muse-Glimmer-30B-GGUF \
  --hf-file muse-glimmer-30B-kquant-dynamic.gguf \
  --no-mmproj --flash-attn on \
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

No speculative-decoding flags, per the DFlash note above. If you re-enable them
once #26894 is fixed, a `[spec] failed to measure draft model memory` warning at
startup is expected and harmless per Meta's card — the drafter loads and serves
normally after it.

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

Cache footprint as configured here: **19.65 GB**, the weights alone — the
drafter is not downloaded, since speculative decoding is off.

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
muse-glimmer detection of tool calls after EOM", #26879, 2026-08-11). It took a
few days to reach a tagged release, so a build from early August will not have
it — check with `llama-server --version` and compare against that commit. Basic
tool calling works without it (see Verified below); *parallel* tool calls need
it.

## Deploying to the cloud

The [`remote/`](../../../remote/) stack can serve this too, but **not without a
re-bake first**: its llama.cpp AMI installs a prebuilt binary from
`ai-dock/llama.cpp-cuda`, and the pin in
[`remote/lib/config.ts`](../../../remote/lib/config.ts) has to be new enough for
both the Muse Glimmer merge and the EOM tool-call fix above. `b10423`
(2026-08-14) was ai-dock's first build carrying both; the pin now sits at
`b10435`. Each of their builds ships a
`llama.cpp-<tag>-cuda-12.8-amd64.tar.gz` asset, which is what the bake
downloads.

Bumping needs **both** halves, or nothing changes:

```sh
# 1. llamacppRelease -> <tag>, in remote/lib/config.ts AND remote/cdk.json
# 2. bump the llamacpp entry in RUNNER_VERSION in remote/lib/image-stack.ts —
#    Image Builder treats a recipe version as immutable, so without this the
#    pin change produces no new AMI
pnpm deploy:image
pnpm bake llamacpp     # ~15-25 min
```

Muse Glimmer itself runs fine on CUDA. Two CUDA-side reports exist but neither
applies to a single-GPU `g6e.xlarge`: a multi-GPU tensor-split assert
([#26902](https://github.com/ggml-org/llama.cpp/issues/26902)) and an mmproj
memory/prefill regression ([#26873](https://github.com/ggml-org/llama.cpp/issues/26873)),
which this text-only example avoids anyway.

**The drafter would be carried across, but is off** — see the DFlash note
above. `outfit remote deploy` reads `spec-draft-model` from the preset, takes
its **basename** and asks the seed for that file from the model's own repo, so
the local path is never sent and the instance loads its own synced copy. Deploy
prints what it picked up:

```
  draft:   dflash-kquant.gguf
```

With the flags commented out there is no such line, and the seed fetches the
weights alone. When #26894 is fixed, uncommenting is all that is needed —
noting that the basename must match the filename in the Hugging Face repo, or
the seed fails with a "not found" naming it, and that `--spec-type
draft-dflash` stays yours to set: the deployment owns *where* the drafter is,
not how the engine is told to use it.

Deploy also needs a `MODEL` line, which the [`Outfit`](Outfit) deliberately
leaves out — the cloud seed globs filenames rather than resolving a tag, so it
wants the quant suffix:

```dockerfile
MODEL  meta-models/Muse-Glimmer-30B-GGUF:kquant-dynamic
REMOTE muse-glimmer-30b
```

Be precise with that suffix. The seed downloads everything matching
`*<quant>*`, sets aside the files named as companions, drops projectors, sorts
what's left and takes the first — so a looser `:kquant` would pull in more than
you meant. `kquant-dynamic` matches exactly one file.

The encoder stays out unless you ask for it, which is what we want here: this
example sets `no-mmproj`, and the seed only fetches a projector when one is
named as an `mmproj` companion.

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
