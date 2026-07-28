# outfit completion

Tab completion for bash, zsh, and PowerShell.

```sh
source <(outfit completion bash)   # add to ~/.bashrc
source <(outfit completion zsh)    # or ~/.zshrc (needs compinit)
outfit completion powershell | Out-String | Invoke-Expression   # or $PROFILE
```

Homebrew installs the bash and zsh completions for you.

## What completes

TAB completes commands, flags, and — context-aware — the values that follow
them:

- provider names after `-p` (honouring a `--providers` override on the line)
- family and model names, scoped to the provider you've already typed
- harness names after `-H`, `--harness`, or `--set`
- your [registered aliases](alias.md) wherever an Outfit path goes —
  `outfit unalias <TAB>` offers exactly the names you have
- the supported shells after `completion`

## See also

- [`outfit alias`](alias.md) — the names completion offers
