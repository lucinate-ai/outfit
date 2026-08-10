## MODIFIED Requirements

### Requirement: Engine stats collection

The system SHALL collect token and request statistics from a running engine by
querying the engine's own metrics endpoint over HTTP on the engine's serving
address. The collected statistics SHALL include prompt and generated token
counts and request counts as exposed by the engine. When the engine requires
API-key authentication for its metrics endpoint, the collector SHALL
authenticate with the key the engine was started with.

The serving address SHALL be the one the engine was actually told to bind:
when the engine's command states a host or port, those SHALL determine where
the collector looks, in preference to any address configured elsewhere or
compiled in as that engine's default. An engine started on a non-default port
is the ordinary case, not an exception — a deployment that describes what to
serve without stating a base URL still binds wherever its arguments say.

A bind naming every interface SHALL be read as loopback for collection
purposes. Collection is always to an engine on the same host, and a wildcard
is not an address to dial.

Where the engine's command states no address at all, the collector SHALL fall
back to a configured base URL, and failing that to the engine's default.

#### Scenario: Running engine yields token stats

- **WHEN** metrics are collected while a supervised engine is running and
  serving requests
- **THEN** the result includes the engine's prompt token, generated token, and
  request counts

#### Scenario: The engine's own arguments locate it

- **WHEN** an engine is started on a port other than its default, and no base
  URL is configured
- **THEN** the collector queries the port the engine was given, not the
  engine's default

#### Scenario: A wildcard bind is collected over loopback

- **WHEN** an engine is started bound to every interface
- **THEN** the collector queries it on loopback

#### Scenario: Unreachable engine does not fail collection

- **WHEN** metrics are collected and the engine's metrics endpoint cannot be
  reached
- **THEN** the result omits engine stats, reports the rest, and the collection
  as a whole does not error

### Requirement: Graceful platform degradation

When a system stat's source is unavailable on the host (for example
`nvidia-smi` on a machine without NVIDIA tooling, or Linux-only commands on
macOS), the collector SHALL omit that stat and return the remainder, rather
than failing the collection. The absence SHALL be distinguishable from a zero
value in the collected result.

A source that is *present and failing* SHALL be distinguished from one that is
absent. Where the collector has an address to query and the query fails, it
SHALL report that failure among the collected errors, naming what it tried, so
a misdirected or broken collector is visible rather than presenting as an
engine that has simply served nothing. An absent source SHALL remain silent:
reporting the routine absence of a source as an error would bury the failures
worth seeing.

#### Scenario: macOS host lacks GPU stats

- **WHEN** metrics are collected on a macOS host
- **THEN** the result includes engine stats and available CPU/RAM figures,
  omits GPU stats, and reports no error

#### Scenario: Missing command omits only its section

- **WHEN** one system stat source is missing but others are present
- **THEN** only the missing stat is absent from the result

#### Scenario: A failing scrape is reported, not hidden

- **WHEN** the collector has an engine address to query and the query fails
- **THEN** the result omits the engine's counters and reports an error naming
  the address it tried

#### Scenario: An engine with no metrics endpoint stays silent

- **WHEN** the engine exposes no metrics endpoint, so there is no address to
  query
- **THEN** the result omits the engine's counters and reports no error
