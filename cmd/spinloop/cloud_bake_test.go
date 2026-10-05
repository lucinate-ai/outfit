package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/spinloop-ai/spinloop/internal/cloud"
)

// stubBakeSeams wires the shared seams to hermetic fakes for the bake flow:
// no AWS, no network, no pnpm. It returns a pointer to the recorded command
// list and to the bake-poll call count.
func stubBakeSeams(t *testing.T, deployed bool) (*[]recordedStep, *int) {
	t.Helper()
	var steps []recordedStep
	bakedCalls := 0

	origRun, origDl := runStep, downloadFn
	origStack, origBaked, origPre := stackDeployedFn, bakedFn, preflightFn
	t.Cleanup(func() {
		runStep, downloadFn = origRun, origDl
		stackDeployedFn, bakedFn, preflightFn = origStack, origBaked, origPre
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
	stackDeployedFn = func(context.Context, aws.Config, string) (bool, error) { return deployed, nil }
	bakedFn = func(context.Context, aws.Config) (map[string]bool, error) {
		bakedCalls++
		return map[string]bool{}, nil
	}
	preflightFn = func(name string, _ bool) (packageManager, error) { return managerByName(name), nil }
	return &steps, &bakedCalls
}

func TestBake_DefaultRunners(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	steps, bakedCalls := stubBakeSeams(t, true)
	if err := cmdCloudBake([]string{"--region", "us-east-1", "--no-wait"}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range *steps {
		got = append(got, strings.Join(s.argv, " "))
	}
	want := []string{"pnpm install", "pnpm run bake llamacpp", "pnpm run bake vllm"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands = %v, want %v", got, want)
	}
	if *bakedCalls != 0 {
		t.Errorf("--no-wait should not poll the bake, polled %d times", *bakedCalls)
	}
}

func TestBake_SingleRunner(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	steps, _ := stubBakeSeams(t, true)
	if err := cmdCloudBake([]string{"llamacpp", "--region", "us-east-1", "--no-wait"}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range *steps {
		got = append(got, strings.Join(s.argv, " "))
	}
	want := []string{"pnpm install", "pnpm run bake llamacpp"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands = %v, want %v", got, want)
	}
}

func TestBake_UnknownRunnerRejected(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	steps, _ := stubBakeSeams(t, true)
	err := cmdCloudBake([]string{"bogus", "--region", "us-east-1"})
	if err == nil {
		t.Fatal("expected an error for a bad runner")
	}
	if !strings.Contains(err.Error(), "llamacpp and vllm") {
		t.Errorf("error should name the accepted runners, got %v", err)
	}
	if len(*steps) != 0 {
		t.Errorf("bad runner should run nothing, got %v", *steps)
	}
}

func TestBake_NoControlPlane(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	steps, _ := stubBakeSeams(t, false)
	err := cmdCloudBake([]string{"--region", "us-east-1"})
	if err == nil {
		t.Fatal("expected an error when the control plane is missing")
	}
	if !strings.Contains(err.Error(), "spinloop cloud bootstrap") {
		t.Errorf("error should point at bootstrap, got %v", err)
	}
	if len(*steps) != 0 {
		t.Errorf("missing control plane should run nothing, got %v", *steps)
	}
}

func TestBake_WaitsByDefault(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	steps, _ := stubBakeSeams(t, true)

	origPoll := bakePollInterval
	bakePollInterval = time.Millisecond
	t.Cleanup(func() { bakePollInterval = origPoll })

	origBaked := bakedFn
	calls := 0
	bakedFn = func(context.Context, aws.Config) (map[string]bool, error) {
		calls++
		if calls == 1 {
			return map[string]bool{"llamacpp": true}, nil
		}
		return map[string]bool{"llamacpp": true, "vllm": true}, nil
	}
	t.Cleanup(func() { bakedFn = origBaked })

	if err := cmdCloudBake([]string{"--region", "us-east-1"}); err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Errorf("default run should poll until both AMIs are available, polled %d times", calls)
	}
	if len(*steps) != 3 {
		t.Errorf("bake should run install plus one bake per runner, got %v", *steps)
	}
}

func TestBake_SkipsInstallWhenPresent(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	steps, _ := stubBakeSeams(t, true)
	cdkDir := must1(cloud.SourceDir(cloud.ResolveRef(version, "")))
	if err := os.MkdirAll(filepath.Join(cdkDir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cmdCloudBake([]string{"--region", "us-east-1", "--no-wait"}); err != nil {
		t.Fatal(err)
	}
	for _, s := range *steps {
		if strings.Contains(strings.Join(s.argv, " "), "install") {
			t.Errorf("node_modules present should skip install, got %v", s.argv)
		}
	}
}

func TestWaitForBake_Timeout(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	stubBakeSeams(t, true)
	cfg, err := loadCreds(context.Background(), "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForBake(ctx, cfg, []string{"llamacpp"}); err == nil {
		t.Fatal("expected a timeout error for a cancelled context")
	}
}

func TestBake_PrunesOtherRefsByDefault(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	stubBakeSeams(t, true)
	root := must1(cloud.SourceRoot())
	stale := filepath.Join(root, "v9.9.9")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cmdCloudBake([]string{"--region", "us-east-1", "--no-wait"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale ref cache should be pruned on success, err=%v", err)
	}
}

func TestBake_ExplicitDirNotPruned(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	stubBakeSeams(t, true)
	root := must1(cloud.SourceRoot())
	stale := filepath.Join(root, "v9.9.9")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cmdCloudBake([]string{"--region", "us-east-1", "--no-wait", "--dir", t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("an explicit --dir run should not prune other refs, err=%v", err)
	}
}

func TestBakeRunnerSlot(t *testing.T) {
	got, _ := bakeRunnerSlot(nil, nil, "")
	if strings.Join(got, ",") != "llamacpp,vllm" {
		t.Errorf("empty line should offer both runners, got %v", got)
	}
	got, _ = bakeRunnerSlot(nil, []string{"llamacpp"}, "")
	if strings.Join(got, ",") != "vllm" {
		t.Errorf("a typed runner should be dropped from the candidates, got %v", got)
	}
	got, _ = bakeRunnerSlot(nil, []string{"llamacpp", "vllm"}, "")
	if len(got) != 0 {
		t.Errorf("both runners typed should leave no candidates, got %v", got)
	}
}
