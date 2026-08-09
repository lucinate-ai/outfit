## MODIFIED Requirements

### Requirement: The instance engine runs under the daemon

At boot the instance SHALL run `outfit daemon` as its engine host, bound to
loopback, instead of a per-runner engine unit. The daemon's config directory
SHALL be pinned to a fixed system path via `OUTFIT_CONFIG_DIR` on its service
unit, so it does not depend on `$HOME` (which a systemd service does not get) —
what the boot writes and what the daemon reads are the same location. The boot
sequence SHALL derive the daemon's deploy config from the environment's stored
deploy config — with the cloud-owned settings (bind address and port, API-key
delivery, the synced local weights path) resolved into it — write it under that
pinned config directory, and request the engine's start through the control API
once the daemon answers, so the boot start is the same explicit API start any
client performs. The engine command the daemon runs SHALL be equivalent to the
one the boot script previously installed for that runner.

#### Scenario: Boot starts the engine through the daemon

- **WHEN** an instance boots for an environment whose deploy config names a
  runner and model
- **THEN** the daemon starts that engine serving the synced weights, and the
  endpoint answers on the instance's serving port as before

#### Scenario: The daemon reads what the boot wrote

- **WHEN** the boot writes the deploy config under the daemon's pinned
  `OUTFIT_CONFIG_DIR` and requests a start
- **THEN** the daemon finds that deploy config and starts the engine, rather
  than reporting nothing to serve

#### Scenario: The control API is loopback-only

- **WHEN** the daemon's control API is listening on the instance
- **THEN** it is bound to loopback, unreachable from the network, and needs no
  bearer token
