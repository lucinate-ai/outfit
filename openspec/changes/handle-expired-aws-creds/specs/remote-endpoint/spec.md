## MODIFIED Requirements

### Requirement: Authenticated control requests

Requests to the control URLs SHALL be signed with the caller's own AWS
credentials, resolved from the standard credential chain, and SHALL carry the
hash of the request body so that a request with a payload is signed over that
payload. Outfit SHALL NOT store AWS credentials of its own.

Every control subcommand — `start`, `stop`, `status`, `deploy`, and `stats` —
SHALL treat a non-success reply from the control endpoint as a failure: it SHALL
return an error and a non-zero exit, and SHALL NOT print an empty or partial
result as though the call succeeded.

A rejected request SHALL be reported with an actionable cause. When the request
is rejected because the caller's AWS credentials are expired or invalid, the
command SHALL say to refresh them (env credentials, a profile, or an SSO
session), distinct from the case where the credentials are resolvable but may
lack permission to invoke the endpoint.

#### Scenario: A request carrying a body is signed over it

- **WHEN** `outfit remote deploy` sends a configuration
- **THEN** the request is signed including the body's hash, not as an empty
  payload

#### Scenario: Credentials are missing

- **WHEN** no AWS credentials can be resolved
- **THEN** the command fails saying how to configure them

#### Scenario: Credentials are expired

- **WHEN** `outfit remote status` runs with expired or invalid AWS credentials
  and the control endpoint rejects the signed request
- **THEN** the command fails with a non-zero exit and a message saying to
  refresh the AWS credentials, rather than printing a blank state

#### Scenario: The endpoint rejects a control request

- **WHEN** any control subcommand receives a non-success HTTP reply from the
  control endpoint
- **THEN** the command reports the failure with its status and cause, and does
  not present the empty reply as a successful result
