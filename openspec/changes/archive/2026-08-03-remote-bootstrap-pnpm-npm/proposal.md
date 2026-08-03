## Why

`outfit remote bootstrap` hardcodes `pnpm` for every Node step — install, `cdk
bootstrap`, `deploy:image`, `bake`, and `deploy` — and its preflight fails when
`pnpm` is absent. Users who already have `npm` (which ships with Node) but not
`pnpm` cannot run bootstrap without first installing another tool, even though
the `remote/` project's scripts run just as well under npm.

## What Changes

- Bootstrap detects an available Node package manager instead of assuming one:
  it prefers `pnpm` and falls back to `npm` (by PATH lookup), so a stock Node
  install works out of the box.
- A `--package-manager` flag and an `OUTFIT_REMOTE_PACKAGE_MANAGER` env var let
  the user pin the manager explicitly; the flag wins over the env var, which
  wins over auto-detection. An explicit choice that is not on the path fails the
  preflight, naming the manager the user asked for.
- The preflight fails only when **neither** `pnpm` nor `npm` is on the path
  (Node 22+ is still required), rather than failing specifically on missing
  `pnpm`.
- Bootstrap logs which package manager it selected once, before the steps run,
  so the run is self-explanatory.
- Every step (`install`, `cdk bootstrap`, `deploy:image`, `bake`, `deploy`) and
  the printed plan use the selected manager, with npm's argument conventions
  (`npm install`, `npm run <script> [-- <args>]`, `npm exec`) handled correctly.
- No change to behaviour when `pnpm` is present — pnpm stays the default.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `endpoint-provisioning`: the preflight requirement now accepts `pnpm` **or**
  `npm` (preferring `pnpm`) rather than mandating `pnpm`; the version-matched
  sources and idempotency requirements refer to the install step generically
  rather than to `pnpm install`; and a new requirement records that bootstrap
  selects, allows overriding (flag/env var), and logs the package manager it
  uses.

## Impact

- Code: `cmd/outfit/remote_bootstrap.go` — the `--package-manager` flag, the
  `OUTFIT_REMOTE_PACKAGE_MANAGER` env var, package-manager resolution, the
  preflight (`checkNodeAndPnpm` → a manager-aware check), `runBootstrapSequence`,
  and `renderBootstrapPlan`. Tests in `cmd/outfit/remote_bootstrap_test.go`.
- No new dependencies (Go stdlib `os/exec` only). No AWS or CDK changes; the
  `remote/` project keeps `pnpm` as its declared package manager.
- User-facing: bootstrap now works with a stock Node/npm install and prints the
  manager it chose.
