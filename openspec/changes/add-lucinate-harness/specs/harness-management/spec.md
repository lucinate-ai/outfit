## ADDED Requirements

### Requirement: lucinate is a registered harness

lucinate SHALL be a registered harness alongside opencode and Pi, selectable by
the same runtime precedence (`--harness`/`-H` flag, then `OUTFIT_HARNESS`, then
the stored preference), and settable as the stored default. Registering it SHALL
NOT change the default harness (`opencode`) used when nothing selects one, and
SHALL NOT change how opencode or Pi behave.

#### Scenario: lucinate is available

- **WHEN** the user lists or selects harnesses
- **THEN** lucinate appears among the available harnesses and `-H lucinate`
  resolves to it

#### Scenario: lucinate can be the stored default

- **WHEN** the user sets lucinate as the stored default harness and then runs a
  command with no `-H` flag and no `OUTFIT_HARNESS`
- **THEN** the lucinate harness is used and the source is reported as the stored
  preference

### Requirement: lucinate receives its OpenAI key at launch

When outfit launches the lucinate harness, it SHALL inject the active provider's
resolved API key into the launched agent's environment as
`LUCINATE_OPENAI_API_KEY`, in addition to the provider key variables it already
forwards. This is what lets lucinate authenticate an OpenAI-compatible
connection whose stored secret outfit deliberately left unwritten. As with the
other harnesses, outfit SHALL place this value only in the launched agent's
environment and SHALL NOT write the secret to disk. When outfit cannot resolve a
key for the active provider, it SHALL inject nothing under this name and leave
lucinate to fall back to its own stored secret or auth prompt.

#### Scenario: The active provider's key reaches lucinate

- **WHEN** outfit launches lucinate for a provider whose key it can resolve
- **THEN** the launched agent's environment carries `LUCINATE_OPENAI_API_KEY` set
  to that key

#### Scenario: No resolvable key injects nothing

- **WHEN** outfit launches lucinate and cannot resolve a key for the active
  provider
- **THEN** no `LUCINATE_OPENAI_API_KEY` is injected and the launch still proceeds
