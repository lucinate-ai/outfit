# outfit unapply

Remove what an [`Outfit` file](../outfit-file.md) selects from your agent's
config — the inverse of [`outfit apply`](apply.md), just as
[`remove`](remove.md) is to [`add`](add.md).

```sh
outfit unapply                 # reads ./Outfit in the current directory
outfit unapply path/to/Outfit  # a full path to the file
outfit unapply path/to/dir     # a directory holding an Outfit
outfit unapply qwen3.6-27b     # a name registered with `outfit alias`
```

## Flags

| Flag | Meaning |
| ---- | ------- |
| `-H`, `--harness` | Which harness to configure (or set `OUTFIT_HARNESS`) |
| `--providers` | Path to a custom catalogue |

## Notes

- It honours `--harness`/`-H` and `OUTFIT_HARNESS` like everything else, so
  unapply from whichever harness you applied to.
- If the agent's default model pointed at something the Outfit selected, it is
  cleared too.

## See also

- [`outfit apply`](apply.md) — the other direction
- [`outfit remove`](remove.md) — the same removal, driven by flags
