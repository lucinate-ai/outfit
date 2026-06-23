# The `Outfit` file

An **Outfit** is a small, declarative file that captures one opencode provider
selection — which provider, and which model family and/or model — so you can
apply it with a single command instead of remembering flags. Think of it like a
`Dockerfile`, but for pointing opencode at a model.

```dockerfile
# Outfit — point opencode at one provider
PROVIDER openrouter
FAMILY   deepseek-v4
MODEL    deepseek/deepseek-v4-pro   # optional; becomes the default model
CONTEXT  128k                       # optional; context window
BASEURL  https://gateway/v1         # optional; API base URL override
```

Applying it is the same as running the equivalent `outfit add`, so everything
you already have in your opencode config is preserved.

## Applying an Outfit

```sh
outfit apply            # reads ./Outfit in the current directory
outfit apply path/to/Outfit
```

Run `outfit apply` with no arguments and it looks for a file named `Outfit`
in the current directory. Point it at any path to apply a different file.

After applying, just run `opencode`.

## Syntax

One instruction per line: a keyword followed by a single value.

| Keyword    | Required?                  | Maps to        | Example                        |
| ---------- | -------------------------- | -------------- | ------------------------------ |
| `PROVIDER` | yes                        | `--provider`   | `PROVIDER openrouter`          |
| `FAMILY`   | one of `FAMILY` / `MODEL`  | `--model-family` | `FAMILY deepseek-v4`         |
| `MODEL`    | one of `FAMILY` / `MODEL`  | `--model`      | `MODEL deepseek/deepseek-v4-pro` |
| `CONTEXT`  | no                         | `--context`    | `CONTEXT 128k`                 |
| `BASEURL`  | no                         | `--base-url`   | `BASEURL https://gateway/v1`   |

Rules:

- An Outfit describes **exactly one provider**. `PROVIDER` is required and may
  appear only once; so may every other keyword.
- You need **at least one** of `FAMILY` or `MODEL`. Give a `FAMILY` to add all
  of that family's models; give a `MODEL` to add or pin a specific one; give
  both to add the family but make `MODEL` the default.
- `CONTEXT` sets the context window for the model(s). It accepts human suffixes
  (`128k`, `1m`) or an absolute count (`200000`).
- `BASEURL` overrides the provider's API base URL — handy for a gateway or a
  llama.cpp server on a non-default port. `URL`, `BASE-URL`, and `BASE_URL` are
  accepted as aliases.
- Keywords are **case-insensitive** — `provider`, `Provider`, and `PROVIDER` are
  all accepted — but **UPPERCASE is canonical** and is what `outfit export`
  writes.
- **Comments** start with `#`, either on their own line or at the end of a line.
  Blank lines are ignored.

To see the available providers, families, and models, run `outfit list`.

## Examples

A local model served by llama.cpp (no API key needed):

```dockerfile
PROVIDER llamacpp
MODEL    qwen3.6-35b-a3b
```

A whole model family from OpenRouter (its key comes from your `.env` or
environment, exactly as with `outfit add`):

```dockerfile
PROVIDER openrouter
FAMILY   deepseek-v4
```

Any OpenAI-compatible endpoint, with a single pinned model:

```dockerfile
PROVIDER openai-compatible
MODEL    my-model
```

Ready-to-use Outfits live under [`examples/`](../examples/).

## Capturing your current setup

`outfit export` prints your current opencode configuration as an Outfit, so
you can save a setup you built by hand:

```sh
outfit export > Outfit
```

By default it exports the provider behind your default model (or the only
configured provider). If you have several, choose one with `-p`:

```sh
outfit export -p openrouter > Outfit
```

Where the configured models match a known family, export names the `FAMILY`;
otherwise it writes the specific `MODEL`.
