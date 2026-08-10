## Context

See proposal.md — Why. What shapes the fix:

- `scrapeTargetFor` (`cmd/outfit/serve_daemon.go`) already parses the engine's
  argv — it lifts `--api-key`, and reads `--api-key-file` off disk. It just
  never looked at `--host`/`--port` in that same argv, taking the address from
  the Outfit's `BASEURL` or the engine table's `defaultBaseURL` instead.
- `defaultBaseURL` is `http://127.0.0.1:8080` for llama.cpp and
  `http://127.0.0.1:8000` for vLLM. The cloud starts both on `:8000`, which is
  why only llama.cpp was visibly broken.
- The daemon on a cloud instance has no Outfit. It is driven by a deploy
  config whose `serveArgs` carry the bind, so `BASEURL` is empty and the
  fallback was always reached.
- `ScrapeTokenStats` strips a trailing `/v1` and appends `/metrics`, so a bare
  `http://host:port` and a `.../v1` base URL both resolve correctly.

## Goals / Non-Goals

**Goals:**

- Collect from where the engine actually is, in every deployment shape.
- Make a failing collector visible at the moment it fails.

**Non-Goals:**

- No change to what counts as activity, the sample interval, or any rendering.
- No new configuration. The information needed was already in the argv.
- No retry or backoff on a failed scrape — a sample that fails is already
  specified as a non-observation, and that is still right.

## Decisions

### D1: The engine's own arguments win

Precedence becomes argv bind → configured `BASEURL` → engine default.

`BASEURL` is a *client-facing* address: it may name a public IP or a proxy, and
an Outfit may state it while the engine binds somewhere else entirely. The
argv is the only source that describes what the process did. Since the scrape
is always local, the argv is both more accurate and more specific.

Alternative considered: keep `BASEURL` first and use the argv only as a
fallback. Rejected — that preserves the failure mode wherever an Outfit states
a public `BASEURL` for an engine bound to loopback, which is a normal remote
setup.

### D2: A wildcard bind means loopback

`--host 0.0.0.0` is a bind directive, not a destination. Dialling it happens to
work on Linux and does not everywhere; loopback is what is meant and always
correct for a same-host scrape. `::` and `[::]` are treated the same way.

### D3: Report a failing source, stay silent about an absent one

`Stats.Errors` gains the scrape failure with the address it tried. The
existing contract — an absent source is omitted rather than reported — is
deliberately preserved for sources that genuinely are not there, because a
macOS host without `nvidia-smi` reporting an error every time would bury the
failures worth seeing.

The dividing line is whether the collector had somewhere to look. An engine
with no metrics endpoint yields no scrape target and stays quiet; a scrape
target that does not answer is a fault.

This is what made the original bug survive: the token block simply never
appeared, and an engine that has served nothing looks exactly the same.

## Risks / Trade-offs

- **Existing deployments start reporting an error they did not before** —
  anything whose scrape was already failing now says so, on stderr, in
  `outfit remote metrics` and `outfit fleet metrics`. → That is the point, and
  it is the correct reading of a broken collector. It is a visible change for
  anyone currently broken, which is worth calling out in a release note.

- **A preset that sets a port the engine ignores** would send the collector
  somewhere wrong. → Both supported engines take `--host`/`--port` literally;
  an engine that did not would have been equally misdescribed by the old
  default.

- **`BASEURL` demotion could surprise** someone who set it specifically to
  redirect collection. → No such use is documented, and the field is described
  as where clients reach the engine. The argv only wins when it states an
  address, so an Outfit-only setup is unaffected.

## Migration Plan

None. No configuration changes, no persisted state. A deployment already
scraping the right address is unaffected; a broken one starts working, and
says so if it still cannot reach the engine.

Reaching a cloud instance requires the usual path — a published release, an
AMI rebake, and a fresh instance — because the instance's outfit binary is
pinned into the AMI.
