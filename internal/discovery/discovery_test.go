package discovery

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lucinate-ai/outfit/internal/catalog"
)

func noEnv(string) string { return "" }

// resetCache clears the in-process cache so tests don't see each other's
// entries. Endpoints already differ per httptest server, but this keeps the
// count-based assertions unambiguous.
func resetCache() {
	mu.Lock()
	cache = map[string]cacheEntry{}
	mu.Unlock()
}

func stubModels(body string, hits *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		_, _ = w.Write([]byte(body))
	}))
}

func providerAt(url string) *catalog.Provider {
	return &catalog.Provider{Options: map[string]any{"baseURL": url}}
}

func TestModels_OpenAICompatibleSortsIDs(t *testing.T) {
	resetCache()
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[{"id":"zeta"},{"id":"alpha"},{"id":""}]}`))
	}))
	defer srv.Close()

	got, err := Models(providerAt(srv.URL), "", noEnv)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if path != "/models" {
		t.Errorf("queried path = %q, want /models", path)
	}
	if len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Errorf("got %v, want [alpha zeta] (sorted, empty id dropped)", got)
	}
}

func TestModels_SendsBearerKeyOnlyWhenResolved(t *testing.T) {
	resetCache()
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer srv.Close()

	p := &catalog.Provider{APIKeyEnv: "TOK", Options: map[string]any{"baseURL": srv.URL}}
	resolve := func(n string) string {
		if n == "TOK" {
			return "secret"
		}
		return ""
	}
	if _, err := Models(p, "", resolve); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer secret")
	}

	// With the key unset, no Authorization header is sent.
	resetCache()
	auth = ""
	if _, err := Models(p, "", noEnv); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Errorf("Authorization = %q, want empty when key is unset", auth)
	}
}

func TestModels_CachesPerEndpoint(t *testing.T) {
	resetCache()
	var hits int32
	srv := stubModels(`{"data":[{"id":"m"}]}`, &hits)
	defer srv.Close()

	p := providerAt(srv.URL)
	for i := 0; i < 3; i++ {
		if _, err := Models(p, "", noEnv); err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("endpoint hit %d times, want 1 (later lookups cached)", got)
	}
}

func TestModels_FailuresYieldError(t *testing.T) {
	resetCache()

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	if _, err := Models(providerAt(bad.URL), "", noEnv); err == nil {
		t.Error("expected error for a 500 status")
	}

	junk := stubModels("not json at all", nil)
	defer junk.Close()
	if _, err := Models(providerAt(junk.URL), "", noEnv); err == nil {
		t.Error("expected error for an unparseable body")
	}

	// Port 1 refuses fast, standing in for an unreachable endpoint.
	if _, err := Models(providerAt("http://127.0.0.1:1"), "", noEnv); err == nil {
		t.Error("expected error for an unreachable endpoint")
	}
}

func TestModels_NotDiscoverable(t *testing.T) {
	resetCache()
	if _, err := Models(&catalog.Provider{}, "", noEnv); !errors.Is(err, ErrNotDiscoverable) {
		t.Errorf("err = %v, want ErrNotDiscoverable for a provider with no base URL", err)
	}
}

func TestResolveBaseURL_Precedence(t *testing.T) {
	p := &catalog.Provider{
		Options:        map[string]any{"baseURL": "http://opt/v1"},
		OptionsFromEnv: map[string]string{"baseURL": "MYURL"},
		Pi:             &catalog.PiConfig{BaseURL: "http://pi/v1"},
	}

	if got := ResolveBaseURL(p, "http://flag/v1", noEnv); got != "http://flag/v1" {
		t.Errorf("override should win, got %q", got)
	}
	env := func(n string) string {
		if n == "OUTFIT_BASE_URL" {
			return "http://env/v1"
		}
		return ""
	}
	if got := ResolveBaseURL(p, "", env); got != "http://env/v1" {
		t.Errorf("OUTFIT_BASE_URL should win over catalogue, got %q", got)
	}
	ofe := func(n string) string {
		if n == "MYURL" {
			return "http://ofe/v1"
		}
		return ""
	}
	if got := ResolveBaseURL(p, "", ofe); got != "http://ofe/v1" {
		t.Errorf("optionsFromEnv should win over static, got %q", got)
	}
	if got := ResolveBaseURL(p, "", noEnv); got != "http://opt/v1" {
		t.Errorf("static options.baseURL expected, got %q", got)
	}

	// Pi endpoint is the last fallback (used by openrouter, which has no
	// options.baseURL).
	piOnly := &catalog.Provider{Pi: &catalog.PiConfig{BaseURL: "http://pi/v1"}}
	if got := ResolveBaseURL(piOnly, "", noEnv); got != "http://pi/v1" {
		t.Errorf("pi baseUrl fallback expected, got %q", got)
	}
	if got := ResolveBaseURL(&catalog.Provider{}, "", noEnv); got != "" {
		t.Errorf("no base URL should resolve to empty, got %q", got)
	}
}
