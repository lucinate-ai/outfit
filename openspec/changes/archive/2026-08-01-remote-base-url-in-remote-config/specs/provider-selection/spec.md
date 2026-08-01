## MODIFIED Requirements

### Requirement: Base URL precedence

The system SHALL let the user override any provider's API base URL, resolved
with the precedence: the explicit override, then the `OUTFIT_BASE_URL`
environment variable, then the catalogue's per-provider values. The explicit
override is the `--base-url`/`-u` flag, or the `BASEURL` of the Outfit being
applied.

When an Outfit names a remote configuration with `REMOTE` and states no
`BASEURL` of its own, applying it SHALL take the explicit override from that
configuration's base URL, so the endpoint's address stays with the deployment
that owns it rather than in the hand-written Outfit. The system SHALL report
that it did so. A remote configuration that does not exist, or that names no
base URL, SHALL NOT be an error: the base URL is left to the rest of the
precedence chain.

#### Scenario: Flag beats environment and catalogue

- **WHEN** `--base-url https://gateway/v1` is given and `OUTFIT_BASE_URL` is
  also set
- **THEN** the configured base URL is `https://gateway/v1`

#### Scenario: Base URL from the remote configuration

- **WHEN** an Outfit with `REMOTE ./remote.json` and no `BASEURL` is applied,
  and that file names a base URL
- **THEN** the provider is configured with the base URL from that file, and the
  output says where it came from

#### Scenario: An Outfit's own BASEURL wins

- **WHEN** an Outfit states both a `BASEURL` and a `REMOTE` whose configuration
  names a different base URL
- **THEN** the provider is configured with the Outfit's `BASEURL`

#### Scenario: Remote configuration not written yet

- **WHEN** an Outfit with `REMOTE ./remote.json` and no `BASEURL` is applied
  before the deployment that writes that file has run
- **THEN** applying succeeds, leaving the base URL to the catalogue
