## Context

Two code paths resolve local environment variables and disagree on precedence.

The remote commands (`cmd/outfit/remote.go`, `applyOutfitEnv`) load the whole
adjacent `.env` into the process environment, filling only gaps, then apply the
Outfit's `ENV` instructions on top — precedence `ENV` > process env > `.env`.
This was set by the `remote-respect-local-env` change and is codified in the
`remote-local-environment` spec.

Everything else resolves provider keys through `opencode.EnvResolver(dir)`
(`internal/opencode/opencode.go:27`), which reads the adjacent `.env` **first**
and only falls back to `os.Getenv`. So a `.env` value beats an exported one — the
opposite rule. The `provider-selection` "API key resolution" requirement
currently documents this reversed order, and a defect was filed for it from the
previous change.

`outfit harness` (`cmd/outfit/main.go:889`) launches the agent as a subprocess.
It builds the child's environment with `harnessEnv` (`main.go:993`), which starts
from `os.Environ()` and appends only the provider API keys it can resolve for
catalogue providers via `EnvResolver`. It never looks at the Outfit's `ENV`
instructions, and it does not forward arbitrary `.env` variables — only provider
keys.

## Goals / Non-Goals

**Goals:**

- One precedence rule across the whole tool: `ENV` > process environment >
  `.env`.
- `outfit harness` honours the worn Outfit's `ENV` instructions and its full
  adjacent `.env`, scoped to the launched agent's environment.
- Keep outfit's own process environment untouched on the harness path (unlike
  `remote`, which must mutate it for the AWS SDK).

**Non-Goals:**

- Extending `ENV` to `serve` or threading `ENV` into the config `apply` writes.
- Changing the `ENV` parser, the Outfit file format, or the `remote` path.
- Changing where the `.env` lives (still beside the Outfit).

## Decisions

### Fix precedence in `EnvResolver`, not at each call site

`EnvResolver` is the single chokepoint every non-remote command uses. Swapping
its two branches — consult `os.Getenv(name)` first, fall back to the `.env` —
fixes precedence everywhere at once (`add`, `apply`, `unapply`, `serve`,
`harness` key resolution, discovery, completion) with one edit and one rule.

*Alternative considered:* fix it only on the harness path. Rejected — it would
leave `add`/`apply`/`serve` on the wrong rule and keep the tool internally
inconsistent, and the filed defect is specifically about `EnvResolver`.

### Harness loads the local environment into the child, not the process

`remote` uses `os.Setenv` because the AWS SDK reads the ambient process
environment. `harness` has no such constraint: it already constructs `cmd.Env`
explicitly for the child. So the Outfit's `.env`/`ENV` go into that child slice,
never into outfit's own process. outfit stays a clean parent; the variables are
scoped exactly to the agent the user asked to launch.

The construction becomes: start from `os.Environ()`; overlay the whole adjacent
`.env` for keys not already present (gap-fill); overlay `sel.Env` (the `ENV`
instructions) unconditionally (override). Provider-key resolution via
`harnessEnv` still runs for keys the catalogue knows about and that are still
unset after that overlay, so a key named only in the catalogue still reaches the
agent. Because a later assignment to the same key in a `cmd.Env` slice wins, the
override ordering is expressed by append order.

*Alternative considered:* reuse `applyOutfitEnv` (process mutation) on the
harness path too. Rejected — mutating outfit's own environment to launch a child
is a needless side effect when we already own the child's env slice, and it
would make `outfit harness --get`/error paths leak state.

### Reuse `opencode.ParseEnvFile` for the whole-file load

The harness whole-`.env` load uses the existing `ParseEnvFile` (added by the
previous change), the same reader `applyOutfitEnv` uses, so both paths parse
`.env` identically (trim, surrounding-quote stripping, skip blanks/comments/
valueless lines, last-wins).

## Risks / Trade-offs

- [An exported provider key now beats a `.env` value during `add`/`apply`/
  `serve`, reversing today's behaviour] → This is the intended fix and matches
  `remote`; call it out in the changelog/docs as a visible behaviour change, and
  update the `provider-selection` spec and its tests so the new order is the
  documented contract.
- [`outfit harness` now forwards arbitrary `.env` variables to the agent, not
  just provider keys] → Same local trust boundary (the user launched the agent
  themselves); it makes `harness` consistent with `remote`. Keys are still
  gap-filled, so an explicit export is never clobbered by `.env`.
- [`ENV` overrides even an exported variable in the child] → Intended and
  identical to `remote`'s rule; `ENV` is a deliberate, in-Outfit override and is
  local-only, so it never leaves the device.

## Migration Plan

No data migration. Ship as a normal release; the behaviour change is the
corrected precedence. Rollback is reverting the commit — no persisted state
depends on the new order. Tests asserting the old `.env`-wins order are updated
in the same change, so a green suite reflects the new contract.
