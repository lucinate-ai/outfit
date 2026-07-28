# outfit serve

Run `llama-server` for the model an [`Outfit` file](../outfit-file.md) names —
so the same file that points your agent at a local model can also start the
server behind it.

```sh
outfit serve              # reads ./Outfit and runs llama-server
outfit serve path/to/Outfit
outfit serve qwen3.6-27b  # a name registered with `outfit alias`
outfit serve --dry-run    # print the command without launching the server
```

It prints the command before running it, and never touches your agent's
config — pair it with [`outfit apply`](apply.md) to point the agent at the
server.

## Simple case — straight from the Outfit

With no `PRESET`, `serve` builds the command from the Outfit itself:

```dockerfile
PROVIDER llamacpp
MODEL    unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_XL   # an HF repo, or a .gguf path
ALIAS    qwen3.6                                    # llama-server --alias
CONTEXT  32768                                      # llama-server --ctx-size
BASEURL  http://127.0.0.1:8080/v1                   # llama-server --host/--port
```

`MODEL` becomes `-hf` (a Hugging Face repo) or `-m` (anything that looks like a
path or ends in `.gguf`); `ALIAS`, `CONTEXT`, and `BASEURL` fill in the rest.

## Full control — a llama.cpp preset

For flags an Outfit doesn't model — `-ngl`, `--jinja`, KV-cache types, draft
models — point at a llama.cpp
[preset `.ini`](https://github.com/ggml-org/llama.cpp/blob/master/docs/preset.md):
a set of `llama-server` flags grouped under named `[model]` sections, with a
`[*]` section for shared defaults. Presets are built for the server's router
(multi-model) mode, so there's no clean way to launch a single model from one —
which is exactly what `serve` does.

```dockerfile
PROVIDER llamacpp
ALIAS    qwen3.6-35b-a3b   # selects the preset's [qwen3.6-35b-a3b] section
PRESET   ./preset.ini
```

`serve` flattens the `[*]` defaults and the matching section into explicit
`llama-server` flags, the section winning over the defaults. Anything the
**Outfit** also states wins over both, so you can keep a shared preset and
tweak one field per project: `CONTEXT` overrides the section's `ctx-size`,
`BASEURL` its `host`/`port`, `ALIAS` its `alias`, and `MODEL` its `hf`/`model`.
Keys map straight to flags — `ctx-size = 262144` becomes `--ctx-size 262144`,
`hf` becomes `--hf-repo`, and boolean toggles like `mmap = 1` become a bare
`--mmap`. Which section runs:

- `ALIAS` names the section.
- With no `ALIAS`, a preset holding exactly one section serves that one.
- Several sections and no `ALIAS` is an error — name one.

A relative `PRESET` path resolves against the Outfit's own directory, so the
pair can travel together.

## Flags

| Flag | Meaning |
| ---- | ------- |
| `-n`, `--dry-run` | Print the `llama-server` command without running it |

## Notes

- `serve` needs `llama-server` on your `PATH` (e.g.
  `brew install llama.cpp`).
- Without a `PRESET`, the Outfit must name a `MODEL`.

## See also

- Worked examples with real models: [`examples/`](../../examples/)
- [The `Outfit` file](../outfit-file.md) — full syntax
