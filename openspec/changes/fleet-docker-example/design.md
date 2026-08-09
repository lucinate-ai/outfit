## Context

See proposal.md — Why. What already exists, and what a live prototype established:

- `outfit daemon` supervises the engine as a **child process** (`exec.Command`,
  own process group, log capture) and exposes the control API `fleet` speaks.
  `engineFor("llamacpp")` resolves the engine binary as `llama-server` from
  `PATH`.
- `internal/metrics` scrapes the engine's own Prometheus `/metrics` and parses
  the `llamacpp:` dialect.
- `OUTFIT_CONFIG_DIR` pins outfit's config directory, which a container needs
  since a bare service has no useful `$HOME` (the bug that motivated it).
- **Prototype findings** (run end to end on a laptop before this was written):
  Imposter's native engine is a standalone `imposter-go` binary invoked as
  `imposter-go <config-dir>` with the port from `IMPOSTER_PORT`; outfit's own
  scraper parses its `llamacpp:`-dialect output (`Running:3 PromptTokens:4096
  Requests:17`); a real daemon supervises it as a genuine child (verified by
  PPID); and `fleet status`/`metrics`/`start`/`stop` all work against it.

## Goals / Non-Goals

**Goals:**

- One artifact that is both a good example and a real integration test.
- Exercise the parts stubs cannot: a real supervised process, a real network
  hop, real auth, real unreachability, real crash detection.
- No production code changes — test outfit as shipped.

**Non-Goals:**

- A real inference engine (llama.cpp/vLLM). The point is the fleet control
  path, not inference; a real engine would need GPUs and minutes of model load.
- Testing `outfit remote` (the cloud path) — that has its own AWS-side story.
- The interactive TUI (#59) or the published multi-arch image (#57), though
  this example will use the latter when it exists.

## Decisions

**D1 — The fake engine is Imposter's native engine, exec'd directly.** The
daemon supervises a child process, so the fake engine has to be a binary it can
exec — a sidecar container would bypass the supervisor entirely, which is the
part most worth testing. Imposter's native engine is a single binary that
serves configured HTTP from static YAML, so it stands in for `llama-server`
convincingly: it answers `/health` and serves a `llamacpp:` Prometheus
`/metrics` that outfit's real collector parses.

**D2 — The shim execs the engine binary, never the Imposter CLI.** This is the
finding that most shapes the design, and it was only visible in a live run:
invoking `imposter up` puts the **CLI wrapper** in the supervisor's child slot.
When the underlying engine dies, the wrapper exits *0*, so the daemon correctly
records a clean `stopped` — and a crash assertion would silently pass while
testing nothing. The shim therefore `exec`s `imposter-go` directly, making the
engine itself the daemon's child so an abnormal death is a real non-zero exit
and reads as `crashed`.

**D3 — A `llama-server` shim on `PATH`, not a code change.** The image places a
script named `llama-server` earlier on `PATH` than anything else; it parses the
`--port` the daemon passes, ignores the rest of llama.cpp's flags, and execs
the engine with `IMPOSTER_PORT` set. `engineFor("llamacpp")` therefore resolves
to it with no test-only branch in outfit — the example tests the shipped
binary, and the shim is a property of the *image*, not of outfit.

**D4 — Each node pins `OUTFIT_CONFIG_DIR`.** Containers get no useful `$HOME`;
the daemon unit on the cloud instance already pins this for the same reason.
Setting it in the image keeps the container's state deterministic and exercises
the same resolution path production uses.

**D5 — Nodes differ deliberately, so the fleet is worth looking at.** The stack
brings up several nodes that are not clones: at least one with a token and one
without (loopback-style), and the test additionally stops one container to
produce a genuinely unreachable node. A fleet view of three identical rows
would demonstrate little.

**D6 — Build outfit from the working tree.** The node image builds outfit from
the repository being tested, so CI verifies *this commit* rather than a
published artifact. A published image (#57) would be friendlier for a user just
kicking the tyres; the Dockerfile should leave that switchable later, but
building from source is the correct default for a test that must not pass
against yesterday's binary.

**D7 — `run-tests.sh` is the CI entry point and a user-runnable script.** It
brings the stack up, waits for readiness, runs the assertions from the spec,
and tears down — with output a human can read when it fails. The same file is
what CI calls, so there is no CI-only path that can drift from what a
maintainer runs locally.

## Risks / Trade-offs

- [Imposter is a new dependency of the example] → only of the *example image*,
  not of outfit; it is fetched during the image build and pinned to a version.
- [A fake engine cannot catch real-engine quirks] → true and accepted: this
  tests the fleet control path. Real-engine behaviour is covered by the cloud
  e2e on an actual GPU box.
- [Docker in CI] → adds a dependency and some runtime; mitigated by keeping the
  stack small and the assertions quick, and by the open question below on
  scheduling.
- [The shim could drift from real llama.cpp flags] → it deliberately ignores
  everything but `--port`, so new flags cannot break it; what it proves is the
  supervision and control path, not flag fidelity.

## Open Questions

- **Per-PR or nightly/on-label?** The stack should run well under a minute,
  which is fine per-PR, but it adds Docker to every run. Settle when the CI job
  is wired.
- Whether the compose file should later offer the published image (#57) as an
  alternative to building from source, via a build arg.
