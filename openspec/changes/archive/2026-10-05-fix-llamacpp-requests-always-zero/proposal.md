## Why

The metrics tile's `requests` figure is always 0 for llama.cpp, while the
other engine-sourced counters work. The collector reads the figure from
`request_success_total` for every engine family — a name only vLLM's metrics
endpoint serves. llama.cpp's server exposes no cumulative request counter at
all: its `/metrics` carries the token, decode and speculative-decode counters
and the in-flight gauges (`requests_processing`, `requests_deferred`, already
read as the `running` figure) and nothing more. So for llama.cpp the tile
draws a zero no engine ever produced.

The test suite never caught it: the unit-test fixture and the two example
stacks' engine configs fabricate a `llamacpp:request_success_total` line a
real engine would never serve, so the parser is only ever exercised against
output no engine produces.

The engine-metrics spec already requires the collected statistics to include
the request count *as exposed by the engine*, and the collector's own charter
says every stat is optional — a host without a source for one omits it. This
change enforces that: where the engine exposes no cumulative request counter,
the figure is absent from the collected statistics rather than reported as
zero, and the renderers draw the line only for a figure the statistics
carry.

## What Changes

- The collected statistics carry the request count only where the engine's
  metrics expose a cumulative request counter: vLLM's
  `request_success_total`, as today; none for llama.cpp. The statistics'
  `requests` field becomes optional — present with the counter's value,
  including a genuine zero, absent where the engine exposes none — in the
  daemon's reply, the remote relay's reply, and the `metrics` JSON output.
- The shared token block — the dashboard tile, the `metrics` bar and table
  formats, the serve view — draws the `requests:` line only where the
  figure is present.
- The unit-test fixture and the two example stacks' engine configs stop
  fabricating `llamacpp:request_success_total` and carry the lines a real
  engine of that family serves.
- The OpenAPI description and the control plane's TypeScript mirror mark
  the field as optional.

## Capabilities

### New Capabilities

(None.)

### Modified Capabilities

- `engine-metrics`: the request count is read from the engine's cumulative
  request counter where the engine exposes one, and is absent from the
  collected statistics where it exposes none — never a zero the engine never
  produced.

## Impact

- `internal/metrics`: `TokenStats.Requests` becomes a pointer with
  `omitempty`; the parser sets it from the engine family's request counter
  where the family names one.
- `cmd/spinloop`: the token-block renderer omits the line for an absent
  figure.
- `docs/openapi.yaml`, `remote/lambda/shared/stats.ts`: the field optional.
- `internal/metrics/metrics_test.go`,
  `examples/fleet-docker/engine/engine-config.yaml`,
  `examples/gateway-docker/engine/engine-config.yaml`: fixtures carry what
  real engines serve.
- The tests that construct `TokenStats` with the plain `Requests` field,
  across `cmd/spinloop`, `internal/remote` and `internal/fleet`, take the
  pointer form.
