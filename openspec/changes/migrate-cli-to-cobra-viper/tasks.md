## 1. Dependencies and command-tree skeleton

- [x] 1.1 Add `github.com/spf13/cobra` and `github.com/spf13/viper` to
  `go.mod` and run `go mod tidy` (covers the transitive pflag/afero/cast/
  mapstructure/fsnotify)
- [x] 1.2 Create the Cobra root command and the subcommand tree in
  `cmd/outfit` mirroring `run()`'s switch exactly: `add remove list show
  apply unapply alias unalias serve daemon export init-providers harness
  completion version`, with `fleet` (children status, metrics, logs, route,
  start, stop) and `remote` (children bootstrap, start, pause, stop, status,
  metrics, logs, deploy, env, ls) as parents
- [x] 1.3 Give every command an empty `RunE` that delegates to the existing
  `cmdX(rest)` function with the same args the old dispatch passed, and point
  `run()` at `rootCmd.SetArgs(args); rootCmd.Execute()`; delete the old
  string switch (Cobra's `__complete` now exists but is inert — nothing has
  completion wiring yet)
- [x] 1.4 Verify the skeleton: `go build ./...`, `go vet ./...`, gofmt clean,
  and `go test ./... -cover` green with total coverage >= 80%

## 2. Flag migration: selection and catalogue commands

- [x] 2.1 Move `add` and `remove` flags off `parseSelection`'s `FlagSet` and
  onto `cmd.Flags()` (long + short: `--provider/-p`, `--model/-m`,
  `--alias/-a`, `--context/-c`, `--output/-o`, `--providers`,
  `--base-url/-u`, `--harness/-H`), keep the `--provider` required check,
  and delete `parseSelection`
- [x] 2.2 Migrate `list` (`--providers`, `--models`), `show`
  (`--harness/-H`), `export` (`--provider/-p`, `--providers`,
  `--harness/-H`), and `init-providers` (`--force/-F` + path positional) to
  `cmd.Flags()`
- [x] 2.3 Migrate the outfit-path commands: `apply` (`--providers`,
  `--output/-o`, `--harness/-H`, path positional), `unapply` (`--providers`,
  `--harness/-H`, path), `alias` (`--name/-n`, `--force/-F`, `--list/-l`,
  path), `unalias` (name positional)
- [x] 2.4 Migrate `serve` (`--dry-run/-n`, `--api/-a`, `--api-addr`,
  `--api-token-file`, `--api-token`, `--log-level`, path) and `daemon`
  (`--api-addr`, `--loopback/-l`, `--api-token-file`, `--api-token`,
  `--log-level`)
- [x] 2.5 Make `fleet` a parent whose child flag sets live on the children
  (status/metrics: `--fleet`, `--format`, `--watch/-w`, any node positional;
  logs: `--follow/-f`, `--limit`, `--source` if registered, `--fleet`;
  route: `--fleet`, `--node`, `--prefer`; start/stop: `--fleet` + node),
  matching the flags each `fleetFlags` call registers today; do the same for
  `remote`'s children (bootstrap: `--dry-run/-n`, `--yes/-y`, `--wait`,
  `--force-bake`, `--runners`, `--hf-token`, `--dir`, `--region`,
  `--package-manager`; start: `--timeout/-t`, `--env/-e`, `--keep`;
  metrics: `--cost`, `--format`, `--watch/-w`; logs: `--source`, `--since`,
  `--limit`, `--instance`, `--follow/-f`, `--format`; deploy: `--dry-run/-n`,
  `--overwrite`, `--reseed`, `--allowed-cidr`, `--region`; env, ls, pause,
  stop as registered), keeping each subcommand's `--fleet`-style parent
  spelling working
- [x] 2.6 Add focused pflag parse regression tests for the tricky forms
  (shorthand clusters, `--flag=value` attached, unknown-flag errors, bool
  shorthands) for at least: `add`, `apply`, `serve`, `fleet metrics`, and
  `remote start`
- [x] 2.7 Verify: `go vet ./...`, gofmt, `go test ./... -cover` green,
  coverage >= 80%

## 3. `harness` command parsing (DisableFlagParsing)

- [x] 3.1 Set `DisableFlagParsing: true` on the `harness` command and port its
  manual parse from `flag.FlagSet` to `pflag.FlagSet` with identical
  registrations: `--set`, `--get`, `--harness/-H`, `--outfit/-O` (string
  flag with `NoOptDefVal = "./Outfit"`), `--providers`, `--fleet`, `--node`,
  `--prefer`, `--no-wake`, `--wake-timeout`
- [x] 3.2 Preserve the existing rules exactly: a leading positional naming an
  Outfit is consumed (not forwarded) unless `--` terminated flag parsing or a
  leading `--` opted out; a `--` directly after a consumed Outfit is dropped;
  all remaining args forward byte-for-byte
- [x] 3.3 Add parse tests for the harness corner cases: bare `-O` applies
  `./Outfit`, `-O=<path>` applies the attached path, `-O` with a following
  positional applies the default and forwards the positional, and `--`
  forwarding of harness args that look like flags
- [x] 3.4 Verify: run the existing harness launch/forwarding tests, full
  suite green, coverage >= 80%

## 4. Viper binding (CLI-layer OUTFIT_* reads)

- [x] 4.1 Construct the single Viper instance in the CLI layer
  (`viper.New()`, `SetEnvPrefix("OUTFIT")`,
  `SetEnvKeyReplacer(strings.NewReplacer("-", "_"))`, `AutomaticEnv()`)
  behind a small accessor, and add the `resolve(cmd)` helper that calls
  `v.BindPFlags(cmd.Flags())` at the top of each command's `RunE`
- [x] 4.2 Migrate `OUTFIT_ALIAS` reads (the `readOutfit` empty-path branch
  and the `defaultOutfitNamed` gate in `resolveRemoteConfig`) to the Viper
  instance, keeping argument > `OUTFIT_ALIAS` > `./Outfit` and the gate
  counting the variable as well as a file
- [x] 4.3 Migrate the `OUTFIT_REMOTE_*` config lookups: pass a Viper-backed
  lookup closure (with `BindEnv` of the flat remote.json keys to the
  prefixed names and `LoadConfig` on the per-environment file) into
  `internal/remote`'s existing `func(string) string` injection points,
  leaving the internal package signatures unchanged
- [x] 4.4 Leave the D3 "Unchanged" rows untouched and confirm by review:
  `catalog.ResolveCatalogPath` (OUTFIT_PROVIDERS), the injected `resolve`
  closures (OUTFIT_BASE_URL), `daemon.ParseLevel` (OUTFIT_LOG_LEVEL),
  `harness.Resolve` (OUTFIT_HARNESS + source reporting), and the
  domain-owned vars (OUTFIT_API_TOKEN, OUTFIT_CONFIG_DIR, XDG/LUCINATE
  dirs, AWS_REGION, OPENAI_*)
- [x] 4.5 Add per-variable precedence tests: flag beats env, env beats
  default, unset env falls through, for `OUTFIT_ALIAS` and each
  `OUTFIT_REMOTE_*` variable; assert the harness source-reporting tests
  still pass unmodified
- [x] 4.6 Verify: full suite green, coverage >= 80%, no `os.Getenv`
  of a CLI-layer `OUTFIT_*` var remains outside the D3 "Unchanged" list

## 5. Completion on Cobra's engine

- [x] 5.1 Implement the `completion` command: positional argument validated
  to `bash | zsh | powershell`, `RunE` printing
  `GenBashCompletionV2` / `GenZshCompletion` /
  `GenPowerShellCompletionWithDesc` output for the root; any other argument
  (including `fish`) fails naming the three supported shells; confirm Cobra
  does not auto-add its own `completion` command because ours exists
- [x] 5.2 Port the dynamic-candidate helpers from `complete.go` into
  completion functions used by Cobra: `aliasNames`, `loadCatalogFor`
  (honouring `--providers` on the line via a port of `flagValue`),
  `providerOn` (via port of `flagValue` for `--provider`/`-p`),
  discovery-backed model lookup with best-effort silence on any error
- [x] 5.3 Register `ValidArgsFunction`s: alias-slot (`apply`, `unapply`,
  `alias`, `serve`, `daemon`, `harness` first arg, each `remote <sub>`'s
  outfit slot) → aliases + `ShellCompDirectiveDefault`; `unalias` → aliases +
  `NoFileComp`; `init-providers` → `Nil + ShellCompDirectiveDefault`
- [x] 5.4 Register flag completion functions: `--provider/-p` → catalogue
  names (`NoFileComp`); `--model/-m` → discovered models scoped to the
  provider on the line, empty on any failure; `--harness/-H` and `--set` →
  `harness.Names()`; `--log-level` → `daemon.LevelNames()`;
  `--providers`, `--fleet`, `--dir` → `ShellCompDirectiveDefault`;
  nothing registered for boolean flags
- [x] 5.5 Wire the `--outfit/-O` completion (aliases + files) in
  `harness`'s `ValidArgsFunction`: Cobra short-circuits flag-value
  completion entirely for `DisableFlagParsing` commands, so the attached
  forms (`--outfit=<partial>`, `-O=<partial>`) must be intercepted there
  (toComplete starts with `--outfit=`/`-O=`); the bare `-O`/`--outfit`
  consumes no word, so the following slot is the first positional the
  function sees. Add end-to-end `__complete` cases for both attached forms
  plus the bare `-O` slot
- [x] 5.6 Enforce the silence guarantee: every completion function returns
  `(nil, ShellCompDirectiveNoFileComp)` on any error and writes nothing to
  stderr; add a test that an unreadable outfit config and an unloadable
  catalogue both give exit zero, no candidates, empty stderr
- [x] 5.7 Port the `complete_test.go` scenario assertions through Cobra's
  real `__complete` (root command list; `unalias` exact names;
  `remote <TAB>` subcommands; `remote deploy <TAB>` alias+paths;
  `add -p <TAB>` providers; `--model` consumes its value; providers honour a
  `--providers` override on the line)
- [x] 5.8 Replace `TestCompletionCoversDispatch`: (a) `__complete ""` lists
  every visible root subcommand and nothing hidden, and `__complete fleet ""`
  / `__complete remote ""` list their children; (b) for every visible
  command, running `__complete <cmd> "--"` lists every long flag the command
  registers; (c) delete this file source-scanning logic
- [x] 5.9 Delete the old engine: the `commands` table, `candidateKind`,
  `completions`, `positionalsUsed`, `cmdComplete`, the `__complete` case
  from whatever dispatch remains, `completion.bash`, `completion.zsh`,
  `completion.ps1`, and their `//go:embed` directives — in the same commit
  that deletes the old `__complete` dispatch case so the two never coexist
- [x] 5.10 Verify: full suite green, coverage >= 80%, and a manual smoke on a
  built binary: `outfit completion bash|zsh|powershell` prints the generated
  scripts, `outfit completion fish` fails naming the three shells, and
  sourcing the bash script completes a command, a flag, a provider, an alias,
  and `-O`

## 6. Help, version, and old dispatch removal

- [x] 6.1 Set the root's `Version` to the build version, keep the `version`
  subcommand, and make `-v` a shorthand for `--version`
- [x] 6.2 Delete the hand-written `usage()` and update any tests that
  asserted on its exact text to the Cobra-rendered help
- [x] 6.3 Update the `main` package doc comment (the `// Usage:` block) to
  the Cobra tree and remove now-stale flag-package references from
  `cmd/outfit`
- [x] 6.4 Verify: `outfit` (no args) prints help and exits 0, `outfit
  --help`, `outfit -h`, `outfit --version`, `outfit -v`, and `outfit
  version` all behave as today; unknown command exits 1

## 7. Docs and final verification

- [x] 7. Update AGENTS.md: the `cmd/outfit` layout section (Cobra tree,
  pflag, `completion.bash`/`complete.go` removal, `TestCompletionCoversDispatch`
  replacement), the Traps section (the completion-table trap is replaced by
  tree-derived completion; note `NoOptDefVal`/`-O` and `DisableFlagParsing`
  for `harness`), and any `flag`-package references
- [x] 7. Update the context block in `openspec/config.yaml` to reflect the
  new dependencies (drop "no runtime dependencies" for the CLI layer; keep
  the internal/ domain packages dependency-free)
- [x] 7. Final gate: `gofmt -l .` empty, `go vet ./...` clean,
  `go test ./... -cover` green with total coverage >= 80%, and `go
  build -o outfit ./cmd/outfit`
- [x] 7. Manual completion smoke on the built binary in both bash and zsh:
  alias/provider/model slots, `--outfit` attached form, `remote <TAB>`
  subcommands, a previously-drifted flag (e.g. `list --models`), and the
  `fish` rejection
