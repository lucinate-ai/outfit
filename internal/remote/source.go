package remote

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// defaultSourceRef is the ref bootstrap downloads for a development build, whose
// version is not a clean release tag.
const defaultSourceRef = "main"

// gitDescribeSuffix matches the "-<n>-g<sha>" tail `git describe` adds to a
// version that is not exactly on a tag; such a build is treated as dev.
var gitDescribeSuffix = regexp.MustCompile(`-\d+-g[0-9a-f]+$`)

// isCleanReleaseVersion reports whether version names an exact release tag —
// not a dev build, a dirty working tree, or a build off an untagged commit.
func isCleanReleaseVersion(version string) bool {
	if version == "" || version == "dev" || strings.HasSuffix(version, "-dirty") {
		return false
	}
	return !gitDescribeSuffix.MatchString(version)
}

// ResolveRef picks the ref of the remote/ sources to download so they match the
// running binary. An explicit override wins; a clean release maps to its release
// tag; a "dev", dirty, or mid-history build falls back to the default branch.
func ResolveRef(version, override string) string {
	if override != "" {
		return override
	}
	if !isCleanReleaseVersion(version) {
		return defaultSourceRef
	}
	// Release tags carry a "v" prefix (v1.13.0). The Makefile's git-describe
	// version keeps it, but goreleaser reports the version without it (1.13.0);
	// add it back so the ref matches the tag either way.
	if !strings.HasPrefix(version, "v") {
		return "v" + version
	}
	return version
}

// ReleaseVersion reports the CDK project's outfitVersion for a running binary
// at version, its "v" prefix stripped — empty for a dev, dirty, or
// mid-history build. The downloaded sources are a codeload tarball with no
// .git directory, so the CDK project's own git-describe fallback (see
// remote/lib/config.ts) always comes back empty; bootstrap passes this value
// as -c outfitVersion so a real release still bakes its own version without
// the caller needing to pass it by hand.
func ReleaseVersion(version string) string {
	if !isCleanReleaseVersion(version) {
		return ""
	}
	return strings.TrimPrefix(version, "v")
}

// SourceRoot is the parent of the ref-keyed CDK source caches,
// <configHome>/cdk. It is named cdk/ to avoid confusion with the remotes/
// environment registry.
func SourceRoot() (string, error) {
	home, err := ConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cdk"), nil
}

// SourceDir is where the remote/ sources for a given ref are placed,
// <configHome>/cdk/<ref>. Keying by ref means a re-run at the same version
// reuses its checkout while a new version downloads fresh.
func SourceDir(ref string) (string, error) {
	root, err := SourceRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ref), nil
}

// skipSource reports whether an extracted remote/-relative path should be
// dropped: build output and gitignored generated files that have no place in a
// fresh checkout (and defensively should never be in the archive).
func skipSource(rel string) bool {
	if rel == "node_modules" || strings.HasPrefix(rel, "node_modules/") {
		return true
	}
	if rel == "cdk.out" || strings.HasPrefix(rel, "cdk.out/") {
		return true
	}
	switch rel {
	case ".env", "remote.json", "cdk-outputs.json":
		return true
	}
	return false
}

// ExtractRemote reads a gzipped tar of the repository (as GitHub's codeload
// serves) and writes only its remote/ subtree into destDir, stripping the
// archive's top-level <repo>-<ref>/ segment. Entries that escape destDir are
// rejected.
func ExtractRemote(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("reading source archive: %w", err)
	}
	defer gz.Close()

	cleanDest := filepath.Clean(destDir)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading source archive: %w", err)
		}
		// Strip the leading "<repo>-<ref>/" segment, then keep only remote/*.
		slash := strings.IndexByte(hdr.Name, '/')
		if slash < 0 {
			continue
		}
		rel := hdr.Name[slash+1:]
		if !strings.HasPrefix(rel, "remote/") {
			continue
		}
		rel = strings.TrimPrefix(rel, "remote/")
		if rel == "" || skipSource(rel) {
			continue
		}

		target := filepath.Join(cleanDest, rel)
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in source archive: %q", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode&0o777))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

// DownloadRemote fetches the remote/ sources at ref from the project repository
// and extracts them into destDir. A checkout already present (its package.json
// exists) is reused rather than re-downloaded, so re-runs are cheap and any
// node_modules survives.
func DownloadRemote(ctx context.Context, ref, destDir string) error {
	if _, err := os.Stat(filepath.Join(destDir, "package.json")); err == nil {
		return nil
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	url := "https://codeload.github.com/lucinate-ai/outfit/tar.gz/" + ref
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading remote/ sources at %s: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading remote/ sources at %s: HTTP %d", ref, resp.StatusCode)
	}
	return ExtractRemote(resp.Body, destDir)
}

// PruneSources removes every ref-keyed source cache under root except keepRef,
// so stale-version checkouts do not accumulate. An absent root is not an error.
func PruneSources(root, keepRef string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == keepRef {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
