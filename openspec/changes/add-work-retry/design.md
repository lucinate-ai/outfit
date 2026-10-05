## Context

A finished item's record (`done` or `failed`) sits in the state file beside
the items file. The loop's pass skips an item with such a record
(`WorkList.pass`), and `Add` refuses its id. `Abort` already puts a running
item back in the backlog by deleting its record, under the work list's lock,
and saving. See proposal.md for the motivation.

## Goals / Non-Goals

**Goals:**
- Retry reuses the record-removal route that abort already takes, so the
  loop needs no new state.
- The command and the board are thin clients of one API path.

**Non-Goals:**
- Retrying a `done` item. The issue asks for failed items only.
- Retry counts, back-off, or automatic retry.
- Clearing the failed attempt's kept output on retry.

## Decisions

**A new `WorkList.Retry(id)` removes the record and saves.** It holds the
lock, refuses with `errMissing` where the file does not carry the id and
with `errConflict` where the record's state is not `failed` (naming the item
and its state, backlog where there is no record), then deletes the record
and calls `saveLocked`. Alternative: a `state` field on the request that
sets the record to backlog. Rejected: a backlog item has no record
elsewhere, so a stored backlog record would be a second shape the loop and
`Join` would have to handle.

**Route: `POST /v1/items/{id}/retry`, beside `abort`.** `itemPath` already
splits an action segment, so it is one new case in `ServeHTTP` and a new
entry in `pathsServed`. The handler is the same shape as `handleAbort`.
Alternative: `PATCH` the item. Rejected: nothing else in the API edits an
item in place.

**The kept log stays until the next launch.** Remove deletes the log
because the item is gone. Retry keeps the item, so the old output stays
readable on the detail view and `work logs` until the agent for the new
attempt starts and replaces it. Dropping it at retry would lose the failure
output at the moment someone may still want it.

**No refusal for the pass in progress.** The failed record is only ever
held by the work list under its lock, and a failed item has no child in
flight, so no stopping-set guard like abort's is needed. Retry takes effect
for the next pass.

**CLI:** `work retry <id>` mirrors `workAbortCmd`: `cobra.ExactArgs(1)`,
`workTarget`, `workRequest` with `POST /v1/items/{id}/retry`, the same item
id completion slot, and the message `item %q is back in the backlog`.

**Board:** a `workRetry` verb beside `workAbort`/`workRemove`, bound to the
`t` key (`r` is refresh). As with abort, there is no client-side state guard:
the API's refusal is shown on the status line. The key hint adds `t retry`
on a failed card. A failed card keeps `x remove`, so the hint code shows
both there.

## Risks / Trade-offs

- A retry of an item that failed at launch (for example no matching node)
  will fail again → the failure reason is shown on the card as it is today.
- Two operators retrying at once → the second is refused as not failed,
  naming the state it now has.
