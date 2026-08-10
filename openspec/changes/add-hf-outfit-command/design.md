## Context

See proposal.md — Why. The constraints that shape the approach:

- **outfit has almost nothing to install.** Three direct module dependencies
  today (`hujson`, `yaml.v3`, `aws-sdk-go-v2`), and the AWS SDK is called out in
  AGENTS.md as "the repo's only AWS/network dependency". Anything added here is
  measured against that.
- **`internal/discovery` already sets the pattern for outbound HTTP**: a small
  client, a bounded timeout, an in-process cache, and failures that never spew.
  Hub access should look like its sibling, not like a new subsystem.
- **The pieces the command produces already exist.** `outfit.Selection` and
  `outfit.Format` render an Outfit; `applySelection` writes one to a harness;
  `contextsize.Parse` reads `128k`. This change is a resolver feeding parts that
  are already built.
- **The engines already download.** `llama-server -hf`, vLLM and mlx all fetch
  their own weights on first use. There is no gap to fill there, only a second
  copy to avoid.

## Goals / Non-Goals

**Goals:**

- One command from a pasted model reference to a working Outfit, with every
  inference visible and individually overridable.
- Use the Hugging Face cache that is already on the machine — for the weights
  and, when possible, for the metadata, so a downloaded model resolves offline.
- Keep the dependency footprint where it is.

**Non-Goals:**

- Downloading weights (see the spec — `outfit hf` never transfers a weights
  file). No `--pull`, no progress bars, no resumable transfers.
- Writing the Hugging Face cache in any way. Reading it is a stable, documented
  layout; writing it correctly means blob dedup, symlinks, locking and etag
  bookkeeping, and there is nothing to gain from owning that.
- Searching or browsing the Hub (`outfit hf --search qwen`). A reference is
  something you paste, and the model page is a better browser than a terminal.
- Datasets, spaces, adapters, or non-model repo types.
- Teaching the catalogue about Hugging Face. `providers.yaml` stays plumbing
  only; nothing here adds a provider.

## Decisions

### A new `outfit hf` command rather than `--hf` on `add`/`apply`

The output of this work is a *file* — a reproducible description of a model
choice — and the existing commands take a selection rather than produce one.
Making it a separate command also keeps its flag set honest: `-q`, `--no-cache`
and `--output-file` are meaningless on `add`.

`--apply` closes the one-liner gap by routing the resolved `Selection` through
the same `applySelection` that `add` and `apply` use, so there is one code path
that dresses a harness, not two.

*Alternative considered:* `outfit add --hf <ref>`. Rejected because it produces
no artefact — the user ends up running `outfit export` to get back the file the
resolver already had in hand.

### `-o` means `--output-file` here, and there is no output-tokens flag

Every other command spells `-o` as `--output` (max output tokens). On `hf` it
names the file to write, as requested. To make that unambiguous rather than
merely inconsistent, `outfit hf` **omits an output-tokens flag entirely**: the
Outfit it writes carries no `OUTPUT` line, and applying one defaults output to a
quarter of the context (`contextsize.DefaultOutput`), which is what an
unspecified `OUTPUT` already means. There is therefore no reading of `-o` on
this command that silently does the other thing. The docs page states the
difference explicitly.

### A small in-repo Hub client, not a Go SDK

The closest thing to a Go SDK is `github.com/gomlx/go-huggingface`
(Apache-2.0), which does share the Python cache layout. Its module requires
`gomlx/gomlx`, `gomlx/compute`, `parquet-go`, `protobuf`, `go-sentencepiece`,
`lipgloss` and `klog` — a machine-learning framework's dependency graph, taken
on for a CLI that reads two JSON endpoints and lists a directory. The other
candidates (`bodaay/HuggingFaceModelDownloader`, `cozy-creator/hf-hub`,
`sgl-project/ome`) are downloader-shaped: their value is the write path this
change explicitly does not want.

`internal/hf` is therefore stdlib-only, importing nothing of ours except
`internal/contextsize`. What it needs:

- `GET {endpoint}/api/models/{repo}/revision/{rev}` — `siblings[].rfilename`
  for the file list, plus `tags`, `library_name` and `sha`.
- `GET {endpoint}/{repo}/resolve/{rev}/config.json` — the model's declared
  window.
- The cache layout: `models--{org}--{name}/refs/{rev}` holds the commit sha;
  `snapshots/{sha}/{path}` is a symlink into `blobs/`.

If the write path is ever wanted, the package boundary is where a library would
slot in without the command changing.

### The cache is read through injected roots, never a package-level default

`internal/hf` resolves cache roots in one place (`HF_HUB_CACHE`, else
`$HF_HOME/hub`, else `~/.cache/huggingface/hub`; `LLAMA_CACHE`, else the
platform cache dir) and every lookup takes them as arguments. That is not
tidiness: `internal/config`'s alias registry has the same rule, for the same
reason — a test that silently reads the developer's real cache passes on their
machine and fails in CI, or worse, the reverse. Tests point the roots at temp
dirs; nothing reaches `$HOME` unless the caller asked for it.

A snapshot entry counts as cached only when the symlink resolves to a readable
regular file. An interrupted download leaves either a `.incomplete` blob or a
dangling snapshot link, and both must read as "not cached" — the same rule the
`model-weights` capability already applies to the cloud's fetch marker.

### llama.cpp's cache is matched loosely, and a miss is free

`llama-server -hf` writes into `LLAMA_CACHE` (else its platform cache dir), not
the Hugging Face cache, so a model downloaded by `serve` is invisible to the HF
layout and vice versa. Its filename convention is not a documented contract, so
the lookup is a case-insensitive scan of that directory for a `.gguf` whose name
carries the repo's owner, name and the chosen quant. A false negative costs
nothing — the Outfit falls back to the repo reference and `llama-server` finds
its own cached copy anyway. A false positive is what must not happen, hence
requiring all three parts to match.

### Quantisation: parse the filenames, prefer a mid-sized K-quant

Quant names live in GGUF filenames (`…-Q4_K_M.gguf`,
`…-UD-Q4_K_XL.gguf`, `…-Q4_K_M-00001-of-00003.gguf`) and sometimes in a
directory level (`Q4_K_M/…gguf`). The parser strips the extension and any
`-000NN-of-000NN` shard suffix, then matches a quant token
(`IQ*`/`Q*_*`/`UD-*`/`F16`/`BF16`/`F32`/`MXFP4`), preferring a directory name
when the repo uses one. Files under one quant name group into a single choice.

The default preference order is an explicit, documented list — `Q4_K_M`,
`UD-Q4_K_XL`, `Q4_K_S`, `Q5_K_M`, `Q6_K`, `Q8_0` — falling back to the smallest
non-full-precision group, with the name as tie-break so the choice is stable.
Roughly 4-bit is the size that fits the machines people run local models on;
the narration lists what else was there, so the default never has to be right,
only defensible.

### Provider inference from files and library, in that order

- any `.gguf` → `llamacpp`
- else `library_name` is `mlx`, or the repo carries an `mlx` tag → `omlx`
- else any `.safetensors` → `vllm`
- else fail, naming what the repo appears to hold

Files come before tags because a repo's tags are freely edited and its files are
not. `-p` overrides the whole chain and is not validated against the repo's
contents beyond existing in the catalogue.

### `MODEL` is written in each engine's own vocabulary

| Engine | Cached | Not cached |
| --- | --- | --- |
| `llamacpp` | path to the (first) `.gguf` on disk | `org/model:QUANT` |
| `omlx` | `org/model` | `org/model` |
| `vllm` | `org/model` | `org/model` |

`llama-server` given the first shard of a split GGUF loads the rest itself, so a
path is safe for sharded quants. oMLX serves a whole model directory and picks
per request, and vLLM resolves repo ids through the HF cache on its own, so
neither gains from a path — and `local-serving` already says `MODEL` keeps its
harness-facing meaning for oMLX.

`--no-cache` forces the right-hand column. A path makes an Outfit
machine-specific, and Outfits get committed (`remote/Outfit` is, deliberately),
so the escape hatch is a flag rather than a comment in the docs.

### Context comes from the config, or not at all

`max_position_embeddings` (falling back to `text_config.max_position_embeddings`
for multimodal configs) is read from the repo's `config.json`, which GGUF and
MLX repos generally republish. Nothing is derived from the GGUF header — that
would mean a ranged read of a weights file, which the spec forbids for good
reason. No window declared means no `CONTEXT` line, matching `export`'s existing
refusal to invent one.

The declared maximum is written as-is even when it is large. It is a published
fact rather than a guess, the narration shows it, and `-c` overrides it; picking
a "sensible" smaller number would be outfit inventing a policy it cannot justify
per machine.

### stdout is the artefact, stderr is the reasoning

`outfit hf <ref> > Outfit` has to produce a clean file, so every explanation —
provider and why, quant chosen and alternatives, context and its source, cache
hit and which cache — goes to stderr. This matches `export` (pure stdout) and
`serve` (which narrates before it runs).

## Risks / Trade-offs

**A cached-path `MODEL` is not portable** → the narration says so at the moment
it happens, and `--no-cache` produces the shareable form. The default favours
the common case: an Outfit in a working directory, on the machine that has the
model.

**Hub API responses could change shape** → only three fields are relied on
(`siblings[].rfilename`, `library_name`/`tags`, `sha`), all long-standing.
Unparseable responses fail with a message naming the repo and the endpoint, and
the cache path keeps working regardless.

**llama.cpp's cache filenames are a convention, not a contract** → matching is
deliberately conservative and a miss degrades to the repo reference, which is
what the command would have written anyway.

**The default quant may not be what the user wanted** → it is named on stderr
with the alternatives beside it, and both `:QUANT` and `-q` override it. A wrong
default costs one re-run, not a download.

**A second outbound host** → `huggingface.co` joins the provider endpoints
`discovery` already contacts. Same discipline: bounded timeout, no retries, no
credentials sent unless a token was explicitly configured, nothing logged.

**Test suites that read a real cache** → all cache roots are injected, and the
test helpers set `HF_ENDPOINT`, `HF_HUB_CACHE` and `LLAMA_CACHE` to temp
locations. No test may depend on what the developer has downloaded.

## Migration Plan

Purely additive: a new command, a new leaf package, no change to the Outfit
format, the catalogue, or any harness adapter. Nothing to migrate and nothing to
roll back beyond reverting the commit.

## Open Questions

- Whether a later change should add `--pull` (fetch into the HF cache through
  outfit) or `outfit hf list` (what is already cached). Both sit on top of this
  package without altering it, and neither is needed to make the command useful.
