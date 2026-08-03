## ADDED Requirements

### Requirement: harness injects remote env vars
When `outfit harness` launches with an Outfit that contains a `REMOTE` instruction, it SHALL automatically obtain the remote endpoint's environment variables and inject them into the child process environment.

#### Scenario: harness injects OPENAI_BASE_URL and OPENAI_API_KEY
- **WHEN** the user runs `outfit harness my-remote-outfit` and the Outfit has a `REMOTE` instruction and the endpoint is running
- **THEN** the launched harness process receives `OPENAI_BASE_URL` and `OPENAI_API_KEY` in its environment

#### Scenario: harness calls env Lambda to fetch key
- **WHEN** the user runs `outfit harness` with a remote Outfit
- **THEN** the command calls the remote env Lambda (not Start) to obtain the `api_key` and `base_url` from the response

#### Scenario: harness fails when endpoint is stopped
- **WHEN** the remote instance is stopped and the user runs `outfit harness` with a remote Outfit
- **THEN** the command fails with an error telling the user to run `outfit remote start` first

#### Scenario: harness informs user it is fetching remote env
- **WHEN** the user runs `outfit harness` with a remote Outfit
- **THEN** a message is printed to stderr (e.g. "Fetching remote endpoint env vars...") so the user knows a network call is happening

#### Scenario: harness without REMOTE is unaffected
- **WHEN** the user runs `outfit harness` with an Outfit that has no `REMOTE` instruction
- **THEN** the command behaves as before with no remote Lambda calls

#### Scenario: existing env vars are not overridden
- **WHEN** `OPENAI_BASE_URL` or `OPENAI_API_KEY` is already set in the user's shell environment
- **THEN** the existing value is preserved (the remote value is only injected when the variable is not already set)

### Requirement: harness remote error is loud
When the remote endpoint cannot be reached during `outfit harness`, the command SHALL fail before launching the harness.

#### Scenario: AWS credentials failure surfaces early
- **WHEN** AWS credentials are not available and the user runs `outfit harness` with a remote Outfit
- **THEN** the command fails with a clear error before attempting to launch the harness

#### Scenario: remote not deployed surfaces early
- **WHEN** the remote config does not exist (no deploy yet) and the user runs `outfit harness` with a remote Outfit
- **THEN** the command fails with a clear error referencing the missing remote config

#### Scenario: missing env_url in config surfaces early
- **WHEN** the remote config lacks an `env_url` field and the user runs `outfit harness` with a remote Outfit
- **THEN** the command fails with an error indicating the remote deployment needs to be updated
