package fleet

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// registerRemoteEnv points the environment registry (OUTFIT_CONFIG_DIR) at a
// temp config directory and writes one environment's remote.json, whose control
// plane is the start and stop servers it is handed. It returns the name a
// kind-remote fleet node uses to refer to it.
func registerRemoteEnv(t *testing.T, name, startURL, stopURL string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OUTFIT_CONFIG_DIR", home)
	envDir := filepath.Join(home, "remotes", name)
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(
		`{"start_url":%q,"stop_url":%q,"region":"us-east-1","environment":%q}`,
		startURL, stopURL, name)
	if err := os.WriteFile(filepath.Join(envDir, "remote.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A kind-remote node in the fleet file is built by the regular NewNode and
// observed through the regular fan-out, resolved from the environment registry.
func TestFanOutLoadsARemoteNodeFromTheFile(t *testing.T) {
	stubAWSCreds(t)
	up := remoteControlServer(t, `{"state":"running","healthy":true}`, http.StatusOK)
	registerRemoteEnv(t, "prod", up.URL, up.URL)

	path := writeFleet(t, "nodes:\n  - name: prod\n    kind: remote\n", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.FanOut(context.Background(), StatusCall)
	if len(r) != 1 {
		t.Fatalf("got %d results, want 1", len(r))
	}
	if !r[0].OK() || r[0].Status.State != "running" {
		t.Errorf("remote node via the file = %+v (err %v)", r[0], r[0].Err)
	}
}

// A node for an environment that is not registered cannot be built, so the
// fan-out reports it as a config error — naming the environment — rather than
// blanking the view or surfacing a 401.
func TestFanOutRemotesANUnregisteredEnvAsAConfigError(t *testing.T) {
	stubAWSCreds(t)
	// A registry directory with no such environment in it.
	t.Setenv("OUTFIT_CONFIG_DIR", t.TempDir())

	path := writeFleet(t, "nodes:\n  - name: prod\n    kind: remote\n", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.FanOut(context.Background(), StatusCall)
	if len(r) != 1 {
		t.Fatalf("got %d results, want 1", len(r))
	}
	if r[0].Outcome != OutcomeConfigError {
		t.Errorf("outcome = %q, want %q", r[0].Outcome, OutcomeConfigError)
	}
	if r[0].Name != "prod" {
		t.Errorf("name = %q, want the node name", r[0].Name)
	}
}

// A fleet file that mixes a local daemon node and a remote environment is
// observed through the one fan-out, in file order, as the same kind of row.
func TestFanOutOverAMixedFile(t *testing.T) {
	stubAWSCreds(t)
	remoteUp := remoteControlServer(t, `{"state":"running","healthy":true}`, http.StatusOK)
	registerRemoteEnv(t, "prod", remoteUp.URL, remoteUp.URL)

	// A daemon node, built the way the daemon tests build it.
	cfg := fleetFor(t, stubDaemon(t, "", "running"), "")
	cfg.Nodes = append(cfg.Nodes, NodeConfig{Name: "prod", Kind: KindRemote})

	r := cfg.FanOut(context.Background(), StatusCall)
	if len(r) != 2 {
		t.Fatalf("got %d results, want 2", len(r))
	}
	// File order: the daemon node comes first, the remote environment after.
	if r[0].Name != "box" || r[1].Name != "prod" {
		t.Fatalf("order = %q, %q; want box then prod", r[0].Name, r[1].Name)
	}
	if !r[0].OK() || r[0].Status.State != "running" {
		t.Errorf("daemon node via the file = %+v (err %v)", r[0], r[0].Err)
	}
	if !r[1].OK() || r[1].Status.State != "running" {
		t.Errorf("remote node via the file = %+v (err %v)", r[1], r[1].Err)
	}
}
