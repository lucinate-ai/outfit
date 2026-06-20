# configure-opencode

A small Go tool that configures the current user's [opencode](https://opencode.ai)
installation to use [OpenRouter](https://openrouter.ai) with DeepSeek V4 models.

## What it does

- Reads your OpenRouter API key from a local `.env` file at run time.
- Updates the user's **global** opencode config under
  `${XDG_CONFIG_HOME:-$HOME/.config}/opencode` (updating an existing
  `opencode.json`/`opencode.jsonc` in place, or creating `opencode.json`).
- **Deep-merges** an `openrouter` provider — DeepSeek V4 Flash and DeepSeek V4
  Pro — into the config and sets the default model, without disturbing the rest
  of your configuration:
  - Existing providers and settings are preserved.
  - The config is parsed as **JSONC**, so comments and trailing commas are
    handled, and comments outside the managed `openrouter` block are kept.
  - The API key is injected directly into the config so opencode works from any
    directory (it only auto-loads `.env` from the current project).
- Writes the config with `0600` permissions, since it now contains your key.

Re-running the tool is safe and idempotent.

## Models

| Model | ID | |
| --- | --- | --- |
| DeepSeek V4 Flash | `openrouter/deepseek/deepseek-v4-flash` | default |
| DeepSeek V4 Pro | `openrouter/deepseek/deepseek-v4-pro` | |

## Prerequisites

- Go 1.24 or later
- An OpenRouter API key (starts with `sk-or-`)

## Usage

1. Create a `.env` file in this directory with your OpenRouter key:

   ```sh
   echo 'DEEPSEEK_API_KEY=sk-or-v1-...' > .env
   ```

2. Run the tool:

   ```sh
   go run .
   ```

   Or build a binary and run it:

   ```sh
   go build -o configure-opencode .
   ./configure-opencode
   ```

3. Start opencode from any directory:

   ```sh
   opencode
   ```

The `.env` file (and any built binary) are git-ignored.
