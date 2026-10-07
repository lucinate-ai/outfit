package fleet

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A registered environment becomes a fleet of one, whose single node is the
// cloud node the same name in a fleet file would build.
func TestForEnvironmentBuildsAFleetOfOne(t *testing.T) {
	stubAWSCreds(t)
	up := cloudControlServer(t, `{"state":"running","healthy":true}`, http.StatusOK)
	registerCloudEnv(t, "prod", up.URL, up.URL)

	cfg, err := ForEnvironment("prod")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(cfg.Nodes))
	}
	if cfg.Nodes[0].Name != "prod" {
		t.Errorf("name = %q, want prod", cfg.Nodes[0].Name)
	}
	if cfg.Nodes[0].Kind != KindCloud {
		t.Errorf("kind = %q, want %q", cfg.Nodes[0].Kind, KindCloud)
	}
	// The node builds and answers, which is the whole claim: the fan-out and
	// the renderers need nothing special for a fleet assembled this way.
	node, err := cfg.NewNode(cfg.Nodes[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.Status(context.Background()); err != nil {
		t.Fatalf("status: %v", err)
	}
}

// The same environment named by a fleet file and by ForEnvironment yields the
// same reading, so what a command prints does not depend on how the
// environment was named.
func TestForEnvironmentMatchesTheSameNodeInAFile(t *testing.T) {
	stubAWSCreds(t)
	up := cloudControlServer(t, `{"state":"running","healthy":true}`, http.StatusOK)
	registerCloudEnv(t, "prod", up.URL, up.URL)

	path := writeFleet(t, "nodes:\n  - name: prod\n    kind: cloud\n", "")
	fromFile, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	fromEnv, err := ForEnvironment("prod")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	a := fromFile.FanOut(ctx, StatusCall)
	b := fromEnv.FanOut(ctx, StatusCall)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("results = %d and %d, want 1 each", len(a), len(b))
	}
	if a[0].Outcome != b[0].Outcome || a[0].Status.State != b[0].Status.State {
		t.Errorf("file gave %s/%s, env gave %s/%s",
			a[0].Outcome, a[0].Status.State, b[0].Outcome, b[0].Status.State)
	}
}

// A name that is not a plain identifier, and a name nothing is registered
// under, each fail naming the fix — before anything is contacted.
func TestForEnvironmentRejects(t *testing.T) {
	t.Setenv("SPINLOOP_CONFIG_DIR", t.TempDir())
	tests := []struct {
		name string
		env  string
		want string
	}{
		{"a path", "./cloud.json", "plain identifier"},
		{"a nested path", "envs/prod", "plain identifier"},
		{"a json file", "prod.json", "plain identifier"},
		{"empty", "", "plain identifier"},
		{"unregistered", "nope", "clouds/nope/cloud.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ForEnvironment(tt.env)
			if err == nil {
				t.Fatalf("ForEnvironment(%q) succeeded, want an error", tt.env)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

// An environment that resolves to nothing fails naming the file to create, so
// the message is a repair rather than a report.
func TestForEnvironmentUnregisteredNamesTheFix(t *testing.T) {
	t.Setenv("SPINLOOP_CONFIG_DIR", t.TempDir())
	_, err := ForEnvironment("nope")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "clouds/nope/cloud.json") {
		t.Errorf("error %q does not name the environment's registry path", err)
	}
}

// A fleet of one carries no file, so nothing can read a path or a directory
// off it — and the fleet-wide settings a file would supply are absent rather
// than inherited from anywhere.
func TestForEnvironmentCarriesNoFileOrFleetWideSettings(t *testing.T) {
	stubAWSCreds(t)
	up := cloudControlServer(t, `{"state":"running"}`, http.StatusOK)
	registerCloudEnv(t, "prod", up.URL, up.URL)

	cfg, err := ForEnvironment("prod")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != "" || cfg.Dir != "" {
		t.Errorf("Path = %q, Dir = %q, want both empty", cfg.Path, cfg.Dir)
	}
	if cfg.Prefer != "" {
		t.Errorf("Prefer = %q, want unset", cfg.Prefer)
	}
	if cfg.WakePolicy != "" {
		t.Errorf("WakePolicy = %q, want unset", cfg.WakePolicy)
	}
	if cfg.Gateway != nil {
		t.Error("Gateway set, want none")
	}
	if cfg.Concurrency != nil {
		t.Error("Concurrency set, want none")
	}
	if cfg.APIKeyEnv != "" {
		t.Errorf("APIKeyEnv = %q, want unset", cfg.APIKeyEnv)
	}
	// The defaults still apply, so a caller reading them gets an answer
	// rather than a zero value it has to interpret.
	prefer, err := cfg.Preference("")
	if err != nil {
		t.Fatal(err)
	}
	if prefer != PreferIdle {
		t.Errorf("Preference = %q, want %q", prefer, PreferIdle)
	}
}

// A fleet.yaml in the working directory supplies a fleet of one with nothing:
// the target is the environment, and the file is not consulted.
func TestForEnvironmentIgnoresADirectoryFleetFile(t *testing.T) {
	stubAWSCreds(t)
	up := cloudControlServer(t, `{"state":"running"}`, http.StatusOK)
	registerCloudEnv(t, "prod", up.URL, up.URL)

	dir := t.TempDir()
	body := "prefer: active\nnodes:\n  - name: other\n    host: elsewhere\n"
	if err := os.WriteFile(filepath.Join(dir, DefaultFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	cfg, err := ForEnvironment("prod")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Nodes) != 1 || cfg.Nodes[0].Name != "prod" {
		t.Fatalf("nodes = %+v, want just prod", cfg.Nodes)
	}
	if cfg.Prefer != "" {
		t.Errorf("Prefer = %q — the directory's fleet file was consulted", cfg.Prefer)
	}
}

// A cloud node names no token variable, so a fleet of one resolves none and
// never looks for a .env — which is what lets it carry no directory at all.
func TestForEnvironmentReadsNoAdjacentEnvFile(t *testing.T) {
	stubAWSCreds(t)
	up := cloudControlServer(t, `{"state":"running"}`, http.StatusOK)
	registerCloudEnv(t, "prod", up.URL, up.URL)

	dir := t.TempDir()
	// A .env that would be read if the lookup ever ran, holding a value no
	// node should pick up.
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("PROD_TOKEN=from-dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	cfg, err := ForEnvironment("prod")
	if err != nil {
		t.Fatal(err)
	}
	token, err := cfg.Token(cfg.Nodes[0])
	if err != nil {
		t.Fatalf("resolving a token for a cloud node: %v", err)
	}
	if token != "" {
		t.Errorf("token = %q, want empty: a cloud node names none", token)
	}
}
