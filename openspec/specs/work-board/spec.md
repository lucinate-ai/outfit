# work-board Specification

## Purpose
Watches a running orchestrator's work list as a full-screen kanban board —
one column per state, a card per item, kept current as the run works — and
lets the operator add, abort, remove and read items, all through the same
work list API the one-shot work commands use.

## Requirements

### Requirement: The board is a full-screen view of the run's work

`spinloop work board` SHALL open an interactive, full-screen view of the
work list the named API holds. Everything the board draws and does — every
column, card, action and refusal — SHALL come from the work list API: the
board SHALL NOT read or write the items file, the state, or the logs
directly, and SHALL NOT take any lock beside them. With no terminal to draw
on it SHALL refuse before drawing anything, naming `spinloop work list` as
the command that carries the same information into a pipe.

#### Scenario: The board opens on the run

- **WHEN** the operator opens the board naming a running orchestrator's
  work list API
- **THEN** the screen shows the run's whole work: every item the list
  carries, as it stands at that moment

#### Scenario: A cold run is openable

- **WHEN** the operator opens the board against a run whose items file is
  empty
- **THEN** the board opens with its empty columns drawn and stays usable,
  rather than faulting

#### Scenario: A piped run is refused

- **WHEN** the operator runs `work board` with its output piped
- **THEN** it fails before drawing anything, naming `spinloop work list` as
  the pipe's command

### Requirement: Columns are states and cards are items

The board SHALL draw exactly four columns, standing in this order —
Backlog, Running, Done, Failed — and every item the list carries SHALL
appear as a card in exactly the one column its state names; an item the run
records not at all is backlog. Within a column the cards SHALL stand in the
order the API listed them, and each column heading SHALL name itself and
count its cards. A column fuller than the screen is tall SHALL show the
selected card's neighbourhood rather than drop cards silently.

#### Scenario: Every item stands in its state's column

- **WHEN** the run holds items recorded running, done and failed beside
  ones with no record
- **THEN** the first stands under Running, the rest under Done, Failed and
  Backlog, and each heading shows its count

#### Scenario: A card moves as the run works

- **WHEN** the board is open and a running item finishes between two
  refreshes
- **THEN** its card stands under Done, with no key pressed

#### Scenario: More cards than the screen is tall

- **WHEN** a column holds more items than fit the screen
- **THEN** the cards around the selected one are shown, and moving the
  selection brings the others into view

### Requirement: The card tells the item's situation

Each card SHALL show its item's id and its instructions, clipped to the
card's width rather than wrapped, and its priority where the item has one.
A running card SHALL also show the node it runs on and the time since it
started, counted up as it is watched. A card's state SHALL be told by the
colour that reports it — the amber of running, the green of done, the red
of failed — as the work list colours the same state, and never by the
brand accent.

#### Scenario: Long instructions are clipped, not wrapped

- **WHEN** an item's instructions are longer than its card is wide
- **THEN** the card shows their beginning, clipped, and the card keeps its
  shape

#### Scenario: A running card counts up

- **WHEN** the operator watches a running card without pressing anything
- **THEN** the time since that item started grows, and the board keeps
  drawing

#### Scenario: States keep the tool's state colours

- **WHEN** the board draws running, done and failed cards on a terminal
  that takes colour
- **THEN** each carries the colour the work list gives that state, and no
  card is coloured with the accent for its state

### Requirement: The selection and the item's detail

The operator SHALL move the selection between cards with the arrow keys —
between columns sideways, within a column up and down — and the board SHALL
mark the selected card with the brand accent alone. Enter SHALL open that
item's detail: its instructions — wrapped to the frame's width rather than
clipped — its directory, its tags, every time its record carries, and,
where it failed, the reason, likewise wrapped rather than cut at the
edge. The detail SHALL keep its frame inside the terminal: where a record
is taller than the room the frame has, the fields show what fits and a
note counts the rows left behind, and the keys offered there stay
reachable on screen. While the detail
stands open the command SHALL keep asking the API for the item's kept
output and show whatever arrives, an item with no output yet shown as empty
rather than as a fault; once the item has ended, or dropped out of the
list, the tailing SHALL stop. Escape SHALL return to the board; while a
detail stands open the board SHALL not be quit from, and the keys offered
there are only those that do something there.

#### Scenario: The detail shows the whole item

- **WHEN** the operator opens the detail of a failed item with long
  instructions
- **THEN** the full instructions, its directory, tags, timings and failure
  reason are all shown, each wrapped to the frame where it is wider than
  the frame is wide

#### Scenario: A record taller than the terminal is truncated honestly

- **WHEN** the operator opens the detail of an item whose instructions fill
  more rows than the terminal has
- **THEN** the frame closes inside the terminal with its keys on screen,
  what does not fit is replaced by a note counting its rows, and nothing
  falls past the bottom edge unseen

#### Scenario: The open detail tails the kept output

- **WHEN** the detail of a running item stands open and its agent writes
  more output
- **THEN** the detail shows the new output as the polls see it

#### Scenario: An item with no kept output is empty, not a fault

- **WHEN** the operator opens the detail of an item the run has kept no
  output for
- **THEN** its log pane shows empty and the board goes on drawing

#### Scenario: The way back is Escape

- **WHEN** a detail stands open and the operator presses escape
- **THEN** the board returns, the selection on the card it was on, and
  pressing quit there quits the board

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

### Requirement: The board adds an item

The operator SHALL be able to add an item to the run's work list without
leaving the board: a key SHALL open a form over the board showing the
item's five fields — its id, instructions, working directory, tags, and
priority — marking the three the item cannot do without, and keeping the
field that takes keystrokes visibly the selected one. The form SHALL not
pause the board behind it: reads keep their cadence, and the selection
keeps its place until the form closes. Sending belongs to the work list
API — the same add path the one-shot `work add` sends — and the API SHALL
be the only judge of an item's shape: a refusal SHALL keep the form open,
carry the refusal into it worded the way the API states it, and leave the
operator to correct a field and send again. The one field the form SHALL
guard itself is the priority, which stands only as a number or as nothing.
Where the API accepts, the form SHALL close, the status line shall say the
item is added, and the board SHALL ask the list again at once so the new
card stands under Backlog. Escape SHALL send nothing: an empty form closes
and says nothing was added, and a form holding anything SHALL ask once
before discarding it, defaulting to keep.

#### Scenario: An item is added end to end

- **WHEN** the operator opens the form, fills its fields, and sends what
  the API accepts
- **THEN** the form closes, the status line says the item is added, and
  once the board has read again the new card stands under Backlog

#### Scenario: A refusal is corrected in the form

- **WHEN** the operator sends an id the list already carries
- **THEN** the form stays open carrying the refusal as the API states it,
  and a second send after correcting the id closes it as added

#### Scenario: The board lives behind the form

- **WHEN** the form stands open and a running item ends between two reads
- **THEN** the board behind keeps drawing, and the card stands under Done
  where the board was left when the form closes

#### Scenario: An empty form closes on escape

- **WHEN** the operator presses escape with nothing entered anywhere
- **THEN** the form closes, nothing is sent, and the board says nothing
  was added

#### Scenario: Typed text is discarded deliberately

- **WHEN** the operator presses escape with text entered, and declines the
  question that follows
- **THEN** the form stands where it was with its text, and nothing has
  been sent

#### Scenario: A non-numeric priority does not stand

- **WHEN** the operator types text that is not a number into the priority
  field
- **THEN** the keystroke does not enter the field, and no API call is
  made about it

### Requirement: The board keeps the run's company

While the board stands open it SHALL re-read the work list at a fixed
cadence, and `r` SHALL ask for a read at once. Where an API call fails,
the board SHALL NOT exit: it shall keep drawing its last reading, marked
with its age and no longer presented as the present state of anything, and
keep asking until the API answers again. The operator's interrupt, or quit,
SHALL end the board cleanly, restoring the terminal.

#### Scenario: A dropped API does not drop the board

- **WHEN** the orchestrator stops answering while the board stands open
- **THEN** the board keeps drawing its last reading, shown with its age,
  and recovers without a key press once the API answers again

#### Scenario: Refresh now

- **WHEN** the operator presses `r`
- **THEN** the board asks the API again at once, without waiting for the
  next tick

#### Scenario: Quit restores the terminal

- **WHEN** the operator quits the board
- **THEN** the command ends cleanly and the terminal is the terminal that
  was there before
