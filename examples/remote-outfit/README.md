# Fetching an Outfit from a URL

Every other example in this directory is meant to be run in place — clone the
repo, `cd` into it, `outfit apply`. This one is different: the
[`Outfit`](Outfit) and [`preset.ini`](preset.ini) here are meant to be
published somewhere reachable over HTTP and consumed with a URL, not cloned.
It's the same Qwen3.6-27B-on-llama.cpp setup as
[`examples/llamacpp/qwen3.6-27b`](../llamacpp/qwen3.6-27b/README.md) — see
that example for the model itself, the `llama-server` prerequisites, and what
the preset's flags do. This one is about the *distribution* of the file, not
the model.

## 1. Publish the pair

`outfit` fetches whatever's at the URL, so any static host works: a
[gist](https://gist.github.com)'s raw URL, a GitHub raw URL
(`https://raw.githubusercontent.com/<org>/<repo>/<ref>/path/Outfit`), an
internal artifact bucket, or — to try this locally — a plain file server from
this directory:

```sh
cd examples/remote-outfit
python3 -m http.server 8000
```

That serves `Outfit` and `preset.ini` at `http://localhost:8000/Outfit` and
`http://localhost:8000/preset.ini`. Wherever it's actually published, the two
files travel together — `PRESET ./preset.ini` in the `Outfit` resolves
relative to whatever URL fetched it, the same way it resolves against a local
directory.

## 2. Apply it straight from the URL

```sh
outfit apply http://localhost:8000/Outfit
```

This fetches the Outfit and applies it exactly as if it were a local file — no
clone, no local copy. A URL ending in `/` is treated like a directory, so
`outfit apply http://localhost:8000/` would fetch the same `Outfit` too.

## 3. Give it a short name

Remembering a URL is worse than remembering a path. [`outfit
alias`](../../docs/commands/alias.md) registers one under a name, exactly as
it does for a local file:

```sh
outfit alias -n qwen3.6-27b-team http://localhost:8000/Outfit
outfit apply qwen3.6-27b-team
outfit serve qwen3.6-27b-team
```

Registering fetches the Outfit once, to validate it — after that, the name
works from any directory, on this machine.

## 4. Only `serve` fetches the preset

`outfit apply` never reads `PRESET` — a local one or a URL, it's `outfit
serve`'s business alone. Only running `outfit serve` (or `outfit remote
deploy`) fetches `http://localhost:8000/preset.ini`:

```sh
outfit serve qwen3.6-27b-team --dry-run   # fetches preset.ini, prints the command
outfit apply qwen3.6-27b-team             # never touches preset.ini
```

Nothing here is fetched until the command that actually needs it runs —
applying the Outfit doesn't fetch the preset, and registering the alias
doesn't either. See [Fetching an Outfit from a
URL](../../docs/outfit-file.md#fetching-an-outfit-from-a-url) for the full
picture, including the same rules for `REMOTE`.

## See also

- [`examples/llamacpp/qwen3.6-27b`](../llamacpp/qwen3.6-27b/README.md) — the
  same setup, run in place from a local clone
- [`outfit alias`](../../docs/commands/alias.md) — registering a name for a
  path or a URL
