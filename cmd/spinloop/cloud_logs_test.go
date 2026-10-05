package main

import (
	"context"
	"testing"
	"time"

	"github.com/spinloop-ai/spinloop/internal/cloud"
	"github.com/spinloop-ai/spinloop/internal/fleet"
)

// stubTopLevelLogsFetch substitutes the log store read for the duration of a
// test, handing the stub each query so it can assert on what the command
// asked for. The read goes through fleet.FetchLogsFn rather than an HTTP
// server: CloudWatch is reached through the AWS SDK, not a Function URL.
func stubTopLevelLogsFetch(t *testing.T, fn func(q cloud.LogQuery) (cloud.LogResult, error)) {
	t.Helper()
	prev := fleet.FetchLogsFn
	fleet.FetchLogsFn = func(_ context.Context, _ cloud.Config, q cloud.LogQuery) (cloud.LogResult, error) {
		return fn(q)
	}
	t.Cleanup(func() { fleet.FetchLogsFn = prev })
}

func TestRunnerForAcceptsExactlyTheRunnersWithLogGroups(t *testing.T) {
	for _, runner := range cloud.Runners {
		if _, err := runnerFor(runner); err != nil {
			t.Errorf("deploy rejects runner %q, but its engine log group %q is read: %v",
				runner, cloud.EngineLogGroup(runner), err)
		}
	}
	// The reverse direction: a runner deploy accepts must have a group read for
	// it, or its instances' logs would be unreachable.
	for _, provider := range []string{"llamacpp", "vllm"} {
		runner, err := runnerFor(provider)
		if err != nil {
			t.Fatalf("runnerFor(%q): %v", provider, err)
		}
		found := false
		for _, known := range cloud.Runners {
			if known == runner {
				found = true
			}
		}
		if !found {
			t.Errorf("runner %q can be deployed but has no engine log group in cloud.Runners", runner)
		}
	}
}

// spinloop logs --env defaults to the engine source and an hour window, and
// threads --source/--since/--limit/--instance through to the environment's
// log store query.
func TestTopLevelLogsDefaultsToTheEngineSourceAndAnHourWindow(t *testing.T) {
	isolateConfig(t)
	registerEnv(t, "prod", cloud.Config{
		StartURL: "https://start.lambda-url.eu-west-1.on.aws/",
		StopURL:  "https://stop.lambda-url.eu-west-1.on.aws/",
		Region:   "eu-west-1",
	})

	var got cloud.LogQuery
	stubTopLevelLogsFetch(t, func(q cloud.LogQuery) (cloud.LogResult, error) {
		got = q
		return cloud.LogResult{}, nil
	})

	if err := cmdLogs([]string{"--env", "prod"}); err != nil {
		t.Fatal(err)
	}
	if got.Source != cloud.LogSourceEngine {
		t.Errorf("source = %q, want engine by default", got.Source)
	}
	if got.Limit != 200*bytesPerLineGuess {
		t.Errorf("limit = %d, want the default 200 lines' worth", got.Limit)
	}
	if window := time.Since(got.Start); window < 55*time.Minute || window > 65*time.Minute {
		t.Errorf("window = %s, want about an hour", window)
	}
}

func TestTopLevelLogsPassesTheFlagsThrough(t *testing.T) {
	isolateConfig(t)
	registerEnv(t, "prod", cloud.Config{
		StartURL: "https://start.lambda-url.eu-west-1.on.aws/",
		StopURL:  "https://stop.lambda-url.eu-west-1.on.aws/",
		Region:   "eu-west-1",
	})

	var got cloud.LogQuery
	stubTopLevelLogsFetch(t, func(q cloud.LogQuery) (cloud.LogResult, error) {
		got = q
		return cloud.LogResult{}, nil
	})

	if err := cmdLogs([]string{"--env", "prod", "--source", "boot", "--since", "15m",
		"--limit", "5", "--instance", "i-42"}); err != nil {
		t.Fatal(err)
	}
	if got.Source != cloud.LogSourceBoot {
		t.Errorf("source = %q, want boot", got.Source)
	}
	if got.Instance != "i-42" {
		t.Errorf("instance = %q, want i-42", got.Instance)
	}
	if got.Limit != 5*bytesPerLineGuess {
		t.Errorf("limit = %d, want the 5-line budget", got.Limit)
	}
	if window := time.Since(got.Start); window < 10*time.Minute || window > 20*time.Minute {
		t.Errorf("window = %s, want about 15 minutes", window)
	}
}
