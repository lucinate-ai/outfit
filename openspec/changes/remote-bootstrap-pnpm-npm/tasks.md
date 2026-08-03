## 1. Package-manager abstraction

- [x] 1.1 Add a `packageManager` type to `cmd/outfit/remote_bootstrap.go` with a
  `name` field, an `install` argv, and a `script(name string, args ...string) []string`
  method that shapes argv per manager (pnpm: `pnpm <script> <args>`, and
  `pnpm run deploy` for the no-arg `deploy` case; npm: `npm run <script>` with a
  `--` separator before any args).
- [x] 1.2 Define the two managers (`pnpm`, `npm`) so the pnpm argv exactly matches
  today's commands (`pnpm install`, `pnpm cdk bootstrap`, `pnpm deploy:image`,
  `pnpm bake <r>`, `pnpm run deploy`).
- [x] 1.3 Add `detectPackageManager() (packageManager, bool)` that prefers `pnpm`
  on PATH, falls back to `npm`, and reports whether either was found.

## 2. Override, preflight, and sequence

- [x] 2.1 Add a `--package-manager` flag to `cmdRemoteBootstrap` (accepting `pnpm`
  or `npm`) and resolve the selection name with precedence flag >
  `OUTFIT_REMOTE_PACKAGE_MANAGER` env var > auto-detection, rejecting any value
  that is not `pnpm` or `npm` with an error naming the accepted values.
- [x] 2.2 Replace `checkNodeAndPnpm` with a manager-aware preflight: when a
  manager is pinned, require that specific manager on PATH and fail naming it if
  absent; otherwise fail only when neither `pnpm` nor `npm` is found, naming both
  prerequisites; keep the Node 22+ version check. Thread the resolved name into
  the `bootstrapPreflightFn` seam.
- [x] 2.3 Thread the resolved `packageManager` into `runBootstrapSequence` and use
  `pm.install` / `pm.script(...)` for install, `cdk bootstrap`, `deploy:image`,
  `bake`, and `deploy`, preserving the `node_modules`-present install skip and the
  already-bootstrapped bake skip.
- [x] 2.4 Log the selected manager once to stderr before the steps run, matching
  the surrounding `fmt` diagnostic style.

## 3. Plan rendering

- [x] 3.1 Pass the resolved `packageManager` into `renderBootstrapPlan` and print
  the command list using that manager. For `--dry-run` with neither manager
  installed, render against the preferred default (`pnpm`) for display only.

## 4. Tests and verification

- [x] 4.1 Keep the confirming-run test asserting the unchanged pnpm argv; add a
  test driving `--package-manager npm` that asserts the translated argv
  (`npm install`, `npm run cdk -- bootstrap`, `npm run deploy:image`,
  `npm run bake -- llamacpp`, `npm run bake -- vllm`, `npm run deploy`).
- [x] 4.2 Add a detection test: a tempdir PATH with a fake `npm` and no `pnpm`
  selects npm; an empty PATH makes the auto preflight fail naming both managers.
- [x] 4.3 Add override tests: precedence (flag beats `OUTFIT_REMOTE_PACKAGE_MANAGER`
  beats auto-detect), an invalid value is rejected naming the accepted values, and
  a pinned manager absent from PATH fails the preflight naming that manager.
- [x] 4.4 Update the existing "missing tooling fails" test, which currently keys
  on `pnpm`, to reflect the new "neither pnpm nor npm" message.
- [x] 4.5 Run `gofmt`, `go build ./...`, and `go test ./... -cover`; confirm
  coverage stays ≥ 80%.
- [x] 4.6 Run `openspec validate remote-bootstrap-pnpm-npm --strict` and fix any
  issues.
