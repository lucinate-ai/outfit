## Why

`outfit harness <remote-outfit>` warned that no API key was set, and then
launched an agent that had one all along:

```
Warning: no API key was set, but http://…:8000/v1 is not a local address, so
requests will probably be rejected. Set OPENAI_API_KEY and apply again.
```

The apply ran first and resolved the key from the process environment and the
adjacent `.env`; the fetch from the control plane — the only place a remote
endpoint's key exists — ran afterwards, to build the launched agent's
environment. So the warning was about a variable that was about to be set, and
following its advice (`export OPENAI_API_KEY=…`) was unnecessary.

Two things were hidden behind that. The fetch's error was discarded entirely, so
a stopped endpoint, absent AWS credentials or a remote config predating
`env_url` all produced the same output as success — an agent that starts and is
refused by the endpoint, with the misleading warning as the only clue. And the
`harness-remote-env` spec already said this case SHALL fail before launching;
the code has never done that.

Separately, `eval "$(outfit remote env <alias>)"` failed in the shell. The
command's stdout is meant for `eval`, but `readOutfit` printed
`Using alias "dev-1" (/path/to/Outfit)` there, and a shell asked to evaluate a
prose line reports a syntax error.

## What Changes

- Fetch the remote endpoint's environment *before* the apply, and resolve the
  apply's API key against it, so the config is written knowing the key the
  launch will supply and the missing-key warning is left for a key that really
  is missing.
- Report a failed fetch instead of discarding it. It is fatal — before the
  harness config is written — when no API key is otherwise available, since the
  endpoint refuses every request without one; it is a warning when the key is
  already exported, in the adjacent `.env`, or set by an `ENV` instruction.
- Announce the fetch on stderr, as the spec already required.
- Bound the fetch with a timeout (it had none), so an unresponsive control plane
  delays a launch rather than blocking it.
- Move `readOutfit`'s alias line to stderr, so `outfit remote env` prints
  nothing but export lines on stdout.
- Give the lucinate harness the fetched key too: the launch resolved
  `LUCINATE_OPENAI_API_KEY` from a resolver that did not know about the remote,
  so a remote Outfit under `-H lucinate` got a key lucinate does not read.

Non-goal: how the key reaches the agent is unchanged — it is injected into the
launched process's environment, never written to any harness config.

## Impact

- Affected specs: `harness-remote-env`, `remote-env`, `alias-registry`
- Affected code: `cmd/outfit/main.go`, `cmd/outfit/remote.go`
- Behaviour change: a launch that cannot obtain a key now fails instead of
  starting an agent that cannot authenticate. Anyone who worked around the old
  warning by exporting `OPENAI_API_KEY` is unaffected — an exported key still
  wins over the fetched one, and now also downgrades a fetch failure to a
  warning.
- `outfit remote env` keeps its stdout contract; the alias line it used to break
  `eval` with now goes to stderr, where every other command's alias line also
  goes.
