## ADDED Requirements

### Requirement: Fleet dashboard

`outfit fleet dashboard` SHALL open an interactive, full-screen view of the fleet:
one panel per node in the fleet file, arranged in a grid and refreshed
continuously without operator input. It is the one place where what the fleet is
doing and acting on it meet: unlike `fleet metrics --watch`, it takes keyboard
input and drives the node the operator has selected.

The fleet file SHALL resolve as it does for the rest of the fleet commands. The
dashboard SHALL be openable and usable from cold — a fleet where nothing is up:
every panel SHALL show its node's outcome and reason rather than metrics, and a
node SHALL be startable from within the dashboard.

A problem with the fleet file itself (missing, unparseable) SHALL fail the
command before the view opens, as it does for the other fleet commands; a problem
with any node SHALL NOT.

#### Scenario: A mixed fleet renders every node

- **WHEN** the operator opens the dashboard against a fleet with some nodes
  answering and some not
- **THEN** every node has a panel: answering nodes show their metrics, the others
  show their outcome and reason, and the view keeps running

#### Scenario: Opening on a fleet where nothing is up

- **WHEN** the operator opens the dashboard and no node is reachable
- **THEN** every panel shows its node's outcome and reason
- **AND** the dashboard does not exit, and starting a node from it works

#### Scenario: A fleet-file problem fails before the view

- **WHEN** the named fleet file is missing or unparseable
- **THEN** the command fails naming the problem, and no interactive view opens

#### Scenario: A non-interactive context is refused

- **WHEN** the dashboard is run with its input or output not on a terminal, such
  as through a pipe or in the background
- **THEN** it fails with a message pointing at `fleet metrics --watch`, and it
  does not enter raw terminal mode or emit screen escapes

### Requirement: Dashboard panels show the node's metrics

Each panel SHALL show, for a node that answered the last completed refresh, the
same facts the bar format of `fleet metrics` renders for that node: its state,
what it serves (runner and model when known), how long since it last did work
(with the same labelling rules as the rest of the fleet surfaces), its resource
usage, and its token and request counters. A panel SHALL show the answer of the
last completed refresh for that node — not a mix of refreshes and not a stale
bar with a fresh outcome.

A panel SHALL degrade gracefully when a node answers with fewer facts (no system
stats, no GPUs, an engine that is not running) rather than failing to render.
A node whose answer is a failure — unreachable, unauthorised, a configuration
error — SHALL show its typed outcome and reason in its panel instead of metrics.

One node SHALL be selected at a time, following the fleet file's order, and the
selected panel SHALL be distinguishable at a glance from the others.

#### Scenario: A running node's panel updates

- **WHEN** a node's engine is running and its counters or utilisation change
- **THEN** the node's panel shows the new figures on a subsequent refresh,
  without the operator pressing any key

#### Scenario: A node that reports fewer facts still renders

- **WHEN** a node's answer carries no GPU or system statistics
- **THEN** its panel shows the facts it has, with the missing ones absent rather
  than an error

#### Scenario: A failing node's panel shows why

- **WHEN** a node cannot be reached, or its daemon rejects the client
- **THEN** its panel shows the node's outcome and reason, and the other panels
  are unaffected

#### Scenario: Selection moves and is visible

- **WHEN** the operator moves the selection with the navigation keys
- **THEN** the selection moves in the fleet file's order and the selected panel
  is visibly distinct from the others

### Requirement: The dashboard drives the selected node

The dashboard SHALL let the operator start and stop the engine of the node
currently selected, from the keyboard, through the same node operations the one-
shot `fleet start` and `fleet stop` commands use. An action SHALL reach the
node under the cursor and no other. An action is one per node, not one per
board: while one node is starting, the operator selects another and starts it,
and the wakes run side by side. A node that already has an action in flight
SHALL take no further start or stop; the dashboard says it is still working
and drives nothing.

Start SHALL proceed without confirmation. Stop SHALL require an explicit
confirmation before it is sent, because it ends an engine that may be serving
work: a declined or abandoned confirmation SHALL send nothing.

While an action is in flight, the node's own tile SHALL carry it: the verb
and the action's status lines as the call reports them, in place of the node's
last report — a boot's progress is the node's truth until the report returns.
A node whose call reports nothing SHALL show the verb alone. Each finished
action SHALL clear its node's tile back to the node's next report and leave
its outcome on the status line, the one-shot wording.

The outcome of an action SHALL be shown inside the dashboard (a status line the
operator can read before the next refresh replaces attention), and a refused or
unreachable action SHALL NOT close the dashboard. What an action changes in state
— an engine coming up after a start, coming down after a stop — SHALL appear
through the normal refresh, without the operator asking for it.

#### Scenario: Starting a cold node

- **WHEN** the operator selects a node with no engine running and issues the
  start
- **THEN** the start is sent without a prompt, its outcome is shown in the
  status line, and the panel shows the node's new state as the refreshes come
  around

#### Scenario: Stopping asks first

- **WHEN** the operator issues the stop on the selected node
- **THEN** the dashboard asks for confirmation and nothing is sent
- **WHEN** the operator declines
- **THEN** the stop is not sent and the node's state is unchanged

#### Scenario: A confirmed stop is sent

- **WHEN** the operator issues the stop and confirms
- **THEN** the stop is sent to the selected node only, its outcome is shown in
  the status line, and the panel follows the node's state on subsequent refreshes

#### Scenario: A start is watched on its own tile

- **WHEN** the operator starts a node whose start reports progress as it works
- **THEN** the node's tile shows the verb and the start's status lines while
  the start is in flight
- **AND** when the start finishes, the tile returns to the node's report and
  the outcome is on the status line

#### Scenario: Two nodes wake at once

- **WHEN** the operator starts one node, selects another, and starts it
- **THEN** both wakes run at the same time, each reported on its own tile
- **AND** each finishing action clears its own node and leaves its outcome on
  the status line without disturbing the other

#### Scenario: A node still starting is not started again

- **WHEN** the operator presses start on a node whose start is in flight
- **THEN** the dashboard drives nothing and says the node is still starting

#### Scenario: An action that fails keeps the dashboard open

- **WHEN** an action is sent to a node that cannot be reached, or the node
  refuses it
- **THEN** the failure is shown in the status line with the daemon's own reason
  and the dashboard keeps running with its refreshes

### Requirement: The fleet refreshes without stalling

The dashboard SHALL refresh the fleet continuously, and SHALL also refresh
immediately on the operator's request. The cadence SHALL be by node kind: a
local daemon machine SHALL refresh on a short interval — seconds, not the
watch mode's minute — and a `kind: remote` environment SHALL refresh on a
much slower cadence, a 60-second interval, one status call a minute, because
its status is a signed call through the cloud control plane rather than a
local socket, and its state changes on the scale of minutes. A manual refresh
SHALL read every node, whether or not its kind's cadence was due. A refresh of a group SHALL read that group's nodes
concurrently, as fan-out does elsewhere.

One node that is slow or unanswerable SHALL NOT hold the rest of the fleet
hostage to it: a refresh SHALL give up on a node that has not answered within
the budget of the node's group, show that node with its outcome for this
round, and the other nodes SHALL keep their cadence. The dashboard SHALL NOT
start a second read of a group while that group's previous one has not
finished; the two groups' reads MAY run at the same time, and a slow cloud
round SHALL NOT stretch the local machines' cadence.

#### Scenario: Continuous refresh without input

- **WHEN** the dashboard is open and no key is pressed
- **THEN** the local panels keep updating on the short interval, and each
  cloud panel is re-read once a minute, on the 60-second cadence

#### Scenario: An immediate refresh

- **WHEN** the operator presses the refresh key
- **THEN** every node is re-read, local and cloud alike, rather than waiting
  for each kind's next cadence

#### Scenario: One hung node does not stall the board

- **WHEN** one node does not answer within its group's budget while the others
  do
- **THEN** that node's panel shows its outcome, the other panels refreshed on
  their normal cadence, and no second read of its group started over the
  in-flight one

#### Scenario: A slow cloud round keeps the local cadence

- **WHEN** a cloud environment is slow to answer while its group's read runs
- **THEN** the local machines keep refreshing on the short interval, and the
  cloud panel shows its outcome on the cloud cadence

### Requirement: The dashboard exits cleanly

The dashboard SHALL quit on its quit key and on interrupt. On every exit path it
SHALL restore the terminal — leaving the alternate screen, showing the cursor
again, and restoring terminal modes — so the operator's shell is immediately
usable, and it SHALL leave no trace in the terminal's scrollback.

#### Scenario: Quitting returns to the shell

- **WHEN** the operator presses the quit key, or interrupts
- **THEN** the dashboard exits without an error and the terminal is in the state
  it was before the dashboard opened, with the shell prompt ready
