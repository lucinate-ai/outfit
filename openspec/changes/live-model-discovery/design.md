## Context

`outfit` currently has no code that fetches model metadata over the network; the only HTTP
client (`internal/remote/remote.go`, a 10-minute-timeout client for AWS Lambda control) is
unrelated. After `retire-model-families`, the catalogue is provider plumbing only, so there
is no static list of models to browse. Each provider, however, exposes its own model list:
OpenAI-compatible servers at `GET /v1/models`, Ollama at `GET /api/tags`. This change adds a
small, best-effort discovery layer that reads those.

## Goals / Non-Goals

**Goals:**
- A `internal/discovery` package that, given a provider and a resolve function, returns the
  provider's currently-served model ids.
- Per-provider protocol handling (OpenAI-compatible `data[].id`; Ollama `models[].name`).
- Short-TTL in-process caching and a bounded per-request timeout.
- Best-effort semantics: any failure means "no models", never a command failure.
- Surfacing via `outfit list --models <provider>` and model tab-completion.

**Non-Goals:**
- Amazon Bedrock discovery (`ListFoundationModels`) — deferred; it needs the AWS SDK path.
- Fetching pricing, context windows, or other metadata — only model ids/names here.
- Persisting a discovery cache across processes.
- Making plain `outfit list` hit the network — discovery is opt-in via `--models`, and
  completion (already latency-sensitive and failure-tolerant) sources it on demand.

## Decisions

**Protocol selected by provider kind, not a new catalogue field where avoidable.** The
existing `pi.api` value (`openai-completions`, `openai-responses`,
`google-generative-ai`) plus the provider id already distinguish OpenAI-compatible from
Ollama. Prefer deriving the protocol from those; add an explicit optional `discovery:`
field to `providers.yaml` only if the mapping proves ambiguous. Keeping the catalogue free
of a new required field preserves the "plumbing only" outcome of the prior change.

**Reuse the base-URL and key resolution the selection path already uses.** Discovery calls
the same `resolve` closure (`.env` beside the Outfit / working dir, then environment) and
the same base-URL precedence, so a provider that applies also discovers, with no new config
surface. The key is sent as an `Authorization: Bearer` header (or Ollama's keyless call)
and never written anywhere.

**A dedicated short-timeout HTTP client**, separate from the remote package's 10-minute
client. Discovery is interactive; a few seconds is the right ceiling. Model completion uses
the same layer, so its cache and timeout keep completion instant.

**Best-effort by construction.** The public function returns `([]string, error)` internally
but callers (`list`, completion) treat any error as "no models". `list --models` prints a
one-line "no models found" note; completion emits nothing. This matches the completion
spec's existing "never error, never stderr" rule.

Alternatives considered: a build-time snapshot of each provider's models (rejected — that
is the curation treadmill again); querying an aggregator like OpenRouter for *all*
providers (rejected — only OpenRouter knows its own catalogue; other providers must be
asked directly); Hugging Face Hub as the source (rejected — it is a weights hub and does
not know a provider's served ids).

## Risks / Trade-offs

- [A provider endpoint is slow or down] → bounded timeout + best-effort fallback; the
  command still succeeds on plumbing alone.
- [`--models` leaks that a key is set by succeeding/failing auth] → acceptable; the user is
  querying their own configured provider. No key value is ever printed.
- [Completion latency if discovery is slow] → TTL cache + short timeout; completion treats
  a timeout as "no candidates", so a cold, slow endpoint just yields no model suggestions
  that keystroke.
- [Ordering with `retire-model-families`] → this change is additive (a new capability) and
  does not modify the same requirements, but its narrative assumes the static lists are
  gone. It should be reviewed/merged after `retire-model-families`.

## Migration Plan

Additive; no data migration. Ship the `internal/discovery` package plus the `--models`
flag and completion wiring together. Rollback is a straight revert — nothing else depends
on discovery.

## Open Questions

- Do we derive the discovery protocol purely from provider id / `pi.api`, or add an
  explicit `discovery:` field to `providers.yaml`? Resolve during implementation against
  the actual provider set.
- Should `outfit list` (no flag) opportunistically show discovered models for *local*
  providers only (llama.cpp/Ollama/vLLM on localhost), where the call is cheap and offline
  is the norm? Left out for now to keep plain `list` network-free.
