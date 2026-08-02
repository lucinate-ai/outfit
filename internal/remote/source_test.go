package remote

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRef(t *testing.T) {
	cases := []struct{ version, override, want string }{
		{"v1.10.0", "", "v1.10.0"},
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

func TestExtractRemoteRejectsTraversal(t *testing.T) {
	dest := t.TempDir()
	archive := makeTarGz(t, map[string]string{
		"outfit-1.10.0/remote/../../evil": "escape",
	})
	if err := ExtractRemote(bytes.NewReader(archive), dest); err == nil {
		t.Fatal("expected a path-traversal error")
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
