## Why

`outfit` can point a harness at Claude on AWS Bedrock, but there is no GCP
equivalent. Google Cloud's Vertex AI is the direct analogue — it hosts both
Gemini and Anthropic Claude — and teams standardised on GCP have no first-class
provider today. Adding it mirrors the Bedrock pattern (cloud creds, no injected
API key) and needs only catalogue plumbing plus one small schema addition.

## What Changes

- Add two built-in providers to the catalogue:
  - `google-vertex` — Gemini on Vertex AI (opencode's built-in provider id).
  - `google-vertex-anthropic` — Claude on Vertex AI (opencode's built-in id).
  Neither injects an API key: like `amazon-bedrock`, authentication rides on
  ambient Google credentials (Application Default Credentials — `gcloud auth
  application-default login`, or a service-account key file). Both take
  `project` and `location` options, resolved from `GOOGLE_VERTEX_PROJECT` and
  `GOOGLE_VERTEX_LOCATION`, with `location` defaulting to `global`. Neither has
  a `pi` block: Vertex authenticates with OAuth/ADC, which the Pi harness does
  not support (its `google-generative-ai` protocol is the API-key Gemini
  Developer API, a different service).
- Add an `optionsRequired` capability to the provider catalogue: a provider MAY
  declare a list of option keys that must resolve to a non-empty value, or the
  command fails with a clear error. Vertex needs this because it has no usable
  default `project` — unlike Bedrock, which ships a working default region. The
  existing `apiKeyRequired` only guards the injected API key, not options.

## Capabilities

### New Capabilities

None. Both changes extend the existing catalogue capability.

### Modified Capabilities

- `provider-catalog`: extend the embedded-provider schema with `optionsRequired`
  (a validated, non-empty-option check at apply time), and ship the two Vertex
  providers as part of the built-in catalogue.

## Impact

- `internal/catalog/providers.yaml` — two new provider entries; header-comment
  documentation for `optionsRequired`.
- `internal/catalog/catalog.go` — `OptionsRequired []string` on `Provider`;
  a validation step in `BuildProviderBlock` (opencode). No Go code enumerates
  models, so nothing else changes there.
- `outfit list` — the two providers appear in the alphabetical listing.
- Docs: `docs/commands/add.md` gains a Vertex example alongside the Bedrock one.
- No change to the Pi harness (both providers are opencode-only, as Bedrock is).
