# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A single-command Go tool (`configure-opencode`) that configures the current user's global [opencode](https://opencode.ai) installation to use OpenRouter with DeepSeek V4 models. All logic lives in `main.go`.

## Commands

```sh
go run .                          # configure opencode (requires .env, see below)
go build -o configure-opencode .  # build a binary
go vet ./...                      # vet
```

There is no test suite. To exercise the tool safely without touching your real config, point `XDG_CONFIG_HOME` at a temp dir and provide a throwaway `.env`:

```sh
tmp="$(mktemp -d)"; mkdir -p "$tmp/opencode"
echo 'DEEPSEEK_API_KEY=sk-or-v1-test' > .env
XDG_CONFIG_HOME="$tmp" go run .
```

## How it works (the important part)

The tool does an **in-place merge**, not an overwrite. Understanding this is essential before changing `writeConfig`:

- It targets `${XDG_CONFIG_HOME:-$HOME/.config}/opencode`, preferring an existing `opencode.json` then `opencode.jsonc`, falling back to creating `opencode.json`.
- The existing config is parsed as **JSONC** via `github.com/tailscale/hujson` (tolerates comments and trailing commas).
- Merging is done with an **RFC 6902 JSON Patch** applied to the hujson AST. This is deliberate: it preserves comments and formatting *outside* the managed `openrouter` block. Parent objects (`/provider`) are only created with an `add` op when absent, so sibling providers are never clobbered.
- The `openrouter` block itself is deep-merged (`deepMerge`) over any existing one so user extras survive, then replaces `/provider/openrouter` wholesale (internal comments of that block are not preserved — it is script-managed).
- The OpenRouter API key is read from `.env` at run time and injected directly into the config (opencode only auto-loads `.env` from the current project, so a global config must embed the key). The file is written `0600`.

Invariants to keep when editing: the merge must stay idempotent, must never drop unrelated config or comments, and the written file must remain `0600`.

## Key location resolution

`.env` is resolved relative to `main.go`'s source directory via `runtime.Caller`, mirroring "the `.env` next to the tool". The key must start with `sk-or-`.
