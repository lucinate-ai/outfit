package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLog puts content in a temp file and returns its path.
func writeLog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine.log")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadLogTailsByDefault(t *testing.T) {
	path := writeLog(t, "one\ntwo\nthree\n")

	got, err := ReadLog(path, TailLog, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "one\ntwo\nthree\n" {
		t.Errorf("content = %q, want the whole log when it fits", got.Content)
	}
	if got.NextOffset != got.Size || got.Size != 14 {
		t.Errorf("offset/size = %d/%d, want 14/14", got.NextOffset, got.Size)
	}
	if got.Missing || got.StaleOffset {
		t.Errorf("flags = %+v, want neither set", got)
	}
}

func TestReadLogTailDropsThePartialFirstLine(t *testing.T) {
	// A limit that lands mid-line: the fragment is dropped rather than
	// rendered as though it were a whole line.
	path := writeLog(t, "aaaa\nbbbb\ncccc\n")

	got, err := ReadLog(path, TailLog, 8)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "cccc\n" {
		t.Errorf("content = %q, want only the whole trailing line", got.Content)
	}
	if got.NextOffset != got.Size {
		t.Errorf("offset = %d, want the end (%d)", got.NextOffset, got.Size)
	}
}

func TestReadLogResumesFromAnOffset(t *testing.T) {
	path := writeLog(t, "one\ntwo\n")

	first, err := ReadLog(path, TailLog, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	second, err := ReadLog(path, first.NextOffset, 0)
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "three\n" {
		t.Errorf("content = %q, want only what was appended", second.Content)
	}
	if second.NextOffset != second.Size {
		t.Errorf("offset = %d, want the new end (%d)", second.NextOffset, second.Size)
	}
}

func TestReadLogReturnsNothingWhenNothingIsNew(t *testing.T) {
	path := writeLog(t, "one\n")

	first, err := ReadLog(path, TailLog, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReadLog(path, first.NextOffset, 0)
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "" {
		t.Errorf("content = %q, want nothing", second.Content)
	}
	if second.NextOffset != first.NextOffset {
		t.Errorf("offset moved from %d to %d with no new output",
			first.NextOffset, second.NextOffset)
	}
	if second.StaleOffset {
		t.Error("an up-to-date cursor is not stale")
	}
}

func TestReadLogTrimsAForwardReadToWholeLines(t *testing.T) {
	path := writeLog(t, "aaaa\nbbbb\ncccc\n")

	// Reading forward with a limit that lands mid-"bbbb".
	got, err := ReadLog(path, 0, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "aaaa\n" {
		t.Errorf("content = %q, want whole lines only", got.Content)
	}
	if got.NextOffset != 5 {
		t.Errorf("offset = %d, want 5 — the position after what was returned", got.NextOffset)
	}
}

func TestReadLogReportsATruncatedFile(t *testing.T) {
	path := writeLog(t, "one\ntwo\nthree\n")
	end, err := ReadLog(path, TailLog, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The file is replaced by a shorter one, stranding the cursor.
	if err := os.WriteFile(path, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ReadLog(path, end.NextOffset, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StaleOffset {
		t.Error("a cursor beyond the end should be reported as stale")
	}
	if got.NextOffset != got.Size || got.Size != 4 {
		t.Errorf("offset/size = %d/%d, want 4/4 so the caller can resume",
			got.NextOffset, got.Size)
	}
}

func TestReadLogReportsAMissingLog(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "never-ran.log")
	got, err := ReadLog(absent, TailLog, 0)
	if err != nil {
		t.Fatalf("an absent log is not an error: %v", err)
	}
	if !got.Missing {
		t.Error("an absent log file should be reported as missing")
	}

	// An empty log is NOT a missing one: the engine ran and said nothing.
	empty := writeLog(t, "")
	got, err = ReadLog(empty, TailLog, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Missing {
		t.Error("an existing empty log is not missing")
	}
}

func TestReadLogReportsNoPathAsMissing(t *testing.T) {
	// The daemon forwards engine output to its own stdio, so there is no file.
	got, err := ReadLog("", TailLog, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Missing {
		t.Error("no configured log path should be reported as missing")
	}
}

func TestReadLogCapsTheRequestedLimit(t *testing.T) {
	path := writeLog(t, strings.Repeat("x", MaxLogRead*2)+"\n")

	got, err := ReadLog(path, TailLog, MaxLogRead*2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content) > MaxLogRead {
		t.Errorf("returned %d bytes, want at most the %d cap", len(got.Content), MaxLogRead)
	}
	if clampLogRead(0) != DefaultLogRead || clampLogRead(-5) != DefaultLogRead {
		t.Error("an unstated limit should fall back to the default")
	}
	if clampLogRead(10) != 10 {
		t.Error("a limit within bounds should be honoured")
	}
}

func TestReadLogReturnsALineLongerThanTheLimit(t *testing.T) {
	// No newline to cut at: returning nothing would stall the cursor forever,
	// so the fragment is returned and the caller makes progress.
	path := writeLog(t, strings.Repeat("y", 100)+"\n")

	got, err := ReadLog(path, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content) != 10 || got.NextOffset != 10 {
		t.Errorf("content len/offset = %d/%d, want 10/10 so reading advances",
			len(got.Content), got.NextOffset)
	}
}
