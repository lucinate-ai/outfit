# Getting started

Install to launched agent, end to end. Five minutes, tops.

## 1. Install

```sh
brew install lucinate-ai/tap/outfit
```

(Or build from source: `go build -o outfit ./cmd/outfit`.)

## 2. See what's on offer

```sh
outfit list
```

That's the catalogue: every provider, the API key it needs (if any), and which
harnesses support it.

## 3. Dress your agent

Pick a provider and model. For a hosted one, drop the key in a `.env` or
export it first:

```sh
echo 'DEEPSEEK_API_KEY=sk-or-v1-...' > .env
outfit add -p openrouter -m deepseek/deepseek-v4-flash
```

Or go local — no key needed:

```sh
outfit add -p ollama -m llama3.2
```

Your agent's existing config survives — `outfit` merges the provider in,
touching nothing else.

## 4. Launch

```sh
outfit harness
```

That launches the agent (opencode by default) wearing the model you picked.
Prefer Pi? `outfit harness --set pi` once, and every command targets it from
then on.

## 5. Make it declarative

Commit the selection to a file instead of remembering flags. Drop an `Outfit`
in your project:

```dockerfile
# Outfit
PROVIDER openrouter
MODEL    deepseek/deepseek-v4-pro
```

Then:

```sh
outfit apply        # apply it to the agent
outfit harness -O   # ...or apply and launch in one go
```

Already set up by hand? Capture it: `outfit export > Outfit`.

## 6. Serving a local model too?

If the model runs on llama.cpp, the same file launches the server:

```dockerfile
# Outfit
PROVIDER llamacpp
MODEL    unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_XL
ALIAS    qwen3.6
CONTEXT  32768
```

```sh
outfit serve    # runs llama-server for it
outfit apply    # points the agent at it
```

## 7. Name the ones you keep

```sh
outfit alias              # registers ./Outfit under its own ALIAS
outfit apply qwen3.6      # now the name works anywhere a path does
outfit harness qwen3.6
```

## Where next

- [The `Outfit` file](outfit-file.md) — full syntax
- [Command reference](README.md#commands) — a page per command
- [Examples](../examples/) — ready-to-apply Outfits, with walkthroughs
