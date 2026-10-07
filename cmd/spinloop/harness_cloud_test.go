package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spinloop-ai/spinloop/internal/cloud"
	"github.com/spinloop-ai/spinloop/internal/fleet"
	"github.com/spinloop-ai/spinloop/internal/harness"
	"github.com/spinloop-ai/spinloop/internal/spinloop"
)

// cloudSpinloopDir registers an environment served by envURL and writes a
// Spinloop in a fresh directory, returning the directory. baseURL is the
// endpoint address the environment records — a public one, so the apply has a
// key to warn about when it has none.
func cloudSpinloopDir(t *testing.T, name, envURL, baseURL string) string {
	t.Helper()
	registerEnv(t, name, cloud.Config{
		StartURL:    envURL,
		StopURL:     envURL,
		EnvURL:      envURL,
		BaseURL:     baseURL,
		Region:      "eu-west-1",
		Environment: name,
	})
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Spinloop"), "PROVIDER llamacpp\nALIAS q3\n")
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

// deployedEnvServer answers the env Lambda call the way an upgraded control
// plane does for an environment with a model deployed to it: base_url/api_key
// alongside the deploy-config facts a bare `spinloop harness --env` needs to
// configure the harness with no Spinloop at all.
func deployedEnvServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"base_url": "http://198.51.100.1:8000/v1",
			"api_key": "sk-remote",
			"deployed": true,
			"runner": "llamacpp",
			"modelId": "org/model",
			"servedName": "q3",
			"contextSize": 32768
		}`))
	}))
}

// The key of a cloud endpoint is known only to the control plane, and
// `spinloop harness` fetches it and hands it to the agent it launches. The apply
// that precedes the launch must therefore not warn that no key is set: it is
// about to be.
func TestApplyBeforeLaunch_RemoteKeySilencesTheMissingKeyWarning(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	t.Setenv("OPENAI_API_KEY", "")

	server := envServer(t)
	defer server.Close()
	dir := cloudSpinloopDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")

	h, _ := harness.Lookup("opencode")
	var sel spinloopSelectionResult
	out := captureStdout(t, func() {
		sel.selection, sel.envDir, sel.resp, _, sel.err = applyBeforeLaunch(
			spinloopPathFlag{set: true, path: dir}, "", h, nil, routeOptions{envName: "dev-1"})
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
	env := harnessEnv("", cloudLaunchResolver(func(string) string { return "" }, sel.resp), sel.resp)
	if got, _ := envValue(env, "OPENAI_API_KEY"); got != "sk-remote" {
		t.Errorf("launched agent's OPENAI_API_KEY = %q, want sk-remote", got)
	}
}

// TestHarness_EnvFlagAfterTheAlias is the regression guard for the ordering
// bug where --env was silently forwarded to the harness, instead of being
// consumed by spinloop, whenever it followed the Spinloop-naming alias
// (`spinloop harness dev-3 --env dev-1 ...`, the order the flag's own name
// suggests). --env must be honoured, and dropped from what is forwarded, no
// matter which side of the alias it is on.
func TestHarness_EnvFlagAfterTheAlias(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	// A base URL the machine's environment carries would otherwise reach
	// the stub agent and shadow the one the Spinloop names.
	t.Setenv("OPENAI_BASE_URL", "")

	server := envServer(t)
	defer server.Close()
	dir := cloudSpinloopDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")
	captureStdout(t, func() {
		if err := cmdAlias([]string{"-n", "dev-3", dir}); err != nil {
			t.Fatalf("cmdAlias: %v", err)
		}
	})

	argsFile := filepath.Join(t.TempDir(), "args")
	envFile := filepath.Join(t.TempDir(), "env")
	stubDir := t.TempDir()
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\nenv > " + envFile + "\n"
	if err := os.WriteFile(filepath.Join(stubDir, "opencode"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	captureStderr(t, func() {
		captureStdout(t, func() {
			if err := cmdOpen([]string{"dev-3", "--env", "dev-1", "--prompt", "hello"}); err != nil {
				t.Fatalf("cmdOpen: %v", err)
			}
		})
	})

	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("harness was not launched: %v", err)
	}
	if strings.TrimSpace(string(got)) != "--prompt\nhello" {
		t.Errorf("forwarded args = %q, want \"--prompt\\nhello\" (--env consumed, not forwarded)", got)
	}

	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("could not read the launched agent's env: %v", err)
	}
	if v, _ := envValue(strings.Split(string(env), "\n"), "OPENAI_API_KEY"); v != "sk-remote" {
		t.Errorf("launched agent's OPENAI_API_KEY = %q, want the fetched cloud key", v)
	}
	if v, _ := envValue(strings.Split(string(env), "\n"), "OPENAI_BASE_URL"); v != "http://198.51.100.1:8000/v1" {
		t.Errorf("launched agent's OPENAI_BASE_URL = %q, want the cloud endpoint's address", v)
	}
}

// TestHarness_BareEnvAutoConfiguresAndLaunches is the end-to-end check for the
// UX this change adds: `spinloop harness --env <name>` with no Spinloop at
// all configures the harness from what is deployed and launches it, trailing
// args forwarded exactly as they are for a Spinloop-driven launch.
func TestHarness_BareEnvAutoConfiguresAndLaunches(t *testing.T) {
	home := isolateConfig(t)
	stubAWSEnv(t)
	t.Setenv("OPENAI_API_KEY", "")

	server := deployedEnvServer(t)
	defer server.Close()
	registerEnv(t, "dev-3", cloud.Config{
		StartURL: server.URL, StopURL: server.URL, EnvURL: server.URL,
		BaseURL: "http://198.51.100.1:8000/v1", Region: "eu-west-1", Environment: "dev-3",
	})

	forwarded, out := launchedArgs(t, []string{"--env", "dev-3", "--prompt", "hello"})
	if forwarded != "--prompt\nhello" {
		t.Errorf("forwarded args = %q, want \"--prompt\\nhello\"", forwarded)
	}
	if !strings.Contains(out, "Configuring from what is deployed to dev-3") {
		t.Errorf("expected the auto-configure to be reported:\n%s", out)
	}

	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "dev-3/q3" {
		t.Errorf("default model = %v, want dev-3/q3 (from the deploy-config)", m["model"])
	}
}

// TestHarness_AppliedSpinloopStillWinsOverDeployConfig guards the precedence
// rule: applying a Spinloop alongside --env uses the Spinloop's own values,
// never the environment's deploy-config, exactly as a hand-written BASEURL
// already wins over the environment's registered address.
func TestHarness_AppliedSpinloopStillWinsOverDeployConfig(t *testing.T) {
	home := isolateConfig(t)
	stubAWSEnv(t)
	t.Setenv("OPENAI_API_KEY", "")

	server := deployedEnvServer(t) // deploy-config's servedName is "q3"
	defer server.Close()
	dir := cloudSpinloopDir(t, "dev-3", server.URL, "http://198.51.100.1:8000/v1")
	mustWrite(t, filepath.Join(dir, "Spinloop"), "PROVIDER llamacpp\nALIAS from-spinloop\n")

	forwarded, _ := launchedArgs(t, []string{dir, "--env", "dev-3", "--", "run"})
	if forwarded != "run" {
		t.Errorf("forwarded args = %q, want \"run\"", forwarded)
	}

	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "dev-3/from-spinloop" {
		t.Errorf("default model = %v, want dev-3/from-spinloop (the Spinloop's ALIAS, not the deploy-config's servedName)", m["model"])
	}
}

// spinloopSelectionResult carries the applyBeforeLaunch results these tests read
// out of the
// output-capturing closure.
type spinloopSelectionResult struct {
	selection spinloop.Selection
	envDir    string
	resp      *cloud.Response
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
	dir := cloudSpinloopDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")

	h, _ := harness.Lookup("opencode")
	var res spinloopSelectionResult
	captureStderr(t, func() {
		captureStdout(t, func() {
			res.selection, res.envDir, res.resp, _, res.err = applyBeforeLaunch(
				spinloopPathFlag{set: true, path: dir}, "", h, nil, routeOptions{envName: "dev-1"})
		})
	})
	if res.err == nil {
		t.Fatal("a launch that cannot authenticate should fail")
	}
	for _, want := range []string{"could not fetch the API key for dev-1", "spinloop cloud start --env dev-1", "OPENAI_API_KEY"} {
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
	dir := cloudSpinloopDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")

	h, _ := harness.Lookup("opencode")
	var res spinloopSelectionResult
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			res.selection, res.envDir, res.resp, _, res.err = applyBeforeLaunch(
				spinloopPathFlag{set: true, path: dir}, "", h, nil, routeOptions{envName: "dev-1"})
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
	dir := cloudSpinloopDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")
	mustWrite(t, filepath.Join(dir, "Spinloop"),
		"PROVIDER llamacpp\nALIAS q3\nENV OPENAI_API_KEY=sk-from-spinloop\n")

	h, _ := harness.Lookup("opencode")
	var res spinloopSelectionResult
	captureStderr(t, func() {
		captureStdout(t, func() {
			res.selection, res.envDir, res.resp, _, res.err = applyBeforeLaunch(
				spinloopPathFlag{set: true, path: dir}, "", h, nil, routeOptions{envName: "dev-1"})
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
	dir := cloudSpinloopDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")

	h, _ := harness.Lookup("opencode")
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if _, _, _, _, err := applyBeforeLaunch(spinloopPathFlag{set: true, path: dir}, "", h, nil, routeOptions{envName: "dev-1"}); err != nil {
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

// A Spinloop that names no cloud contacts nothing and reports nothing.
func TestApplyBeforeLaunch_LocalSpinloopFetchesNothing(t *testing.T) {
	isolateConfig(t)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Spinloop"), "PROVIDER llamacpp\nMODEL gemma\n")

	h, _ := harness.Lookup("opencode")
	var res spinloopSelectionResult
	stderr := captureStderr(t, func() {
		captureStdout(t, func() {
			res.selection, res.envDir, res.resp, _, res.err = applyBeforeLaunch(
				spinloopPathFlag{set: true, path: dir}, "", h, nil, routeOptions{})
		})
	})
	if res.err != nil {
		t.Fatalf("applyBeforeLaunch: %v", res.err)
	}
	if res.resp != nil {
		t.Errorf("a local Spinloop should fetch no cloud environment, got %+v", res.resp)
	}
	if strings.Contains(stderr, "could not fetch") {
		t.Errorf("nothing was fetched, so nothing should be reported:\n%s", stderr)
	}
}

// --env and a fleet are two answers to one question — where the model is
// served from — so a launch that states both fails, naming both.
func TestApplyBeforeLaunch_EnvAndFleetConflict(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	server := envServer(t)
	defer server.Close()
	dir := cloudSpinloopDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")
	fleetDir := writeFleetFile(t, "nodes:\n  - name: gpu-a\n    kind: cloud\n")

	h, _ := harness.Lookup("opencode")
	_, _, _, _, err := applyBeforeLaunch(spinloopPathFlag{set: true, path: dir}, "", h, nil,
		routeOptions{envName: "dev-1", fleetPath: filepath.Join(fleetDir, "fleet.yaml")})
	if err == nil {
		t.Fatal("a launch stating both --env and a fleet should fail")
	}
	for _, want := range []string{"--env dev-1", "fleet.yaml", "so state one"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got %v", want, err)
		}
	}
}

// A bare `spinloop harness --env <name>` — no Spinloop at all — configures
// the harness from what the environment reports is deployed to it.
func TestApplyFromEnvironment_ConfiguresFromDeployConfig(t *testing.T) {
	home := isolateConfig(t)
	stubAWSEnv(t)
	t.Setenv("OPENAI_API_KEY", "")

	server := deployedEnvServer(t)
	defer server.Close()
	registerEnv(t, "dev-3", cloud.Config{
		StartURL: server.URL, StopURL: server.URL, EnvURL: server.URL,
		BaseURL: "http://198.51.100.1:8000/v1", Region: "eu-west-1", Environment: "dev-3",
	})

	h, _ := harness.Lookup("opencode")
	var sel spinloop.Selection
	var resp *cloud.Response
	var choice *fleet.Choice
	var err error
	captureStderr(t, func() {
		captureStdout(t, func() {
			sel, _, resp, choice, err = applyFromEnvironment("", h, routeOptions{envName: "dev-3"})
		})
	})
	if err != nil {
		t.Fatalf("applyFromEnvironment: %v", err)
	}
	if choice != nil {
		t.Errorf("no fleet routing should have happened, got %+v", choice)
	}
	if resp == nil || resp.APIKey != "sk-remote" {
		t.Fatalf("the endpoint's key was not fetched: %+v", resp)
	}
	if sel.Provider != "llamacpp" || sel.Alias != "q3" || sel.Context != "32768" {
		t.Errorf("selection = %+v, want provider llamacpp, alias q3, context 32768", sel)
	}

	// The written config is exactly what an equivalent Spinloop
	// (`PROVIDER llamacpp\nALIAS q3\nCONTEXT 32768`) applied with --env dev-3
	// would have produced.
	m := readConfigMap(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if m["model"] != "dev-3/q3" {
		t.Errorf("default model = %v, want dev-3/q3", m["model"])
	}
}

// An unregistered runner value could never actually reach a deploy-config —
// runnerFor already refuses anything but llamacpp/vllm at deploy time — but
// the launch must still fail rather than trust one, should it ever happen.
func TestApplyFromEnvironment_UnrecognisedRunnerFails(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"base_url":"http://198.51.100.1:8000/v1","api_key":"sk-remote","deployed":true,"runner":"bogus","servedName":"q3"}`))
	}))
	defer server.Close()
	registerEnv(t, "dev-3", cloud.Config{
		StartURL: server.URL, StopURL: server.URL, EnvURL: server.URL, Region: "eu-west-1", Environment: "dev-3",
	})

	h, _ := harness.Lookup("opencode")
	captureStderr(t, func() {
		captureStdout(t, func() {
			if _, _, _, _, err := applyFromEnvironment("", h, routeOptions{envName: "dev-3"}); err == nil {
				t.Fatal("an unrecognised runner should fail the launch")
			}
		})
	})
}

// With nothing deployed to the environment, a bare `--env` launch fails
// naming the environment and how to fix it, rather than launching an
// unconfigured harness or falling through to applySelection's generic error.
func TestApplyFromEnvironment_FailsWithNothingDeployed(t *testing.T) {
	home := isolateConfig(t)
	stubAWSEnv(t)

	server := envServer(t) // base_url/api_key only — no deploy-config fields
	defer server.Close()
	registerEnv(t, "dev-3", cloud.Config{
		StartURL: server.URL, StopURL: server.URL, EnvURL: server.URL, Region: "eu-west-1", Environment: "dev-3",
	})

	h, _ := harness.Lookup("opencode")
	var err error
	captureStderr(t, func() {
		captureStdout(t, func() {
			_, _, _, _, err = applyFromEnvironment("", h, routeOptions{envName: "dev-3"})
		})
	})
	if err == nil {
		t.Fatal("a launch with nothing to auto-configure from should fail")
	}
	for _, want := range []string{"dev-3", "cloud deploy", "cloud bootstrap"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Errorf("no config should have been written (stat: %v)", err)
	}
}

// cloudLaunchResolver widens a lookup rather than replacing it: an exported
// key or one from the .env is the user's own and still wins.
func TestCloudLaunchResolver_KeepsTheLocalValue(t *testing.T) {
	base := func(name string) string {
		if name == "OPENAI_API_KEY" {
			return "sk-local"
		}
		return ""
	}
	resolve := cloudLaunchResolver(base, &cloud.Response{APIKey: "sk-remote"})
	if got := resolve("OPENAI_API_KEY"); got != "sk-local" {
		t.Errorf("resolved %q, want the local value to win", got)
	}
	if got := resolve("SOMETHING_ELSE"); got != "" {
		t.Errorf("resolved an unrelated variable as %q", got)
	}
	// With nothing fetched the lookup is the base one, unchanged.
	if got := cloudLaunchResolver(base, nil)("OPENAI_API_KEY"); got != "sk-local" {
		t.Errorf("resolved %q with no cloud response, want sk-local", got)
	}
}

// `spinloop cloud env` is meant to be eval'd, so nothing but export lines may
// reach stdout — an alias, which is reported, is the case that broke it.
func TestCloudEnv_StdoutIsEvalSafe(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	server := envServer(t)
	defer server.Close()
	dir := cloudSpinloopDir(t, "dev-1", server.URL, "http://198.51.100.1:8000/v1")
	captureStdout(t, func() {
		if err := cmdAlias([]string{dir}); err != nil {
			t.Fatalf("cmdAlias: %v", err)
		}
	})
	t.Chdir(t.TempDir()) // somewhere the alias is the only way to find the Spinloop

	var stdout string
	captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if err := cmdCloudEnv([]string{"q3", "--env", "dev-1"}); err != nil {
				t.Fatalf("cmdCloudEnv: %v", err)
			}
		})
	})
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line != "" && !strings.HasPrefix(line, "export ") {
			t.Errorf("stdout line %q would break `eval $(spinloop cloud env)`:\n%s", line, stdout)
		}
	}
	if !strings.Contains(stdout, "export OPENAI_API_KEY=sk-remote") {
		t.Errorf("the exports were not printed:\n%s", stdout)
	}
}
