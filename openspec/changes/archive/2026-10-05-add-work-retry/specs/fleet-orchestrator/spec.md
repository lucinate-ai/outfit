## ADDED Requirements

### Requirement: Retrying a failed item through the work list API

The work list API SHALL serve `POST /v1/items/{id}/retry`, which puts a
failed item back in the backlog: the item's record is removed from the run's
view and the state, so the item is backlog, and the run's loop admits it on
a later pass the way it admits any backlog item. Only a failed item SHALL be
retried: an item recorded done, running, or with no record SHALL be refused
with a conflict naming the item and its state, and an id the items file does
not carry SHALL be refused as not found, naming it. The item's fields in the
items file SHALL NOT change. The pass that handles the request SHALL NOT
admit the item before the record is gone, and a retried item SHALL be
subject to the same matching and limits as any backlog item.

#### Scenario: A failed item returns to the backlog

- **WHEN** a request retries an item the run records failed
- **THEN** the item shows backlog to a request that reads the list, and the
  loop's next pass may admit it

#### Scenario: An item that has not failed is refused

- **WHEN** a request retries an item that is running, done, or backlog
- **THEN** the API answers a conflict naming the item and its state, and
  the run's view is unchanged

#### Scenario: An unknown id is refused

- **WHEN** a request retries an id the items file does not carry
- **THEN** the API answers not found, naming the id

#### Scenario: The items file is untouched

- **WHEN** a failed item is retried
- **THEN** the items file is byte-for-byte what it was before
