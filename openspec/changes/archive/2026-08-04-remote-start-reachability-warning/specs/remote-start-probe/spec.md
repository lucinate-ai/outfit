## ADDED Requirements

### Requirement: Post-start TCP probe

After `outfit remote start` reports the endpoint is ready, the CLI SHALL perform a TCP connection probe to the inference endpoint's host and port. The probe SHALL use the `base_url` from the start response to derive the target address. If the probe succeeds (connection established), no output is produced and the command exits normally.

The probe SHALL use a short timeout (default 5 seconds) so it does not materially extend the command's runtime. The timeout SHALL be configurable via a package-level variable for testing.

#### Scenario: Probe succeeds on a reachable endpoint

- **WHEN** the endpoint is ready and the TCP probe connects successfully
- **THEN** no warning is printed and the command exits normally

#### Scenario: Probe fails due to security group blocking

- **WHEN** the endpoint is ready but the TCP probe fails to connect
- **THEN** a warning is printed to stderr explaining that the endpoint is ready but unreachable from this network

### Requirement: Reachability warning message

When the probe fails, the warning SHALL state that the endpoint is ready but not reachable from the current network, and SHALL provide a remediation command using `outfit remote deploy --overwrite --allowed-cidr`. The command SHALL include the caller's public IP as a /32 CIDR, detected by calling `checkip.amazonaws.com`. If IP detection fails, the warning SHALL use a placeholder value instead of failing the command.

The warning SHALL NOT cause the command to exit with a non-zero status. The command SHALL still print exports (if `--env` is set) and exit 0.

#### Scenario: Warning includes the caller's IP

- **WHEN** the probe fails and the IP detection succeeds
- **THEN** the remediation command includes the caller's public IP as `/32`

#### Scenario: IP detection failure does not break start

- **WHEN** the probe fails and the IP detection also fails
- **THEN** the warning is still printed with a placeholder CIDR and the command exits 0

### Requirement: Probe is skipped during status

The TCP probe SHALL only run on the interactive `start` success path. It SHALL NOT run during `status`, `env`, `stop`, or any other subcommand.

#### Scenario: Status does not probe

- **WHEN** the user runs `outfit remote status`
- **THEN** no TCP probe is performed and no reachability warning is printed
