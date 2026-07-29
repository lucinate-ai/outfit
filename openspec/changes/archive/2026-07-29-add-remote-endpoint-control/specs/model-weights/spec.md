## ADDED Requirements

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

Presence SHALL be judged by a marker written only once a fetch has completed,
so a fetch that failed or is still running is not mistaken for weights that are
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

#### Scenario: A half-finished fetch is not mistaken for success

- **WHEN** an earlier fetch failed part way, leaving some files behind
- **THEN** the weights are treated as absent and fetched again

#### Scenario: A deployment that could never serve is refused

- **WHEN** the weights are absent and a fetch cannot be started
- **THEN** the deployment is rejected and the stored configuration is unchanged
