# From a Hugging Face model

You have a model page open and you want your agent pointed at it. `spinloop
hf` takes the reference you would paste anyway and writes the
[`Spinloop` file](../spinloop-file.md) that serves it:

```sh
spinloop hf unsloth/Qwen3.6-35B-A3B-GGUF
```

```
PROVIDER llamacpp
MODEL    unsloth/Qwen3.6-35B-A3B-GGUF:Q4_K_M
ALIAS    qwen3.6-35b-a3b
CONTEXT  262144
```

Stdout is a clean Spinloop you can pipe anywhere; what was inferred, and from
what, is narrated on stderr — the provider and why, the quantisation and the
alternatives, the context and its source.

## The reference

Any of these name the same thing:

```
unsloth/Qwen3.6-35B-A3B-GGUF
unsloth/Qwen3.6-35B-A3B-GGUF:Q4_K_M
https://huggingface.co/unsloth/Qwen3.6-35B-A3B-GGUF
https://huggingface.co/unsloth/Qwen3.6-35B-A3B-GGUF/blob/main/Qwen3.6-35B-A3B-Q4_K_M.gguf
```

A `:QUANT` suffix picks the quantisation, `@revision` a non-default revision,
and a `/blob/…/file` URL names one file whose quantisation is read off the
filename.

## What it infers, and how to override it

| Value | Inferred | Override |
| ----- | -------- | -------- |
| `PROVIDER` | From the repo's files: a `.gguf` means `llamacpp`, a repo published for MLX means `omlx`, plain `.safetensors` means `vllm` | `-p`, `--provider` |
| `MODEL` | The repo reference, with the chosen quantisation when the engine picks a file | `-q`, `--quant` or a `:QUANT` suffix |
| `ALIAS` | The repo name, lower-cased, with a packaging suffix such as `-GGUF` or `-MLX` removed | `-a`, `--alias` |
| `CONTEXT` | The window the model's own `config.json` declares | `-c`, `--context` (`32k`, `1.5m`, …) |

The quantisation, when not named, follows a preference for a mid-sized
K-quant — `Q4_K_M`, `UD-Q4_K_XL`, `Q4_K_S`, and so on — and the narration
lists what else the repo holds.

## The caches

Before it asks the hub anything, it checks the two places a model may already
be: the Hugging Face hub cache and llama.cpp's cache. Where the chosen
quantisation is on disk, the `MODEL` is that file's path and the engine loads
what is there instead of downloading a second copy. A path names one machine's
disk, so `--no-cache` writes the repo reference even when a copy is cached —
for a Spinloop meant to be committed and shared.

`spinloop hf` **never downloads weights**. Reading a repo is metadata only.

## From page to running agent

```sh
spinloop hf unsloth/Qwen3.6-35B-A3B-GGUF -o ./Spinloop   # write it into the project
spinloop serve                                            # start the engine for it
spinloop harness apply                                    # point the agent at it
```

Or skip the middle: `spinloop hf <ref> --apply` configures the active harness
straight from the result. To serve the model from a cloud GPU instead of this
machine, give the written Spinloop to
[`spinloop cloud deploy`](cloud.md).

## Where next

- [`spinloop hf`](../commands/hf.md) — the full reference, including gated
  repos and mirrors
- [Serve a model locally](local-serving.md) — running the engine
- [Deploy to a cloud GPU](cloud.md) — the same Spinloop, in the cloud
