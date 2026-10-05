# Muse-Glimmer-30B on llama.cpp

Run Meta's [`meta-models/Muse-Glimmer-30B`](https://huggingface.co/meta-models/Muse-Glimmer-30B)
(Apache 2.0, ~29.6B params, 131k context, text + image input) via
`llama-server`, using the [`Spinloop`](Spinloop) and [`preset.ini`](preset.ini) in
this directory. This example targets Meta's **K-Quant-Dynamic** build.

## Which build this uses

Meta publishes GGUFs at
[`meta-models/Muse-Glimmer-30B-GGUF`](https://huggingface.co/meta-models/Muse-Glimmer-30B-GGUF):

| File | Size | Target | Degradation vs full precision |
|---|---|---|---|
| `Muse-Glimmer-30B-KQuant-Dynamic-Q4_K_XL.gguf` | 19.65 GB | 32 GB VRAM | 0.2% |
| `Muse-Glimmer-30B-KQuant-17GB-Q4_K_M.gguf` | 16.76 GB | 24 GB VRAM | 1.0% |
| `mmproj-Muse-Glimmer-30B-Q4_K_M.gguf` | 1.40 GB | perception encoder — required for image input | — |
| `dflash-Muse-Glimmer-30B-Q4_K_M.gguf` | 1.63 GB | DFlash drafter for speculative decoding | — |

Both main builds are **text-only on their own**;
`mmproj-Muse-Glimmer-30B-Q4_K_M.gguf` is what adds image input.

Meta renamed every file in this repo in August 2026 — the old
`muse-glimmer-30B-kquant-dynamic.gguf` spellings are gone. `--hf-file` has to
match exactly, and a miss fails in two stages that don't obviously belong
together: `common_download_get_hf_plan: file 'X' not found in repository`
(which helpfully lists what *is* there), then `failed to load model ''` as the
empty path reaches the loader.

## You need llama.cpp b10353 or newer — b10423 for tool calling

Support landed in
[PR #26841](https://github.com/ggml-org/llama.cpp/pull/26841), commit
`62bf73d2`, merged 2026-08-10. The first tagged release carrying it is
**`b10353`** (2026-08-10, four commits past the merge); `b10344` and earlier
sit before it.

Homebrew is the usual trap here. `brew install llama.cpp` was pinned at
`10330` for a long stretch — old enough that the architecture is not
registered at all — and the formula has since switched to semantic versions, so
`brew upgrade llama.cpp` moves you to `0.4.0` or later, well past the merge.

That is enough to *run* the model. For **parallel tool calling** you want
**`b10423`** or newer, which is where the EOM fix (`0b1bad14`, see below) first
reached a release — the gap between the two is worth knowing about if you are
pointing a coding agent at this. Check before you build:

```sh
llama-server --version    # e.g. "version: 10330 (687e77892)" — too old
```

If it predates the merge, build from master (Apple Silicon):

```sh
git clone https://github.com/ggml-org/llama.cpp && cd llama.cpp
cmake -B build && cmake --build build --config Release -j
```

The binaries land in `build/bin`. On CUDA add `-DGGML_CUDA=ON` to the configure
step.

## Three things that don't work the way you'd expect

**The `-hf repo:TAG` shorthand can't select these files.** Bare
`-hf meta-models/Muse-Glimmer-30B-GGUF` resolves to the repo default, which is
the **17GB** build, not the dynamic one, and the manifest endpoint won't take a
filename fragment in place of a tag. Name the repo and file separately instead —
which is what the preset does, via `hf` (`--hf-repo`) and `hff` (`--hf-file`).

**DFlash speculative decoding works, but only if you leave it room.** It is on
in the preset. The failure people hit is a crash at load:

```
vector::_M_range_check: __n (which is 1) >= this->size() (which is 1)
```

That is [ggml-org/llama.cpp#26894](https://github.com/ggml-org/llama.cpp/issues/26894),
and it is **not** a model or metadata problem, despite what the issue title
still says. The original report blamed an array-valued
`muse-glimmer.attention.sliding_window_pattern`; the reporter withdrew that
diagnosis on 2026-08-13 after failing to reproduce it that way, and
[PR #26900](https://github.com/ggml-org/llama.cpp/pull/26900) — which its author
had struck out "Nixes #26894" on — is unrelated.

The confirmed cause is the default layer-split path in `src/llama-model.cpp`.
Splits are weighted by each device's free memory; when every device reports
zero free, the normalisation divides by zero, the resulting NaNs make
`std::upper_bound` return `end()`, and `devices.at(n_devices())` throws exactly
that message. So it fires when the target model plus its KV cache has eaten the
device by the time the drafter is loaded — a pure function of `--ctx-size`.
A second reporter pinned the boundary on a 24 GB RTX 4090: `-c 100000` loads,
`-c 115000` throws, with ~466 MiB free against a 1.14 GB drafter.
[PR #28221](https://github.com/ggml-org/llama.cpp/pull/28221) is the open guard
for the NaN itself; it will turn the crash into a clean error, not into free
memory.

Which means the fix is a memory budget, not a flag. See
[Memory](#memory) below for this machine's, and note that the preset runs
**one** slot rather than four for exactly this reason.

Three more things follow, none of them obvious:

- **`--spec-type draft-dflash` is required.** Meta's card shows only
  `-md dflash-… -ngld 99`, which leaves llama.cpp on its default `draft-simple`
  path — ordinary autoregressive drafting, the wrong shape for a
  block-diffusion drafter that emits 16 tokens per forward pass.
- **You don't need to download the drafter yourself.** That flag also sets
  `download_dflash` on the `--hf-repo` plan, so `llama-server` picks the
  `dflash-` sibling out of the same repo and fetches it
  ([PR #25811](https://github.com/ggml-org/llama.cpp/pull/25811)). Setting
  `--spec-draft-model` as well downloads it and then ignores it, so the preset
  leaves that to the cloud path.
- **`--spec-draft-n-max` caps out at 15, not 16.** The drafter's
  `dflash.block_size` is 16 and in-place denoising yields at most
  `block_size - 1` tokens. Asking for 16 logs `requested draft size … exceeds
  the trained block size 16 -- clamping to 15` and carries on.

**DFlash2 is not available for this model.** DFlash2
([PR #27816](https://github.com/ggml-org/llama.cpp/pull/27816), merged
2026-08-27) adds a local convolution and a candidate selector, but it is
neither a flag nor a `--spec-type` value — the type list is `draft-simple`,
`draft-eagle3`, `draft-mtp`, `draft-dflash`, `draft-dspark` and the `ngram-*`
family, with no `draft-dflash2`. llama.cpp reads
`llama_model_dflash_selector_top_k()` off the *drafter* and sets `is_dflash2`
when it is greater than zero. Meta's `dflash-Muse-Glimmer-30B-Q4_K_M.gguf`
carries 33 metadata keys, none of them `dflash.selector_top_k`, and no
`selector_*` tensors — so it is a v1 sidecar and there is nothing to switch on
until a DFlash2 drafter is published for Muse Glimmer.

The rope format, incidentally, is *not* the problem either. An earlier revision
of this file claimed the drafter was unusable because the merge commit said it
"breaks compatibility with Meta's distributed DFlash GGUFs, as the Q/K are
stored in NEOX (rotated half) format". That was one bullet of a squashed PR and
does not describe where the branch landed: master resolves a non-DSV4 DFlash
backbone to `LLAMA_ROPE_TYPE_NEOX`, and the drafter converter deliberately does
no permutation to match.

## Running it

```sh
llama-server \
  --hf-repo meta-models/Muse-Glimmer-30B-GGUF \
  --hf-file Muse-Glimmer-30B-KQuant-Dynamic-Q4_K_XL.gguf \
  --no-mmproj --flash-attn on \
  --jinja --ctx-size 131072 --parallel 1 -ngl 99 \
  --spec-type draft-dflash --spec-draft-ngl 999 --spec-draft-n-max 15 \
  --chat-template-kwargs '{"reasoning_strength":"high"}' \
  --temp 1.0 --top-p 0.95 --top-k 64 \
  --host 127.0.0.1 --port 8080 --alias muse-glimmer-30b
```

(`spinloop serve --dry-run` prints the same command, built from
[`preset.ini`](preset.ini); this is what it produces.)

`--no-mmproj` is what makes this text-only: the repo publishes
`mmproj-Muse-Glimmer-30B-Q4_K_M.gguf` beside the weights, and `--hf-repo`
fetches and loads it automatically otherwise. Dropping it saves 1.4 GB and the
encoder load.

`--jinja` is **mandatory**, not a nicety. The chat template is embedded in the
GGUF and nothing else supplies it — there is no separate template file and
`--chat-template-file` is not needed — but without the flag the multimodal CLI
aborts with `this custom template is not supported, try using --jinja`.

The three `--spec-*` flags are all that speculative decoding needs — the
drafter is fetched from the same repo, not named. A `[spec] failed to measure
draft model memory` warning at startup is expected and harmless per Meta's
card; the drafter loads and serves normally after it.

### `--ctx-size` is a total, and overflow fails silently

`llama-server` divides `--ctx-size` across `--parallel` slots, so **one request
gets `ctx-size / np`**. The startup log's `n_ctx_slot` is the number that
actually bounds a generation.

This bites harder here than it looks, because Muse Glimmer reasons at length
and *nothing errors when a generation runs out of slot context* — the request
simply returns no answer. In an eval that reads as a wrong answer rather than a
failure, with nothing in the logs to explain the lower score.

So scale the total **with** `np` rather than trimming it. The KV cache is
cheap per token — GQA with 2 KV heads at head_dim 128 is 1 KiB per layer per
token, and 39 of the 52 layers are sliding-window, capped at the 2048 window —
but the 13 full-attention layers still cost 1.6 GiB per 131072 tokens. So
`--ctx-size 524288 --parallel 4` gives four slots the full trained window at
6.5 GiB of KV, and `--ctx-size 131072 --parallel 1` gives one slot the same
window at 1.6 GiB.

This example takes the second, because the drafter has to fit as well — see
[Memory](#memory). Drop `--spec-type` and the four-slot version fits again.

21.3 GB has to go somewhere, and llama.cpp keeps its **own** download cache —
it never reads the Hugging Face cache, so `HF_HOME` and `~/.cache/huggingface`
have no effect here. The location is platform-dependent
(`common/common.cpp:fs_get_cache_directory`): `~/Library/Caches/llama.cpp` on
macOS, `~/.cache/llama.cpp` on Linux. `LLAMA_CACHE` is checked first on both,
so it's the portable way to put the weights on another volume:

```sh
export LLAMA_CACHE=/Volumes/big-disk/llama.cpp
```

Or let `spinloop` build that from [`preset.ini`](preset.ini):

```sh
spinloop serve --dry-run    # print the command
spinloop serve              # run it
curl http://127.0.0.1:8080/v1/models
spinloop harness apply              # point opencode at it
```

This example is deliberately **text-only**. If you do want image input, drop
`no-mmproj` from the preset: `llama-server` then picks up
`mmproj-Muse-Glimmer-30B-Q4_K_M.gguf` from the same repo, fetches it alongside
the weights and logs `loaded multimodal model`, and `/v1/models` advertises the
`multimodal` capability — budget **21.05 GB** of cache for that pair rather than
19.65 GB. Note that this is cache footprint *and* VRAM: the encoder does not fit
alongside the drafter on a 32 GB machine.

Cache footprint as configured here: **21.28 GB** — 19.65 GB of weights plus the
1.63 GB drafter, which `--spec-type draft-dflash` fetches from the same repo.

### Reasoning comes back on a separate field

This model reasons before answering, and llama.cpp splits that out: the answer
is in `message.content`, the thinking in `message.reasoning_content`. A short
`max_tokens` will be spent entirely on reasoning and return **empty content** —
which looks like a broken model but isn't. Give it room, and read the right
field.

### Memory

This is the setting that decides whether the configuration runs at all, and on
a 32 GB Mac there is not much slack. Read your own ceiling rather than assuming
one — it is Metal's `recommendedMaxWorkingSetSize`, which on a base M4 / 32 GB
is **24.96 GiB**:

```sh
echo 'import Metal
let d = MTLCreateSystemDefaultDevice()!
print(Double(d.recommendedMaxWorkingSetSize)/1073741824, "GiB")' > /tmp/m.swift && swift /tmp/m.swift
```

The budget as this example is configured:

| | |
|---|---|
| weights, `Q4_K_XL` | 18.3 GiB |
| drafter, `dflash` `Q4_K_M` | 1.5 GiB |
| KV, 13 full-attention layers x 131072 x 1 KiB | 1.6 GiB |
| KV, 39 sliding layers capped at the 2048 window | ~0.2 GiB |
| compute buffers | ~1.0 GiB |
| **total** | **~22.6 GiB** |

That leaves roughly 2 GiB against the working-set limit, and around 9 GiB of
the machine's 32 GB for macOS and everything else. It is not a reservation —
the limit is advisory and Metal will let you past it into swap, where
generation slows to a crawl rather than failing cleanly.

Four slots at `--ctx-size 524288` costs 6.5 GiB of KV instead of 1.6 and does
not fit with the drafter loaded; that overrun is what produces the
`vector::_M_range_check` crash described above. If you want both the
concurrency and the drafter, pick one of:

- `Muse-Glimmer-30B-KQuant-17GB-Q4_K_M.gguf` — 2.7 GB smaller, 1.0%
  degradation instead of 0.2%
- `ctk = q8_0` and `ctv = q8_0` in the preset — halves the KV cache
- `sudo sysctl iogpu.wired_limit_mb=28000` — raises the ceiling, resets on
  reboot, and starves the rest of the machine

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

Being bandwidth-bound is also why the drafter is worth its 1.5 GiB here. A
verified draft block is checked in one pass over the weights, so accepted
tokens come at close to no extra bandwidth cost — the ceiling that tuning
cannot move is a per-*pass* ceiling, not a per-token one.

### Verified

Confirmed working on llama.cpp master `030ebb5` (reported as `version: 200`),
built for Metal on macOS 26.5, base M4 / 32 GB, no `iogpu.wired_limit_mb`
change needed: model and encoder load, chat completions return correct answers,
and tool calls come back well-formed with the right arguments.

That run predates the current file: it was text-only with no drafter, and at
`--ctx-size 524288 --parallel 4`. The one-slot-plus-drafter configuration above
is derived from the memory budget, not measured — expect to check
`n_ctx_slot` and the buffer sizes in the startup log the first time you run
it.

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

**The drafter needs one extra line for the cloud.** Locally the preset relies
on `--spec-type draft-dflash` pulling the `dflash-` sibling off `--hf-repo`;
`spinloop cloud deploy` does not go through that path. It reads
`spec-draft-model` from the preset, takes its **basename** and asks the seed for
that file from the model's own repo, so the local path is never sent and the
instance loads its own synced copy. Add:

```ini
spec-draft-model = ./Muse-Glimmer-30B-GGUF/dflash-Muse-Glimmer-30B-Q4_K_M.gguf
```

and deploy prints what it picked up:

```
  draft:   dflash-Muse-Glimmer-30B-Q4_K_M.gguf
```

Without that line there is no such output and the seed fetches the weights
alone. The basename has to match the repo filename **exactly** — companions are
selected by exact name, not by the case-insensitive glob the quant tag uses, so
the old `dflash-kquant.gguf` spelling now fails the deploy with a "not found"
naming it. `--spec-type draft-dflash` stays yours to set either way: the
deployment owns *where* the drafter is, not how the engine is told to use it.

A `g6e.xlarge` is a 48 GB L40S, so the local memory budget does not bind there —
`ctx-size` and `np` can go back to 524288 and 4 in a cloud-only Spinloop.

Deploy also needs a `MODEL` line, which the [`Spinloop`](Spinloop) deliberately
leaves out — the cloud seed globs filenames rather than resolving a tag, so it
wants the quant suffix:

```dockerfile
MODEL  meta-models/Muse-Glimmer-30B-GGUF:kquant-dynamic
```

…and register it under a name of your choosing:

```sh
spinloop cloud deploy --env muse-glimmer-30b
```

Be precise with that suffix. The seed downloads everything matching
`*<quant>*`, sets aside the files named as companions, drops projectors, and
requires exactly one match for the weights — so a looser `:kquant` fails with
both text builds listed. The glob is case-insensitive, which is why
`kquant-dynamic` still matches the renamed
`Muse-Glimmer-30B-KQuant-Dynamic-Q4_K_XL.gguf`.

The encoder stays out unless you ask for it, which is what we want here: this
example sets `no-mmproj`, and the seed only fetches a projector when one is
named as an `mmproj` companion.

Adding `MODEL` breaks the local `spinloop serve` path above, since it becomes
`--hf-repo meta-models/Muse-Glimmer-30B-GGUF:kquant-dynamic` — a tag that
doesn't resolve. Keep separate Spinloops if you want both.

```sh
spinloop cloud deploy --env muse-glimmer-30b --dry-run
spinloop cloud deploy --env muse-glimmer-30b
eval "$(spinloop cloud start --env muse-glimmer-30b --print-env)"   # --print-env
                                        # is what prints the OPENAI_BASE_URL/OPENAI_API_KEY
                                        # export lines; without it, start's output is
                                        # progress text on stderr and there is
                                        # nothing on stdout for eval to run
spinloop cloud stop --env muse-glimmer-30b
```

Costs and the idle/max-runtime bounds are in
[`remote/docs/costs.md`](../../../remote/docs/costs.md).

## See also

- [`examples/llamacpp/qwen3.6-27b`](../qwen3.6-27b/README.md) — the example this
  one is modelled on.
- [`docs/commands/cloud.md`](../../../docs/commands/cloud.md) — full
  `spinloop cloud` reference.
