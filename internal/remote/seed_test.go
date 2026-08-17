package remote

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// seedTestServer records the request the client made and replies with body.
type seedTestServer struct {
	Method string
	Query  map[string]string
	Body   map[string]any
	Auth   string
}

func newSeedServer(t *testing.T, status int, body string) (*httptest.Server, *seedTestServer) {
	t.Helper()
	got := &seedTestServer{Query: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Method = r.Method
		got.Auth = r.Header.Get("Authorization")
		for k := range r.URL.Query() {
			got.Query[k] = r.URL.Query().Get(k)
		}
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &got.Body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, got
}

func seedCfg(url string) Config {
	return Config{StartURL: url, StopURL: url, SeedURL: url, Region: "eu-west-1"}
}

func TestSeedStart_SignsAndPostsTheRequest(t *testing.T) {
	stubAWSEnv(t)
	server, got := newSeedServer(t, 200, `{"seedId":"vllm--org-m","instanceId":"i-1","started":true}`)

	out, err := SeedStart(context.Background(), seedCfg(server.URL), SeedRequest{
		Runner: "vllm", ModelID: "org/m",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.Method)
	}
	if !strings.HasPrefix(got.Auth, "AWS4-HMAC-SHA256") {
		t.Errorf("request is not SigV4-signed: %q", got.Auth)
	}
	if got.Body["runner"] != "vllm" || got.Body["modelId"] != "org/m" {
		t.Errorf("wrong body: %+v", got.Body)
	}
	if out.SeedID != "vllm--org-m" || !out.Started {
		t.Errorf("unexpected reply: %+v", out)
	}
}

func TestSeedStart_OmitsEmptyOptionalFields(t *testing.T) {
	stubAWSEnv(t)
	server, got := newSeedServer(t, 200, `{"seedId":"vllm--org-m","started":true}`)

	if _, err := SeedStart(context.Background(), seedCfg(server.URL), SeedRequest{
		Runner: "vllm", ModelID: "org/m",
	}); err != nil {
		t.Fatal(err)
	}
	// An unpinned revision and an unforced start must not appear at all, so the
	// Lambda's defaults apply rather than an explicit empty string.
	for _, key := range []string{"revision", "quant", "force"} {
		if _, present := got.Body[key]; present {
			t.Errorf("%q should be omitted when unset, got %+v", key, got.Body)
		}
	}
}

func TestSeedStart_SendsNoEnvironment(t *testing.T) {
	stubAWSEnv(t)
	server, got := newSeedServer(t, 200, `{"seedId":"vllm--org-m","started":true}`)

	// Seeds are account-wide: one model seeded once serves every environment,
	// so an environment on the config must not leak onto a seed call.
	cfg := seedCfg(server.URL)
	cfg.Environment = "prod"
	if _, err := SeedStart(context.Background(), cfg, SeedRequest{Runner: "vllm", ModelID: "org/m"}); err != nil {
		t.Fatal(err)
	}
	if _, present := got.Query["env"]; present {
		t.Errorf("a seed call must not carry an environment, got %+v", got.Query)
	}
}

func TestSeedStart_SurfacesTheCapRefusal(t *testing.T) {
	stubAWSEnv(t)
	server, _ := newSeedServer(t, 429, `{"error":"3 seeds are already running (cap 3) — wait for one to finish"}`)

	_, err := SeedStart(context.Background(), seedCfg(server.URL), SeedRequest{Runner: "vllm", ModelID: "org/m"})
	if err == nil || !strings.Contains(err.Error(), "cap 3") {
		t.Errorf("the cap refusal should be reported verbatim, got %v", err)
	}
}

func TestSeedStart_ReportsAnOtherwiseFailedStart(t *testing.T) {
	stubAWSEnv(t)
	server, _ := newSeedServer(t, 400, `{"error":"modelId must be a non-empty string"}`)

	_, err := SeedStart(context.Background(), seedCfg(server.URL), SeedRequest{Runner: "vllm"})
	if err == nil || !strings.Contains(err.Error(), "modelId must be") {
		t.Errorf("want the Lambda's reason, got %v", err)
	}
}

func TestSeedGet_PutsTheSeedIDOnTheQuery(t *testing.T) {
	stubAWSEnv(t)
	server, got := newSeedServer(t, 200,
		`{"seedId":"vllm--org-m","state":"transferring","progressPercent":41.5,"bytesDone":10,"bytesTotal":20}`)

	out, err := SeedGet(context.Background(), seedCfg(server.URL), "vllm--org-m")
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", got.Method)
	}
	if got.Query["id"] != "vllm--org-m" {
		t.Errorf("id = %q, want the seed id", got.Query["id"])
	}
	if out.State != "transferring" || out.Progress != 41.5 || out.BytesTotal != 20 {
		t.Errorf("unexpected status: %+v", out)
	}
}

func TestSeedGet_ReportsAnUnknownSeed(t *testing.T) {
	stubAWSEnv(t)
	server, _ := newSeedServer(t, 404, `{"seedId":"nope","state":"unknown"}`)

	_, err := SeedGet(context.Background(), seedCfg(server.URL), "nope")
	if err == nil || !strings.Contains(err.Error(), `no seed "nope" is known`) {
		t.Errorf("want an unknown-seed error, got %v", err)
	}
}

func TestSeedGet_ReportsAFailedSeedRatherThanErroring(t *testing.T) {
	stubAWSEnv(t)
	// A seed that died is a successful *query* — the caller needs the reason.
	server, _ := newSeedServer(t, 200,
		`{"seedId":"vllm--org-m","state":"failed","error":"the seed stopped reporting while transferring"}`)

	out, err := SeedGet(context.Background(), seedCfg(server.URL), "vllm--org-m")
	if err != nil {
		t.Fatalf("a failed seed is not a failed query: %v", err)
	}
	if out.State != "failed" || !strings.Contains(out.Err, "stopped reporting") {
		t.Errorf("unexpected status: %+v", out)
	}
}

func TestSeedList_ReturnsEverySeed(t *testing.T) {
	stubAWSEnv(t)
	server, got := newSeedServer(t, 200,
		`{"seeds":[{"seedId":"a","state":"transferring","ageSeconds":10},{"seedId":"b","state":"starting"}],"count":2}`)

	seeds, err := SeedList(context.Background(), seedCfg(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, present := got.Query["id"]; present {
		t.Error("a list must not carry an id")
	}
	if len(seeds) != 2 || seeds[0].SeedID != "a" || seeds[1].State != "starting" {
		t.Errorf("unexpected listing: %+v", seeds)
	}
}

func TestSeedList_EmptyIsNotAnError(t *testing.T) {
	stubAWSEnv(t)
	server, _ := newSeedServer(t, 200, `{"seeds":[],"count":0}`)

	seeds, err := SeedList(context.Background(), seedCfg(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if len(seeds) != 0 {
		t.Errorf("want no seeds, got %+v", seeds)
	}
}

func TestSeedStop_DeletesByID(t *testing.T) {
	stubAWSEnv(t)
	server, got := newSeedServer(t, 200, `{"seedId":"vllm--org-m","stopped":true,"instanceIds":["i-1"]}`)

	out, err := SeedStop(context.Background(), seedCfg(server.URL), "vllm--org-m")
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodDelete || got.Query["id"] != "vllm--org-m" {
		t.Errorf("want DELETE ?id=vllm--org-m, got %s ?id=%s", got.Method, got.Query["id"])
	}
	if !out.Stopped || len(out.InstanceIDs) != 1 {
		t.Errorf("unexpected reply: %+v", out)
	}
}

func TestSeedStop_NothingRunningIsNotAnError(t *testing.T) {
	stubAWSEnv(t)
	server, _ := newSeedServer(t, 200, `{"seedId":"vllm--org-m","stopped":false,"message":"no seed is running"}`)

	// Stopping twice must be safe.
	out, err := SeedStop(context.Background(), seedCfg(server.URL), "vllm--org-m")
	if err != nil {
		t.Fatalf("stopping an idle seed should succeed: %v", err)
	}
	if out.Stopped {
		t.Error("stopped should be false when nothing was running")
	}
}

func TestSeedCalls_NameTheMissingSeedURL(t *testing.T) {
	stubAWSEnv(t)
	// A remote config written before the seed Lambda existed.
	cfg := Config{StartURL: "http://x", StopURL: "http://x", Region: "eu-west-1"}
	ctx := context.Background()

	calls := map[string]func() error{
		"start":  func() error { _, err := SeedStart(ctx, cfg, SeedRequest{}); return err },
		"status": func() error { _, err := SeedGet(ctx, cfg, "x"); return err },
		"list":   func() error { _, err := SeedList(ctx, cfg); return err },
		"stop":   func() error { _, err := SeedStop(ctx, cfg, "x"); return err },
	}
	for name, call := range calls {
		err := call()
		if err == nil {
			t.Errorf("%s: want an error when no seed endpoint is configured", name)
			continue
		}
		// The message must say what to add and where it comes from.
		for _, want := range []string{"seed_url", "SeedUrl", "OUTFIT_REMOTE_SEED_URL"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the error should name %q, got: %v", name, want, err)
			}
		}
	}
}

func TestSeedCall_ReportsAnUnparseableReply(t *testing.T) {
	stubAWSEnv(t)
	server, _ := newSeedServer(t, 200, `<html>not json</html>`)

	_, err := SeedList(context.Background(), seedCfg(server.URL))
	if err == nil || !strings.Contains(err.Error(), "not json") {
		t.Errorf("want the body quoted back, got %v", err)
	}
}

func TestSeedCall_HintsOnForbidden(t *testing.T) {
	stubAWSEnv(t)
	// A Function URL denial arrives as non-JSON; the shared hint explains it.
	server, _ := newSeedServer(t, 403, `{"Message":"Forbidden"}`)

	_, err := SeedList(context.Background(), seedCfg(server.URL))
	if err == nil {
		t.Fatal("want an error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("the status should be reported, got %v", err)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "third"); got != "third" {
		t.Errorf("firstNonEmpty = %q, want third", got)
	}
	if got := firstNonEmpty("first", "second"); got != "first" {
		t.Errorf("firstNonEmpty = %q, want first", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty = %q, want empty", got)
	}
}

// The seed URL travels with the rest of the config, including its override.
func TestConfig_CarriesTheSeedURL(t *testing.T) {
	isolateConfig(t)
	writeConfig(t, Config{
		StartURL: "http://start", StopURL: "http://stop",
		SeedURL: "http://seed", Region: "eu-west-1",
	})
	cfg, err := LoadConfig(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SeedURL != "http://seed" {
		t.Errorf("SeedURL = %q, want it read from the config file", cfg.SeedURL)
	}
}

func TestConfig_SeedURLOverride(t *testing.T) {
	isolateConfig(t)
	writeConfig(t, Config{StartURL: "http://start", StopURL: "http://stop", Region: "eu-west-1"})
	cfg, err := LoadConfig(func(k string) string {
		if k == "OUTFIT_REMOTE_SEED_URL" {
			return "http://override"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SeedURL != "http://override" {
		t.Errorf("SeedURL = %q, want the override", cfg.SeedURL)
	}
}

// The regression this guards: SeedUrl was added as a stack output and to
// Config, but the discovery that maps outputs onto Config did not read it — so
// `outfit remote seed` reported "no seed_url configured" against every real
// deployment while the stubbed CLI tests passed.
func TestControlPlaneFromOutputs_CarriesEveryURL(t *testing.T) {
	layer, err := controlPlaneFromOutputs(map[string]string{
		"StartUrl":      "http://start",
		"StopUrl":       "http://stop",
		"DeployUrl":     "http://deploy",
		"StatsUrl":      "http://stats",
		"EnvUrl":        "http://env",
		"SeedUrl":       "http://seed",
		"Region":        "eu-west-1",
		"WeightsBucket": "bucket",
	}, "cloud-vm-llm")
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{
		"StartURL":  layer.Config.StartURL,
		"StopURL":   layer.Config.StopURL,
		"DeployURL": layer.Config.DeployURL,
		"StatsURL":  layer.Config.StatsURL,
		"EnvURL":    layer.Config.EnvURL,
		"SeedURL":   layer.Config.SeedURL,
	} {
		if got == "" {
			t.Errorf("%s was dropped by the output mapping", name)
		}
	}
	if layer.Config.SeedURL != "http://seed" {
		t.Errorf("SeedURL = %q, want http://seed", layer.Config.SeedURL)
	}
	if layer.WeightsBucket != "bucket" || layer.Config.Region != "eu-west-1" {
		t.Errorf("unexpected control plane: %+v", layer)
	}
}

// A control plane deployed before the seed Lambda existed still works for every
// other subcommand; only the seed subcommand complains, and it says what to add.
func TestControlPlaneFromOutputs_ToleratesAnOlderStack(t *testing.T) {
	layer, err := controlPlaneFromOutputs(map[string]string{
		"StartUrl":  "http://start",
		"StopUrl":   "http://stop",
		"DeployUrl": "http://deploy",
	}, "cloud-vm-llm")
	if err != nil {
		t.Fatalf("an older stack must still resolve: %v", err)
	}
	if layer.Config.SeedURL != "" {
		t.Errorf("SeedURL = %q, want empty", layer.Config.SeedURL)
	}
}

func TestControlPlaneFromOutputs_RejectsAStackMissingTheControlURLs(t *testing.T) {
	_, err := controlPlaneFromOutputs(map[string]string{"StartUrl": "http://start"}, "cloud-vm-llm")
	if err == nil || !strings.Contains(err.Error(), "missing its control-URL outputs") {
		t.Errorf("want a missing-outputs error, got %v", err)
	}
}

// A config predating the seed Lambda must not stop the other subcommands
// working — the seed URL is optional exactly as env_url is.
func TestConfig_LoadsWithoutASeedURL(t *testing.T) {
	isolateConfig(t)
	writeConfig(t, Config{StartURL: "http://start", StopURL: "http://stop", Region: "eu-west-1"})
	cfg, err := LoadConfig(func(string) string { return "" })
	if err != nil {
		t.Fatalf("a config without seed_url must still load: %v", err)
	}
	if cfg.SeedURL != "" {
		t.Errorf("SeedURL = %q, want empty", cfg.SeedURL)
	}
}
