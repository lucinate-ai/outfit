## Context

The CLI layer (`cmd/outfit`) is built on the standard `flag` package: `run()`
string-switches on the first arg to `cmdX` functions, each of which registers
its own `FlagSet` and parses `args`. Two properties of this shape produce
observable cost:

1. **Two descriptions of the same surface.** `cmd/outfit/complete.go` keeps a
   hand-maintained `commands` table (flags, value kinds, positionals,
   subcommands per command) plus three embedded shell scripts, because the
   `flag` package cannot enumerate a `FlagSet`. It has already drifted from
   the real surface: `list --models`, `daemon --api-token-file`/`--api-token`,
   `remote start --keep`/`--timeout`/`-t`, the `remote logs` flags, and the
   fleet/remote subcommand positionals all register in the commands but are
   absent from the table.
2. **Env-var resolution is re-implemented per variable.** `OUTFIT_PROVIDERS`
   (internal/catalog), `OUTFIT_BASE_URL` (internal/catalog + discovery, via
   injected `resolve` closures), `OUTFIT_LOG_LEVEL` (internal/daemon),
   `OUTFIT_HARNESS` (internal/harness, with source reporting), `OUTFIT_ALIAS`
   (cmd layer), `OUTFIT_REMOTE_*` (internal/remote, via an injected
   `os.Getenv`), `OUTFIT_API_TOKEN` and `OUTFIT_CONFIG_DIR` (domain-owned).
   Each encodes its own "flag beats env" rule.

Constraints that shape the approach:

- The user-visible interface must not change: command names, long and short
  flag names, defaults, outputs, exit codes, and every `OUTFIT_*` name and its
  precedence. Several of these are spec-locked (provider-catalog, api-logging,
  harness-management, alias-registry, shell-completion, remote-* capabilities).
- `cmd/outfit` tests call `run(args)` directly and assert on printed output;
  they must keep passing with minimal diff.
- `outfit harness` forwards trailing args to the launched harness byte-for-byte
  and has special rules: a leading positional that names an Outfit is consumed
  (not forwarded), `--` before the harness's args keeps them, bare
  `--outfit`/`-O` means `./Outfit` (the flag implements `IsBoolFlag` today).
- `internal/` packages stay dependency-free; the domain logic and the daemon
  API are untouched.
- Coverage >= 80%, gofmt, go vet (per `openspec/config.yaml`).

## Goals / Non-Goals

**Goals:**

- The whole `cmd/outfit` dispatch and flag parsing runs on a Cobra command
  tree with pflag flags, with the `cmdX` logic bodies unchanged.
- One Viper instance in the CLI layer is the single place that reads
  `OUTFIT_*` values the CLI owns, with flag > env > (config) > default
  precedence expressed in one mechanism.
- Tab completion is served by Cobra's native `__complete` engine and generated
  scripts; every completion offered today is still offered, every registered
  flag completes (drift fixed), and the never-error/never-stderr guarantee
  holds.
- All existing tests green; coverage >= 80%; AGENTS.md and
  `openspec/config.yaml` accurate afterwards.

**Non-Goals:**

- No new commands, flags, or shells: `outfit completion fish` still fails,
  listing bash/powershell/zsh. No described candidates (the `__complete`
  output stays plain candidate lines plus the directive line).
- No changes to `internal/` contracts, the Outfit format, harness adapters,
  daemon API, or provider catalogue.
- Viper is not given write access to `~/.config/outfit/config.json` (the
  read-modify-write with unknown-key round-trip stays in `internal/config`),
  and it does not re-implement env-var precedence that internal packages own
  by spec (see the ownership table in D3).
- No node-name completion for `fleet` subcommands (fleet.yaml names are not
  completable today either; parity, not new feature).

## Decisions

### D1 — Command tree mirrors the dispatch one-for-one

`run()`'s switch becomes a Cobra tree: root `outfit` with subcommands
`add remove list show apply unapply alias unalias serve daemon export
init-providers harness completion version` and two parents, `fleet` (children
status, metrics, logs, route, start, stop) and `remote` (children bootstrap,
start, pause, stop, status, metrics, logs, deploy, env, ls). Each `cmdX`
becomes a `*cobra.Command` whose `RunE` body is the existing logic; flags move
from the per-command `FlagSet` onto `cmd.Flags()` (pflag), keeping identical
names, shorthands, defaults, and usage strings.

Flags stay registered **per command, not as root persistent flags**. Today
`outfit list --providers x` parses and `outfit --providers x list` fails;
root persistent flags would silently widen the accepted surface. (Cobra's
`TraverseChildren` only affects parents that *are* parsed — `fleet`/`remote`
use it because their flags are documented at the parent spelling `outfit fleet
<sub> --fleet …`, and pflag accepts parent flags before the child name; both
spellings already work today because `fleetFlags`/remote flag parsing runs at
the parent level.)

Test seam: keep `func run(args []string) error` as a thin wrapper
(`rootCmd.SetArgs(args); return rootCmd.Execute()`) so the large existing
per-command test files change little. Each `cmdX`'s signature moves from
`args []string` to reading values off `*cobra.Command` (or a small struct of
resolved values), with the parsing logic deleted.

Alternative considered: a big-bang rewrite of `main.go` in one commit.
Rejected — the per-group sequence in the Migration Plan keeps every step
testable and bisectable.

### D2 — `harness` keeps manual parsing via `DisableFlagParsing`

Cobra would parse `outfit harness`'s flags, but pflag stops at the first
positional and cannot distinguish "positional that names an Outfit (consume
it)" from "the harness's own args (forward verbatim)" — that decision is
outfit's, not a parser's. So the `harness` command sets
`DisableFlagParsing: true` and runs the existing manual parse, ported from
`flag.FlagSet` to `pflag.FlagSet` (same registration API). The port keeps:

- `--outfit`/`-O` as a string flag with `NoOptDefVal = "./Outfit"` — the pflag
  replacement for today's `IsBoolFlag` on `outfitPathFlag`; bare `-O` and
  `-O`-without-path both mean `./Outfit`, `-O=<path>`/`--outfit=<path>` take
  the attached value.
- The `--` termination rules (`flagsTerminated`, leading `--` opt-out, dropping
  a `--` that directly follows a consumed Outfit) unchanged — they operate on
  the raw arg slice before parsing.
- Everything after the consumed outfit/`--` point is forwarded byte-for-byte.

Alternative considered: let Cobra parse and forward `cmd.Args()`. Rejected —
it cannot preserve the consume-leading-Outfit rule or the exact `--`
handling, both of which are spec- and invariant-locked (AGENTS.md "Alias
registry", harness-management spec).

### D3 — Viper scope: CLI-layer `OUTFIT_*` reads only, with an ownership table

One `*viper.Viper` is constructed once per process in the CLI layer:
`viper.New()`, `SetEnvPrefix("OUTFIT")`,
`SetEnvKeyReplacer(strings.NewReplacer("-", "_"))`, `AutomaticEnv()`. For each
command, a tiny `resolve(cmd *cobra.Command)` helper (run at the top of each
`RunE`) calls `v.BindPFlags(cmd.Flags())` so that for flags with an env
counterpart the precedence is pflag-changed > env > flag default — the rule
re-implemented today at each site.

Ownership per variable family — this table is the contract, and the
implementation must keep it that way:

| Variable(s) | After migration | Reason |
|---|---|---|
| `OUTFIT_ALIAS` | Viper (CLI layer) | Read only by the CLI (`readOutfit` empty-path branch, `defaultOutfitNamed` gate); no internal ownership, no flag spelling |
| `OUTFIT_REMOTE_*` (start/stop/deploy/region/base_url) | Viper, via `BindEnv` of the flat remote.json keys to the prefixed names, with `LoadConfig` on the per-environment `<path>` file | Replaces the injected `os.Getenv` in `internal/remote`'s loaders with the same viper instance; the internal package keeps its `func(string) string` injection point, the CLI hands it a viper-backed lookup |
| `OUTFIT_PROVIDERS` | Unchanged: `catalog.ResolveCatalogPath` | provider-catalog spec pins "--providers flag, then OUTFIT_PROVIDERS, then embedded"; all catalogue loads already funnel through that one function |
| `OUTFIT_BASE_URL` | Unchanged: injected `resolve` closures in catalog/discovery | Same precedence lives where the builders run (a flag value may be resolved at build time, not at parse time) |
| `OUTFIT_LOG_LEVEL` | Unchanged: `daemon.ParseLevel(flagValue)` | api-logging spec pins flag > env for daemon and serve |
| `OUTFIT_HARNESS` | Unchanged: `harness.Resolve` | Source of the choice (flag/env/stored/default) is *reported to the user* and asserted by tests; a viper-mediated read would lose that |
| `OUTFIT_API_TOKEN`, `OUTFIT_CONFIG_DIR`, `LUCINATE_DATA_DIR`, `XDG_CONFIG_HOME`, `AWS_REGION`, `OPENAI_*` | Unchanged | Domain-owned at their current sites; not CLI-surface variables |

Rationale for the boundary: Viper is introduced where a resolution lives
*inside the CLI layer today* (so the migration consolidates rather than
duplicates), and left out everywhere the precedence is an internal contract
with a named env var constant. Putting Viper in front of `OUTFIT_HARNESS` or
`OUTFIT_API_TOKEN` would create two readers of the same variable and risk the
exact class of silent drift this migration exists to end.

Alternatives considered:

- *Viper also loads `~/.config/outfit/config.json` as its config file.*
  Rejected — `internal/config` is the single writer with unknown-key
  round-trip through `File.extra`; viper cannot round-trip and a second reader
  of the same file would drift.
- *Cobra only, no Viper.* Rejected — the request names both, and the
  CLI-layer `OUTFIT_*` lookups are precisely the ones with no spec-locked
  home; a single instance also gives a uniform test seam.
- *Per-command viper instances.* Rejected — no benefit over one instance;
  `BindPFlags` is idempotent per flag name and the CLI never needs isolation
  between commands (a process runs one command).

### D4 — Completion: Cobra's engine, no custom `__complete`, no described candidates

Cobra ships the hidden `__complete` command and generates the shell scripts
that drive it. Its on-the-wire protocol is close to the one this codebase
hand-implemented but not identical: candidate lines may carry a
tab-separated description, the final directive line is the framework's
integer directive (`:0` = files allowed, `:4` = no files), and the process
writes a one-line status to its error stream on every call. The scripts the
framework generates require the integer form — the old `:nofile`/`:file`
spelling would parse as zero and silently re-enable file completion in the
generated bash script — and the scripts are required to be generated, so the
"Completion protocol" requirement is amended (see the delta spec) to the
framework's byte stream rather than re-emitting the old spelling through a
hand-rolled `__complete`. The guarantees that matter are preserved and become
testable: exit zero, no stderr (the root's error stream is discarded — the
framework's status line flows through it, while user-facing errors are still
printed by main), and no candidates on a broken config or catalogue. The
custom engine in `complete.go` and the three embedded scripts are deleted.

- **`completion` command.** Cobra auto-adds a `completion` command with
  bash/zsh/**fish**/powershell; since a command named `completion` already
  exists, it adds none. We define it: positional argument validated to
  `bash | zsh | powershell`, failing for anything else (including `fish`) with
  an error naming the three supported shells — preserving the shell-completion
  spec scenario. Its `RunE` prints `GenBashCompletionV2` / `GenZshCompletion` /
  `GenPowerShellCompletionWithDesc` output for the root command.
- **Descriptions ride along.** Candidate lines are `name\tdescription` where
  Cobra has a description (command shorts, flag usages); the generated
  scripts strip or present them per shell, so the set of candidates never
  differs between shells. There is no custom emission to keep byte-stable, so
  the old byte-parity goal is dropped in favour of the amended protocol.
  Tests compare candidate sets with the description stripped.
- **`harness` flag visibility.** `harness` runs with `DisableFlagParsing`, so
  Cobra can only complete flags it can see on the command's own flag set.
  The body's flag parsing therefore moves from a local `FlagSet` to the
  command's: one registry serves both the body's parse and the completion
  surface.
- **Wiring, replacing the `commands` table** (the old `candidateKind` values
  map one-to-one onto Cobra constructs):

  | Old kind | Cobra construct |
  |---|---|
  | positional alias-slot (`apply`, `unapply`, `alias`, `serve`, `daemon`, `harness` first arg, `remote <sub>` outfit slot) | `ValidArgsFunction` → `(aliasNames(), ShellCompDirectiveDefault)` |
  | `unalias` | `ValidArgsFunction` → `(aliasNames(), ShellCompDirectiveNoFileComp)` |
  | `init-providers` | `ValidArgsFunction` → `(nil, ShellCompDirectiveDefault)` |
  | `--provider`/`-p` | `RegisterFlagCompletionFunc` → catalogue `SortedProviderNames()`, `NoFileComp`; loads the catalogue honouring a `--providers` override already on the line (port of `flagValue`) |
  | `--model`/`-m` | `RegisterFlagCompletionFunc` → `discovery.Models` scoped to the `--provider` on the line (port of `providerOn`), best-effort: any failure → empty, `NoFileComp` (model-discovery spec already requires the silence) |
  | `--harness`/`-H`, `--set` | → `harness.Names()`, `NoFileComp` |
  | `--log-level` | → `daemon.LevelNames()`, `NoFileComp` |
  | `--providers`, `--fleet`, `--dir` (file-kind flags) | → `(nil, ShellCompDirectiveDefault)` |
  | boolean flags (`--dry-run`, `--api`, …) | nothing to register: Cobra completes flag *names* automatically and bools consume no value word |

  Command names and subcommands complete automatically from the tree — this is
  where the drift fix lands: `list --models`, `remote start --keep`, every
  `remote logs`/`fleet` flag are completable by construction, with no entry.
- **The `--outfit`/`-O` corner.** It is a `NoOptDefVal` flag taking an
  optional attached value; today only the attached form
  (`--outfit=<partial>` and bash's `--outfit` `=` `` split) completes it. The
  flag completion function returns `(aliasNames(), ShellCompDirectiveDefault)`.
  Cobra's handling of optional-value flags is the one construct with no direct
  precedent in the current tests, so tasks include an end-to-end
  `__complete` case for both attached forms; if Cobra does not invoke the
  function for a `NoOptDefVal` flag's value, the fallback is to intercept
  `--outfit=`/`-O=`-prefixed tokens inside the command's
  `ValidArgsFunction` (the slot is unambiguous), keeping the old behaviour.
- **Silence guarantee.** Every completion function returns
  `(nil, ShellCompDirectiveNoFileComp)` on any error (unreadable config,
  unloadable catalogue, discovery failure, nonsense input) and writes nothing
  to stderr. Cobra's `__complete` itself prints nothing to stderr on the
  normal path.

### D5 — The coverage guard walks the tree through the protocol

`TestCompletionCoversDispatch` (source-scan of `main.go` for `case "…":`)
cannot survive a Cobra tree and must be re-derived. New guard, exercised
*through the real protocol* rather than by reflection:

1. `__complete ""` lists every visible subcommand of the root (and, per
   parent, every child of `fleet`/`remote`), and no hidden command.
2. For **every** visible command, `__complete <cmd> "--"` lists every long
   flag it registers — this is the direct regression test for the drift that
   motivated the change: a flag that registers but does not complete fails
   the build.
3. The existing scenario-level assertions (unalias offers exactly the
   registered names, `remote <TAB>` offers subcommands, providers from the
   catalogue, `--model` consumes its value) port from `complete_test.go`
   unchanged in intent.

This is strictly stronger than the old guard: it tests the actual bytes the
shell receives, not a source pattern.

### D6 — Help, version, and error surface

- Root gets `Version: version`, keeping `outfit version`, and `-v` as a
  shorthand for `--version` (parity with today's top-level `-v` handling).
- The hand-written `usage()` is deleted; Cobra renders per-command help; a
  bare `outfit` prints root help (exit 0, as today's `run([])` does).
- Unknown commands produce Cobra's `unknown command … (did you mean …?)`
  error, exit 1. Text differs from today; nothing spec-locked depends on the
  old wording.
- `outfit <cmd> -h` works for every command (pflag auto-adds it, as Go `flag`
  already did per `FlagSet`).

## Risks / Trade-offs

- **pflag vs go-flag parse differences (interspersed flags, `-` prefixes,
  shorthand clusters).** Mitigation: the existing per-command tests already pin
  the accepted spellings; D2 ports the `harness` parse mechanically and the
  migration adds focused parse cases for the nastiest forms (bare `-O`,
  `-O=path`, `--` forwarding, shorthand clusters) before cutover.
- **Viper precedence subtleties (`IsSet` counting flag defaults,
  case-insensitive keys, empty-vs-unset).** Mitigation: read "did the user
  pass the flag" from `flag.Changed`, never from viper; viper is only ever
  asked for the env/config value. Per-variable precedence tests pin each row
  of the D3 table.
- **Cobra auto-added surface (help command, completion command, usage on
  error) changes some outputs.** Mitigation: none of it is spec-locked; tests
  that assert on help/usage text are updated to the new format in the same
  step that removes `usage()`.
- **`NoOptDefVal` + flag completion untested territory (D4 corner).**
  Mitigation: explicit end-to-end `__complete` cases and the
  `ValidArgsFunction` fallback; worst case the attached `-O` completion
  degrades to today's *no-candidate* behaviour, never to an error.
- **Dependency weight and supply chain: cobra (+pflag) and viper (+afero,
  cast, mapstructure, fsnotify) end the CLI layer's zero-framework property.**
  Accepted: trade-off made by the request; `internal/` stays clean, so the
  domain logic and its testability are undiminished. Pinned in go.mod at the
  versions current when the change lands.
- **One large PR.** Mitigation: the Migration Plan sequences the work so each
  step compiles, passes the full suite, and is independently revertable; the
  risky steps (harness parse, completion engine) land before the old engine is
  deleted, with both present momentarily.
- **Completion behaviour silently regressing at the shell level.** Mitigation:
  D5's protocol-level guard (it runs the same `__complete` the generated
  scripts call) plus a manual smoke checklist (bash + zsh, alias/provider/model
  slots, `-O`, `remote <TAB>`) against the real binary.

## Migration Plan

Staged so each step ends with `go build`, `go vet`, gofmt, and
`go test ./... -cover` green (coverage >= 80%):

1. **Dependencies + skeleton.** Add cobra and viper to go.mod. Create the
   root command and the full subcommand tree with empty `RunE`s delegating to
   the existing `cmdX(rest)` functions (flags still parsed by the old
   `FlagSet`s — zero flag-parsing change yet). `run()` routes every command
   through `rootCmd.Execute()`; the old switch is deleted. No user-visible
   change; the suite pins it.
2. **Flag migration, per group.** For each group — selection commands
   (`add`/`remove`), outfit-path commands (`apply`/`unapply`/`alias`/
   `unalias`), `list`/`show`/`export`/`init-providers`, `serve`/`daemon`,
   `fleet`+children, `remote`+children — move flags onto `cmd.Flags()`,
   change the `cmdX` body to read them, delete the `FlagSet`. Add the
   focused parse tests (D2 forms) as the group lands.
3. **`harness` parsing (D2).** `DisableFlagParsing` + the pflag manual parse,
   `NoOptDefVal` for `-O`; port its existing forwarding tests first as
   regression pins.
4. **Viper binding (D3).** Introduce the instance + `resolve(cmd)` helper;
   migrate `OUTFIT_ALIAS` and the `OUTFIT_REMOTE_*` lookups; add the
   per-variable precedence tests.
5. **Completion (D4 + D5).** Implement the `completion` command with the
   three generated scripts; wire `ValidArgsFunction`s and flag completion
   functions; add the new guard test; port `complete_test.go` assertions
   through `__complete`; delete the `commands` table, `cmdComplete`,
   `completions()`, and the three embedded scripts. (Cobra's `__complete`
   exists from step 1; until this step it is inert for our surface because
   nothing has completion functions yet — the hand-rolled `__complete`
   dispatch case is removed in the same commit as the old engine, so the two
   never coexist under one name.)
6. **Surface cleanup.** Delete `usage()`, wire `Version`/`-v`, update any
   tests asserting on help/usage wording.
7. **Docs + verification.** Update AGENTS.md (layout, Traps: embedded
   catalogue stays, completion section, `TestCompletionCoversDispatch`
   replacement, `-O`/`NoOptDefVal`) and the context in
   `openspec/config.yaml` (drop "no runtime dependencies"); full suite with
   coverage, gofmt, vet; manual completion smoke on a built binary
   (bash/zsh, alias/provider/model/`-O` slots, `remote <TAB>`, `fish`
   rejection).

Rollback: each step is a normal commit; the change set is revertable step by
step in reverse. There is no persistent-state migration — no config file,
alias, or Outfit format changes — so a full revert of the branch restores
today's CLI exactly.

## Open Questions

None that block: the one genuinely deferrable choice (whether to later serve
node-name completion from a resolved `fleet.yaml`, or add `fish`) is excluded
by the Non-Goals and needs no decision now.
