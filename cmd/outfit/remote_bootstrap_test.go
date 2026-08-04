package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/lucinate-ai/outfit/internal/remote"
)

type recordedStep struct {
	argv []string
	dir  string
}

// stubBootstrapSeams wires the bootstrap package seams to hermetic fakes: no
// AWS, no network, no pnpm. It returns a pointer to the recorded command list.
func stubBootstrapSeams(t *testing.T, alreadyDeployed bool) *[]recordedStep {
	t.Helper()
	var steps []recordedStep

	origRun, origDl := bootstrapRunStep, bootstrapDownloadFn
	origAcct, origStack := bootstrapAccountFn, bootstrapStackDeployedFn
	origPre := bootstrapPreflightFn
	t.Cleanup(func() {
		bootstrapRunStep, bootstrapDownloadFn = origRun, origDl
		bootstrapAccountFn, bootstrapStackDeployedFn = origAcct, origStack
		bootstrapPreflightFn = origPre
	})

	bootstrapRunStep = func(_ context.Context, _ string, argv []string, workDir string) error {
		steps = append(steps, recordedStep{argv: argv, dir: workDir})
		return nil
	}
	bootstrapDownloadFn = func(_ context.Context, _, destDir string) error {
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destDir, "package.json"), []byte(`{"name":"cloud-vm-llm"}`), 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destDir, "cdk.json"), []byte(`{"context":{}}`), 0o644)
	}
	bootstrapAccountFn = func(context.Context, aws.Config) (string, error) { return "1", nil }
	bootstrapStackDeployedFn = func(context.Context, aws.Config, string) (bool, error) { return alreadyDeployed, nil }
	bootstrapPreflightFn = func(name string, _ bool) (packageManager, error) { return managerByName(name), nil }
	return &steps
}

func withStdin(t *testing.T, input string) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(f, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	fh, err := os.Open(f)
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = fh
	t.Cleanup(func() { os.Stdin = orig; fh.Close() })
}

func TestBootstrap_EnvAndCdkWrites(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("ALLOWED_CIDR=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := upsertEnvVar(filepath.Join(dir, ".env"), "HF_TOKEN", "hf_secret"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(data), "HF_TOKEN=hf_secret") {
		t.Errorf(".env missing token:\n%s", data)
	}
	fi, _ := os.Stat(filepath.Join(dir, ".env"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %v, want 0600", fi.Mode().Perm())
	}

	if err := os.WriteFile(filepath.Join(dir, "cdk.json"), []byte(`{"app":"x","context":{"//":"note"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setCdkContext(dir, "runners", "llamacpp,vllm"); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	raw, _ := os.ReadFile(filepath.Join(dir, "cdk.json"))
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	ctxObj := doc["context"].(map[string]any)
	if ctxObj["runners"] != "llamacpp,vllm" || ctxObj["//"] != "note" || doc["app"] != "x" {
		t.Errorf("cdk.json not merged as expected: %v", doc)
	}
}

func TestBootstrap_PlanOutput(t *testing.T) {
	out := captureStderr(t, func() {
		renderBootstrapPlan("1", "us-east-1", []string{"llamacpp", "vllm"}, "v1.10.0", "/tmp/cdk/v1.10.0", false, pnpmManager)
	})
	for _, want := range []string{"AWS account:  1\n", "us-east-1", "llamacpp, vllm", "Image Builder", "Cost:", "pnpm run deploy\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "$") {
		t.Errorf("plan should carry no dollar figures:\n%s", out)
	}
}

func TestBootstrap_DryRunRunsNothing(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	steps := stubBootstrapSeams(t, false)
	if err := cmdRemoteBootstrap([]string{"--region", "us-east-1", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	if len(*steps) != 0 {
		t.Errorf("--dry-run should run no commands, got %v", *steps)
	}
}

func TestBootstrap_ConfirmGate(t *testing.T) {
	t.Run("declining runs nothing", func(t *testing.T) {
		isolateConfig(t)
		stubAWSEnv(t)
		steps := stubBootstrapSeams(t, false)
		withStdin(t, "n\n")
		if err := cmdRemoteBootstrap([]string{"--region", "us-east-1"}); err != nil {
			t.Fatal(err)
		}
		if len(*steps) != 0 {
			t.Errorf("declining should run nothing, got %v", *steps)
		}
	})

	t.Run("confirming runs the sequence", func(t *testing.T) {
		isolateConfig(t)
		stubAWSEnv(t)
		steps := stubBootstrapSeams(t, false)
		if err := cmdRemoteBootstrap([]string{"--region", "us-east-1", "--yes"}); err != nil {
			t.Fatal(err)
		}
		var got []string
		cdkDir := remote.SourceDir(remote.ResolveRef(version, ""))
		for _, s := range *steps {
			got = append(got, strings.Join(s.argv, " "))
			if s.dir != cdkDir {
				t.Errorf("step %v ran in %q, want %q", s.argv, s.dir, cdkDir)
			}
		}
		want := []string{
			"pnpm install", "pnpm run cdk bootstrap", "pnpm run deploy:image",
			"pnpm run bake llamacpp", "pnpm run bake vllm", "pnpm run deploy",
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("commands = %v, want %v", got, want)
		}
	})

	t.Run("already bootstrapped skips the bake", func(t *testing.T) {
		isolateConfig(t)
		stubAWSEnv(t)
		steps := stubBootstrapSeams(t, true) // stack already deployed
		if err := cmdRemoteBootstrap([]string{"--region", "us-east-1", "--yes"}); err != nil {
			t.Fatal(err)
		}
		for _, s := range *steps {
			if strings.Contains(strings.Join(s.argv, " "), "bake") {
				t.Errorf("re-run should not bake without --force-bake, got %v", s.argv)
			}
		}
	})
}

func TestBootstrap_PreflightAndRunners(t *testing.T) {
	t.Run("bad runner rejected before anything", func(t *testing.T) {
		isolateConfig(t)
		stubAWSEnv(t)
		steps := stubBootstrapSeams(t, false)
		if err := cmdRemoteBootstrap([]string{"--runners", "bogus", "--yes"}); err == nil {
			t.Fatal("expected an error for a bad runner")
		}
		if len(*steps) != 0 {
			t.Errorf("bad runner should run nothing, got %v", *steps)
		}
	})

	t.Run("missing tooling fails naming both managers", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir()) // no node/pnpm/npm
		_, err := checkNodeAndPackageManager("", false)
		if err == nil || !strings.Contains(err.Error(), "pnpm") || !strings.Contains(err.Error(), "npm") {
			t.Errorf("empty PATH should fail naming pnpm and npm, got %v", err)
		}
	})

	t.Run("pinned manager not installed fails naming it", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir()) // npm not present
		_, err := checkNodeAndPackageManager("npm", true)
		if err == nil || !strings.Contains(err.Error(), "npm") {
			t.Errorf("pinned npm absent should fail naming npm, got %v", err)
		}
	})
}

// fakeExecOnPath writes an empty executable named `name` into a fresh dir and
// puts that dir on PATH (only), so exec.LookPath finds it and nothing else.
func fakeExecOnPath(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// fakeNode writes a fake `node` (echoing version) and optionally `pnpm` into a
// fresh dir that becomes the only PATH entry, so the preflight's Node-version
// check runs against a known value without a real toolchain.
func fakeNode(t *testing.T, version string, alsoPnpm bool) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte("#!/bin/sh\necho "+version+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if alsoPnpm {
		if err := os.WriteFile(filepath.Join(dir, "pnpm"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestCheckNodeAndPackageManager_NodeVersion(t *testing.T) {
	t.Run("modern node with a manager passes", func(t *testing.T) {
		fakeNode(t, "v22.3.0", true)
		pm, err := checkNodeAndPackageManager("", false)
		if err != nil || pm.name != "pnpm" {
			t.Errorf("modern node + pnpm should pass, got pm=%q err=%v", pm.name, err)
		}
	})
	t.Run("old node is rejected", func(t *testing.T) {
		fakeNode(t, "v18.19.0", true)
		if _, err := checkNodeAndPackageManager("", false); err == nil || !strings.Contains(err.Error(), "Node 22") {
			t.Errorf("old node should be rejected, got %v", err)
		}
	})
	t.Run("missing node is rejected", func(t *testing.T) {
		fakeExecOnPath(t, "pnpm") // manager present, no node
		if _, err := checkNodeAndPackageManager("", false); err == nil || !strings.Contains(err.Error(), "node not found") {
			t.Errorf("missing node should be rejected, got %v", err)
		}
	})
}

func TestRunBootstrapSequence_SkipsSatisfiedSteps(t *testing.T) {
	var steps []recordedStep
	orig := bootstrapRunStep
	t.Cleanup(func() { bootstrapRunStep = orig })
	bootstrapRunStep = func(_ context.Context, _ string, argv []string, _ string) error {
		steps = append(steps, recordedStep{argv: argv})
		return nil
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	// node_modules present + already bootstrapped, no force-bake: install and bake skip.
	if err := runBootstrapSequence(context.Background(), dir, []string{"llamacpp"}, true, false, npmManager); err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		joined := strings.Join(s.argv, " ")
		if strings.Contains(joined, "install") {
			t.Errorf("node_modules present should skip install, got %v", s.argv)
		}
		if strings.Contains(joined, "bake") {
			t.Errorf("alreadyBootstrapped should skip bake, got %v", s.argv)
		}
	}
}

func TestPackageManager_Script(t *testing.T) {
	cases := []struct {
		pm   packageManager
		args []string
		want []string
	}{
		{pnpmManager, []string{"cdk", "bootstrap"}, []string{"pnpm", "run", "cdk", "bootstrap"}},
		{pnpmManager, []string{"deploy"}, []string{"pnpm", "run", "deploy"}},
		{npmManager, []string{"cdk", "bootstrap"}, []string{"npm", "run", "cdk", "--", "bootstrap"}},
		{npmManager, []string{"deploy"}, []string{"npm", "run", "deploy"}},
	}
	for _, c := range cases {
		got := c.pm.script(c.args[0], c.args[1:]...)
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%s.script(%v) = %v, want %v", c.pm.name, c.args, got, c.want)
		}
	}
}

func TestDetectPackageManager_PrefersPnpm(t *testing.T) {
	t.Run("npm only when pnpm absent", func(t *testing.T) {
		fakeExecOnPath(t, "npm")
		pm, ok := detectPackageManager()
		if !ok || pm.name != "npm" {
			t.Errorf("want npm detected, got %q ok=%v", pm.name, ok)
		}
	})
	t.Run("pnpm wins when both present", func(t *testing.T) {
		fakeExecOnPath(t, "pnpm", "npm")
		pm, ok := detectPackageManager()
		if !ok || pm.name != "pnpm" {
			t.Errorf("want pnpm detected, got %q ok=%v", pm.name, ok)
		}
	})
	t.Run("neither present", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, ok := detectPackageManager(); ok {
			t.Errorf("empty PATH should detect nothing")
		}
	})
}

func TestResolvePackageManagerName_Precedence(t *testing.T) {
	t.Run("flag beats env", func(t *testing.T) {
		t.Setenv(packageManagerEnv, "pnpm")
		name, pinned, err := resolvePackageManagerName("npm")
		if err != nil || name != "npm" || !pinned {
			t.Errorf("flag should win: name=%q pinned=%v err=%v", name, pinned, err)
		}
	})
	t.Run("env used when no flag", func(t *testing.T) {
		t.Setenv(packageManagerEnv, "npm")
		name, pinned, err := resolvePackageManagerName("")
		if err != nil || name != "npm" || !pinned {
			t.Errorf("env should apply: name=%q pinned=%v err=%v", name, pinned, err)
		}
	})
	t.Run("neither set auto-detects", func(t *testing.T) {
		t.Setenv(packageManagerEnv, "")
		name, pinned, err := resolvePackageManagerName("")
		if err != nil || name != "" || pinned {
			t.Errorf("unset should auto-detect: name=%q pinned=%v err=%v", name, pinned, err)
		}
	})
	t.Run("invalid flag rejected", func(t *testing.T) {
		if _, _, err := resolvePackageManagerName("yarn"); err == nil || !strings.Contains(err.Error(), "pnpm or npm") {
			t.Errorf("invalid flag should be rejected, got %v", err)
		}
	})
	t.Run("invalid env rejected", func(t *testing.T) {
		t.Setenv(packageManagerEnv, "bun")
		if _, _, err := resolvePackageManagerName(""); err == nil || !strings.Contains(err.Error(), packageManagerEnv) {
			t.Errorf("invalid env should be rejected, got %v", err)
		}
	})
}

func TestBootstrap_NpmOverrideDrivesNpmCommands(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	steps := stubBootstrapSeams(t, false)
	if err := cmdRemoteBootstrap([]string{"--region", "us-east-1", "--yes", "--package-manager", "npm"}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range *steps {
		got = append(got, strings.Join(s.argv, " "))
	}
	want := []string{
		"npm install", "npm run cdk -- bootstrap", "npm run deploy:image",
		"npm run bake -- llamacpp", "npm run bake -- vllm", "npm run deploy",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands = %v, want %v", got, want)
	}
}
