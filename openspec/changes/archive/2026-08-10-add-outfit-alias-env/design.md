## Context

See proposal.md — Why.

Every Outfit command already funnels through one function, `readOutfit(usage,
path)` in `cmd/outfit/main.go`: `apply`, `unapply`, `alias`, `serve`, `daemon`,
`harness --outfit` and the five `remote` subcommands. It defaults an empty path
to `./Outfit` and hands a name-shaped path to `resolveAlias`, which reads the
registry from `internal/config`. That single choke point is why one change can
give every command the same shorthand, and it is where this one belongs too.

Two existing rules shape the design. A path-shaped argument never causes a
registry read at all, which is what keeps `serve` working with no outfit config
present. And a path on disk beats a registered name, so registering an alias can
never change what an already-working command does.

## Goals / Non-Goals

**Goals:**

- One place reads `OUTFIT_ALIAS`, so no command can be left out or behave
  differently from the rest.
- The variable cannot change what a command with an explicit argument does.
- Failures name the variable, so a stale export in a shell profile is
  diagnosable from the error alone.
- The package's tests are insulated from a developer's exported
  `OUTFIT_ALIAS`, the same way they are already insulated from the real config
  directory.

**Non-Goals:**

- An environment variable for an Outfit *path* (`OUTFIT_FILE` or similar).
  Aliases already cover naming an Outfit from anywhere; a second variable can be
  added later if a path turns out to be wanted.
- Making a bare `outfit harness` apply an Outfit — see the decision below.
- Any change to `internal/config` or the on-disk registry format.

## Decisions

**Read it in `readOutfit`, in the empty-path branch.** The variable is consulted
only when `path == ""`, before the `./Outfit` default. Every caller inherits it,
including the ones added later, and an explicit argument is beyond its reach
without a single comparison being written. The alternative — resolving in each
`cmdX` — reintroduces exactly the duplication the choke point exists to prevent,
and an omission would show up as one command quietly disagreeing with the
others.

**A separate resolver, not `resolveAlias`.** The environment lookup is a
different rule, so it gets its own small function rather than a flag threaded
through `resolveAlias`: it validates the name shape as an *error* rather than a
silent fall-through to path handling, it does no shadowing check, and it fails
when the name is unregistered instead of returning `ok=false`. Both still share
`config.Load`/`File.Alias`, so there is one registry read path.

**The variable is a name, never a path.** Accepting a path would mean carrying
both meanings and deciding what a value that is both means. A name-only variable
has one rule and one error message. A user who wants a path can `cd`, or pass
one.

**No shadowing by the working directory.** For an argument, "path beats alias"
exists because every pre-existing invocation passes a path. A variable has no
such history: it can only ever have been set to name an alias, so a file that
happens to share the name is a coincidence, not an intent. Honouring the
shadowing rule here would make the variable stop working depending on which
directory you stand in, which is the opposite of the point.

**`OUTFIT_ALIAS` beats `./Outfit`.** This mirrors `OUTFIT_HARNESS`, which beats
the stored preference: an environment variable is a deliberate act for this
shell, and a default is what happens when nobody said anything. The alternative
— `./Outfit` first, the variable only as a last resort — was rejected because it
makes the variable inert in exactly the directories people work in, leaving it
useful only where a bare command fails anyway.

**Which Outfit, not whether an Outfit.** `outfit harness` with no argument and
no `--outfit`/`-O` never calls `readOutfit`, so it keeps launching without
applying anything; `outfit harness -O` asks for the default Outfit and therefore
picks the variable up. Nothing special is written for this — it falls out of
reading the variable inside `readOutfit`. It is worth stating in the docs
because a user could reasonably expect either behaviour.

**`outfit alias` opts out by naming its default explicitly.** Rather than a new
parameter or a second function, `cmdAlias` passes `outfit.DefaultFile` when it
has no argument, so `readOutfit` never sees an empty path from it. That says
what it means — `outfit alias` with no argument means literally "the Outfit
here" — and the not-found message is unchanged, because it already keys off the
path being `outfit.DefaultFile`.

**The report goes to stderr.** Same reason the existing alias note does:
`outfit remote env` writes shell exports to stdout for `eval`, and a prose line
would break it. Wording follows the existing note — `Using OUTFIT_ALIAS "qwen"
(/path/to/Outfit)`.

**A package-level `TestMain` clears the variable.** `cmd/outfit` has no
`TestMain` today; tests are insulated from the real config by `XDG_CONFIG_HOME`.
An exported `OUTFIT_ALIAS` would now leak into every test that resolves a
default Outfit, so the package gets a `TestMain` that unsets it before running.
(`OUTFIT_HARNESS` has the same hazard already; that is a pre-existing gap and
not this change's to fix.)

## Risks / Trade-offs

- **A forgotten export in a shell profile silently redirects `outfit apply` in
  a project that has its own `Outfit`.** → The stderr note names the variable
  and the resolved path on every command it decides, so the surprise is visible
  in the output rather than only in the result. This is the cost of ranking the
  variable above `./Outfit`, and it is accepted deliberately.
- **A dangling or unregistered value breaks every Outfit command in that
  shell at once.** → The error names `OUTFIT_ALIAS` and points at
  `outfit alias --list`, so the fix is one `unset` or one re-point away.
- **`serve` now reads outfit's config in a case where it previously might not
  have.** → Only when `OUTFIT_ALIAS` is set, which is the user asking for a
  registry lookup. A missing or unreadable config with the variable unset
  behaves exactly as before.

## Migration Plan

Purely additive: with `OUTFIT_ALIAS` unset, every command resolves exactly as it
does today. No config migration, no flag deprecation. Rollback is reverting the
change.
