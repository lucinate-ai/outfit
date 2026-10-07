package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/spinloop-ai/spinloop/internal/cloud"
)

type recordedStep struct {
	argv []string
	dir  string
}

// stubBootstrapSeams wires the shared bootstrap/bake seams to hermetic fakes:
// no AWS, no network, no pnpm. It returns a pointer to the recorded command
// list.
func stubBootstrapSeams(t *testing.T, alreadyDeployed bool) *[]recordedStep {
	t.Helper()
	var steps []recordedStep

	origRun, origDl := runStep, downloadFn
	origAcct, origStack := accountFn, stackDeployedFn
	origPre := preflightFn
	t.Cleanup(func() {
		runStep, downloadFn = origRun, origDl
		accountFn, stackDeployedFn = origAcct, origStack
		preflightFn = origPre
	})

	runStep = func(_ context.Context, _ string, argv []string, workDir string) error {
		steps = append(steps, recordedStep{argv: argv, dir: workDir})
		return nil
	}
	downloadFn = func(_ context.Context, _, destDir string) error {
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destDir, "package.json"), []byte(`{"name":"cloud-vm-llm"}`), 0o644)
	}
	accountFn = func(context.Context, aws.Config) (string, error) { return "1", nil }
	stackDeployedFn = func(context.Context, aws.Config, string) (bool, error) { return alreadyDeployed, nil }
	preflightFn = func(name string, _ bool) (packageManager, error) { return managerByName(name), nil }
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

func TestBootstrap_EnvWrites(t *testing.T) {
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

}

func TestBootstrap_PlanOutput(t *testing.T) {
	out := captureStderr(t, func() {
		renderBootstrapPlan("1", "us-east-1", "v1.10.0", "/tmp/cdk/v1.10.0", false, pnpmManager)
	})
	for _, want := range []string{"AWS account:  1\n", "us-east-1", "Image Builder", "spinloop cloud bake", "Cost:", "pnpm run deploy\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "bake llamacpp") {
		t.Errorf("plan should list no bake commands:\n%s", out)
	}
	if strings.Contains(out, "$") {
		t.Errorf("plan should carry no dollar figures:\n%s", out)
	}
}

func TestBootstrap_DryRunRunsNothing(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	steps := stubBootstrapSeams(t, false)
	if err := cmdCloudBootstrap([]string{"--region", "us-east-1", "--dry-run"}); err != nil {
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
		if err := cmdCloudBootstrap([]string{"--region", "us-east-1"}); err != nil {
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
		if err := cmdCloudBootstrap([]string{"--region", "us-east-1", "--yes"}); err != nil {
			t.Fatal(err)
		}
		var got []string
		cdkDir := must1(cloud.SourceDir(cloud.ResolveRef(version, "")))
		for _, s := range *steps {
			got = append(got, strings.Join(s.argv, " "))
			if s.dir != cdkDir {
				t.Errorf("step %v ran in %q, want %q", s.argv, s.dir, cdkDir)
			}
		}
		want := []string{
			"pnpm install", "pnpm run cdk bootstrap", "pnpm run deploy:image", "pnpm run deploy",
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("commands = %v, want %v", got, want)
		}
	})
}

func TestBootstrap_SignpostsTheBake(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	stubBootstrapSeams(t, false)
	out := captureStdout(t, func() {
		if err := cmdCloudBootstrap([]string{"--region", "us-east-1", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	bakeIdx := strings.Index(out, "spinloop cloud bake")
	deployIdx := strings.Index(out, "spinloop cloud deploy")
	if bakeIdx < 0 || deployIdx < 0 {
		t.Fatalf("success output should signpost bake then deploy:\n%s", out)
	}
	if bakeIdx > deployIdx {
		t.Errorf("bake should be signposted before deploy:\n%s", out)
	}
}

// Bootstrap and bake provision the control plane, so their credential
// preflight resolves the ambient chain only: with no ambient credentials
// there is nothing to fall back to — the stored control-plane key must not
// stand in for the administrator. The loader itself is pinned to that
// behaviour by TestLoadAmbientAWSConfigIgnoresStoredKey in internal/cloud.
func TestLoadCredsRequiresAmbientCredentials(t *testing.T) {
	isolateConfig(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "no-such-file"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "no-such-file"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	if _, err := loadCreds(context.Background(), "us-east-1"); err == nil {
		t.Fatal("loadCreds without ambient credentials should fail, got a config")
	}
}

func TestBootstrap_Preflight(t *testing.T) {
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
	orig := runStep
	t.Cleanup(func() { runStep = orig })
	runStep = func(_ context.Context, _ string, argv []string, _ string) error {
		steps = append(steps, recordedStep{argv: argv})
		return nil
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	// node_modules present: install skips, and no bake step exists at all.
	if err := runBootstrapSequence(context.Background(), dir, npmManager); err != nil {
		t.Fatal(err)
	}
	want := []string{"npm run cdk -- bootstrap", "npm run deploy:image", "npm run deploy"}
	var got []string
	for _, s := range steps {
		got = append(got, strings.Join(s.argv, " "))
		if strings.Contains(strings.Join(s.argv, " "), "install") {
			t.Errorf("node_modules present should skip install, got %v", s.argv)
		}
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands = %v, want %v", got, want)
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
	if err := cmdCloudBootstrap([]string{"--region", "us-east-1", "--yes", "--package-manager", "npm"}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range *steps {
		got = append(got, strings.Join(s.argv, " "))
	}
	want := []string{
		"npm install", "npm run cdk -- bootstrap", "npm run deploy:image", "npm run deploy",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands = %v, want %v", got, want)
	}
}

// Each bootstrap step runs with the CLI's version in its environment, which
// the CDK app stamps onto the control plane.
func TestExecStep_PassesTheControlPlaneVersion(t *testing.T) {
	orig := version
	t.Cleanup(func() { version = orig })
	version = "1.30.0"

	check := []string{"sh", "-c", `test "$` + controlPlaneVersionEnv + `" = "1.30.0"`}
	if err := execStep(context.Background(), "check-env", check, t.TempDir()); err != nil {
		t.Errorf("the step did not see %s=1.30.0: %v", controlPlaneVersionEnv, err)
	}
}
