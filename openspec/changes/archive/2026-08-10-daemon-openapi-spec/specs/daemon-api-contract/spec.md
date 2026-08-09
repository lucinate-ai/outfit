## Purpose

The machine-readable description of the daemon's control API: what it covers,
how it is kept honest against the implementation, and how a consumer gets the
one matching the outfit version it is talking to. The API's *behaviour* is
specified by `daemon-api`; this is about the contract document describing it.

## ADDED Requirements

### Requirement: A published OpenAPI description

The repository SHALL carry an OpenAPI description of the daemon control API,
in a documented location, covering every endpoint the API exposes: its method
and path, its authentication, its request body where it takes one, and the
shape of its responses including their error replies.

The description SHALL be the reference a non-human consumer works from — the
control-plane Lambdas that call the API over SSM, and any future fleet client —
rather than each consumer re-deriving the shapes from prose.

#### Scenario: Every endpoint is described

- **WHEN** the control API exposes an endpoint
- **THEN** that endpoint appears in the OpenAPI description with its method,
  path, authentication and response schema

#### Scenario: The prose documentation points at it

- **WHEN** a reader opens the API's prose documentation
- **THEN** it links the OpenAPI description, and keeps prose only for behaviour
  a schema cannot express

### Requirement: The description is verified against the implementation

The OpenAPI description SHALL be checked against the running code by the test
suite, not maintained by hand alone. The check SHALL fail when the description
and the implementation disagree about which routes exist, or about the fields
of the JSON the API returns.

A failing check SHALL name what disagrees, so the fix is obvious without
reading both documents side by side.

#### Scenario: A new endpoint without a description fails the build

- **WHEN** a route is added to the control API and the OpenAPI description is
  not updated
- **THEN** the test suite fails, naming the undescribed route

#### Scenario: A described endpoint that does not exist fails the build

- **WHEN** the OpenAPI description names a route the API does not serve
- **THEN** the test suite fails, naming the phantom route

#### Scenario: A changed response field fails the build

- **WHEN** a field is added to, removed from, or renamed in a response the API
  serialises, and the description is not updated
- **THEN** the test suite fails, naming the field and the schema it belongs to

### Requirement: The description ships with each release

The OpenAPI description SHALL be attached to each published release as an
asset, so a consumer can fetch the contract for the exact version it targets
rather than reading whatever currently sits on the default branch.

#### Scenario: A release carries the contract

- **WHEN** a release is published
- **THEN** the OpenAPI description is among its downloadable assets
