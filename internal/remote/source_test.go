package remote

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRef(t *testing.T) {
	cases := []struct{ version, override, want string }{
		{"v1.10.0", "", "v1.10.0"},
		{"1.10.0", "", "v1.10.0"}, // goreleaser strips the v; the tag has it
		{"1.13.0", "", "v1.13.0"},
		{"dev", "", "main"},
		{"", "", "main"},
		{"v1.10.0-5-gabc1234", "", "main"},
		{"v1.10.0-dirty", "", "main"},
		{"v1.10.0", "my-branch", "my-branch"},
		{"dev", "v2.0.0", "v2.0.0"},
	}
	for _, c := range cases {
		if got := ResolveRef(c.version, c.override); got != c.want {
			t.Errorf("ResolveRef(%q,%q) = %q, want %q", c.version, c.override, got, c.want)
		}
	}
}

// makeTarGz builds a gzipped tar with the given name->content entries.
func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractRemote(t *testing.T) {
	dest := t.TempDir()
	archive := makeTarGz(t, map[string]string{
		"outfit-1.10.0/README.md":                    "top-level, skip",
		"outfit-1.10.0/remote/package.json":          `{"name":"cloud-vm-llm"}`,
		"outfit-1.10.0/remote/lib/config.ts":         "export const x = 1",
		"outfit-1.10.0/remote/node_modules/dep/i.js": "should be skipped",
	})
	if err := ExtractRemote(bytes.NewReader(archive), dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "package.json")); err != nil {
		t.Errorf("remote/package.json not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "lib", "config.ts")); err != nil {
		t.Errorf("remote/lib/config.ts not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); !os.IsNotExist(err) {
		t.Error("top-level README.md should not be extracted")
	}
	if _, err := os.Stat(filepath.Join(dest, "node_modules")); !os.IsNotExist(err) {
		t.Error("node_modules should be skipped")
	}
}

func TestExtractRemoteSkipsGeneratedFiles(t *testing.T) {
	dest := t.TempDir()
	archive := makeTarGz(t, map[string]string{
		"outfit-1.10.0/remote/package.json":      `{"name":"cloud-vm-llm"}`,
		"outfit-1.10.0/remote/.env":              "SECRET=1",
		"outfit-1.10.0/remote/remote.json":       "{}",
		"outfit-1.10.0/remote/cdk-outputs.json":  "{}",
		"outfit-1.10.0/remote/cdk.out/tree.json": "build output",
	})
	if err := ExtractRemote(bytes.NewReader(archive), dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "package.json")); err != nil {
		t.Errorf("remote/package.json not extracted: %v", err)
	}
	for _, skipped := range []string{".env", "remote.json", "cdk-outputs.json", "cdk.out"} {
		if _, err := os.Stat(filepath.Join(dest, skipped)); !os.IsNotExist(err) {
			t.Errorf("%s should be skipped", skipped)
		}
	}
}

func TestExtractRemoteRejectsBadGzip(t *testing.T) {
	if err := ExtractRemote(strings.NewReader("not a gzip stream"), t.TempDir()); err == nil {
		t.Fatal("expected an error for a non-gzip reader")
	}
}

func TestExtractRemoteRejectsTraversal(t *testing.T) {
	dest := t.TempDir()
	archive := makeTarGz(t, map[string]string{
		"outfit-1.10.0/remote/../../evil": "escape",
	})
	if err := ExtractRemote(bytes.NewReader(archive), dest); err == nil {
		t.Fatal("expected a path-traversal error")
	}
}

// roundTripFunc lets a test stand in for httpClient's transport so
// DownloadRemote's hardcoded codeload URL can be intercepted.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stubTransport swaps httpClient for one whose transport is fn, restoring the
// original when the test ends.
func stubTransport(t *testing.T, fn roundTripFunc) {
	t.Helper()
	orig := httpClient
	httpClient = &http.Client{Transport: fn}
	t.Cleanup(func() { httpClient = orig })
}

func TestDownloadRemoteHTTPError(t *testing.T) {
	var gotURL string
	stubTransport(t, func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	err := DownloadRemote(context.Background(), "v1.13.0", t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
	if !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "v1.13.0") {
		t.Errorf("error should name the status and ref, got: %v", err)
	}
	// The ref must reach codeload verbatim; a missing v prefix here is the 404 bug.
	if !strings.HasSuffix(gotURL, "/tar.gz/v1.13.0") {
		t.Errorf("unexpected download URL: %q", gotURL)
	}
}

func TestDownloadRemoteExtracts(t *testing.T) {
	archive := makeTarGz(t, map[string]string{
		"outfit-v1.13.0/README.md":           "skip",
		"outfit-v1.13.0/remote/package.json": `{"name":"cloud-vm-llm"}`,
	})
	stubTransport(t, func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), Header: make(http.Header)}, nil
	})
	dir := t.TempDir()
	if err := DownloadRemote(context.Background(), "v1.13.0", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		t.Errorf("remote/package.json not extracted: %v", err)
	}
}

func TestDownloadRemoteReusesExisting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A checkout already present is reused without any network access.
	if err := DownloadRemote(context.Background(), "unused-ref", dir); err != nil {
		t.Errorf("DownloadRemote should reuse an existing checkout: %v", err)
	}
}

func TestSourceDirLayout(t *testing.T) {
	if base := filepath.Base(SourceRoot()); base != "cdk" {
		t.Errorf("SourceRoot should live under cdk/, got base %q", base)
	}
	// A ref-keyed dir sits directly under the root, so re-runs at one version reuse it.
	if got, want := SourceDir("v1.13.0"), filepath.Join(SourceRoot(), "v1.13.0"); got != want {
		t.Errorf("SourceDir = %q, want %q", got, want)
	}
}

func TestPruneSources(t *testing.T) {
	root := t.TempDir()
	for _, ref := range []string{"v1.9.0", "v1.10.0", "main"} {
		if err := os.MkdirAll(filepath.Join(root, ref), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := PruneSources(root, "v1.10.0"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name() != "v1.10.0" {
		t.Errorf("after prune, want only v1.10.0, got %v", got)
	}
	// Absent root is not an error.
	if err := PruneSources(filepath.Join(root, "nope"), "x"); err != nil {
		t.Errorf("prune of absent root: %v", err)
	}
}
