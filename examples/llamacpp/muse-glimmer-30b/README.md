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

Or let `outfit` build that from [`preset.ini`](preset.ini):

```sh
outfit serve --dry-run    # print the command
outfit serve              # run it
curl http://127.0.0.1:8080/v1/models
outfit apply              # point opencode at it
```

For image input, fetch the encoder and add `--mmproj`:

```sh
hf download meta-models/Muse-Glimmer-30B-GGUF --include "mmproj-kquant.gguf"
```

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
