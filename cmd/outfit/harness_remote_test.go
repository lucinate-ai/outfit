package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/harness"
	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// remoteOutfitDir writes an Outfit whose REMOTE names a registered environment
// served by envURL, and returns its directory. baseURL is the endpoint address
// the environment records — a public one, so the apply has a key to warn about
// when it has none.
func remoteOutfitDir(t *testing.T, name, envURL, baseURL string) string {
	t.Helper()
	registerEnv(t, name, remote.Config{
		StartURL:    envURL,
		StopURL:     envURL,
		EnvURL:      envURL,
		BaseURL:     baseURL,
		Region:      "eu-west-1",
		Environment: name,
	})
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Outfit"), "PROVIDER llamacpp\nALIAS q3\nREMOTE "+name+"\n")
	return dir
}

// envServer answers the env Lambda call with a live key.
func envServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"base_url":"http://198.51.100.1:8000/v1","api_key":"sk-remote"}`))
	}))
}

// The key of a remote endpoint is known only to the control plane, and
// `outfit harness` fetches it and hands it to the agent it launches. The apply
// that precedes the launch must therefore not warn that no key is set: it is
// about to be.
func TestApplyBeforeLaunch_RemoteKeySilencesTheMissingKeyWarning(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	t.Setenv("OPENAI_API_KEY", "")

	server := envServer(t)
	defer server.Close()
	dir := remoteOutfitDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")

	h, _ := harness.Lookup("opencode")
	var sel outfitSelectionResult
	out := captureStdout(t, func() {
		sel.selection, sel.envDir, sel.resp, sel.err = applyBeforeLaunch(
			outfitPathFlag{set: true, path: dir}, "", h, nil)
	})
	if sel.err != nil {
		t.Fatalf("applyBeforeLaunch: %v", sel.err)
	}
	if strings.Contains(out, "no API key was set") {
		t.Errorf("the apply warned about a key the launch supplies:\n%s", out)
	}
	if !strings.Contains(out, "API key read from OPENAI_API_KEY") {
		t.Errorf("the apply should say where the key comes from:\n%s", out)
	}
	if sel.resp == nil || sel.resp.APIKey != "sk-remote" {
		t.Fatalf("the endpoint's key was not fetched: %+v", sel.resp)
	}

	// ...and it reaches the launched agent's environment.
	env := harnessEnv("", remoteLaunchResolver(func(string) string { return "" }, sel.resp), sel.resp)
	if got, _ := envValue(env, "OPENAI_API_KEY"); got != "sk-remote" {
		t.Errorf("launched agent's OPENAI_API_KEY = %q, want sk-remote", got)
	}
}

// outfitSelectionResult carries applyBeforeLaunch's four results out of the
// output-capturing closure.
type outfitSelectionResult struct {
	selection outfit.Selection
	envDir    string
	resp      *remote.Response
	err       error
}

// failingEnvServer answers the env Lambda call with an error.
func failingEnvServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message":"boom"}`))
	}))
}

// With no key to be had, launching the agent is pointless — the endpoint
// refuses every request — so the command stops, before it has rewritten the
// harness config, and says what to do about it.
func TestApplyBeforeLaunch_FailsWhenNoKeyCanBeHad(t *testing.T) {
	home := isolateConfig(t)
	stubAWSEnv(t)
	t.Setenv("OPENAI_API_KEY", "")

	server := failingEnvServer(t)
	defer server.Close()
	dir := remoteOutfitDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")

	h, _ := harness.Lookup("opencode")
	var res outfitSelectionResult
	captureStderr(t, func() {
		captureStdout(t, func() {
			res.selection, res.envDir, res.resp, res.err = applyBeforeLaunch(
				outfitPathFlag{set: true, path: dir}, "", h, nil)
		})
	})
	if res.err == nil {
		t.Fatal("a launch that cannot authenticate should fail")
	}
	for _, want := range []string{"could not fetch the API key for dev-1", "outfit remote start dev-1", "OPENAI_API_KEY"} {
		if !strings.Contains(res.err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, res.err)
		}
	}
	// It failed before the apply, so the harness config is untouched.
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Errorf("the config was written by a launch that then failed (stat: %v)", err)
	}
}

// A key the launch can supply itself makes the fetch a convenience, so its
// failure is reported and the launch goes ahead.
func TestApplyBeforeLaunch_CarriesOnWhenTheKeyIsAlreadySet(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-exported")

	server := failingEnvServer(t)
	defer server.Close()
	dir := remoteOutfitDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")

	h, _ := harness.Lookup("opencode")
	var res outfitSelectionResult
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			res.selection, res.envDir, res.resp, res.err = applyBeforeLaunch(
				outfitPathFlag{set: true, path: dir}, "", h, nil)
		})
	})
	if res.err != nil {
		t.Fatalf("an exported key should carry the launch through a failed fetch: %v", res.err)
	}
	if res.resp != nil {
		t.Errorf("a failed fetch should yield no response, got %+v", res.resp)
	}
	if !strings.Contains(stderr, "could not fetch the API key for dev-1") {
		t.Errorf("the failed fetch was not reported:\n%s", stderr)
	}
	if strings.Contains(stdout, "no API key was set") {
		t.Errorf("the exported key is the one that will be used, so nothing is missing:\n%s", stdout)
	}
}

// An ENV instruction overrides everything in the launched agent's environment,
// so a key set there counts as one the launch can supply.
func TestApplyBeforeLaunch_CountsAnEnvInstructionAsTheKey(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	t.Setenv("OPENAI_API_KEY", "")

	server := failingEnvServer(t)
	defer server.Close()
	dir := remoteOutfitDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")
	mustWrite(t, filepath.Join(dir, "Outfit"),
		"PROVIDER llamacpp\nALIAS q3\nREMOTE dev-1\nENV OPENAI_API_KEY=sk-from-outfit\n")

	h, _ := harness.Lookup("opencode")
	var res outfitSelectionResult
	captureStderr(t, func() {
		captureStdout(t, func() {
			res.selection, res.envDir, res.resp, res.err = applyBeforeLaunch(
				outfitPathFlag{set: true, path: dir}, "", h, nil)
		})
	})
	if res.err != nil {
		t.Fatalf("an ENV key should carry the launch through a failed fetch: %v", res.err)
	}
}

// The fetch crosses the network, so it says it is happening.
func TestApplyBeforeLaunch_AnnouncesTheFetch(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	t.Setenv("OPENAI_API_KEY", "")

	server := envServer(t)
	defer server.Close()
	dir := remoteOutfitDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")

	h, _ := harness.Lookup("opencode")
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if _, _, _, err := applyBeforeLaunch(outfitPathFlag{set: true, path: dir}, "", h, nil); err != nil {
				t.Fatalf("applyBeforeLaunch: %v", err)
			}
		})
	})
	if !strings.Contains(stderr, "Fetching the endpoint's environment from dev-1") {
		t.Errorf("the fetch was not announced:\n%s", stderr)
	}
	if strings.Contains(stdout, "Fetching") {
		t.Errorf("progress belongs on stderr:\n%s", stdout)
	}
}

// An Outfit that names no remote contacts nothing and reports nothing.
func TestApplyBeforeLaunch_LocalOutfitFetchesNothing(t *testing.T) {
	isolateConfig(t)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Outfit"), "PROVIDER llamacpp\nMODEL gemma\n")

	h, _ := harness.Lookup("opencode")
	var res outfitSelectionResult
	stderr := captureStderr(t, func() {
		captureStdout(t, func() {
			res.selection, res.envDir, res.resp, res.err = applyBeforeLaunch(
				outfitPathFlag{set: true, path: dir}, "", h, nil)
		})
	})
	if res.err != nil {
		t.Fatalf("applyBeforeLaunch: %v", res.err)
	}
	if res.resp != nil {
		t.Errorf("a local Outfit should fetch no remote environment, got %+v", res.resp)
	}
	if strings.Contains(stderr, "could not fetch") {
		t.Errorf("nothing was fetched, so nothing should be reported:\n%s", stderr)
	}
}

// remoteLaunchResolver widens a lookup rather than replacing it: an exported
// key or one from the .env is the user's own and still wins.
func TestRemoteLaunchResolver_KeepsTheLocalValue(t *testing.T) {
	base := func(name string) string {
		if name == "OPENAI_API_KEY" {
			return "sk-local"
		}
		return ""
	}
	resolve := remoteLaunchResolver(base, &remote.Response{APIKey: "sk-remote"})
	if got := resolve("OPENAI_API_KEY"); got != "sk-local" {
		t.Errorf("resolved %q, want the local value to win", got)
	}
	if got := resolve("SOMETHING_ELSE"); got != "" {
		t.Errorf("resolved an unrelated variable as %q", got)
	}
	// With nothing fetched the lookup is the base one, unchanged.
	if got := remoteLaunchResolver(base, nil)("OPENAI_API_KEY"); got != "sk-local" {
		t.Errorf("resolved %q with no remote response, want sk-local", got)
	}
}

// `outfit remote env` is meant to be eval'd, so nothing but export lines may
// reach stdout — an alias, which is reported, is the case that broke it.
func TestRemoteEnv_StdoutIsEvalSafe(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	server := envServer(t)
	defer server.Close()
	dir := remoteOutfitDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")
	captureStdout(t, func() {
		if err := cmdAlias([]string{dir}); err != nil {
			t.Fatalf("cmdAlias: %v", err)
		}
	})
	t.Chdir(t.TempDir()) // somewhere the alias is the only way to find the Outfit

	var stdout string
	captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if err := cmdRemoteEnv([]string{"q3"}); err != nil {
				t.Fatalf("cmdRemoteEnv: %v", err)
			}
		})
	})
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line != "" && !strings.HasPrefix(line, "export ") {
			t.Errorf("stdout line %q would break `eval $(outfit remote env)`:\n%s", line, stdout)
		}
	}
	if !strings.Contains(stdout, "export OPENAI_API_KEY=sk-remote") {
		t.Errorf("the exports were not printed:\n%s", stdout)
	}
}
