package daemon

import (
	"bytes"
	"io"
	"os"
)

// The bounds on one log read. The engine log is append-only and nothing
// rotates it, so a long-lived daemon's file can be arbitrarily large: a read
// is always bounded, and a caller asking for more than the maximum gets the
// maximum rather than an error, since the cursor lets it simply ask again.
const (
	// DefaultLogRead is how much a request that states no limit receives.
	DefaultLogRead = 64 << 10
	// MaxLogRead caps any single read, however much was asked for.
	MaxLogRead = 256 << 10
)

// TailLog is the offset meaning "no position stated": read the end of the log
// rather than its beginning, because the recent end is what diagnosis wants,
// and it makes the first read of a follow the backlog.
const TailLog int64 = -1

// LogsResponse is the control API's engine-log reply. Content is a slice of
// the log; NextOffset is the position immediately after it, which a caller
// passes back to receive only what has been appended since. Size is the log's
// current length, so a caller can see how far behind it is.
//
// Missing and StaleOffset are the two states a caller can act on, kept
// distinct from an empty Content: a log that does not exist is not a log that
// happens to be empty, and a cursor the file has outgrown is not a log with
// nothing new.
type LogsResponse struct {
	Content    string `json:"content"`
	NextOffset int64  `json:"nextOffset"`
	Size       int64  `json:"size"`
	Path       string `json:"path,omitempty"`
	// Missing reports that there is no log file: no engine has ever run here,
	// or the daemon forwards engine output to its own stdio instead of a file.
	Missing bool `json:"missing,omitempty"`
	// StaleOffset reports that the requested position is past the log's end —
	// the file was truncated or replaced — so the caller should resume from
	// NextOffset rather than wait for a position that will never arrive.
	StaleOffset bool `json:"staleOffset,omitempty"`
}

// clampLogRead brings a requested limit within bounds. A non-positive limit
// means "unstated", which is the default rather than "unbounded".
func clampLogRead(limit int) int {
	if limit <= 0 {
		return DefaultLogRead
	}
	if limit > MaxLogRead {
		return MaxLogRead
	}
	return limit
}

// ReadLog reads a bounded slice of the engine log at path. offset is where to
// read from, or TailLog for the end of the file. The result is trimmed to whole
// lines wherever the boundary is the reader's own doing rather than the end of
// the file, so a caller never renders half a line — and NextOffset always
// describes what was actually returned, so the cursor stays exact despite the
// trimming.
func ReadLog(path string, offset int64, limit int) (LogsResponse, error) {
	if path == "" {
		return LogsResponse{Missing: true}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LogsResponse{Missing: true, Path: path}, nil
		}
		return LogsResponse{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return LogsResponse{}, err
	}
	size := info.Size()
	limit = clampLogRead(limit)

	start, tail := offset, offset == TailLog
	if tail {
		if start = size - int64(limit); start < 0 {
			start = 0
		}
	}
	if start < 0 {
		start = 0
	}
	if start > size {
		// The file is shorter than the caller's cursor: truncated or replaced.
		return LogsResponse{NextOffset: size, Size: size, Path: path, StaleOffset: true}, nil
	}

	n := size - start
	if n > int64(limit) {
		n = int64(limit)
	}
	buf := make([]byte, n)
	if n > 0 {
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return LogsResponse{}, err
		}
	}

	// A tail read starts wherever the size arithmetic landed, which is usually
	// mid-line; drop that fragment. Nothing is lost — it is older output the
	// caller did not ask for.
	if tail && start > 0 {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			start += int64(i + 1)
			buf = buf[i+1:]
		}
	}
	end := start + int64(len(buf))
	// Stopping short of the end means the limit cut the read, probably
	// mid-line; give back only whole lines. A single line longer than the
	// limit has no newline to cut at, and is returned as-is rather than
	// stalling the cursor forever.
	if end < size {
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			buf = buf[:i+1]
			end = start + int64(len(buf))
		}
	}

	return LogsResponse{
		Content:    string(buf),
		NextOffset: end,
		Size:       size,
		Path:       path,
	}, nil
}
