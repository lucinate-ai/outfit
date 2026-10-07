package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/spinloop-ai/spinloop/internal/cloud"
)

// authSeamState is what the stubbed IAM/STS seams answer with and record.
type authSeamState struct {
	userExists bool
	existsErr  error

	accessKeys []string
	accessErr  error

	newKeyID   string
	newSecret  string
	createErr  error
	createdFor string // the user name the create ran for

	deleteErr error
	deleted   []string // the key ids delete ran for

	account        string            // default account for identity calls
	accounts       map[string]string // per resolved access key id, when a key resolves elsewhere
	identity       []string          // the access key id each identity call resolved with
	identityErrFor string            // the access key id whose identity calls return identityErrs
	identityErrs   []error           // returned in order, then the call answers as usual
	existsKey      string            // the access key id the user check resolved with
}

func stubAuthSeams(t *testing.T) *authSeamState {
	t.Helper()
	state := &authSeamState{accounts: map[string]string{}}

	origExists, origKeys := iamUserExistsFn, iamUserAccessKeysFn
	origCreate, origDelete := iamCreateAccessKeyFn, iamDeleteAccessKeyFn
	origIdentity := authCallerIdentityFn
	origBackoff := verifyProbeBackoff
	verifyProbeBackoff = 0
	t.Cleanup(func() {
		iamUserExistsFn, iamUserAccessKeysFn = origExists, origKeys
		iamCreateAccessKeyFn, iamDeleteAccessKeyFn = origCreate, origDelete
		authCallerIdentityFn = origIdentity
		verifyProbeBackoff = origBackoff
	})

	iamUserExistsFn = func(ctx context.Context, cfg aws.Config, _ string) (bool, error) {
		if creds, err := cfg.Credentials.Retrieve(ctx); err == nil {
			state.existsKey = creds.AccessKeyID
		}
		return state.userExists, state.existsErr
	}
	iamUserAccessKeysFn = func(context.Context, aws.Config, string) ([]string, error) {
		return state.accessKeys, state.accessErr
	}
	iamCreateAccessKeyFn = func(_ context.Context, _ aws.Config, user string) (string, string, error) {
		state.createdFor = user
		return state.newKeyID, state.newSecret, state.createErr
	}
	iamDeleteAccessKeyFn = func(_ context.Context, _ aws.Config, _ string, keyID string) error {
		state.deleted = append(state.deleted, keyID)
		return state.deleteErr
	}
	authCallerIdentityFn = func(ctx context.Context, cfg aws.Config) (string, error) {
		creds, err := cfg.Credentials.Retrieve(ctx)
		if err != nil {
			return "", err
		}
		state.identity = append(state.identity, creds.AccessKeyID)
		if state.identityErrFor == creds.AccessKeyID && len(state.identityErrs) > 0 {
			e := state.identityErrs[0]
			state.identityErrs = state.identityErrs[1:]
			return "", e
		}
		if acct, ok := state.accounts[creds.AccessKeyID]; ok {
			return acct, nil
		}
		return state.account, nil
	}
	return state
}

// invalidClientTokenErr is what STS reports while a freshly issued key has
// not propagated yet: the pinned SDK version has no typed error for it, so
// the seam returns the wire message.
var invalidClientTokenErr = errors.New("operation error STS: GetCallerIdentity, https response error StatusCode: 403, api error InvalidClientTokenId: The security token included in the request is invalid.")

// countKey is how many times the named access key id appears in the recorded
// identity calls — i.e. how many attempts ran with that key.
func countKey(calls []string, keyID string) int {
	n := 0
	for _, k := range calls {
		if k == keyID {
			n++
		}
	}
	return n
}

// authStoreEnv points the credential store at a temp file store, so the tests
// never touch the machine's real keystore or config.
func authStoreEnv(t *testing.T) {
	t.Helper()
	isolateConfig(t)
	t.Setenv("SPINLOOP_CONFIG_DIR", "")
	t.Setenv("SPINLOOP_CLOUD_KEYSTORE", "file")
}

// noAmbientCreds pins the process to have no ambient AWS credential at all:
// no env credentials, no profile, no config files, no instance metadata.
func noAmbientCreds(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "no-such-file"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "no-such-file"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

func seedStoredCred(t *testing.T, region, keyID, secret, account string) {
	t.Helper()
	if err := cloud.StoreCredential(cloud.StoredCredential{
		AccessKeyID:     keyID,
		SecretAccessKey: secret,
		Account:         account,
		User:            cloud.ControlPlaneUserName,
		Region:          region,
		StoredAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCloudAuthStoreFirstStore(t *testing.T) {
	authStoreEnv(t)
	stubAWSEnv(t)
	st := stubAuthSeams(t)
	st.userExists = true
	st.accessKeys = []string{"AKIAEXISTSALREADY01"}
	st.newKeyID = "AKIANEWNEWNEWNEW01"
	st.newSecret = "new-secret"
	st.account = "1"
	st.accounts["AKIANEWNEWNEWNEW01"] = "1"

	out := captureStderr(t, func() {
		if err := runCloudAuth(true, false, "ap-southeast-2"); err != nil {
			t.Fatalf("runCloudAuth --store: %v", err)
		}
	})

	cred, ok := cloud.LookupStoredCredential("ap-southeast-2")
	if !ok {
		t.Fatal("the credential was not stored")
	}
	if cred.AccessKeyID != "AKIANEWNEWNEWNEW01" || cred.SecretAccessKey != "new-secret" {
		t.Errorf("stored key = %q/%q", cred.AccessKeyID, cred.SecretAccessKey)
	}
	if cred.Account != "1" || cred.Region != "ap-southeast-2" || cred.User != cloud.ControlPlaneUserName {
		t.Errorf("stored entry = %+v", cred)
	}
	if cred.Store != "file" {
		t.Errorf("store kind = %q, want file", cred.Store)
	}
	if st.existsKey != "AKIATESTTESTTESTTEST" {
		t.Errorf("the user check ran with %q, want the ambient credential", st.existsKey)
	}
	if st.createdFor != cloud.ControlPlaneUserName {
		t.Errorf("the key was created for %q", st.createdFor)
	}
	if len(st.deleted) != 0 {
		t.Errorf("a failed-free store deleted keys: %v", st.deleted)
	}
	for _, want := range []string{"ap-southeast-2", "1", cloud.ControlPlaneUserName, "AKIANEWNEWNEWNEW01", "file"} {
		if !strings.Contains(out, want) {
			t.Errorf("confirmation missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "new-secret") {
		t.Errorf("the confirmation printed the secret:\n%s", out)
	}
}

func TestCloudAuthStoreMissingUser(t *testing.T) {
	authStoreEnv(t)
	stubAWSEnv(t)
	st := stubAuthSeams(t)
	st.userExists = false

	err := runCloudAuth(true, false, "ap-southeast-2")
	if err == nil || !strings.Contains(err.Error(), "spinloop cloud bootstrap") {
		t.Fatalf("error = %v, want it naming spinloop cloud bootstrap", err)
	}
	if _, ok := cloud.LookupStoredCredential("ap-southeast-2"); ok {
		t.Error("a credential was stored despite the missing user")
	}
}

func TestCloudAuthStoreAccountMismatch(t *testing.T) {
	authStoreEnv(t)
	stubAWSEnv(t)
	st := stubAuthSeams(t)
	st.userExists = true
	st.newKeyID = "AKIANEWNEWNEWNEW01"
	st.newSecret = "new-secret"
	st.account = "1"
	st.accounts["AKIANEWNEWNEWNEW01"] = "2"

	err := runCloudAuth(true, false, "ap-southeast-2")
	if err == nil || !strings.Contains(err.Error(), "resolves to account 2") {
		t.Fatalf("error = %v, want the account mismatch", err)
	}
	if _, ok := cloud.LookupStoredCredential("ap-southeast-2"); ok {
		t.Error("a key that resolves elsewhere was stored")
	}
	if len(st.deleted) != 1 || st.deleted[0] != "AKIANEWNEWNEWNEW01" {
		t.Errorf("the stray key was not deleted: %v", st.deleted)
	}
}

func TestCloudAuthStoreRetriesUnpropagatedKey(t *testing.T) {
	authStoreEnv(t)
	stubAWSEnv(t)
	st := stubAuthSeams(t)
	st.userExists = true
	st.newKeyID = "AKIANEWNEWNEWNEW01"
	st.newSecret = "new-secret"
	st.account = "1"
	st.accounts["AKIANEWNEWNEWNEW01"] = "1"
	st.identityErrFor = "AKIANEWNEWNEWNEW01"
	st.identityErrs = []error{invalidClientTokenErr, invalidClientTokenErr}

	out := captureStderr(t, func() {
		if err := runCloudAuth(true, false, "ap-southeast-2"); err != nil {
			t.Fatalf("a key that propagates late must still be stored: %v", err)
		}
	})
	if n := countKey(st.identity, "AKIANEWNEWNEWNEW01"); n != 3 {
		t.Errorf("probe attempts with the new key = %d, want the two failed and the successful", n)
	}
	if len(st.deleted) != 0 {
		t.Errorf("a late-propagating key was deleted on the AWS side: %v", st.deleted)
	}
	if _, ok := cloud.LookupStoredCredential("ap-southeast-2"); !ok {
		t.Error("the credential was not stored")
	}
	if !strings.Contains(out, "not resolvable yet") {
		t.Errorf("the retry did not say what it was doing:\n%s", out)
	}
}

func TestCloudAuthStoreVerifyExhausted(t *testing.T) {
	authStoreEnv(t)
	stubAWSEnv(t)
	st := stubAuthSeams(t)
	st.userExists = true
	st.newKeyID = "AKIANEWNEWNEWNEW01"
	st.newSecret = "new-secret"
	st.account = "1"
	st.identityErrFor = "AKIANEWNEWNEWNEW01"
	st.identityErrs = make([]error, verifyAttempts)
	for i := range st.identityErrs {
		st.identityErrs[i] = invalidClientTokenErr
	}

	err := runCloudAuth(true, false, "ap-southeast-2")
	if err == nil || !strings.Contains(err.Error(), "verifying the new access key") {
		t.Fatalf("error = %v, want the exhausted verification", err)
	}
	if n := countKey(st.identity, "AKIANEWNEWNEWNEW01"); n != verifyAttempts {
		t.Errorf("probe attempts with the new key = %d, want %d", n, verifyAttempts)
	}
	if len(st.deleted) != 1 || st.deleted[0] != "AKIANEWNEWNEWNEW01" {
		t.Errorf("the unverifiable key was not deleted: %v", st.deleted)
	}
	if _, ok := cloud.LookupStoredCredential("ap-southeast-2"); ok {
		t.Error("an unverifiable key was stored")
	}
}

func TestCloudAuthStoreVerifyOtherErrorNoRetry(t *testing.T) {
	authStoreEnv(t)
	stubAWSEnv(t)
	st := stubAuthSeams(t)
	st.userExists = true
	st.newKeyID = "AKIANEWNEWNEWNEW01"
	st.newSecret = "new-secret"
	st.account = "1"
	st.identityErrFor = "AKIANEWNEWNEWNEW01"
	st.identityErrs = []error{errors.New("operation error STS: GetCallerIdentity, api error AccessDenied: not authorised")}

	err := runCloudAuth(true, false, "ap-southeast-2")
	if err == nil || !strings.Contains(err.Error(), "verifying the new access key") {
		t.Fatalf("error = %v, want the verification failure", err)
	}
	if n := countKey(st.identity, "AKIANEWNEWNEWNEW01"); n != 1 {
		t.Errorf("probe attempts with the new key = %d, want one: only the unpropagated-key shape retries", n)
	}
	if len(st.deleted) != 1 || st.deleted[0] != "AKIANEWNEWNEWNEW01" {
		t.Errorf("the unverifiable key was not deleted: %v", st.deleted)
	}
}

func TestCloudAuthStoreTwoKeys(t *testing.T) {
	authStoreEnv(t)
	stubAWSEnv(t)
	st := stubAuthSeams(t)
	st.userExists = true
	st.accessKeys = []string{"AKIAFIRSTKEYKEYKEY01", "AKIASECONDKEYKEY01"}

	err := runCloudAuth(true, false, "ap-southeast-2")
	if err == nil || !strings.Contains(err.Error(), "two access keys") {
		t.Fatalf("error = %v, want the two-key cap named", err)
	}
	if _, ok := cloud.LookupStoredCredential("ap-southeast-2"); ok {
		t.Error("a credential was stored at the two-key cap")
	}
}

func TestCloudAuthStoreRotationNoAmbient(t *testing.T) {
	authStoreEnv(t)
	noAmbientCreds(t)
	seedStoredCred(t, "ap-southeast-2", "AKIAOLDOLDOLDOLD01", "old-secret", "1")
	st := stubAuthSeams(t)
	st.userExists = true
	st.newKeyID = "AKIANEWNEWNEWNEW01"
	st.newSecret = "new-secret"
	st.accounts["AKIANEWNEWNEWNEW01"] = "1"

	out := captureStderr(t, func() {
		if err := runCloudAuth(true, false, "ap-southeast-2"); err != nil {
			t.Fatalf("rotation without ambient credentials: %v", err)
		}
	})

	if st.existsKey != "AKIAOLDOLDOLDOLD01" {
		t.Errorf("the user check ran with %q, want the stored credential", st.existsKey)
	}
	if len(st.identity) != 1 || st.identity[0] != "AKIANEWNEWNEWNEW01" {
		t.Errorf("identity calls resolved %v, want the new key only", st.identity)
	}
	cred, ok := cloud.LookupStoredCredential("ap-southeast-2")
	if !ok {
		t.Fatal("the rotated credential is missing")
	}
	if cred.AccessKeyID != "AKIANEWNEWNEWNEW01" || cred.SecretAccessKey != "new-secret" || cred.Account != "1" {
		t.Errorf("rotated entry = %+v", cred)
	}
	if len(st.deleted) != 1 || st.deleted[0] != "AKIAOLDOLDOLDOLD01" {
		t.Errorf("the superseded key was not deleted: %v", st.deleted)
	}
	if !strings.Contains(out, "AKIAOLDOLDOLDOLD01") {
		t.Errorf("the confirmation does not name the superseded key:\n%s", out)
	}
}

func TestCloudAuthClear(t *testing.T) {
	authStoreEnv(t)
	noAmbientCreds(t)
	seedStoredCred(t, "ap-southeast-2", "AKIACLEARMEKEYKEY01", "secret", "1")
	st := stubAuthSeams(t)

	out := captureStderr(t, func() {
		if err := runCloudAuth(false, true, "ap-southeast-2"); err != nil {
			t.Fatalf("runCloudAuth --clear: %v", err)
		}
	})
	if _, ok := cloud.LookupStoredCredential("ap-southeast-2"); ok {
		t.Error("the entry was not removed")
	}
	if len(st.deleted) != 1 || st.deleted[0] != "AKIACLEARMEKEYKEY01" {
		t.Errorf("AWS-side deletion = %v, want the stored key id", st.deleted)
	}
	if !strings.Contains(out, "deleted its access key") {
		t.Errorf("confirmation missing the AWS-side deletion:\n%s", out)
	}
}

func TestCloudAuthClearAWSDeploymentFails(t *testing.T) {
	authStoreEnv(t)
	noAmbientCreds(t)
	seedStoredCred(t, "ap-southeast-2", "AKIACLEARMEKEYKEY01", "secret", "1")
	st := stubAuthSeams(t)
	st.deleteErr = errors.New("boom")

	out := captureStderr(t, func() {
		if err := runCloudAuth(false, true, "ap-southeast-2"); err != nil {
			t.Fatalf("a failed AWS-side deletion must not fail the clear: %v", err)
		}
	})
	if _, ok := cloud.LookupStoredCredential("ap-southeast-2"); ok {
		t.Error("the entry was not removed after a failed AWS-side deletion")
	}
	if !strings.Contains(out, "boom") || !strings.Contains(out, "may still exist") {
		t.Errorf("the report does not name the failure and the lingering key:\n%s", out)
	}
}

func TestCloudAuthClearNone(t *testing.T) {
	authStoreEnv(t)
	noAmbientCreds(t)

	out := captureStdout(t, func() {
		if err := runCloudAuth(false, true, "ap-southeast-2"); err != nil {
			t.Fatalf("clearing nothing is not an error: %v", err)
		}
	})
	if !strings.Contains(out, "No stored credential for ap-southeast-2") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestCloudAuthReportEmpty(t *testing.T) {
	authStoreEnv(t)

	out := captureStdout(t, func() {
		if err := runCloudAuth(false, false, "ap-southeast-2"); err != nil {
			t.Fatalf("runCloudAuth: %v", err)
		}
	})
	if !strings.Contains(out, "No stored credential") || !strings.Contains(out, "--store") {
		t.Errorf("the empty report does not name --store:\n%s", out)
	}
}

func TestCloudAuthReport(t *testing.T) {
	authStoreEnv(t)
	noAmbientCreds(t)
	seedStoredCred(t, "ap-southeast-2", "AKIASECONDKEYKEY01", "second-secret", "1")
	seedStoredCred(t, "eu-west-1", "AKIAFIRSTKEYKEYKEY01", "first-secret", "2")

	st := stubAuthSeams(t)
	out := captureStdout(t, func() {
		if err := runCloudAuth(false, false, ""); err != nil {
			t.Fatalf("runCloudAuth: %v", err)
		}
	})
	if len(st.identity) != 0 || len(st.deleted) != 0 {
		t.Errorf("the report made AWS calls: identity %v, deleted %v", st.identity, st.deleted)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two entries:\n%s", out)
	}
	if !strings.HasPrefix(lines[0], "region\taccount\tuser\tkey id\tstored at\tstore") {
		t.Errorf("unexpected header:\n%s", lines[0])
	}
	for i, want := range []string{
		"ap-southeast-2\t1\t" + cloud.ControlPlaneUserName + "\tAKIASECONDKEYKEY01",
		"eu-west-1\t2\t" + cloud.ControlPlaneUserName + "\tAKIAFIRSTKEYKEYKEY01",
	} {
		if !strings.HasPrefix(lines[i+1], want) {
			t.Errorf("entry %d = %q, want it starting %q", i+1, lines[i+1], want)
		}
		if !strings.Contains(lines[i+1], "\tfile") {
			t.Errorf("entry %d does not say which store: %q", i+1, lines[i+1])
		}
	}
	if strings.Contains(out, "first-secret") || strings.Contains(out, "second-secret") {
		t.Errorf("the report printed a secret:\n%s", out)
	}
}
