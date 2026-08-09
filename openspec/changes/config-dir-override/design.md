## Context

See proposal.md — Why. The concrete state:

- Two duplicated resolvers compute outfit's config home identically today:
  `internal/config.Path()` (→ `config.json`) and
  `internal/remote.ConfigHome()` (→ `remote.json`, `remotes/<name>/`, the CDK
  source dir, and `daemon.StateDir()`). Both do
  `os.Getenv("XDG_CONFIG_HOME")` → else `os.UserHomeDir()` + `.config`.
- `os.UserHomeDir()` returns `$HOME`; the systemd `outfit-daemon` unit sets no
  `HOME`/`User=`/`WorkingDirectory=`, so on the instance it resolved empty and
  `filepath.Join("", ".config")` gave a relative `.config/outfit` — read from
  the daemon's CWD (`/`), i.e. `/.config/outfit`, not the `/root/.config/outfit`
  the boot wrote.
- `internal/config` is the documented stdlib-only **leaf** package; anything
  may import it, it imports no internal package. So it is the right home for
  the shared resolver.
- The instance's engine-log path and its baked logrotate target both currently
  hard-code `/root/.config/outfit/daemon/engine.log`
  (`remote/lambda/start/index.ts`, `remote/lib/image-stack.ts`).

## Goals / Non-Goals

**Goals:**

- One resolver for outfit's config dir, overridable by `OUTFIT_CONFIG_DIR`,
  used everywhere outfit reads/writes its own state.
- A deterministic daemon config dir on the instance, independent of `$HOME`.
- No behaviour change for existing local installs (default path preserved).

**Non-Goals:**

- The harnesses' own config files (Pi `~/.pi`, opencode config) — out of scope.
- A general per-file override or multiple config roots — one dir, one override.
- Migration of any existing on-disk config — the default path is unchanged, so
  there is nothing to move.

## Decisions

**D1 — `config.Dir()` is the single resolver; `Path()` and
`remote.ConfigHome()` build on it.** New `func Dir() (string, error)` in
`internal/config`: return `OUTFIT_CONFIG_DIR` verbatim if set; else
`${XDG_CONFIG_HOME}/outfit` if `XDG_CONFIG_HOME` set; else `~/.config/outfit`
via `os.UserHomeDir()`; if that errors, return an error naming
`OUTFIT_CONFIG_DIR`. `Path()` becomes `Dir()` + `config.json`.
`remote.ConfigHome()` calls `config.Dir()`. `daemon.StateDir()` already goes
through `remote.ConfigHome()`, so it inherits the override with no change.

**D2 — Resolver returns an error, not a silent empty.** Today both resolvers
swallow the `os.UserHomeDir()` error (`home, _ :=`). That is exactly what let
the daemon read a bogus path. The shared resolver surfaces the error. Callers
that currently return a bare path get a small signature change; where a caller
truly cannot thread an error (e.g. a place that must return a string), it
falls back to a clearly-invalid sentinel only after the loud error has been
logged — but the preferred shape is to propagate. Audit each of the ~5 call
sites (`config.Path`, `remote.ConfigPath`, `remotesRoot`/`EnvDir`,
`source` cdk dir, `daemon.StateDir`) and thread the error where it is cheap;
this is the bulk of the Go work.

**D3 — The instance pins `OUTFIT_CONFIG_DIR=/var/lib/outfit`.** The
`outfit-daemon` unit (`daemon-boot.ts`) gains
`Environment=OUTFIT_CONFIG_DIR=/var/lib/outfit`; the boot writes the deploy
config to `/var/lib/outfit/daemon/deploy-config.json` (replacing the
`/root/.config/...` path); the CloudWatch-agent engine-log path
(`start/index.ts`) and the baked logrotate target (`image-stack.ts`) move to
`/var/lib/outfit/daemon/engine.log`. `/var/lib/outfit` is created in the boot
(or the bake) before the daemon starts.

**D4 — Rollout is release → bump → re-bake → redeploy.** The override is a
binary feature and the binary is baked, so: cut a new outfit release, bump
`outfitVersion` in `remote/lib/config.ts`, re-bake the AMIs (the logrotate
path change also needs the bake), then `pnpm run deploy` for the Lambda unit
and boot changes. The engine-log path moving is why the bake — not just a
Lambda redeploy — is required.

## Risks / Trade-offs

- [Threading an error through resolvers touches several call sites] →
  mechanical; `internal/config` is a leaf so there is no cycle risk, and the
  compiler finds every caller.
- [A path override pointing somewhere unwritable] → surfaces as the normal
  file-write error at first use, naming the path; no special handling needed.
- [Cloud fix needs a re-bake, not just a redeploy] → unavoidable because the
  engine-log path (baked logrotate) changes; batched with the `outfitVersion`
  bump so it is one bake. dev-1 can hold on the manual `HOME` workaround until
  the re-baked AMI is out.
- [Someone sets `OUTFIT_CONFIG_DIR` to a relative path] → used verbatim as
  documented; not our job to reject, and it resolves against CWD like any
  relative path. The spec says "verbatim".

## Open Questions

- Whether `/var/lib/outfit` is created by the boot script or baked into the
  AMI — both work; lean to the boot script (keeps the AMI change to the
  logrotate path only). Decide in implementation; no spec impact.
