// Package outfitsrc resolves and fetches an Outfit-family reference — the
// Outfit file itself, a PRESET, or a path-form REMOTE — that may name either a
// local filesystem path or an http(s) URL.
//
// It is the one place these references dispatch between local disk and the
// network, so every call site (the Outfit path itself in readOutfit, PRESET in
// serve.go, REMOTE in remote.go) resolves and fetches a relative reference the
// same way regardless of which kind of source named it.
package outfitsrc

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotFound wraps a 404 response from a remote fetch, the URL analogue of
// os.IsNotExist for a local path — callers that already tolerate a missing
// local file (a REMOTE config that may not exist yet, say) can tolerate its
// URL-sourced equivalent the same way, with errors.Is.
var ErrNotFound = errors.New("not found")

// IsURL reports whether ref names a remote resource fetched over HTTP, rather
// than a local filesystem path. Only http:// and https:// are recognized.
func IsURL(ref string) bool {
	return strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://")
}

// Resolve joins ref against base, which may itself be a local path or a URL:
//
//   - ref is already absolute (a URL, or an absolute local path) — returned
//     unchanged, regardless of what base is. This lets a local Outfit name a
//     PRESET or REMOTE that is itself a URL, and a URL-sourced Outfit name one
//     that is an absolute local path.
//   - base is a URL — ref resolves against it the way a relative link resolves
//     against a base document: base's own last path segment is dropped.
//   - otherwise — ref resolves as a filesystem path joined against base's own
//     directory, the rule PRESET and REMOTE have always used.
func Resolve(base, ref string) (string, error) {
	if IsURL(ref) || filepath.IsAbs(ref) {
		return ref, nil
	}
	if IsURL(base) {
		baseURL, err := url.Parse(base)
		if err != nil {
			return "", fmt.Errorf("parsing %s: %w", base, err)
		}
		refURL, err := url.Parse(ref)
		if err != nil {
			return "", fmt.Errorf("parsing %s: %w", ref, err)
		}
		return baseURL.ResolveReference(refURL).String(), nil
	}
	return filepath.Join(filepath.Dir(base), ref), nil
}

// fetchTimeout bounds a single remote fetch. This is not backing interactive
// tab-completion (unlike internal/discovery's shorter timeout), so it affords
// a slower third-party host serving a static file.
const fetchTimeout = 15 * time.Second

// maxFetchSize caps how much of a response body Fetch reads. Every reference
// this package fetches — an Outfit, a preset .ini, a remote.json — is a small,
// hand-editable text file, so this is generous headroom, not a tight limit.
const maxFetchSize = 1 << 20 // 1 MiB

var client = &http.Client{Timeout: fetchTimeout}

// Fetch reads ref's content: os.ReadFile for a local path, a bounded HTTP GET
// for a URL. A non-2xx status or an oversized response body is an error.
func Fetch(ref string) ([]byte, error) {
	if !IsURL(ref) {
		return os.ReadFile(ref)
	}

	resp, err := client.Get(ref)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("fetching %s: HTTP %d: %w", ref, resp.StatusCode, ErrNotFound)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetching %s: HTTP %d", ref, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchSize+1))
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", ref, err)
	}
	if len(data) > maxFetchSize {
		return nil, fmt.Errorf("fetching %s: response exceeds %d bytes", ref, maxFetchSize)
	}
	return data, nil
}
