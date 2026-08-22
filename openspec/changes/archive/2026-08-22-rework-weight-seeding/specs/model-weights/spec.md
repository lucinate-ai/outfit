## MODIFIED Requirements

### Requirement: Fetching weights that are absent

Deploying a model whose weights are not in storage SHALL fetch them, without
anyone staging them by hand. The fetch SHALL run within the deployment rather
than from the caller's machine, and SHALL dispose of whatever ran it once the
weights are stored.

Presence SHALL be judged by a manifest object that the fetch writes as its
final step, once every file it set out to transfer is stored. Presence SHALL
NOT be judged by the presence of a weights file, whose write order is a
property of the transfer rather than a guarantee, so a fetch that failed or is
still running is not mistaken for weights that are ready.

The manifest SHALL record what was fetched — the model, the exact revision, the
file list with each file's size and checksum — and what fetched it, so the
contents of a location are identifiable rather than inferred. A request MAY pin
the revision to fetch; absent a pin, the revision the source currently resolves
to SHALL be used, and either way the resolved revision SHALL be recorded.

A deployment SHALL NOT be stored if its weights are absent and a fetch cannot
be started, so a configuration that could never serve does not replace one that
can. The reply SHALL identify the fetch it started, by a handle the operator can
use to follow that fetch's progress and outcome, because an instance started
before it finishes would copy an incomplete model.

#### Scenario: Weights already present

- **WHEN** a deployment names a model whose weights are stored
- **THEN** nothing is fetched and the deployment is stored

#### Scenario: Weights absent

- **WHEN** a deployment names a model whose weights are absent
- **THEN** a fetch is started, and the reply identifies it well enough to follow
  its progress

#### Scenario: A companion added to stored weights is fetched

- **WHEN** a deployment names a companion, and its main weights are already
  stored from an earlier deployment that named no companion
- **THEN** the weights are treated as absent and a fetch is started, rather
  than the stored weights being reused without the companion

#### Scenario: A half-finished fetch is not mistaken for success

- **WHEN** an earlier fetch failed part way, leaving some files behind
- **THEN** the weights are treated as absent and fetched again

#### Scenario: Weights files present without a manifest are not complete

- **WHEN** every weights file happens to be stored but no manifest was written
- **THEN** the weights are treated as absent

#### Scenario: What is stored is identifiable

- **WHEN** weights have been fetched into a location
- **THEN** the exact revision they came from can be read back from that location

#### Scenario: A revision may be pinned

- **WHEN** a fetch names the revision to take
- **THEN** that revision is fetched and recorded, rather than whatever the source
  currently resolves to

#### Scenario: A deployment that could never serve is refused

- **WHEN** the weights are absent and a fetch cannot be started
- **THEN** the deployment is rejected and the stored configuration is unchanged
