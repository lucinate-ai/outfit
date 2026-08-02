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
	bootstrapPreflightFn = func() error { return nil }
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
		renderBootstrapPlan("1", "us-east-1", []string{"llamacpp", "vllm"}, "v1.10.0", "/tmp/cdk/v1.10.0", false)
	})
	for _, want := range []string{"AWS account:  1\n", "us-east-1", "llamacpp, vllm", "Image Builder", "Cost:", "pnpm run deploy"} {
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
			"pnpm install", "pnpm cdk bootstrap", "pnpm deploy:image",
			"pnpm bake llamacpp", "pnpm bake vllm", "pnpm run deploy",
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

	t.Run("missing tooling fails", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir()) // no node/pnpm
		if err := checkNodeAndPnpm(); err == nil || !strings.Contains(err.Error(), "pnpm") {
			t.Errorf("empty PATH should fail naming pnpm, got %v", err)
		}
	})
}
