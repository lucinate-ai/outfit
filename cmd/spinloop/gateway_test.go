package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spinloop-ai/spinloop/internal/gateway"
)

// The gateway starts with the fleet file it serves and answers, and its
// banner names the address a fleet file's gateway section puts in.
func TestGatewayStartsAndAnswers(t *testing.T) {
	isolateConfig(t)
	t.Setenv("SPINLOOP_API_TOKEN", "")
	node := newRoutableNode(t, "qwen3-27b", true, 300)
	dir := t.TempDir()
	fleetFileIn(t, dir, "nodes:\n"+node.entry("gpu-box"))
	t.Chdir(dir)

	var srv *http.Server
	var ln net.Listener
	out := captureStdout(t, func() {
		var err error
		srv, ln, err = newGatewayServer("", "127.0.0.1:0", "", "", 0)
		if err != nil {
			t.Fatal(err)
		}
	})
	defer ln.Close()
	go srv.Serve(ln)

	port := ln.Addr().(*net.TCPAddr).Port
	if !strings.Contains(out, "http://127.0.0.1:"+fmt.Sprint(port)) {
		t.Errorf("the banner should name the address to put in the fleet file's gateway section, got:\n%s", out)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health answered %d", resp.StatusCode)
	}

	// The printed address is the one the fleet file's gateway section names:
	// asking it for the fleet's models gets the running node's.
	resp, err = client.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/models", port))
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("models answered %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "qwen3-27b") {
		t.Errorf("the running node's model should be listed, got: %s", body)
	}
}

// A missing fleet file fails naming the expected path, and nothing listens.
func TestGatewayFailsWithoutAFleetFile(t *testing.T) {
	t.Chdir(t.TempDir())
	_, ln, err := newGatewayServer("", "127.0.0.1:0", "", "", 0)
	if err == nil {
		t.Fatal("a gateway with no fleet file should fail")
	}
	if ln != nil {
		t.Error("a failing gateway opened a listener")
	}
	if !strings.Contains(err.Error(), "fleet.yaml") {
		t.Errorf("the failure should name the expected path, got: %v", err)
	}
}

// A fleet file naming a token variable set nowhere fails at startup, naming
// the node and the variable, rather than listening and failing per request.
func TestGatewayFailsOnAnUnsetTokenVariable(t *testing.T) {
	dir := t.TempDir()
	fleetFileIn(t, dir, "nodes:\n  - name: gated\n    host: 127.0.0.1\n    port: 14242\n    tokenEnv: GW_NODE_TOKEN_UNSET\n")
	t.Chdir(dir)

	_, ln, err := newGatewayServer("", "127.0.0.1:0", "", "", 0)
	if err == nil {
		t.Fatal("an unset token variable should fail the gateway at startup")
	}
	if ln != nil {
		t.Error("a failing gateway opened a listener")
	}
	for _, want := range []string{"gated", "GW_NODE_TOKEN_UNSET"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure should name %q, got: %v", want, err)
		}
	}
}

// Two token sources at once is a conflict, resolved the way the daemon's is.
func TestGatewayTokenSourcesConflict(t *testing.T) {
	dir := t.TempDir()
	fleetFileIn(t, dir, "nodes:\n  - name: n\n    host: 127.0.0.1\n    port: 14242\n")
	t.Chdir(dir)

	tokenFile := filepath.Join(t.TempDir(), "token")
	mustWrite(t, tokenFile, "from-file\n")
	_, _, err := newGatewayServer("", "127.0.0.1:0", "literal", tokenFile, 0)
	if err == nil {
		t.Fatal("two token sources should be a conflict")
	}
	if !strings.Contains(err.Error(), "both given") {
		t.Errorf("the conflict should name both sources, got: %v", err)
	}
}

// A tokenless non-loopback listen is refused at startup, naming every way a
// token can be supplied.
func TestGatewayRefusesTokenlessNonLoopback(t *testing.T) {
	dir := t.TempDir()
	fleetFileIn(t, dir, "nodes:\n  - name: n\n    host: 127.0.0.1\n    port: 14242\n")
	t.Chdir(dir)
	t.Setenv("SPINLOOP_API_TOKEN", "")

	_, ln, err := newGatewayServer("", "0.0.0.0:0", "", "", 0)
	if err == nil {
		t.Fatal("a tokenless non-loopback gateway should refuse to start")
	}
	if ln != nil {
		t.Error("a refusing gateway opened a listener")
	}
	for _, want := range []string{"--api-token-file", "SPINLOOP_API_TOKEN", "--api-token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should name %q, got: %v", want, err)
		}
	}
}

func TestGatewayListenAddr(t *testing.T) {
	tests := []struct {
		name     string
		listen   string
		explicit bool
		loopback bool
		want     string
		wantErr  bool
	}{
		{name: "neither", listen: gateway.DefaultListen, want: gateway.DefaultListen},
		{name: "typed address alone", listen: "10.0.0.5:9999", explicit: true, want: "10.0.0.5:9999"},
		{name: "loopback replaces the default", listen: gateway.DefaultListen, loopback: true, want: gateway.LoopbackListen},
		{name: "loopback and a typed address conflict", listen: "10.0.0.5:9999", explicit: true, loopback: true, wantErr: true},
		{name: "loopback and the repeated default conflict", listen: gateway.DefaultListen, explicit: true, loopback: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gatewayListenAddr(tt.listen, tt.explicit, tt.loopback)
			if (err != nil) != tt.wantErr {
				t.Fatalf("gatewayListenAddr(%q, %v, %v) error = %v, wantErr %v", tt.listen, tt.explicit, tt.loopback, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("gatewayListenAddr = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCmdGateway_LoopbackConflictsWithExplicitListen covers the rule through
// the real flag parsing, so the -l spelling and a --listen typed to the
// default's own value are both counted as explicit. The conflict is detected
// by fs.Changed, which is order-independent — the last case types the address
// first, pinning that a sequential rewrite can't make the rule depend on flag
// position.
func TestCmdGateway_LoopbackConflictsWithExplicitListen(t *testing.T) {
	for _, args := range [][]string{
		{"--loopback", "--listen", "127.0.0.1:0"},
		{"--loopback", "--listen", gateway.DefaultListen},
		{"-l", "--listen", gateway.DefaultListen},
		{"--listen", "127.0.0.1:0", "--loopback"},
	} {
		isolateConfig(t)
		t.Setenv("SPINLOOP_API_TOKEN", "")
		t.Chdir(t.TempDir())
		err := cmdGateway(args)
		if err == nil || !strings.Contains(err.Error(), "--loopback") || !strings.Contains(err.Error(), "--listen") {
			t.Fatalf("cmdGateway(%v) = %v, want a conflict naming both flags", args, err)
		}
	}
}

// TestCmdGateway_LoopbackBindsLoopback checks the shorthand end to end: the
// gateway binds gateway.LoopbackListen and answers unauthenticated, because a
// loopback listen needs no token. The port is fixed, so the test declines
// rather than fights one — a developer in this repo often has a real gateway
// on it, and the rest of the suite never binds a fixed port for the same
// reason.
func TestCmdGateway_LoopbackBindsLoopback(t *testing.T) {
	probe, err := net.DialTimeout("tcp", gateway.LoopbackListen, 500*time.Millisecond)
	if err == nil {
		probe.Close()
		t.Skipf("%s is taken", gateway.LoopbackListen)
	}
	isolateConfig(t)
	t.Setenv("SPINLOOP_API_TOKEN", "")
	node := newRoutableNode(t, "qwen3-27b", true, 300)
	dir := t.TempDir()
	fleetFileIn(t, dir, "nodes:\n"+node.entry("gpu-box"))
	t.Chdir(dir)

	// The banner goes to stdout; wait for it the way the daemon test waits
	// for its stderr record.
	out := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	t.Cleanup(func() {
		os.Stdout = old
		f.Close()
	})

	done := make(chan error, 1)
	go func() { done <- cmdGateway([]string{"--loopback"}) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(out)
		if strings.Contains(string(data), "listening on "+gateway.LoopbackListen) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), "listening on "+gateway.LoopbackListen) {
		t.Fatalf("gateway --loopback did not bind %s; stdout so far:\n%s", gateway.LoopbackListen, data)
	}

	// No token was configured anywhere: an unauthenticated health answers.
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + gateway.LoopbackListen + "/health")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unauthenticated health answered %d, want 200", resp.StatusCode)
	}

	interruptSelf(t)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("gateway exited with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("gateway did not exit on SIGINT")
	}
}

func TestFleetURL(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:4000":   "http://127.0.0.1:4000",
		"gw.internal:4000": "http://gw.internal:4000",
		":4000":            "http://<this-machine>:4000",
		"0.0.0.0:4000":     "http://<this-machine>:4000",
		"[::]:4000":        "http://<this-machine>:4000",
	}
	for got, want := range cases {
		if url := fleetURL(got); url != want {
			t.Errorf("fleetURL(%q) = %q, want %q", got, url, want)
		}
	}
}

func TestGatewayRefusesANonPositiveRequestLimit(t *testing.T) {
	err := cmdGateway([]string{"--max-request-bytes", "0"})
	if err == nil || !strings.Contains(err.Error(), "--max-request-bytes") {
		t.Fatalf("got %v, want an error naming --max-request-bytes", err)
	}
}
