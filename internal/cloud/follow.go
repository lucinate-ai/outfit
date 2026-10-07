package cloud

import (
	"sync"
	"time"
)

// FollowOverlap is how far back of the newest event already seen a follow
// re-asks from by default. The shipping agent's delivery lag means an event
// can land with a timestamp slightly behind one already returned, so a poll
// deliberately re-reads a little; FollowCursor suppresses what it has
// already returned, by event id. Both `spinloop cloud logs -f` and a fleet
// node's own log follow share this constant, so a fleet-dashboard poll and a
// standalone follow tolerate the same shipping lag.
const FollowOverlap = 10 * time.Second

// FollowCursor turns repeated LogResult polls into a stream of events shown
// exactly once. CloudWatch offers no resumable read position — Start is a
// lower bound on a query, not a cursor a store can resume from — so a follow
// has to re-ask a little behind the newest event it has already seen, to
// catch anything the shipping agent delivered late, and then suppress by
// event id whatever that overlap re-reads. One cursor holds that state for
// the life of one follow, whether that follow is `spinloop cloud logs -f`
// polling in a loop or a fleet node answering repeated Logs calls.
type FollowCursor struct {
	overlap time.Duration

	mu      sync.Mutex
	printed map[string]time.Time
	newest  time.Time
}

// NewFollowCursor starts an empty cursor that re-asks overlap behind the
// newest event seen on every poll.
func NewFollowCursor(overlap time.Duration) *FollowCursor {
	return &FollowCursor{overlap: overlap, printed: map[string]time.Time{}}
}

// Advance filters events down to the ones this cursor has not already
// returned, in the order given, and folds them into its state so a later
// poll that re-reads the overlap does not return them again.
func (c *FollowCursor) Advance(events []LogEvent) []LogEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	fresh := make([]LogEvent, 0, len(events))
	for _, e := range events {
		if _, seen := c.printed[e.ID]; seen {
			continue
		}
		c.printed[e.ID] = e.Timestamp
		fresh = append(fresh, e)
		if e.Timestamp.After(c.newest) {
			c.newest = e.Timestamp
		}
	}
	// Only ids within the overlap window can ever be re-read, so anything
	// older is forgotten and the map cannot grow across a long follow.
	for id, ts := range c.printed {
		if ts.Before(c.newest.Add(-2 * c.overlap)) {
			delete(c.printed, id)
		}
	}
	return fresh
}

// Start is the query bound the next poll should use: zero until an event has
// been seen, then the overlap window behind the newest one.
func (c *FollowCursor) Start() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.newest.IsZero() {
		return time.Time{}
	}
	return c.newest.Add(-c.overlap)
}

// Reset drops everything the cursor has seen, as a follow restarted from the
// tail rather than continued needs: a fresh open should show its own tail
// again, not have it suppressed as already seen by a previous session.
func (c *FollowCursor) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.printed = map[string]time.Time{}
	c.newest = time.Time{}
}
