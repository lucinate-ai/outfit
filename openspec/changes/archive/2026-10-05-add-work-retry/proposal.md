## Why

A failed item stays failed: the run records it `failed` and never admits it
again, and `work add` refuses its id. The only ways to work it again are to
remove it and add it back with every field retyped, or to edit the state file
by hand. Issue 250 asks for a retry that puts a failed item back in the
backlog, from the shell and from the work board.

## What Changes

- The work list API gains `POST /v1/items/{id}/retry`: it clears a failed
  item's record so the item is backlog again and the run admits it on a later
  pass. An item that is not failed is refused (`409`, naming the item and its
  state) and an id the file does not carry is refused (`404`).
- New `spinloop work retry <id>` subcommand, a client of that path, worded
  and refused the way `work abort` is.
- The work board gains a key that retries the selected failed card. Its key
  hint shows only on a failed card.
- The item's kept output from the failed attempt is left in place until the
  retried agent starts and writes over it.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `fleet-orchestrator`: the work list API serves a retry path.
- `work-commands`: `work` gains a `retry` subcommand.
- `work-board`: the board offers retry on a failed card.

## Impact

- `internal/orchestrator/worklist.go`, `internal/orchestrator/api.go`: a
  `Retry` operation and its route.
- `cmd/spinloop/work.go`: the `retry` subcommand; `cmd/spinloop/work_board_model.go`
  and `work_board_render.go`: the key, the verb and the hint.
- Shell completion for the item id slot.
- `docs/commands/orchestrator.md` and the work command docs.
- `docs/openapi.yaml` does not cover this API, so it is unchanged.
