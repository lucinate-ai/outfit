// Package discovery fetches the models a provider currently serves from the
// provider's own OpenAI-compatible `/models` endpoint. It is best-effort: any
// failure (unreachable endpoint, timeout, non-2xx status, unparseable body,
// missing key) returns an error that callers treat as "no models", never a
// panic, and a bounded timeout keeps a slow endpoint from hanging a command.
//
// The catalogue holds no models (see the provider-catalog spec); this is how
// `outfit list --models` and model tab-completion learn what a provider offers,
// live and without curation. Every discoverable provider here speaks the
// OpenAI-compatible `GET {baseURL}/models` shape — OpenRouter, vLLM, llama.cpp,
// the generic openai-compatible endpoint, and Ollama (whose compatibility layer
// serves `/v1/models`). A provider with no resolvable base URL (amazon-bedrock)
// is not discoverable over HTTP.
package discovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lucinate-ai/outfit/internal/catalog"
)

// baseURLEnv mirrors the catalogue's OUTFIT_BASE_URL override so discovery
// targets the same endpoint a selection would.
const baseURLEnv = "OUTFIT_BASE_URL"

// requestTimeout bounds a single discovery request. Discovery is interactive
// (it backs `outfit list` and tab-completion), so a few seconds is the ceiling:
// a reachable endpoint answers in well under this, and an unreachable one fails
// fast enough not to strand the command.
const requestTimeout = 3 * time.Second

// cacheTTL is how long a provider endpoint's models are reused within one
// process, so listing then completing does not re-hit the network.
const cacheTTL = 60 * time.Second

var client = &http.Client{Timeout: requestTimeout}

// ErrNotDiscoverable is returned for a provider with no resolvable base URL
// (for example amazon-bedrock), which has no HTTP `/models` endpoint to query.
var ErrNotDiscoverable = errors.New("provider has no discoverable models endpoint")

type cacheEntry struct {
	models  []string
	expires time.Time
}

var (
	mu    sync.Mutex
	cache = map[string]cacheEntry{}
)

// ResolveBaseURL determines the base URL to query for a provider, mirroring the
// base-URL precedence a selection uses: an explicit override, then
// OUTFIT_BASE_URL, then the provider's optionsFromEnv `baseURL`, then its static
// options.baseURL, then its Pi endpoint. It returns "" when none resolves, which
// marks the provider as not discoverable.
func ResolveBaseURL(p *catalog.Provider, override string, resolve func(string) string) string {
	if override != "" {
		return override
	}
	if v := resolve(baseURLEnv); v != "" {
		return v
	}
	if env, ok := p.OptionsFromEnv["baseURL"]; ok {
		if v := resolve(env); v != "" {
			return v
		}
	}
	if s, _ := p.Options["baseURL"].(string); s != "" {
		return s
	}
	if p.Pi != nil && p.Pi.BaseURL != "" {
		return p.Pi.BaseURL
	}
	return ""
}

// Discoverable reports whether a provider can be queried for its models at all
// (i.e. a base URL resolves for it).
func Discoverable(p *catalog.Provider, resolve func(string) string) bool {
	return ResolveBaseURL(p, "", resolve) != ""
}

// Models returns, in stable order, the ids of the models a provider currently
// serves, querying its OpenAI-compatible `{baseURL}/models` endpoint. Results
// are cached per resolved endpoint for cacheTTL. When the provider declares an
// API key variable and it resolves to a value, that value is sent as a Bearer
// header and never stored. Any failure returns an error (callers treat it as
// "no models"); ErrNotDiscoverable means the provider has no endpoint to query.
func Models(p *catalog.Provider, baseURLOverride string, resolve func(string) string) ([]string, error) {
	base := ResolveBaseURL(p, baseURLOverride, resolve)
	if base == "" {
		return nil, ErrNotDiscoverable
	}
	endpoint := strings.TrimRight(base, "/") + "/models"

	mu.Lock()
	if e, ok := cache[endpoint]; ok && time.Now().Before(e.expires) {
		models := e.models
		mu.Unlock()
		return models, nil
	}
	mu.Unlock()

	models, err := fetch(endpoint, p.APIKeyEnv, resolve)
	if err != nil {
		return nil, err
	}

	mu.Lock()
	cache[endpoint] = cacheEntry{models: models, expires: time.Now().Add(cacheTTL)}
	mu.Unlock()
	return models, nil
}

// fetch performs the HTTP GET and parses the OpenAI `{"data":[{"id":...}]}`
// shape into a sorted list of model ids.
func fetch(endpoint, keyEnv string, resolve func(string) string) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if keyEnv != "" {
		if key := resolve(keyEnv); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("discovery %s: unexpected status %s", endpoint, resp.Status)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
