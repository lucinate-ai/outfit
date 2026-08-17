## Why

`outfit serve` (foreground), `outfit serve --api`, and `outfit daemon` all launch the
same local engine for an Outfit, and they share `engineFor` — the single source of the
engine's binary, subcommand, dialect, params, metrics args and key-file flag. But the
step that turns that metadata into the command line is written twice: once for the
Outfit/preset path serve takes, once for the normalised deploy-config path the daemon
takes. Because the assembly lives in two places, a change to how the command is built
can land on one path and miss the other, and nothing catches it because the two do not
share the code. This is the same drift the project has spent effort preventing elsewhere,
appearing on the serving path.

## What Changes

Internal only — the command `outfit serve` prints and runs, and the command a daemon
builds for a start request, are unchanged.

- Extract one argv-assembly helper that builds `binary + subcommand (+ a positional
  model) + the dialect-flagged params + trailing args`, from an engine, a selection and
  a param list.
- Route the daemon's deploy-config path and `serve`'s preset-less path through it. They
  differ only in where the trailing args come from (the deploy config's `serveArgs` vs.
  none), and that difference is kept at the call site.
- Leave the preset-file path on `pre.CommandIn`: merging a preset's globals and selected
  section with the Outfit's overrides, in dialect order and with short aliases collapsed,
  is a distinct job that already lives in `internal/preset` and is re-implemented nowhere.
- Everything already shared stays shared: `engineFor`, `withMetricsArgs`,
  `scrapeTargetFor`, `engineKeyArgs`, `engineEndpointFor`.

## Capabilities

### New Capabilities
<!-- None. -->

### Modified Capabilities
<!-- None. No requirement's externally visible behaviour changes: the emitted command
     is identical on every path, so there is no spec delta. The change is a pure
     refactor, marked skip_specs. -->

## Impact

- `cmd/outfit/serve.go`: the preset-less branch of `buildServeArgv` and its positional
  handling are replaced by a call to the shared assembler.
- `cmd/outfit/serve_daemon.go`: `argvFromDeployConfig` calls the shared assembler instead
  of assembling the command inline.
- New tests pin the assembled command for each servable engine (`llamacpp`, `omlx`,
  `vllm`) on both paths, so the convergence is provably a no-op on the output.
- No change to the `remote.DeployConfig` payload, the daemon API, or any emitted command.
