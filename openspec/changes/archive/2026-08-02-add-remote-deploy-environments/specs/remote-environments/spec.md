## MODIFIED Requirements

### Requirement: Named environment registry

A remote environment SHALL be a directory under the per-user config,
`${XDG_CONFIG_HOME:-~/.config}/outfit/remotes/<name>/`, whose canonical file is
`remote.json` — the control URLs, region, base URL, and the environment
identifier of one deployed instance. Because the lifecycle Lambda URLs are shared
across environments, the identifier is what selects this environment's instance;
the `remote` client SHALL send it with each control request. The directory form
SHALL be used so that other per-environment state may live alongside `remote.json`
later, and so that distinct environments never share a file. The registry SHALL
hold as many environments as the user has instances.

#### Scenario: An environment is a directory holding remote.json

- **WHEN** an environment named `qwen3.6-27b-prod` is registered
- **THEN** its configuration is `~/.config/outfit/remotes/qwen3.6-27b-prod/remote.json`

#### Scenario: Two environments do not collide

- **WHEN** two environments `a` and `b` both exist
- **THEN** each has its own `~/.config/outfit/remotes/<name>/` directory and
  neither overwrites the other

#### Scenario: The identifier selects the instance

- **WHEN** two environments share the same lifecycle Lambda URLs and a control
  command runs for one of them
- **THEN** the environment identifier in its `remote.json` is sent so the shared
  Lambda acts on that environment's instance

#### Scenario: A control call without an environment is rejected

- **WHEN** a control request reaches a lifecycle Lambda naming no environment
- **THEN** it is rejected with an error saying how to name one, rather than a
  default being silently assumed — defaults are a CLI affordance, not part of
  the control API
