# Cloud Auth Specification

## Purpose

Define how `spinloop cloud auth` stores a long-lived control-plane AWS credential in the OS keystore, how that credential resolves in preference to other sources, and how it is reported, rotated, and cleared.

## Requirements

### Requirement: Storing a control-plane credential

`spinloop cloud auth --store` SHALL create an AWS access key for the control-plane IAM user created by the control-plane stack and store it in the OS keystore (Keychain, Credential Manager, or Secret Service), keyed by the target region. The stored entry SHALL record the access key id, the secret, the AWS account, the user name, the region, and when it was stored. Before storing, the command SHALL verify that the new key resolves to the same AWS account as the caller's credentials, and SHALL NOT store a key that resolves to a different account.

When a credential is already stored for the region, `--store` SHALL rotate rather than create a second entry: it SHALL create the replacement key with the stored credential itself, so that no administrator or other ambient credential is required, verify it, replace the stored entry, and delete the superseded access key on the AWS side.

When the control-plane IAM user does not exist — a control plane deployed before this capability — the command SHALL fail, naming `spinloop cloud bootstrap` as the step to re-run first.

The secret SHALL never be printed in any output. Where no OS keystore is available on the machine, the entry MAY instead be stored in an owner-only file under the user's spinloop config directory. Setting `SPINLOOP_CLOUD_KEYSTORE` to `file` SHALL select that file store even where a keystore is reachable — a machine whose keystore is locked or unreachable, such as a headless session with no keychain access — and the command SHALL say which store it used in every case.

#### Scenario: First store

- **WHEN** the user runs `spinloop cloud auth --store` in an account with a bootstrapped control plane and no stored credential for the region
- **THEN** an access key is created for the control-plane user and stored in the OS keystore for that region, and the confirmation names the account, region, and access key id without printing the secret

#### Scenario: A key that resolves elsewhere is not stored

- **WHEN** the newly created key resolves to a different AWS account than the caller's credentials
- **THEN** the key is not stored and the command fails saying so

#### Scenario: Rotation needs no administrator credential

- **WHEN** a credential is already stored for the region, no other AWS credential is configured, and the user runs `spinloop cloud auth --store`
- **THEN** the stored credential is used to create the replacement key, the stored entry is swapped to the new key, and the superseded access key is deleted on the AWS side

#### Scenario: A control plane without the user

- **WHEN** the user runs `spinloop cloud auth --store` against a control plane deployed before the control-plane user existed
- **THEN** the command fails, naming `spinloop cloud bootstrap` as the step to re-run first

#### Scenario: No keystore on the machine

- **WHEN** the user runs `spinloop cloud auth --store` on a machine with no OS keystore available
- **THEN** the entry is stored in an owner-only file under the user's spinloop config directory instead, and the report says where

#### Scenario: The file store is forced

- **WHEN** `SPINLOOP_CLOUD_KEYSTORE` is set to `file` and the user runs `spinloop cloud auth --store` on a machine with a reachable keystore
- **THEN** the entry is stored in the owner-only file under the user's spinloop config directory, not the keystore, and the report says which store was used

### Requirement: Stored credentials resolve for control calls

For every signed control-plane request made for a target region — each `spinloop cloud` subcommand, and the fleet's operations on cloud environments (the fleet dashboard and `fleet start`, `stop`, `status`, and `keep`), which sign through the same client — a stored credential for that region SHALL be used when no explicit AWS environment credentials and no explicit profile selection are present, and SHALL take precedence over the remaining standard credential sources — shared config files, SSO sessions, and instance metadata. Explicit AWS environment credentials (access key id, secret, and session token set in the process environment) or an explicit profile selection SHALL override the stored credential. When no credential is stored for the region, resolution SHALL fall back to the standard credential chain as before this capability.

`spinloop cloud bootstrap` and `spinloop cloud bake` SHALL NOT consult a stored credential: they provision the control plane itself and SHALL resolve from ambient sources only.

#### Scenario: The stored key signs between log-ins

- **WHEN** a credential is stored for the region, no AWS environment credential or profile is set, and the ambient SSO session is absent or expired
- **THEN** `spinloop cloud status` signs with the stored credential and succeeds

#### Scenario: A fleet operation signs with the stored key

- **WHEN** a credential is stored for the region, no other AWS credential is resolvable, and a fleet operation on that region's environment (a dashboard refresh, `fleet status`, `start`, or `stop`) issues its signed control call
- **THEN** the call is signed with the stored credential and the operation succeeds, exactly as the equivalent `spinloop cloud` subcommand does

#### Scenario: Explicit environment credentials win

- **WHEN** an AWS access key id and secret are set in the process environment and a credential is also stored for the region
- **THEN** the command signs with the environment credentials, not the stored one

#### Scenario: A named profile wins

- **WHEN** an explicit profile is selected and a credential is stored for the region
- **THEN** the command signs with the profile's credentials, not the stored one

#### Scenario: No stored key falls back as before

- **WHEN** no credential is stored for the region
- **THEN** credential resolution behaves exactly as it did before this capability

#### Scenario: Bootstrap ignores the stored key

- **WHEN** a credential is stored for the region and the user runs `spinloop cloud bootstrap`
- **THEN** bootstrap resolves its credentials from ambient sources only, not from the stored credential

### Requirement: Reporting and clearing stored credentials

`spinloop cloud auth` with no flag SHALL report every stored credential — the account, region, user name, access key id, and when it was stored — without contacting AWS and without printing the secret. When nothing is stored, it SHALL say so and name `--store` as the way to store one.

`spinloop cloud auth --clear` SHALL remove the stored credential for the target region and SHALL additionally delete the access key on the AWS side, using the stored credential, so that a cleared key does not linger in the account. If the AWS-side deletion cannot be made, the local entry SHALL still be removed and the failure reported, with a note that the key may still exist on the AWS side.

#### Scenario: Status reports what is stored

- **WHEN** a credential is stored and the user runs `spinloop cloud auth`
- **THEN** the output lists the account, region, user name, access key id, and when it was stored, no AWS call is made, and the secret is not printed

#### Scenario: Status with nothing stored

- **WHEN** no credential is stored and the user runs `spinloop cloud auth`
- **THEN** the output says none is stored and names `--store` as the way to store one

#### Scenario: Clear removes both sides

- **WHEN** the user runs `spinloop cloud auth --clear` and a credential is stored
- **THEN** the stored entry is removed and the access key is deleted on the AWS side

#### Scenario: Clear still removes locally when the AWS deletion fails

- **WHEN** the user runs `spinloop cloud auth --clear` and the AWS-side deletion fails
- **THEN** the local entry is still removed, and the command reports that the key may still exist on the AWS side
