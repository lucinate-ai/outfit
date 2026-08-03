# Harness Management Specification

## Purpose

Define the harness abstraction — the coding agent `outfit` configures — and its
runtime selection, the stored default, launching the agent with
`outfit harness`, and inspecting its configuration with `outfit show`. opencode
and Pi are the supported harnesses.

## Requirements

### Requirement: Harness abstraction

Each supported harness SHALL own its config format end-to-end behind a common
interface: a name, the executable that launches it, its config path, and the
operations apply / remove / read-state. Adding a harness SHALL be a matter of
implementing that interface and registering it; harness-neutral work
(catalogue loading, validation, size parsing) stays outside the adapters.

#### Scenario: Unknown harness

- **WHEN** any command is given `--harness foo` and no harness of that name is
  registered
- **THEN** the command fails listing the available harnesses

### Requirement: Harness resolution precedence

Every command that touches a harness SHALL resolve the active harness with the
precedence: `--harness`/`-H` flag, then the `OUTFIT_HARNESS` environment
variable, then the stored preference, then the default (`opencode`). Output
that names the active harness SHALL also say where the choice came from.

#### Scenario: Flag beats everything

- **WHEN** `OUTFIT_HARNESS=pi` is set, the stored preference is `pi`, and the
  user passes `-H opencode`
- **THEN** the opencode harness is used and the source is reported as the flag

#### Scenario: Default when nothing chooses

- **WHEN** no flag, environment variable, or stored preference selects a
  harness
- **THEN** opencode is used

### Requirement: Stored harness preference

`outfit harness --set <name>` SHALL validate the name against the registered
harnesses and store it as the default in outfit's own config file, without
disturbing the alias registry sharing that file. `outfit harness --get` SHALL
print the active harness and its source, the stored preference (or that none is
set), and the available harnesses, without launching anything.

#### Scenario: Setting the default

- **WHEN** the user runs `outfit harness --set pi`
- **THEN** later commands with no flag or environment override resolve to Pi,
  and the command reports where the preference is stored

#### Scenario: Setting an unknown harness

- **WHEN** the user runs `outfit harness --set foo`
- **THEN** the command fails listing the available harnesses

### Requirement: Launching the harness

`outfit harness` SHALL launch the active harness's executable, forwarding
stdio and any trailing arguments untouched. When the harness exits with a
non-zero code, `outfit` SHALL exit with that same code. When the executable is
not on the PATH, the error SHALL say which binary is missing and suggest
installing the harness.

#### Scenario: Arguments forwarded verbatim

- **WHEN** the user runs `outfit harness run --continue`
  and no leading argument names an Outfit
- **THEN** the harness binary is invoked with `run --continue`

#### Scenario: Exit code surfaces

- **WHEN** the launched harness exits with code 3
- **THEN** `outfit` exits with code 3

### Requirement: Applying an Outfit on launch

`outfit harness` SHALL be able to apply an Outfit before launching, two ways.
The `--outfit`/`-O` flag applies one first: given bare it means `./Outfit`, and
a named path must be attached (`--outfit=<path>`) because positional arguments
belong to the harness — a detached path or alias following a bare `--outfit`
SHALL be rejected with the attached form suggested. Independently, a *leading*
positional argument that names an Outfit (a path, a directory holding one, or a
registered alias) SHALL be applied and not forwarded — but only when no
`--outfit` was given and flag parsing was not ended by an explicit `--`. A `--`
immediately after a consumed leading Outfit SHALL be dropped; any other
arguments are forwarded byte-for-byte. A leading `--` SHALL opt out entirely,
for an alias that collides with a harness subcommand. An unreadable alias
registry SHALL demote the leading argument to a forwarded one, never block the
launch.

#### Scenario: Leading alias dresses then launches

- **WHEN** the user runs `outfit harness qwen3.6-27b -- --agent-flag`
- **THEN** the aliased Outfit is applied first and the harness is launched with
  only `--agent-flag`

#### Scenario: Bare flag applies the default Outfit

- **WHEN** the user runs `outfit harness -O` in a directory holding an `Outfit`
- **THEN** `./Outfit` is applied and the harness launches

#### Scenario: Detached flag value is caught

- **WHEN** the user runs `outfit harness --outfit ./dev/Outfit`
- **THEN** the command fails telling them to write `--outfit=./dev/Outfit`

#### Scenario: Explicit opt-out

- **WHEN** the user runs `outfit harness -- qwen3.6-27b`
- **THEN** nothing is applied and `qwen3.6-27b` is forwarded to the harness

### Requirement: Showing the configured state

`outfit show` SHALL report the active harness (and the source of that choice),
its config file path, the default model when the harness has one, and each
configured provider with its base URL and models — including each model's
context and output limits when set — followed by the registered aliases. It
SHALL honour the same `--harness`/`-H` override as every other command without
changing the stored default. An empty config SHALL be reported with a pointer
to `outfit add`, not an error.

#### Scenario: Inspecting another harness

- **WHEN** the user runs `outfit show --harness pi` while the stored default is
  opencode
- **THEN** Pi's configured providers are shown and the stored default is
  unchanged

#### Scenario: Nothing configured yet

- **WHEN** the harness config holds no providers
- **THEN** show prints the harness, config path, and a hint to run
  `outfit add`

### Requirement: Keys reach the launched agent

When outfit launches a harness, the launched agent's environment SHALL carry the
worn Outfit's local environment: the whole `.env` file beside that Outfit, and
the Outfit's own `ENV` instructions, in addition to the API key variables outfit
can resolve for the catalogue's providers. The precedence, highest to lowest,
SHALL be the Outfit's `ENV` instructions, then a variable already present in
outfit's own environment, then the adjacent `.env`. An `ENV` instruction SHALL
therefore override an exported variable; the `.env` SHALL only fill a variable
that is otherwise unset. These values SHALL be placed only in the launched
agent's environment — outfit SHALL NOT mutate its own process environment on this
path. When outfit launches with no Outfit worn, the whole-`.env` overlay and the
`ENV` instructions SHALL NOT be applied, though outfit SHALL still forward the
provider keys it can resolve. Neither harness stores a secret itself — each
resolves a reference when it runs — so a key kept where only outfit reads it still
reaches the agent. Failure to read the provider catalogue SHALL NOT prevent the
launch.

#### Scenario: A key only outfit can see still reaches the agent

- **WHEN** outfit can resolve a provider's key variable but it is absent from
  the environment, and the harness is launched
- **THEN** the launched agent's environment carries that variable

#### Scenario: An explicit setting is not overridden by the .env

- **WHEN** a variable is set both in outfit's environment and in the `.env`
  beside the worn Outfit, and the harness is launched
- **THEN** the launched agent sees the environment's value, not the `.env` value

#### Scenario: The adjacent .env fills a gap for the agent

- **WHEN** a variable is set in the `.env` beside the worn Outfit and is unset in
  outfit's environment, and the harness is launched
- **THEN** the launched agent's environment carries the `.env` value

#### Scenario: An ENV instruction overrides both

- **WHEN** the worn Outfit sets a variable with an `ENV` instruction and the same
  variable is also present in outfit's environment and/or the adjacent `.env`,
  and the harness is launched
- **THEN** the launched agent sees the `ENV` value

#### Scenario: Launching without an Outfit applies no overlay

- **WHEN** the harness is launched with no Outfit worn
- **THEN** outfit applies no whole-`.env` overlay and no `ENV` instructions; the
  agent runs with outfit's environment plus any provider key outfit resolves

#### Scenario: An unreadable catalogue still launches the agent

- **WHEN** the provider catalogue cannot be loaded
- **THEN** the harness is launched anyway, with the environment otherwise
  unchanged
