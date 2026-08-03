## Why

The remote control commands now follow one precedence rule for local
environment variables — `ENV` (Outfit instruction) > process environment >
adjacent `.env` — but the rest of outfit does not. `opencode.EnvResolver`, which
every non-remote command uses to resolve provider keys, does the opposite: it
lets a `.env` beside the Outfit win over an exported variable. And `outfit
harness`, which wears an Outfit exactly as the remote commands do, ignores the
Outfit's `ENV` instructions entirely. The result is two contradictory rules in
one tool and a keyword that works for `remote` but silently does nothing for
`harness`.

## What Changes

- **BREAKING** Fix `opencode.EnvResolver` precedence so the process environment
  wins over the adjacent `.env`, matching the remote rule. This affects every
  command that resolves provider keys through it — `add`, `apply`, `unapply`,
  `serve`, `harness`, model discovery, and completion. An exported variable now
  always wins; the `.env` only fills a gap. (Closes the defect filed from the
  `remote-respect-local-env` change.)
- Extend `outfit harness` to respect the worn Outfit's full local environment
  when it launches the agent: the whole adjacent `.env` fills gaps in the
  process environment, and the Outfit's `ENV` instructions override both. These
  variables are injected into the launched agent's own environment only — outfit
  never mutates its own process environment on this path, so the change stays
  local to the child.
- Make the `harness` precedence identical to `remote`: `ENV` > process
  environment > `.env`.

Out of scope (noted for a possible follow-up): extending `ENV` to `serve` and
threading `ENV` into the config that `apply` writes. `serve` and `apply` keep
their current behaviour beyond the corrected `EnvResolver` precedence.

## Capabilities

### New Capabilities

<!-- none -->

### Modified Capabilities

- `provider-selection`: the "API key resolution" requirement flips precedence —
  the process environment is consulted before the adjacent `.env`, not after.
- `harness-management`: the "Keys reach the launched agent" requirement grows so
  the launched agent's environment also carries the whole adjacent `.env` (gap-
  filling) and the Outfit's `ENV` instructions (overriding both), with the
  precedence `ENV` > process environment > `.env`.

## Impact

- Code: `internal/opencode/opencode.go` (`EnvResolver`); `cmd/outfit/main.go`
  (`cmdHarness`, `harnessEnv`, and the launch env construction). Existing tests
  in `internal/opencode` and `cmd/outfit` that assert the old `.env`-wins
  precedence will need updating.
- Behaviour: an exported provider key now beats a `.env` value everywhere — a
  visible change for anyone who relied on the old order. `outfit harness` now
  passes arbitrary `.env`/`ENV` variables to the agent, not just provider keys.
- Docs: `README.md`, `docs/outfit-file.md`, and `AGENTS.md` env-resolution notes.
- No new dependencies; no change to the Outfit file format or the `ENV` parser.
