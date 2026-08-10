# outfit apply

Apply an [`Outfit` file](../outfit-file.md) — a declarative description of one
provider selection — exactly as if you had run the equivalent
[`outfit add`](add.md). Everything else in your agent's config is preserved.

```sh
outfit apply                 # reads ./Outfit in the current directory
outfit apply path/to/Outfit  # a full path to the file
outfit apply path/to/dir     # a directory holding an Outfit
outfit apply qwen3.6-27b     # a name registered with `outfit alias`
```

Add `--harness pi` (or set `OUTFIT_HARNESS`) to apply it to Pi instead of
opencode. After applying, just run your coding agent — or do both at once with
[`outfit harness -O`](harness.md).

## Flags

| Flag | Meaning |
| ---- | ------- |
| `-o`, `--output` | Max output tokens — overrides the Outfit's `OUTPUT` |
| `-H`, `--harness` | Which harness to configure (or set `OUTFIT_HARNESS`) |
| `--providers` | Path to a custom catalogue (an Outfit never names one) |

## Notes

- With no argument, `apply` uses the alias `OUTFIT_ALIAS` names, and failing
  that a file named `Outfit` in the current directory — see
  [`outfit alias`](alias.md#naming-one-for-the-whole-shell).
- An Outfit's `PRESET` line is for [`outfit serve`](serve.md); `apply` ignores
  it.
- An Outfit with a `REMOTE` line and no `BASEURL` takes the endpoint's address
  from that [remote config](remote.md)'s `base_url`, which its deployment
  writes. A `BASEURL` in the Outfit wins over it, and a remote config that
  isn't there yet is not an error — apply just leaves the base URL alone.

## See also

- [The `Outfit` file](../outfit-file.md) — full syntax
- [`outfit unapply`](unapply.md) — the inverse
- [`outfit export`](export.md) — write an Outfit from your current setup
