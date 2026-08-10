## Why

The daemon scraped its engine's counters at the engine's *compiled-in default*
address whenever no Outfit stated a `BASEURL` — not at the address the engine
was actually told to bind. On a cloud llama.cpp instance, which is driven by a
deploy config rather than an Outfit, the engine bound the deploy config's
`--port 8000` while the scraper asked `127.0.0.1:8080`. Every scrape was
refused.

It went unnoticed because the failure was silent: `Daemon.Metrics` turned a
scrape error into `tokens = nil` and reported nothing, so the token block just
never appeared and looked like an engine that had served no requests.

The cost was larger than a missing block. Engine activity is *derived* from
those counters, so with the scrape permanently failing the activity record
never moved: `lastActiveAt` stayed pinned at engine start, and every consumer
of it — `outfit fleet status`, the new last-active line in the metrics views,
and the cloud's own idle-stop check — was reading a figure that could only ever
grow, regardless of load. Confirmed on a live `g6e.xlarge`: the engine's
`/metrics` answered 200 on `:8000` while `:8080` refused the connection.

## What Changes

- The scrape address is taken from the engine's own `--host`/`--port` when the
  command states them, because that is where the process actually binds. The
  Outfit's `BASEURL` and the engine's compiled-in default remain as fallbacks,
  in that order.
- A wildcard bind (`0.0.0.0`, `::`) is rewritten to loopback. The scrape is
  always to an engine on the same host, and a wildcard names every interface
  rather than one to dial.
- A scrape that *fails* is reported in the collected stats' errors, naming the
  address it tried. An absent source — an engine with no metrics endpoint at
  all — stays silent as it does today. The distinction is between "there is
  nothing here to collect" and "something is here and it is not answering".
- **No change** to what counts as activity, to the sampling interval, or to any
  rendering. This restores an input that was already specified.

## Capabilities

### New Capabilities

None — this makes the collector do what `engine-metrics` already requires and
makes its failures visible.

### Modified Capabilities

- `engine-metrics`: pins down what "the engine's serving address" means when
  the engine's command states one, and distinguishes a failing source (
  reported) from an absent one (omitted silently).

## Impact

- `cmd/outfit/serve_daemon.go` — `scrapeTargetFor` reads the bind from argv,
  alongside the API key it already lifts from there.
- `internal/daemon/daemon.go` — a failed scrape appends to `Stats.Errors`
  rather than being discarded.
- `docs/openapi.yaml` — the `errors` description currently says an absent
  source is omitted rather than reported, which is still true but no longer the
  whole story.
- Every cloud llama.cpp deployment is affected, and every local one where the
  Outfit states no `BASEURL` and the preset sets a non-default port. vLLM binds
  `:8000` by default, which is why it was not visibly broken.
- No breaking change: a deployment that was already scraping the right address
  keeps doing so, and the new error only appears where collection was already
  failing.
