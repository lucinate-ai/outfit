## Context

See proposal.md — Why. What the change builds on:

- `outfit.Selection` (`internal/outfit/outfit.go:45-68`) is the shared
  currency between CLI flags, the Outfit file, and apply/export. `CONTEXT`
  is a string (parsed lazily with `internal/contextsize.Parse`, which is
  lenient about `128k`/`1m`/plain digits); `PRESET`, `REMOTE`, and `FLEET`
  are Outfit-file-only fields with no CLI flag counterpart in `parseSelection`
  (`cmd/outfit/main.go:257-283`) — the precedent this change follows for
  `PARALLEL`.
- Three functions in `cmd/outfit/serve.go` are the single choke point that
  turns a `Selection` into an engine's argv: `llamacppServeParams` (335-359),
  `vllmServeParams` (297-314), `omlxServeParams` (326-328). They are reused
  unchanged by three callers: `outfit serve` itself (`buildServeArgv`,
  228-281), the daemon's pushed-config path (`argvFromDeployConfig`,
  `cmd/outfit/serve_daemon.go:296-323`, which builds a throwaway `Selection`
  from a `remote.DeployConfig`), and therefore every fleet-node start too.
  Fixing the math once here fixes it everywhere a `Selection` becomes a
  command.
- `preset.Flags` layers globals, a preset section, and override params (in
  that order, later wins by canonical name) — `internal/preset/preset.go:198-246`.
  This is why `CONTEXT` already overrides a preset's own `ctx-size` with no
  special-case code: the Outfit's params are just the last layer. `PARALLEL`
  gets the same win-for-free by being emitted as an ordinary param in the same
  functions.
- The `LlamaCpp` dialect already canonicalises `np` → `parallel` and knows
  `cont-batching` is boolean (`internal/preset/preset.go:253-276`); `VLLM` and
  `OMLX` are the zero-value `Dialect{}` — no aliases, keys render as written.
  So `max-num-seqs` and `max-concurrent-requests` need no dialect changes:
  writing those keys directly is already correct.
- `outfit remote deploy` derives a `remote.DeployConfig` from an Outfit via
  `deployConfig` (`cmd/outfit/remote.go:909-984`): `CONTEXT` (or a preset's
  `ctx-size`) becomes `dc.ContextSize`, an `int`, required for a cloud deploy
  (`requireContext: true`) but not for waking a fleet node. `cloudOwnedFlags`
  (`remote.go:841-847`) lists preset keys the cloud/daemon computes itself
  (`ctx-size`, `alias`, `host`, `port`, …) and strips them out of the
  passthrough `serveArgs` so a preset can't fight the derived value. The cloud
  instance type is fixed per environment (`SEED_INSTANCE_TYPE`,
  `remote/lambda/deploy/index.ts:23`) — `contextSize` is never used to size or
  choose an instance, only to build the launch command later, which is why
  this change has no provisioning impact (see proposal.md — Non-Goals).
- `DeployConfig` is a wire type mirrored in Go (`internal/remote/remote.go:205-212`)
  and TypeScript (`remote/lambda/shared/deploy-config.ts`); `daemon-boot.ts`
  (21-39) copies its fields verbatim into the JSON the on-instance daemon
  reads back via `argvFromDeployConfig`. Any field meant to reach a
  cloud-deployed or fleet-woken engine has to exist on both sides of that
  wire, exactly as `contextSize` does today.
- `outfit serve` never deploys `omlx` to the cloud (`runnerFor` rejects it —
  see the `add-omlx-provider` change's non-goals); the `DeployConfig`/cloud
  side of this change is therefore `llamacpp` and `vllm` only. `omlx` is
  local-serve-only, as it already is for everything else.

## Goals / Non-Goals

**Goals:**

- One Outfit keyword, `PARALLEL`, whose meaning is the same across engines
  (a slot/concurrency count) even though its effect on the command differs.
- `CONTEXT` keeps one fixed meaning everywhere: usable context per request.
  `outfit` — not the person writing the Outfit — carries the burden of
  knowing that llama.cpp's `--ctx-size` needs scaling and vLLM/oMLX's don't.
- Zero behaviour change for any Outfit that does not set `PARALLEL`.
- The same fix applies to `outfit serve`, the daemon/fleet push path, and
  `outfit remote deploy` — not just the first one someone tests.

**Non-Goals:**

- Deriving `--tensor-parallel-size`/`--pipeline-parallel-size` (vLLM) or
  `--cont-batching` (llama.cpp) from `PARALLEL`. Both are real settings with
  real consequences (GPU topology; a scheduling behaviour that most modern
  llama.cpp builds already default on) that deserve their own explicit
  Outfit keyword if they get one, not a side effect of this one.
- Rescaling a `PRESET`-only `ctx-size` (see proposal.md — Non-Goals). Only an
  Outfit-stated `CONTEXT` participates in the multiply; a preset's own value
  is trusted as already correct, the same trust `outfit` already extends to
  every other preset-set flag.
- A CLI flag on `add`/`remove` for `PARALLEL` (see proposal.md — Non-Goals).
- Any change to cloud instance sizing/selection.

## Decisions

**D1 — `PARALLEL` is a new `Selection` field and Outfit keyword, parsed as a
plain positive integer, not a `contextsize`-style size.** It counts slots, not
tokens — no `k`/`m` suffixes, no separators to tolerate. Empty/unset stays the
zero value and changes nothing. A non-positive or non-integer value is a parse
error naming the offending line, matching `CONTEXT`'s and `ENV`'s error style
in `outfit.Parse` (`internal/outfit/outfit.go:110-190`). Validation happens
where `CONTEXT` is validated for the same command (`contextsize.Parse` is
called lazily by each consumer, not at parse time) — `PARALLEL` follows the
same lazy-validation shape rather than diverging into eager validation at
`outfit.Parse` time.

**D2 — Translation lives in the three `*ServeParams` functions, not in a new
shared helper.** Each engine's rule is genuinely different (multiply-and-emit,
emit-only, emit-only), so a shared "parallelism" abstraction would be a
three-line function per engine wrapped in unnecessary ceremony. Concretely:

```go
// llamacppServeParams
if sel.Parallel != "" {
    n, err := strconv.Atoi(sel.Parallel)
    ... // > 0 check
    params = append(params, preset.Param{Key: "parallel", Value: sel.Parallel})
}
if sel.Context != "" {
    ctx, err := contextsize.Parse(sel.Context)
    ...
    total := ctx
    if sel.Parallel != "" { total = ctx * n }
    params = append(params, preset.Param{Key: "ctx-size", Value: strconv.Itoa(total)})
}
```

```go
// vllmServeParams — CONTEXT unchanged; PARALLEL is an independent cap
if sel.Parallel != "" {
    params = append(params, preset.Param{Key: "max-num-seqs", Value: sel.Parallel})
}
```

```go
// omlxServeParams — same shape as vllm, no context flag either way
if sel.Parallel != "" {
    params = append(params, preset.Param{Key: "max-concurrent-requests", Value: sel.Parallel})
}
```

(Values shown inline for brevity; the real code validates via a small shared
`parseParallel(s string) (int, error)` helper so the three call sites and the
deploy-config path in D4 share one error message and one positive-integer
rule, without sharing per-engine translation.)

**D3 — `PARALLEL` is Outfit-file-only.** Like `PRESET`/`REMOTE`/`FLEET`, it
gets no entry in `parseSelection` and so no `outfit add --parallel`/`-P`.
Reasoning: `CONTEXT`/`OUTPUT` exist as flags because they mean something to a
*hosted* provider selection (`opencode`'s `limit.context`/`limit.output`);
`PARALLEL` means nothing until an engine is actually launched, so it belongs
next to the other serve/deploy-only settings.

**D4 — The cloud/fleet path carries `PARALLEL` as a new `DeployConfig.Parallel
int` field, mirrored on both sides of the wire.** `deployConfig`
(`cmd/outfit/remote.go:909-984`) parses `sel.Parallel` into `dc.Parallel`
exactly as it does `sel.Context` into `dc.ContextSize`, for both
`deployConfigFor` (cloud) and `deployConfigForNode` (fleet wake) — no
`requireContext`-style gate, since `PARALLEL` is optional everywhere.
`cloudOwnedFlags`/`isNodeOwned` (`remote.go:841-866`) gain `"parallel"`,
`"max-num-seqs"`, and `"max-concurrent-requests"`, so a preset that also
hand-sets one of those is superseded by the Outfit-derived value the same way
`ctx-size` already is — otherwise a deploy could silently carry both a
computed `--parallel 2` from `PARALLEL` *and* a stale `-np 4` surviving from
the preset's passthrough `serveArgs`, doubly-defining the flag.
`argvFromDeployConfig` (`serve_daemon.go:296-323`) sets `sel.Parallel =
strconv.Itoa(dc.Parallel)` when `dc.Parallel > 0`, mirroring its existing
`dc.ContextSize` handling, and then calls the same `engine.params(sel)` as
every other path — no new translation code on this side, D2 already covers
it. `remote.DeployConfig` (Go, `internal/remote/remote.go:205-212`) and
`DeployConfig` (TypeScript, `remote/lambda/shared/deploy-config.ts`) both gain
an optional `parallel`/`Parallel` field; `daemon-boot.ts`'s
`daemonDeployConfig` (21-39) copies it through to the JSON the instance's
daemon reads, exactly like `contextSize`.

**D5 — No `Format` ambiguity.** `outfit export`
(`internal/outfit/outfit.go:219-239`) gains one more `line("PARALLEL",
sel.Parallel)` call, in the same position as the other serve-only keywords
(after `OUTPUT`, before `BASEURL`, matching the keyword table's order in
`docs/outfit-file.md`).

## Risks / Trade-offs

- **A user who already worked around the llama.cpp trap with a hand-written
  `np`/`parallel` in a `PRESET`, with no `PARALLEL` in their Outfit, sees no
  change** — their preset's `ctx-size` is untouched (D2's multiply only fires
  off an Outfit-stated `CONTEXT`), so their existing, presumably-already-correct
  math keeps working. Migrating them onto `PARALLEL` is documentation, not a
  breaking change.
- **`PARALLEL` meaning something different per engine is an inherent
  disambiguation cost**, not a flaw to design away — it is the actual
  question the proposal exists to answer. The design doc says so in one place
  (D2) so `docs/outfit-file.md` and `docs/commands/serve.md` can each just
  link back to a single explanation rather than re-deriving it.
- **Extending the wire `DeployConfig`** touches a TypeScript Lambda most of
  this change's tests can't reach directly; `remote/test/deploy-config.test.ts`
  needs a case for the new field, and `argvFromDeployConfig`'s Go-side test
  (`serve_daemon_test.go`) is what actually proves the end-to-end math, since
  the Lambda itself only stores and relays the field unchanged.
