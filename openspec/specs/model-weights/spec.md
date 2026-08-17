# Model Weights Specification

## Purpose

Define where a model's weights live, how their location is derived, and
how they come to be there.
## Requirements
### Requirement: Weights live outside the machine image

A model's weights SHALL be held in object storage rather than baked into the
machine image, so that changing model requires no new image and an image stays
useful for any model. An instance SHALL copy them onto its own disk when it
starts, and SHALL therefore depend on no third party at start time.

Because they are copied at start, the storage SHALL be in the same region as
the instance, so a start transfers nothing billable.

#### Scenario: Changing model does not rebuild the image

- **WHEN** a deployment names a model the current image was never built for
- **THEN** the instance serves it, having copied the weights at start

### Requirement: Where weights live is derived, never supplied

The location of a model's weights SHALL be derived from the engine, the model
and the quantisation, so the same model always resolves to the same place and
two engines' copies of one model never collide. A location supplied by a
caller SHALL be ignored: whoever deploys states what to serve, and the
deployment decides where that is kept.

#### Scenario: The same model resolves to the same place

- **WHEN** the same engine, model and quantisation are deployed twice
- **THEN** both resolve to the same location

#### Scenario: Two engines do not collide

- **WHEN** the same model is deployed for each of two engines
- **THEN** each has its own location

#### Scenario: A supplied location is ignored

- **WHEN** a deployment request names a storage location
- **THEN** it has no effect, and the derived location is used

### Requirement: Fetching weights that are absent

Deploying a model whose weights are not in storage SHALL fetch them, without
anyone staging them by hand. The fetch SHALL run within the deployment rather
than from the caller's machine, and SHALL dispose of whatever ran it once the
weights are stored.

Presence SHALL be judged over the whole set the deployment expects — its main
weights and every companion it names — so a deployment is treated as ready only
when everything it needs is stored. Adding a companion to a deployment whose
main weights are already stored SHALL therefore be judged absent and fetched,
rather than mistaken for a deployment that is ready.

Presence SHALL be judged by markers written only once a fetch has completed, so
a fetch that failed or is still running is not mistaken for weights that are
ready.

A deployment SHALL NOT be stored if its weights are absent and a fetch cannot
be started, so a configuration that could never serve does not replace one that
can. The reply SHALL say whether a fetch was started, because an instance
started before it finishes would copy an incomplete model.

#### Scenario: Weights already present

- **WHEN** a deployment names a model whose weights are stored
- **THEN** nothing is fetched and the deployment is stored

#### Scenario: Weights absent

- **WHEN** a deployment names a model whose weights are absent
- **THEN** a fetch is started, and the reply says so

#### Scenario: A companion added to stored weights is fetched

- **WHEN** a deployment names a companion, and its main weights are already
  stored from an earlier deployment that named no companion
- **THEN** the weights are treated as absent and a fetch is started, rather
  than the stored weights being reused without the companion

#### Scenario: A half-finished fetch is not mistaken for success

- **WHEN** an earlier fetch failed part way, leaving some files behind
- **THEN** the weights are treated as absent and fetched again

#### Scenario: A deployment that could never serve is refused

- **WHEN** a deployment's weights are absent and no fetch can be started
- **THEN** the deployment is refused and the stored configuration is left
  unchanged

### Requirement: Companion weights beside the main weights

A deployment MAY name **companion weights**: further files published beside its
main weights that the engine loads in addition to them. Each companion SHALL be
named by a role, and a role SHALL determine what the engine does with the file
rather than the filename doing so. The supported roles SHALL be a fixed,
validated set; an unrecognised role SHALL be rejected when the configuration is
accepted, not discovered at start time.

Companions SHALL be optional. A deployment naming none SHALL behave exactly as
one made before companions existed, and SHALL NOT be re-fetched or re-stored on
that account.

Companions SHALL come from the same source as the main weights, so naming one
adds no new credential, host or trust boundary.

A companion SHALL be stored under the same derived location as the main
weights, and SHALL be given a fixed name determined by its role, so that the
engine's command can name it without discovering what the source happened to
call it.

#### Scenario: A deployment names a drafter

- **WHEN** a deployment names a companion in the drafter role
- **THEN** that file is fetched alongside the main weights and stored beside
  them under a fixed name for that role

#### Scenario: A deployment names no companions

- **WHEN** a deployment names no companions
- **THEN** exactly the main weights are fetched and stored, as before

#### Scenario: An unrecognised role is refused

- **WHEN** a deployment names a companion whose role is not one of the
  supported roles
- **THEN** the configuration is rejected with an error naming the role, and
  nothing is fetched or stored

#### Scenario: A companion is not confused with the main weights

- **WHEN** the source publishes several files and one of them is named as a
  companion
- **THEN** the main weights and the companion are each stored under their own
  fixed name, and neither is served in place of the other

### Requirement: Requesting a re-fetch of stored weights

A deployment request MAY ask for its weights to be fetched even when they are
already stored. When it does, the fetch SHALL be started regardless of what is
in storage, and the reply SHALL report it exactly as it reports a fetch started
because the weights were absent — the caller learns a fetch is running the same
way either way.

A request that does not ask for this SHALL behave as before: the fetch happens
only when the weights are judged absent.

A requested re-fetch SHALL go through the same mechanism as any other fetch, so
it inherits the same guarantees — the same completion markers, the same
protection against a retried launch starting a second fetch, and the same
disposal of whatever ran it.

A re-fetch SHALL overwrite the stored weights in place rather than being kept
somewhere new, so a deployment's weights stay at their derived location and no
stale copy is orphaned.

#### Scenario: A re-fetch is requested for weights already stored

- **WHEN** a deployment asks for a re-fetch and its weights are already stored
- **THEN** a fetch is started, and the reply says so

#### Scenario: Without the request, stored weights are left alone

- **WHEN** a deployment does not ask for a re-fetch and its weights are stored
- **THEN** nothing is fetched and the deployment is stored

#### Scenario: A re-fetch is requested for weights that are absent

- **WHEN** a deployment asks for a re-fetch and its weights are absent
- **THEN** exactly one fetch is started, not two

#### Scenario: A re-fetch that cannot be started refuses the deployment

- **WHEN** a re-fetch is requested and the fetch cannot be started
- **THEN** the deployment is refused and the stored configuration is left
  unchanged

