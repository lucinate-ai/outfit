package cloud

import (
	"testing"
	"time"
)

func TestFollowCursorStartIsZeroUntilAnEventIsSeen(t *testing.T) {
	c := NewFollowCursor(10 * time.Second)
	if !c.Start().IsZero() {
		t.Errorf("a fresh cursor's start bound = %v, want zero", c.Start())
	}
}

func TestFollowCursorAdvanceSuppressesAlreadySeenIDs(t *testing.T) {
	c := NewFollowCursor(10 * time.Second)
	t1 := time.UnixMilli(1000)
	t2 := time.UnixMilli(2000)
	events := []LogEvent{{ID: "a", Timestamp: t1}, {ID: "b", Timestamp: t2}}

	fresh := c.Advance(events)
	if len(fresh) != 2 {
		t.Fatalf("first advance = %d fresh events, want 2", len(fresh))
	}

	// A later poll that re-reads the overlap window returns the same events —
	// they must not come back as fresh a second time.
	fresh = c.Advance(events)
	if len(fresh) != 0 {
		t.Errorf("re-advancing the same events returned %d as fresh, want 0", len(fresh))
	}

	t3 := time.UnixMilli(3000)
	fresh = c.Advance([]LogEvent{{ID: "a", Timestamp: t1}, {ID: "c", Timestamp: t3}})
	if len(fresh) != 1 || fresh[0].ID != "c" {
		t.Errorf("advancing a mixed batch = %+v, want only the new id", fresh)
	}
}

func TestFollowCursorStartTracksTheNewestEventBehindTheOverlap(t *testing.T) {
	overlap := 10 * time.Second
	c := NewFollowCursor(overlap)
	newest := time.UnixMilli(50_000)
	c.Advance([]LogEvent{{ID: "a", Timestamp: time.UnixMilli(10_000)}, {ID: "b", Timestamp: newest}})

	want := newest.Add(-overlap)
	if got := c.Start(); !got.Equal(want) {
		t.Errorf("start = %v, want %v (the newest event minus the overlap)", got, want)
	}
}

func TestFollowCursorForgetsIDsOutsideTheOverlapWindow(t *testing.T) {
	overlap := 10 * time.Second
	c := NewFollowCursor(overlap)
	old := time.UnixMilli(0)
	c.Advance([]LogEvent{{ID: "old", Timestamp: old}})
	// Push the newest event far enough ahead that "old" falls outside twice
	// the overlap window and is forgotten.
	c.Advance([]LogEvent{{ID: "new", Timestamp: old.Add(3 * overlap)}})

	// A duplicate delivery of the forgotten id now reads as fresh again — an
	// acceptable, documented cost, since nothing that old should still be
	// showing up in the overlap window a real poll re-reads.
	fresh := c.Advance([]LogEvent{{ID: "old", Timestamp: old}})
	if len(fresh) != 1 {
		t.Errorf("a forgotten id should be treated as fresh again, got %d fresh", len(fresh))
	}
}

func TestFollowCursorResetForgetsEverything(t *testing.T) {
	c := NewFollowCursor(10 * time.Second)
	c.Advance([]LogEvent{{ID: "a", Timestamp: time.UnixMilli(1000)}})
	if c.Start().IsZero() {
		t.Fatal("the cursor should have advanced past zero")
	}

	c.Reset()
	if !c.Start().IsZero() {
		t.Errorf("a reset cursor's start bound = %v, want zero", c.Start())
	}
	fresh := c.Advance([]LogEvent{{ID: "a", Timestamp: time.UnixMilli(1000)}})
	if len(fresh) != 1 {
		t.Errorf("a reset cursor should show a previously-seen id again, got %d fresh", len(fresh))
	}
}
