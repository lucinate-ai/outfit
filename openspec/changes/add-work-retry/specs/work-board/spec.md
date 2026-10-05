## MODIFIED Requirements

### Requirement: The board acts through the work list API

The board SHALL offer the work list API's actions on the selected
item: a key that aborts a running item, a key that retries a failed one, and a
key that removes one that is not running. A removal and a retry SHALL each ask on screen for confirmation before
they are sent, defaulting to not proceeding, a declined or abandoned question
sending nothing and saying so. An accepted action SHALL be shown underway
on the status line until the API answers, and the answered action's effect
— the card moving, the card leaving — SHALL follow on the next read that
sees it. Where the API refuses — aborting what is not running, retrying what has not failed, removing a
running item, an id the list no longer carries — the board SHALL show the
refusal on its status line, worded the way the API states it, and keep
drawing. While an action is underway the board SHALL keep a spinner moving,
and the keys it names on screen SHALL be only those that would do
something to what is selected: abort on a running card, retry on a failed
one, and remove on any card that is not running.

#### Scenario: A running card is aborted

- **WHEN** the operator aborts a running item's card and the API stops it
- **THEN** the status line says it is stopped and the card stands under
  Backlog once the next read sees it

#### Scenario: A failed card is retried

- **WHEN** the operator retries a failed item's card, confirms, and the API
  accepts
- **THEN** the status line says it is back in the backlog and the card
  stands under Backlog once the next read sees it

#### Scenario: A retry asks first

- **WHEN** the operator asks to retry a failed card
- **THEN** the board asks, and nothing is sent until yes is given

#### Scenario: A declined retry sends nothing

- **WHEN** the operator declines or abandons the retry question
- **THEN** nothing is sent, the board says so, and the card remains under
  Failed

#### Scenario: A removal asks first

- **WHEN** the operator asks to remove a backlog card
- **THEN** the board asks, and nothing is sent until yes is given

#### Scenario: A declined removal sends nothing

- **WHEN** the operator declines or abandons the removal question
- **THEN** nothing is sent, the board says so, and the card remains

#### Scenario: A refusal keeps the board

- **WHEN** an action the API refuses is attempted — a running item
  removed, a backlog item aborted, a done item retried — or the API cannot be reached for it
- **THEN** the status line carries the refusal worded as the API states it,
  or the fault, and the board keeps drawing

