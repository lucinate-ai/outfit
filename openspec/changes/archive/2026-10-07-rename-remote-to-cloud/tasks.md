## 1. Code rename

- [x] 1.1 `git mv internal/remote internal/cloud`; update package name, imports and identifiers
- [x] 1.2 Rename `cmd/spinloop/remote*.go` and their tests to `cloud*.go`; rename the command, subcommand wiring, help text and error messages; do not register a `remote` command
- [x] 1.3 Rename `internal/fleet/remote_node.go` (and test files), `KindRemote` → `KindCloud`, node type names, the `kind` value, and labels in fleet status and dashboard
- [x] 1.4 Update `internal/harness`, `internal/config`, `internal/gateway`, `internal/catalog`, `internal/daemon` and `cmd/spinloop/{route,code,serve,status*,metrics*,logs,follow,dashboard*,palette,complete}.go` references that mean this feature (leave `spinloopsrc` and URL-source "remote" alone)
- [x] 1.5 Re-key the rewrite table and root dispatch in `commands.go` / `main.go` to `cloud`
- [x] 1.6 Rename `SPINLOOP_REMOTE_*` to `SPINLOOP_CLOUD_*` in `cli_viper.go`, code, `.github` workflows and example scripts
- [x] 1.7 Rename the on-disk names to `clouds/<name>/cloud.json` and `cloud-<region>.json`

## 2. Specs and docs

- [x] 2.1 Update wording in the existing `remote-*` main specs and other specs that mention this feature (leave `remote-spinloop-sources`); update `openspec/config.yaml` context
- [x] 2.2 Rename `docs/commands/remote.md` → `cloud.md`, `docs/guides/remote.md` → `cloud.md`, `examples/fleet-remote` → `fleet-cloud`; update `mkdocs.yml`, README, FAQ, troubleshooting, env-vars, fleet file, openapi and links
- [x] 2.3 Update `AGENTS.md` layout section and `docs/maintainer/internals.md`; note the kept `remote/` directory
- [x] 2.4 Check `scripts/check-no-cloud-identifiers.sh`, CI workflows and `.dockerignore` still match

## 3. Tests

- [x] 3.1 Tests for each scenario in `cloud-command`: `cloud` runs, `remote` is an unknown command and not completed, `kind: cloud` loads and `kind: remote` is rejected, `SPINLOOP_CLOUD_*` is read and `SPINLOOP_REMOTE_*` ignored, new paths written and old paths not read
- [x] 3.2 Test that `spinloop apply <url>` is unchanged
- [x] 3.3 `go build ./... && go vet ./... && go test ./... -cover` (total ≥ 80%), `gofmt -l .` clean, `scripts/check-no-cloud-identifiers.sh`, and a `grep -ri remote` review of what is left
