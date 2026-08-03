## Context

`cmd/outfit/remote_bootstrap.go` drives the `remote/` CDK project through `pnpm`,
hardcoded in three places:

- `runBootstrapSequence` runs `pnpm install`, `pnpm cdk bootstrap`,
  `pnpm deploy:image`, `pnpm bake <runner>`, and `pnpm run deploy`.
- `checkNodeAndPnpm` (the `bootstrapPreflightFn` seam) does
  `exec.LookPath("pnpm")` and fails if it is absent.
- `renderBootstrapPlan` prints the literal `pnpm ...` command list.

The `remote/package.json` declares `packageManager: pnpm@10.33.0` and defines the
scripts bootstrap invokes: `cdk`, `deploy:image`, `bake`, `deploy`. These scripts
are plain npm-compatible scripts (`cdk` → `cdk`, `bake` → `bash scripts/bake`,
etc.), so they run under `npm` as well as `pnpm`. Only the invocation syntax
differs. There is no structured logger here — the file uses `fmt` to stderr/stdout.

## Goals / Non-Goals

**Goals:**

- Run bootstrap with either `pnpm` or `npm`, preferring `pnpm` when
  auto-detecting.
- Let the user pin the manager with a `--package-manager` flag or an
  `OUTFIT_REMOTE_PACKAGE_MANAGER` env var (flag > env var > auto-detect).
- Fail the preflight only when the required manager is not present — the pinned
  one if pinned, otherwise either; keep the Node 22+ check.
- Log the selected manager once, before the steps run.
- Keep the existing `pnpm` behaviour when `pnpm` is selected — the same commands
  in the same order (the final step normalises `pnpm run deploy` to the
  behaviourally identical `pnpm deploy`; see Decisions).

**Non-Goals:**

- Supporting yarn, bun, or other managers.
- Lockfile-based selection. The `remote/` tree ships only a `pnpm` lockfile, so
  "prefer the lockfile's manager" would just mean "always pnpm" and defeat the
  point. Auto-detection is purely PATH-based.
- Any change to the `remote/` CDK project or its declared package manager.

## Decisions

### A small package-manager abstraction

Introduce a `packageManager` describing how to shape argv for the two operations
bootstrap needs:

- **install**: `pnpm install` vs `npm install`.
- **run a package script with optional args**: pnpm forwards extra args to a
  script directly (`pnpm cdk bootstrap`, `pnpm bake llamacpp`, `pnpm run deploy`),
  whereas npm needs `npm run <script>` and a `--` separator before script args
  (`npm run cdk -- bootstrap`, `npm run bake -- llamacpp`, `npm run deploy`).

A minimal shape:

```go
type packageManager struct {
    name    string   // "pnpm" or "npm", used for the log line and preflight
    install []string  // argv for the dependency install
}

// script returns the argv to run a package.json script with optional args.
func (pm packageManager) script(name string, args ...string) []string
```

- pnpm: `install = {"pnpm", "install"}`; `script(name, args...)` is the bare
  form `{"pnpm", name, args...}` — `pnpm cdk bootstrap`, `pnpm deploy:image`,
  `pnpm bake <r>`, `pnpm deploy`.
- npm: `install = {"npm","install"}`; `script("cdk","bootstrap")` →
  `{"npm","run","cdk","--","bootstrap"}`; `script("deploy")` →
  `{"npm","run","deploy"}`.

The pnpm forms match today's commands, with one deliberate normalisation: the
final step becomes `pnpm deploy` rather than the current `pnpm run deploy`. The
existing code is inconsistent (`pnpm deploy:image` omits `run` while
`pnpm run deploy` includes it); collapsing to the bare form gives one rule and is
behaviourally identical, since `pnpm deploy` and `pnpm run deploy` invoke the same
script. Encoding the two managers as data keeps `runBootstrapSequence` reading as
a linear list of steps.

### Override, resolution, and preflight

Selection has three sources, highest precedence first: the `--package-manager`
flag, the `OUTFIT_REMOTE_PACKAGE_MANAGER` env var, then PATH auto-detection. The
flag and env var accept only `pnpm` or `npm`.

- `resolvePackageManagerName(flagVal string) (name string, pinned bool, err error)`
  — returns the first of flag / env var that is set (validating it is `pnpm` or
  `npm`, else an error naming the accepted values) with `pinned = true`; if
  neither is set, returns `("", false, nil)` so the caller auto-detects.
- `detectPackageManager() (packageManager, bool)` — the PATH-based fallback:
  pnpm manager if `pnpm` is on PATH, else npm manager if `npm` is, else
  `ok == false`.
- The preflight (`bootstrapPreflightFn`) resolves the name, then:
  - **pinned**: require that specific manager on PATH; if absent, error naming
    the pinned manager (`--package-manager npm was requested but npm is not on PATH`).
  - **auto**: if neither manager is found, error naming both prerequisites
    (`no Node package manager found on PATH — install pnpm (preferred) or npm, and Node 22+`).
  - Then keep the existing Node-22 version check.

Because the flag lives on the flag set parsed inside `cmdRemoteBootstrap`, the
resolved name is threaded to the preflight seam rather than the seam reading the
flag itself; the env var is read via `os.Getenv` consistent with the other
`OUTFIT_REMOTE_*` lookups.

`runBootstrapSequence` and `renderBootstrapPlan` take the resolved
`packageManager` so the plan and the steps agree. For `--dry-run`, which skips
the preflight, resolution is best-effort: a valid pin is honoured for display; if
nothing is pinned and neither manager is installed, the plan is rendered against
the preferred default (`pnpm`) purely for display, since dry-run makes no changes.
An invalid pin value is still rejected in dry-run, since it is a usage error.

### Logging the choice

Emit one line to stderr before the sequence runs, matching the surrounding `fmt`
diagnostics style, e.g.:

```go
fmt.Fprintf(os.Stderr, "\nUsing %s to run the CDK project.\n", pm.name)
```

This sits alongside the existing `$ <cmd>` command echoes from `runBootstrapStep`,
so the log stays consistent with what the file already prints.

### Seams and tests

The `bootstrapRunStep` and `bootstrapPreflightFn` seams stay. Tests already stub
the preflight, so the confirming-run test can pin the pnpm argv unchanged, and a
new test drives `--package-manager npm` end-to-end to assert the translated argv
(`npm install`, `npm run cdk -- bootstrap`, `npm run bake -- llamacpp`,
`npm run deploy`). Override tests cover precedence (flag beats env var beats
auto-detect) and that an invalid value is rejected. A detection test uses a
tempdir PATH containing a fake `npm` (no `pnpm`) to assert npm is chosen, an empty
PATH to assert the auto preflight error names both managers, and a pinned manager
absent from PATH to assert the error names that manager. Coverage stays ≥ 80%.

## Risks / Trade-offs

- **npm arg forwarding**: `npm run <script> -- <args>` is the portable way to
  pass args; without `--`, npm may not forward them. The `script` helper always
  inserts `--` for npm when args are present, avoiding the footgun.
- **Corepack / `packageManager` field**: `remote/package.json` pins
  `pnpm@10.33.0`. With Corepack strict-mode enabled, running `npm` against a repo
  that declares pnpm can warn. This is a warning, not a failure, and only when a
  user has opted into Corepack strictness; documenting it is enough. pnpm remains
  the default so the common path is unaffected.
- **Behaviour drift for pnpm users**: the pnpm argv matches today's commands
  except the final `pnpm run deploy` becomes `pnpm deploy` — the same script, so
  no behaviour change. The confirming-run and plan tests are updated to the
  normalised form.
