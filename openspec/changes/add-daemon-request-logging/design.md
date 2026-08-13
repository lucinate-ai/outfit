## Context

See proposal.md — Why. The constraints that shape the approach:

- `Daemon.Handler(token)` returns `authenticated(token, mux)` and is the single
  place both API hosts build their handler: `cmdDaemon` and
  `runServeForegroundAPI` in `cmd/outfit/serve_daemon.go`. Anything wrapped
  there covers both commands with no per-command wiring, which is the whole
  reason this change can be small.
- `Supervisor` already owns the moments worth recording. `Start` launches the
  process, and a goroutine on `cmd.Wait()` classifies the exit as `stopped` or
  `crashed`. Today that classification is only observable by polling
  `/v1/status` — nothing announces it.
- outfit has no logger at all. Every user-facing line is `fmt.Printf` to stdout,
  including `outfit daemon`'s three startup lines and serve's narration of the
  command it is about to run. There is no convention to follow, so this change
  sets one.
- The dependency rule in AGENTS.md: no runtime dependencies. `log/slog` is
  stdlib, so this stays inside it.
- `internal/daemon` is imported by `cmd/outfit/serve.go` already (for
  `DefaultAPIAddr`), so both commands can reach a level parser that lives there.
- `cmd/outfit/serve_daemon_test.go` finds the API's port by regexing
  `control API on (…)` out of a redirected `os.Stdout`. Anything that moves that
  line has to move the helper with it, or the suite waits ten seconds and fails.

## Goals / Non-Goals

**Goals:**

- One request summary per request, covering every endpoint and both hosts,
  without an endpoint opting in.
- Severity that makes the level control useful: raising it drops routine
  traffic before it drops failures.
- A logger that is injected, so tests are silent by default and there is one
  place that decides where output goes.
- Establish the level flag and `OUTFIT_LOG_LEVEL` as outfit's convention, so a
  later change adding logging elsewhere does not invent a second one.

**Non-Goals:**

- JSON output. `slog` makes it a one-line handler swap later
  (`OUTFIT_LOG_FORMAT=json`); adding it now means specifying a second output
  contract with no caller asking for it.
- Log rotation or a log file. Records go to stderr; whoever runs the process
  (systemd, launchd, tmux, docker) decides where that lands. The engine's own
  log file is a separate thing and stays as it is.
- Request tracing, ids, or correlation across the fleet.
- Turning the rest of the CLI over to the logger. `add`, `apply`, `export` and
  the rest talk to a person at a terminal; a level control is meaningless there.
- Latency histograms or any metric derived from the summaries. `/v1/metrics`
  is the metrics surface.

## Decisions

### One middleware, outside authentication

`Handler` becomes `summarize(logger, authenticated(token, mux))` — the
summariser outermost. Order is the decision, not the mechanism: inside the auth
wrapper, a 401 would never be summarised, and a rejected token is one of the two
things this change exists to make visible. Outermost also means an unrouted path
(the mux's own 404) is recorded, which is how a client calling a misspelled or
not-yet-implemented endpoint shows up.

The middleware wraps the `http.ResponseWriter` to capture the status code and
the byte count, defaulting the status to 200 when a handler writes a body
without calling `WriteHeader` — which `writeJSON` always does, but the default
keeps a future handler honest.

The wrapper deliberately does **not** re-implement `Flusher` or `Hijacker`. No
endpoint streams today, and a wrapper that silently drops those interfaces is a
trap for whoever adds one — so it is named here: **if a streaming logs endpoint
is ever added, this wrapper has to forward `Flusher` first.**

Considered and rejected: logging inside each handler. It puts the same six
lines in six places, cannot see the status a handler returned through
`writeError`, and misses 401s and 404s entirely.

### Severity by outcome, not one level for all traffic

2xx/3xx at info, 4xx at warn, 5xx at error. This is what makes a single level
knob do the job the operator actually wants: `--log-level warn` on a node that
three fleet clients poll every two seconds silences the polling and keeps the
bad token, the malformed cursor and the failed start.

The alternative — everything at info — was rejected because the only way to
quiet a polled node would also hide every rejection, which turns the control
into a choice between noise and blindness.

Trade-off, stated because it is a deliberate departure from "raise the level to
silence request summaries": at `warn` the summaries are not entirely gone, only
the successful ones. That is the intended reading of the request, and the docs
say so plainly rather than letting the operator discover it.

### The level lives in `internal/daemon`, parsed strictly

`daemon.ParseLevel(string) (slog.Level, error)` and a small constructor for the
handler. Both commands already import the package, so a separate
`internal/logging` package would exist to hold forty lines with no second
consumer. If a later change gives the non-API commands a logger, moving it out
is a rename.

Parsing is an explicit switch over `debug|info|warn|error`, case-insensitive,
rather than `slog.Level.UnmarshalText`. `UnmarshalText` would also accept
`INFO+2` and would report an error that does not name what outfit accepts; the
spec requires a mistyped level to fail at startup naming the accepted values,
because a typo that silently logged at the default would be discovered only when
the log was needed.

Precedence is flag > `OUTFIT_LOG_LEVEL` > `info`, matching the precedence rule
outfit already uses for `--harness`/`OUTFIT_HARNESS` and
`--providers`/`OUTFIT_PROVIDERS`. Both `outfit daemon` and `outfit serve` take
the flag. On a plain `outfit serve` with no `--api` there is no API to
summarise, and the flag governs only the engine-lifecycle records — accepted
rather than rejected, so the same command line works with and without `--api`.

### An injected logger, defaulting to discard

`Daemon.Logger *slog.Logger` and `Supervisor.Logger *slog.Logger`, both nil by
default and both read through a helper that substitutes a discarding logger.
This follows how `Collector`, `BuildArgv` and `Now` are already injected: the
package holds no global, the CLI is the one place that decides where output
goes, and the existing tests — which construct daemons and supervisors directly
— stay silent without being edited.

`slog.Default()` as the nil fallback was rejected for exactly that reason: it
would make every existing test print, and would let an unrelated caller's global
handler configuration decide what a daemon logs.

The CLI sets both fields where it constructs both objects, in one place. Having
`Daemon` push its logger into `Sup` was rejected — implicit mutation of one
injected object by another is worse than two assignments next to each other.

### The logger is built from `os.Stderr` inside the command

Not at package init, and not captured in a package variable. The daemon tests
redirect the `os.Stderr` *variable* before invoking the command; a handler
constructed at init would already hold the real file and the redirect would
capture nothing. Building it inside `cmdDaemon`/`runServeForegroundAPI` keeps
the tests able to read what was logged.

### What becomes a record and what stays narration

The rule: **what the API and the engine do goes to the log; what a command is
about to do for the person who typed it stays on stdout.**

Moves to the log (info): `daemon ready`, `engine log: …`, `control API on …`,
and `Serving %s from the pushed deploy config` — that last one is printed from
inside a start handler, so it is an API event that today lands on the daemon's
stdout with no timestamp and no level.

Stays as it is: the formatted engine command, `Using preset …`, `Serving … from
<Outfit>`, and everything `--dry-run` prints. These are read by a person, are
asserted on by the serve tests, and have no business behind a level control.

Consequence to handle rather than discover: `apiAddrFromStdout` in
`cmd/outfit/serve_daemon_test.go` becomes `apiAddrFromStderr`. It is a test
helper, but leaving it is a ten-second hang followed by a confusing failure, so
it is a task in its own right.

### What a record carries, and what it must never carry

Fields: `method`, `path` (including query — cursors and bounds only), `status`,
`duration`, `bytes`, `remote`. The token never appears: the middleware reads no
headers, so there is no code path that could render one, which is a stronger
guarantee than remembering to redact. Bodies never appear either — a pushed
deploy config can carry serve arguments with credentials in them, and the logs
endpoint's response is engine output that may contain prompts and model text.

`r.RemoteAddr` is logged as-is, host and port. Behind a reverse proxy that is
the proxy's address; outfit does not trust `X-Forwarded-For` and does not
pretend to, since the API is meant to be reached directly.

### The crash record comes from the supervisor's own wait

Recording the exit inside the goroutine that already classifies it means a crash
is logged at the moment it happens, with nobody polling. That is a real gain
over the status endpoint: today a node can crash at 03:00 and the only evidence
by morning is a state string that says `crashed` with no timestamp.

## Risks / Trade-offs

- [A busy fleet at the default level writes a summary per poll per node, and
  nothing rotates stderr] → the level control is the answer and the docs lead
  with it; `--log-level warn` is named in `docs/commands/serve.md` as the
  setting for a polled node. outfit does not own the destination of stderr, so
  rotation stays the service manager's job.
- [Severity grading means `warn` does not silence *all* summaries] → intended,
  and documented explicitly so it is not a surprise. `error` is available for
  an operator who wants only outfit's own failures.
- [The `ResponseWriter` wrapper drops `Flusher`/`Hijacker`] → no endpoint needs
  them today; called out above and in a code comment so the person who adds
  streaming finds it before it bites.
- [Paths are logged with query strings] → the API's only parameters are `offset`
  and `limit`. Should a future endpoint take something sensitive in a query, the
  choice to log the full path has to be revisited — noted here rather than left
  implicit.
- [Moving `control API on …` to stderr changes what a script capturing stdout
  sees] → the line is not documented as a machine interface and the daemon's
  address is normally chosen by the operator via `--api-addr`. The one consumer
  in the repo is a test helper, which moves with it.
- [Two log destinations under `serve --api`: engine output on the inherited
  stdio, outfit's records on stderr] → this is the existing behaviour of the
  command, not a new split; the engine's stream stays byte-for-byte what it was.

## Migration Plan

Additive. A daemon started with no flag and no environment variable logs at
info, which is strictly more output than before — the only behaviour change a
current user sees, and the reason the level control ships in the same change.
Nothing persists, nothing is stored, and reverting is reverting. No change to
the API surface, so mixed-version fleets are unaffected: the contract test
passing untouched is the check that this stayed on its own side of the line.

## Open Questions

- Whether `outfit remote`'s control commands should share the same flag and
  variable once they have anything to log. Deferring costs nothing: the parser
  and the precedence rule are set here and a later change reuses them.
- Whether a JSON handler should be selected automatically when stderr is not a
  terminal, rather than by an explicit `OUTFIT_LOG_FORMAT`. Either can be added
  later without changing what the records contain.
