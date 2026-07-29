# outfit remove

Take a provider back out of your agent's config — or just some of its models.
The inverse of [`outfit add`](add.md); everything else in the config stays put.

```sh
outfit remove --provider <name> [--model <id>] [--alias <name>]
```

## Examples

```sh
# Remove a provider entirely
outfit remove -p ollama

# Drop one model, keep the provider's others
outfit remove -p openrouter -m deepseek/deepseek-v4-flash
```

## Flags

| Flag | Meaning |
| ---- | ------- |
| `-p`, `--provider` | Provider to remove from. Required. |
| `-m`, `--model` | Remove this model |
| `-a`, `--alias` | Remove the model stored under this alias |
| `-H`, `--harness` | Which harness to configure (or set `OUTFIT_HARNESS`) |
| `--providers` | Path to a custom catalogue |

## Notes

- With no model or alias, the whole provider goes.
- If the agent's default model pointed at something you removed, it is cleared
  too.
- Removing something that isn't there is not an error — `outfit` just tells you
  there was nothing to remove.

## See also

- [`outfit unapply`](unapply.md) — the same thing, driven by an `Outfit` file
- [`outfit show`](show.md) — check what's configured before and after
