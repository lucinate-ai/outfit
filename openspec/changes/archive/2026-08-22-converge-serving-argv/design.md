## Context

See `proposal.md` — Why. What shapes the approach:

- Every local engine launch reduces to one shape: the binary, then any subcommand the
  engine needs (`omlx-cli serve`, `vllm serve`), then a positional model where the engine
  takes one (vLLM), then the params rendered in the engine's dialect, then — on the
  daemon path only — the deploy config's pre-rendered `serveArgs`.
- `engineFor` already centralises the engine's binary, subcommand, dialect, params and
  positional hook. What is not centralised is the *assembly* of that shape, which is
  written inline in two places: `buildServeArgv` (serve) and `argvFromDeployConfig`
  (daemon). Those two are where a change to the shape can land in one and miss the other.
- The preset branch is different: it merges a preset's `[global]` + selected section +
  the Outfit's overrides, in dialect order with short aliases collapsed, through
  `pre.CommandIn`. That merge is a distinct job that already lives in `internal/preset`,
  and re-implementing it in a shared helper risks changing which value wins. It is not
  part of the duplication.

## Goals / Non-Goals

**Goals:**

- One place builds `binary + subcommand (+ a positional model) + dialect-flags + trailing`
  for the engine, and both the daemon path and the preset-less serve path are that
  place.
- The emitted command is byte-identical on every path before and after, proven by golden
  argv tests for each servable engine.

**Non-Goals:**

- Not moving the preset merge out of `internal/preset`. The preset branch keeps calling
  `pre.CommandIn`; it only switches its subcommand source to the shared helper.
- Not changing `serve` to run through the `Supervisor`, or touching the deploy payload or
  the daemon API.

## Decisions

### A shared `assembleEngineArgv` takes a single params layer plus trailing args

Both callers render exactly one layer of params — `engine.params(sel)` — so the
assembler takes `params []preset.Param` and `trailing []string`, not variadic layers.
The daemon passes `dc.ServeArgs` (already-rendered flags the target did not own) as the
trailing; the preset-less path passes nothing. Passing the layers for multi-layer merges
would be a mistake: the preset path needs `d.Flags(global, section, overrides)` with
in-place canonical deduplication, which `internal/preset` owns, and that must not be
reproduced.

### `subcommandFor` owns the binary/subcommand/positional piece

The subcommand is the engine's subcommand plus the positional model (when the engine has
one). It is factored so the preset branch (which feeds it into `CommandIn`) and the
assembler both draw the subcommand from one place, and it copies the engine's subcommand
before appending so a positional never aliases the engine's own slice.

### `argvFromDeployConfig` still reconstructs the selection first

It reduces its `DeployConfig` to a selection (runner, model:quant, alias, context) and
derives `engine.params` from it, as it does today; only the final assembly is replaced by
the call to `assembleEngineArgv`. The reconstruction stays because the daemon is driven
by a normalised config, not a preset.

## Risks / Trade-offs

- [A shared helper could subtly reorder tokens relative to the inline code it replaces] →
  Golden argv tests for every servable engine on both paths pin the exact command; any
  reorder fails them. The two paths are asserted equal to their pre-change output, so the
  convergence is provably a no-op.
- [The preset and preset-less paths still assemble via different helpers
  (`CommandIn` vs. `assembleEngineArgv`)] → Intentional: the preset merge is a different
  operation. The shared helper owns the *mechanical* assembly both the plain and the
  daemon path need; the preset merge legitimately stays with the preset.
- [`subcommandFor` is used by both the preset and the plain branches] → It is pure and
  covered by both branches' tests, so a regression is caught on either side.
