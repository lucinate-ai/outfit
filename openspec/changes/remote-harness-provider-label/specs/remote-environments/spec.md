## ADDED Requirements

### Requirement: A remote harness provider is labelled distinctly

When an Outfit that has a `REMOTE` is applied, the harness provider's display
name SHALL be distinct from that of the local engine of the same kind, so the
remote environment and a local provider built from the same `PROVIDER` entry are
told apart in a harness model picker. The display name SHALL combine the
catalogue engine's display name with the resolved environment name (for example
`llama.cpp (dev-2)`); when the catalogue entry has no display name, the
environment name SHALL be used on its own. This labelling SHALL apply only to the
display name — the provider key, the `<environment>/<model>` default model, the
engine options, the API-key environment variable, and the base URL are unchanged
from the existing remote-naming behaviour. When no environment name resolves (a
path-form `REMOTE` whose config names none), the display name SHALL remain the
catalogue engine name, as before.

#### Scenario: A remote provider is labelled with its environment

- **WHEN** an Outfit states `PROVIDER llamacpp` (display name `llama.cpp`),
  `ALIAS qwen`, and `REMOTE dev-2`, and is applied
- **THEN** the harness config holds a provider keyed `dev-2` whose display name is
  `llama.cpp (dev-2)`, distinct from a local `llamacpp` provider's `llama.cpp`

#### Scenario: A local and a remote engine of the same kind are distinguishable

- **WHEN** both a local `llamacpp` provider and a remote `dev-2` provider built
  from the same engine are present in the harness config
- **THEN** their display names differ, so the two appear as separate rows in the
  harness model picker rather than two identical `llama.cpp` rows

#### Scenario: An engine with no display name falls back to the environment name

- **WHEN** an Outfit's `PROVIDER` catalogue entry has no display name and its
  `REMOTE` resolves to environment `dev-2`, and is applied
- **THEN** the harness provider's display name is `dev-2`

#### Scenario: A path form without an environment keeps the engine label

- **WHEN** an Outfit states `PROVIDER llamacpp` and `REMOTE ./remote.json`, and
  that `remote.json` has no `environment` field, and is applied
- **THEN** the harness provider's display name is `llama.cpp`, as before
