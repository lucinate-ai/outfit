## MODIFIED Requirements

### Requirement: harness injects remote env vars
When `outfit harness` launches with an Outfit that contains a `REMOTE` instruction, it SHALL automatically obtain the remote endpoint's environment variables and inject them into the child process environment.

The fetch SHALL happen before the Outfit is applied, and the apply SHALL resolve the provider's API key variable against the fetched key as well as the local environment. The config the apply writes is therefore complete, and the apply SHALL NOT warn that no API key is set when the launch is about to supply one. The fetched key SHALL be used only where nothing local supplies a value, so an exported key or one in the adjacent `.env` still wins, and it SHALL satisfy only the API key variable — no other lookup.

The key SHALL reach the agent through its environment alone, and SHALL NOT be written into any harness config. Where a harness reads the key under its own name — lucinate's `LUCINATE_OPENAI_API_KEY` — the fetched key SHALL satisfy that too.

#### Scenario: harness injects OPENAI_BASE_URL and OPENAI_API_KEY
- **WHEN** the user runs `outfit harness my-remote-outfit` and the Outfit has a `REMOTE` instruction and the endpoint is running
- **THEN** the launched harness process receives `OPENAI_BASE_URL` and `OPENAI_API_KEY` in its environment

#### Scenario: harness calls env Lambda to fetch key
- **WHEN** the user runs `outfit harness` with a remote Outfit
- **THEN** the command calls the remote env Lambda (not Start) to obtain the `api_key` and `base_url` from the response

#### Scenario: harness fails when endpoint is stopped
- **WHEN** the remote instance is stopped, no API key is set locally, and the user runs `outfit harness` with a remote Outfit
- **THEN** the command fails with an error telling the user to run `outfit remote start` first

#### Scenario: the apply does not warn about a key the launch supplies
- **WHEN** the user runs `outfit harness` with a remote Outfit, no `OPENAI_API_KEY` set locally, and the endpoint running
- **THEN** the apply reports that the key is read from the environment when the harness runs, and does not warn that no key was set

#### Scenario: harness informs user it is fetching remote env
- **WHEN** the user runs `outfit harness` with a remote Outfit
- **THEN** a message is printed to stderr naming the remote, so the user knows a network call is happening

#### Scenario: harness without REMOTE is unaffected
- **WHEN** the user runs `outfit harness` with an Outfit that has no `REMOTE` instruction
- **THEN** the command behaves as before with no remote Lambda calls

#### Scenario: existing env vars are not overridden
- **WHEN** `OPENAI_BASE_URL` or `OPENAI_API_KEY` is already set in the user's shell environment
- **THEN** the existing value is preserved (the remote value is only injected when the variable is not already set)

#### Scenario: lucinate receives the fetched key under its own name
- **WHEN** the user runs `outfit harness -H lucinate` with a remote Outfit and the endpoint is running
- **THEN** the launched lucinate process receives the fetched key as `LUCINATE_OPENAI_API_KEY`

### Requirement: harness remote error is loud
When the remote endpoint's environment cannot be fetched during `outfit harness`, the command SHALL report the failure on stderr rather than discarding it, and SHALL bound the attempt with a timeout so an unresponsive control plane cannot block the launch indefinitely.

The failure SHALL be fatal — before the harness is launched and before its config is written — when no API key is otherwise available to the launched agent, because the endpoint refuses every request without one. The error SHALL name the remote, carry the underlying cause, and say how to resolve it: start the endpoint, or set the key.

When an API key is already available — exported, in the `.env` beside the Outfit, or set by an `ENV` instruction, which overrides both in the launched agent's environment — the fetch was only a convenience, so the command SHALL warn and carry on.

#### Scenario: AWS credentials failure surfaces early
- **WHEN** AWS credentials are not available, no API key is set locally, and the user runs `outfit harness` with a remote Outfit
- **THEN** the command fails with a clear error before attempting to launch the harness

#### Scenario: remote not deployed surfaces early
- **WHEN** the remote config does not exist (no deploy yet) and the user runs `outfit harness` with a remote Outfit
- **THEN** the command fails with a clear error referencing the missing remote config

#### Scenario: missing env_url in config surfaces early
- **WHEN** the remote config lacks an `env_url` field and the user runs `outfit harness` with a remote Outfit
- **THEN** the command fails with an error indicating the remote deployment needs to be updated

#### Scenario: a fatal fetch leaves the harness config untouched
- **WHEN** the fetch fails with no API key available and the user runs `outfit harness` with a remote Outfit
- **THEN** the harness config is not written

#### Scenario: an available key downgrades the failure to a warning
- **WHEN** the fetch fails but `OPENAI_API_KEY` is already set in the environment, in the `.env` beside the Outfit, or by an `ENV` instruction
- **THEN** the failure is reported on stderr and the harness is launched with the key that is available
