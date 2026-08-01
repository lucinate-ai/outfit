# Design

## Context

Bedrock is a catalogue-only entry today — no Go code special-cases it. Adding
Vertex follows the same path: mostly `providers.yaml`, plus one small schema
addition (`optionsRequired`) because Vertex, unlike Bedrock, has no usable
default for a caller-supplied value.

## Decisions

### Two providers, keyed by opencode's built-in ids

opencode resolves a provider with no `npm` against its built-in registry, keyed
by the provider id (`opencode.go` writes the block under `/provider/<id>`). Its
built-in ids are `google-vertex` (Gemini) and `google-vertex-anthropic`
(Claude). We use those keys verbatim, so neither provider needs an npm package —
identical to how `amazon-bedrock` works. (Naming the Gemini one
`google-vertex-gemini` was considered and rejected: it would not match the
built-in id and would then require `npm: "@ai-sdk/google-vertex"` to load as a
custom provider, for no benefit.)

### No API key; ambient credentials

Like Bedrock, neither provider sets `apiKeyEnv`. The AI SDK's Vertex provider
reads Application Default Credentials (`gcloud auth application-default login`,
or a service-account key file via `GOOGLE_APPLICATION_CREDENTIALS`). outfit
injects nothing secret.

### Options: `project` (required) and `location` (default `global`)

```yaml
options:
  location: global
optionsRequired:
  - project
optionsFromEnv:
  project: GOOGLE_VERTEX_PROJECT
  location: GOOGLE_VERTEX_LOCATION
```

`project` has no sensible default (it is caller-specific), so it is required.
`location` defaults to `global`, overridable via env.

### `optionsRequired` schema addition

Add `OptionsRequired []string` to `catalog.Provider`. In `BuildProviderBlock`,
after merging static options and `optionsFromEnv`, check each listed key is
present and non-empty in the resolved options map; otherwise return an error
naming the option and, when known, the environment variable from
`optionsFromEnv` that supplies it. This runs only in the opencode build path;
Vertex has no `pi` block, so `BuildPiProvider` already rejects it with the
existing "not supported by the pi harness" error.

`apiKeyRequired` stays as-is — it guards the injected key, a separate concern
from a required option.

## Model ids (informational, not in the catalogue)

The catalogue no longer enumerates models; the user names them. For reference in
docs/examples:
- Claude on Vertex: `claude-3-5-sonnet-v2@20241022` (publisher `anthropic`, `@date` form).
- Gemini on Vertex: `gemini-2.0-flash`, `gemini-1.5-pro`.

## Risks / trade-offs

- The `location: global` default may not serve every model in every region;
  users override via `GOOGLE_VERTEX_LOCATION`. Documented, not enforced.
- Exact opencode option key (`location` vs `region`) and model-id forms should
  be spot-checked against a current opencode build during implementation.
