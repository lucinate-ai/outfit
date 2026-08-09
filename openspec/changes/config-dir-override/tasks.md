## 1. Shared resolver (`internal/config`)

- [x] 1.1 Add `func Dir() (string, error)` to `internal/config`:
      `OUTFIT_CONFIG_DIR` verbatim if set; else `${XDG_CONFIG_HOME}/outfit`;
      else `~/.config/outfit`; error naming `OUTFIT_CONFIG_DIR` when the home
      cannot be resolved
- [x] 1.2 Reimplement `config.Path()` on `Dir()` (`Dir()` + `config.json`),
      propagating the error
- [x] 1.3 Tests: override verbatim, override beats XDG, XDG default, home
      default, and the loud failure when HOME/XDG/override are all absent

## 2. Route every outfit-owned path through it

- [x] 2.1 `internal/remote.ConfigHome` delegates to `config.Dir()` (confirm no
      import cycle — `config` is the stdlib-only leaf)
- [x] 2.2 Thread the error through the remote call sites that build on
      `ConfigHome` (`ConfigPath`, `remotesRoot`/`EnvDir`/`EnvConfigPath`, the
      CDK `source` dir) and `daemon.StateDir`; update their callers
- [x] 2.3 Tests: with `OUTFIT_CONFIG_DIR` set, `remote.json`, the
      `remotes/<name>/` registry, and the daemon state dir all resolve under it

## 3. Pin the instance's config dir (remote/)

- [x] 3.1 `daemon-boot.ts`: add `Environment=OUTFIT_CONFIG_DIR=/var/lib/outfit`
      to the `outfit-daemon` unit and write the deploy config under
      `/var/lib/outfit/daemon/`; ensure the dir exists before the daemon starts
- [x] 3.2 `start/index.ts`: move the engine-log path (CloudWatch agent config)
      to `/var/lib/outfit/daemon/engine.log`
- [x] 3.3 `image-stack.ts`: point the baked logrotate config at
      `/var/lib/outfit/daemon/engine.log`; bump the runner recipe versions so
      the change re-bakes
- [x] 3.4 Update `remote/` tests (start user-data asserts the pinned
      `OUTFIT_CONFIG_DIR` and the `/var/lib/outfit` deploy-config/log paths;
      stack test asserts the logrotate path)

## 4. Verification and docs

- [x] 4.1 `go test ./... -cover` >= 80%, `gofmt` and `go vet` clean;
      `remote/` `pnpm test` green
- [x] 4.2 Document `OUTFIT_CONFIG_DIR` (README and AGENTS.md — the config
      resolution note), and the `/var/lib/outfit` instance layout in
      `remote/` docs
- [x] 4.3 `openspec validate config-dir-override --strict` passes
- [ ] 4.4 Rollout (user-run): cut the outfit release, bump `outfitVersion` in
      `remote/lib/config.ts`, re-bake, `pnpm run deploy`, then re-run the
      `outfit remote start` e2e on a fresh instance
