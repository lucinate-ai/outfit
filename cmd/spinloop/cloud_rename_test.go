package main

import (
	"slices"
	"testing"
)

// The old command group is gone: `remote` fails like any unknown command and
// completion does not offer it.
func TestRemoteCommandGroupIsRemoved(t *testing.T) {
	isolateConfig(t)
	if err := run([]string{"remote", "status"}); err == nil {
		t.Fatal("spinloop remote status should fail: the group was renamed to cloud")
	}
	got, _ := complete(t, "")
	if slices.Contains(got, "remote") {
		t.Errorf("completion still offers remote: %v", got)
	}
	if !slices.Contains(got, "cloud") {
		t.Errorf("completion does not offer cloud: %v", got)
	}
}

// Only SPINLOOP_CLOUD_* is read; the old SPINLOOP_REMOTE_* spelling is ignored.
func TestCloudEnvVarsIgnoreTheOldPrefix(t *testing.T) {
	t.Setenv("SPINLOOP_CLOUD_REGION", "")
	t.Setenv("SPINLOOP_REMOTE_REGION", "eu-west-2")
	if got := newCLIViper().GetString("cloud_region"); got != "" {
		t.Errorf("region = %q from SPINLOOP_REMOTE_REGION, want it ignored", got)
	}
	t.Setenv("SPINLOOP_CLOUD_REGION", "us-east-1")
	if got := newCLIViper().GetString("cloud_region"); got != "us-east-1" {
		t.Errorf("region = %q, want us-east-1 from SPINLOOP_CLOUD_REGION", got)
	}
}
