package outfitsrc

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsURL(t *testing.T) {
	cases := map[string]bool{
		"http://example.com/Outfit":  true,
		"https://example.com/Outfit": true,
		"./Outfit":                   false,
		"/abs/path/Outfit":           false,
		"Outfit":                     false,
		"":                           false,
	}
	for ref, want := range cases {
		if got := IsURL(ref); got != want {
			t.Errorf("IsURL(%q) = %v, want %v", ref, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name, base, ref, want string
	}{
		{
			name: "local base, relative ref",
			base: "/home/user/proj/Outfit",
			ref:  "./preset.ini",
			want: "/home/user/proj/preset.ini",
		},
		{
			name: "local base, absolute ref",
			base: "/home/user/proj/Outfit",
			ref:  "/opt/shared/preset.ini",
			want: "/opt/shared/preset.ini",
		},
		{
			name: "URL base, relative ref",
			base: "https://example.com/team/Outfit",
			ref:  "./preset.ini",
			want: "https://example.com/team/preset.ini",
		},
		{
			name: "local base, absolute URL ref",
			base: "/home/user/proj/Outfit",
			ref:  "https://example.com/preset.ini",
			want: "https://example.com/preset.ini",
		},
		{
			name: "URL base, absolute local ref",
			base: "https://example.com/team/Outfit",
			ref:  "/opt/shared/preset.ini",
			want: "/opt/shared/preset.ini",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Resolve(c.base, c.ref)
			if err != nil {
				t.Fatalf("Resolve(%q, %q): %v", c.base, c.ref, err)
			}
			if got != c.want {
				t.Errorf("Resolve(%q, %q) = %q, want %q", c.base, c.ref, got, c.want)
			}
		})
	}
}

func TestFetchLocal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Outfit")
	if err := os.WriteFile(path, []byte("PROVIDER ollama\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := Fetch(path)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(data) != "PROVIDER ollama\n" {
		t.Errorf("Fetch returned %q", data)
	}
}

func TestFetchLocalMissing(t *testing.T) {
	if _, err := Fetch(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected an error for a missing local file")
	}
}

func TestFetchURLSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "PROVIDER openrouter\n")
	}))
	defer srv.Close()

	data, err := Fetch(srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(data) != "PROVIDER openrouter\n" {
		t.Errorf("Fetch returned %q", data)
	}
}

func TestFetchURLNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := Fetch(srv.URL)
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error %q does not name the status", err)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error %q does not wrap ErrNotFound", err)
	}
}

func TestFetchURLOversized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, maxFetchSize+1024))
	}))
	defer srv.Close()

	_, err := Fetch(srv.URL)
	if err == nil {
		t.Fatal("expected an error for an oversized response")
	}
}

func TestFetchURLUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // closed before the request, so the connection is refused

	if _, err := Fetch(url); err == nil {
		t.Fatal("expected an error for an unreachable host")
	}
}
