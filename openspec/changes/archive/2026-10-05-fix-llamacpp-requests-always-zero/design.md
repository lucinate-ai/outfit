## Context

See proposal.md — Why. The implementation-relevant current state:

- `ParseTokenStats` (`internal/metrics/parse.go`) parses an engine's
  Prometheus text against a per-family spec: the metric prefix, the gauges
  that count in-flight work, and the cumulative counters. It sets
  `Requests` from a metric named `request_success_total` for **every**
  family — a name only vLLM's endpoint serves.
- A live llama.cpp server run with `--metrics` serves no request counter at
  all. Its request-bearing lines are the in-flight gauges
  `requests_processing` and `requests_deferred` (already read into
  `Running`); its counters are token, time, decode and speculative-decode
  figures. Nothing counts completed requests.
- `TokenStats.Requests` is a plain `int` with no `omitempty`, so the field
  serialises whatever it holds — including a zero no engine produced — and
  the token block draws it unconditionally on every surface that renders
  the statistics.
- The engine-metrics spec says the collected statistics include the request
  count *as exposed by the engine*, and the package charter says every stat
  is optional: a host without a source for one omits it rather than
  erroring, and an absence is distinguishable from a zero.
- The field crosses the daemon API (`docs/openapi.yaml`), the remote relay
  (`remote/lambda/shared/stats.ts`) and the `metrics` JSON output, so its
  optionality is a contract change as well as a parsing change.

## Goals / Non-Goals

**Goals:**

- A `requests` figure a user reads is one the engine's own metrics
  produced.
- An engine family whose metrics carry no cumulative request counter yields
  statistics without the figure, the way an engine with no metrics endpoint
  yields statistics without any.
- The fix is verifiable against what engines actually serve, and the
  fixtures say what engines say.

**Non-Goals:**

- No estimation. No counter of request completions kept across scrapes by
  the daemon from the in-flight gauges: with parallel slots, gauge
  transitions between scrapes are lossy, and a figure the engine never said
  is a second fabrication where the first was.
- No change to what either engine is asked to serve, and no change to
  `Running`, `Counter`, the token counters, or any system stat.
- No new output format, and no change to the formats' structure beyond a
  line that can now be absent.

## Decisions

**D1: The request counter is per engine family, and may be none.**

`engineSpec` names, beside its gauges and counters, the cumulative counter
that carries the family's request count: `request_success_total` for vLLM,
none for llama.cpp. `ParseTokenStats` sets the figure from that counter
where the family names one and leaves it unset otherwise.

The alternative — reading one shared name for every family — is the bug.
Estimating the figure from gauge transitions was rejected in the
Non-Goals.

**D2: Absence is a pointer, not a zero.**

`TokenStats.Requests` becomes `*int` with `json:"requests,omitempty"`.
*Set* means the family names a request counter: the value is that
counter's, including a genuine zero from an engine that has started but
served nothing. *Unset* means the family names none, and the field does
not serialise at all. Telling "no figure" from "a zero figure" is the same
distinction the spec already requires of the last-active pair and of every
system stat.

The alternative — leaving the field a plain `int` and having the renderers
hide a zero — was rejected: it keeps the field in every JSON reply and
forces every consumer to know that a zero from a llama.cpp node means
"not exposed" while a zero from a vLLM node means "served nothing". The
pointer makes the distinction in the data, where a contract change belongs.

**D3: The token block draws a line only for a figure the statistics carry.**

The shared renderer omits the `requests:` line where the figure is unset.
Which lines appear is the statistics' affair, not the renderer's: one code
path draws whatever every surface's statistics carry, and no surface
special-cases a runner.

**D4: The fixtures serve what engines serve.**

The unit-test fixture and the two example stacks' engine configs drop the
fabricated `llamacpp:request_success_total` line and carry the lines a real
engine of that family serves. The example engines remain "real" in the
sense the fleet-docker-example spec asks — a process serving the dialect
spinloop parses — and now in the stronger sense too: the lines it serves
are lines an engine of that family actually serves.

## Risks / Trade-offs

- [A consumer that reads `tokens.requests` unconditionally sees the field
  vanish on llama.cpp nodes] → the OpenAPI description and the TypeScript
  mirror both mark it optional in the same change, and a consumer asking
  "how many requests did this engine serve" already met an answer that
  depends on the engine family: the field is documented as the family's own
  counter, present where the family has one.
- [The tile loses a line on llama.cpp] → the line today is a zero the
  engine never said; removing a fabrication is the point. The `running`
  figure beside it already carries what the gauges say, and the token
  counters carry the lifetime totals.
- [Old daemons always sent the field, so the relay's TypeScript mirror is
  briefly ahead of the daemons it relays] → an optional field accepts both
  an absent and a present value, so a mirror updated before, with, or after
  the daemon relays no field is correct throughout.

## Migration Plan

None: nothing is persisted, and the change is a revert. The relay's mirror
gains the optionality in the same change as the daemon that first omits the
field; because an optional field accepts both shapes, no ordering
constraint exists between them.
