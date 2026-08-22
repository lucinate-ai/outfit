package daemon

import (
	"io"
	"os"
	"path/filepath"
)

// prewarmChunk is the read size of the page-cache pass. A megabyte keeps the
// kernel's read-ahead engaged the way a small read would not.
const prewarmChunk = 1 << 20

// prewarmModel reads the model's files straight through, in the background,
// filling the page cache ahead of the engine that is about to read them.
//
// The engine maps its weights and faults pages in as it copies them to the
// GPU. On a network volume that is an access pattern served at per-page
// latency, not sequential bandwidth — a 26 GB model can take ten minutes to
// load that way on a volume that would stream the same bytes in under a
// minute. A sequential read that gets ahead of the faults turns them into
// cache hits; when the model does not fit in host memory the tail is simply
// re-read, which is no worse than the engine doing it alone.
//
// Best effort throughout: a missing or unreadable model just means the engine
// faults as before, and nothing here ever blocks a start.
func prewarmModel(path string) {
	go prewarmPath(path)
}

// prewarmPath is the synchronous half of prewarmModel: read every file under
// path (or path itself when it is not a directory) to EOF, skipping whatever
// cannot be opened. Split out so a test can drive it without a goroutine.
func prewarmPath(path string) {
	buf := make([]byte, prewarmChunk)
	for _, file := range modelFiles(path) {
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		_, _ = io.CopyBuffer(io.Discard, f, buf)
		f.Close()
	}
}

// modelFiles lists what a prewarm of path would read: path itself when it is
// a file (or a special node a test stands in for one), every regular file
// under it when it is a directory. A missing path is an empty list, not an
// error — the prewarm is best effort by design.
func modelFiles(path string) []string {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		return []string{path}
	}
	var files []string
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	return files
}
