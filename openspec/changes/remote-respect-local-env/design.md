## Context

The `remote` control commands sign every request with the caller's AWS
credentials (SigV4, service `lambda`) resolved by `remote.LoadAWSConfig`, and
they read connection details through env overrides in `finishConfig`
(`OUTFIT_REMOTE_*`, `AWS_REGION`) and `resolveRegion`. All of these read the
**real process environment**: the AWS SDK's default credential chain reads
`os.Environ()` directly, and the override lookups call `os.Getenv`.

The Outfit already has an adjacent `.env`, but only the provider-key path uses it
(`opencode.EnvResolver`, a per-key lookup closure, `.env`-first). Because the AWS
SDK reads the OS environment itself, a lookup closure cannot influence it — the
values must be present in the process environment. So the remote commands need to
*populate* the process environment from the Outfit's local sources before doing
any AWS or control work.

## Goals / Non-Goals

**Goals:**
- Make `deploy`/`start`/`stop`/`status` respect the `.env` beside the resolved
  Outfit (AWS creds/region, `OUTFIT_REMOTE_*`), with the process environment
  winning over `.env`.
- Add a repeatable `ENV KEY=VALUE` Outfit keyword that overrides both `.env` and
  the process environment, applied locally only.
- Keep the change contained to the remote path and the Outfit parser.

**Non-Goals:**
- Changing the provider-key resolution path (`apply`/`serve`/`harness` via
  `EnvResolver`). Its `.env`-first precedence stays; a GitHub defect tracks
  reconciling it with the new process-environment-first rule.
- Forwarding `ENV`/`.env` to the deployed instance, or adding them to
  `remote.DeployConfig`.
- Supporting `.env`/`ENV` values that contain whitespace, `export ` prefixes, or
  variable interpolation — the format stays as simple as today's parser.

## Decisions

**Populate the process environment (`os.Setenv`), not a lookup closure.**
The AWS SDK reads `os.Environ()` directly, so only real environment variables
reach it. A single helper `applyOutfitEnv(sel, dir)` runs before config/AWS work:
1. Parse the `.env` beside the Outfit; for each var, `os.Setenv` **only when
   `os.Getenv(key) == ""`** (process environment wins — the `.env` fills gaps).
2. For each `ENV` entry, `os.Setenv` unconditionally (`ENV` overrides both).
Applying `.env` first (gap-fill) then `ENV` (override) yields the required order
`ENV > process env > .env`. Alternative rejected: passing a custom `getenv` into
`finishConfig` — it would fix the `OUTFIT_REMOTE_*` overrides but not the AWS
credential chain, so it only solves half the problem.

**Call sites.** `resolveRemoteConfig` (used by start/stop/status) calls the
helper in both branches that read an Outfit, right after `readOutfit` and before
`remote.LoadConfigFile`. `cmdRemoteDeploy` calls it right after its own
`readOutfit` and before `remote.LoadAWSConfig`/`resolveRegion`. The no-Outfit
fallback (`remote.LoadDefault`) has nothing adjacent, so it is untouched.

**`ENV` parsing.** Add `Env []EnvVar` (`struct{ Key, Value string }`) to
`outfit.Selection` and `kwEnv` to the keyword set. In `Parse`, special-case `ENV`
*before* the single-set `seen` check so it may repeat; its value is still one
whitespace-delimited field, so the existing "exactly one value" guard holds. Split
the token on the first `=`, requiring a non-empty key. `Format` emits `ENV` lines
after `REMOTE` so an in-memory round-trip is lossless (`outfit export` never
produces `ENV`, since it reconstructs from harness config).

**Whole-file `.env` parser.** Add `opencode.ParseEnvFile(path) (map[string]string,
error)` beside the existing `readEnvFileVar`, sharing its trim/quote-strip
convention: skip blank lines, `#` comments, lines without `=`, and empty values;
last wins on a duplicate key; a missing file yields an empty map and no error. The
existing `readEnvFileVar`/`EnvResolver` are left as-is.

**Local-only by construction.** `deployConfigFor` builds `remote.DeployConfig`
solely from Outfit fields and the preset; `ENV`/`.env` are never threaded into it,
so nothing reaches the instance. A guard test asserts this.

## Risks / Trade-offs

- **Two precedence rules coexist** (`provider-selection`: `.env`-first;
  `remote-local-environment`: process-env-first) → intentional for now; a GitHub
  defect tracks reconciliation so the inconsistency is visible, not silent.
- **`os.Setenv` mutates global process state** → acceptable: the remote commands
  are short-lived single-shot invocations that exit after the operation.
- **A local `.env` cannot shadow deliberately-exported credentials** (e.g. CI
  role env) → this is the desired safety property of process-env-first; `ENV` is
  the explicit escape hatch when the Outfit author wants to override.
- **No whitespace/quoted values in `ENV`** → matches the rest of the Outfit
  format; documented, and `.env` still supports surrounding double quotes.

## Migration Plan

Additive and backward-compatible: existing Outfits (no `ENV`) and existing remote
flows are unchanged. No config migration. Rollback is reverting the change; no
persisted state is written by this feature.
