# outfit export

Print the active harness's configuration as an [`Outfit` file](../outfit-file.md),
so you can save a setup you built by hand:

```sh
outfit export > Outfit
outfit export --harness pi > Outfit   # read Pi's config instead
```

By default it exports the provider behind your default model (or the only
configured provider). If you have several, choose one with `-p`:

```sh
outfit export -p openrouter > Outfit
```

## Flags

| Flag | Meaning |
| ---- | ------- |
| `-p`, `--provider` | Which configured provider to export |
| `-H`, `--harness` | Which harness to read (or set `OUTFIT_HARNESS`) |
| `--providers` | Path to a custom catalogue |

## Notes

- Export names the configured `MODEL` directly.
- It writes canonical UPPERCASE keywords, and records `CONTEXT`/`OUTPUT` only
  when the exported models agree on a value — it never guesses.
- Secrets are never exported; keys stay in your `.env` or environment.

## See also

- [`outfit apply`](apply.md) — the round trip back
- [`outfit show`](show.md) — a readable view of the same state
