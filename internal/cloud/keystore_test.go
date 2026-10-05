package cloud

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// fakeKeyring is an in-memory keyringBackend for tests: it never touches the
// machine's real keystore.
type fakeKeyring struct {
	items map[string]string
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{items: map[string]string{}}
}

func (f *fakeKeyring) set(user, data string) error {
	f.items[user] = data
	return nil
}

func (f *fakeKeyring) get(user string) (string, error) {
	data, ok := f.items[user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return data, nil
}

func (f *fakeKeyring) delete(user string) error {
	if _, ok := f.items[user]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.items, user)
	return nil
}

func testCred(region string) StoredCredential {
	return StoredCredential{
		AccessKeyID:     "AKIAEXAMPLE" + region[:3],
		SecretAccessKey: "secret-for-" + region,
		Account:         "0",
		User:            "cloud-vm-llm-remote-cli",
		Region:          region,
		StoredAt:        time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC),
	}
}

func fileStore(t *testing.T, dir string) credentialStore {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return credentialStore{kind: "file", dir: dir}
}

func TestCredStoreKeyringRoundTrip(t *testing.T) {
	kr := newFakeKeyring()
	s := credentialStore{kind: "keyring", kr: kr}

	if cred, err := s.get("ap-southeast-2"); err != nil || cred.AccessKeyID != "" {
		t.Fatalf("get of a missing entry = %q, %v; want absent, no error", cred.AccessKeyID, err)
	}
	if err := s.put("ap-southeast-2", testCred("ap-southeast-2")); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.get("ap-southeast-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SecretAccessKey != "secret-for-ap-southeast-2" || got.Region != "ap-southeast-2" {
		t.Fatalf("get returned %+v", got)
	}
	// The entry sits under the region's user name, and the index names it.
	if _, ok := kr.items["ap-southeast-2"]; !ok {
		t.Fatalf("entry missing under its region user name; items: %v", kr.items)
	}
	if idx := kr.items[keyringIndexUser]; idx != "ap-southeast-2" {
		t.Fatalf("index = %q; want the stored region", idx)
	}
	if err := s.delete("ap-southeast-2"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if idx := kr.items[keyringIndexUser]; idx != "" {
		t.Fatalf("index after delete = %q; want empty", idx)
	}
	if err := s.delete("ap-southeast-2"); err != nil {
		t.Fatalf("delete of an absent entry must not fail: %v", err)
	}
}

func TestCredStoreKeyringRegionKeyingAndList(t *testing.T) {
	kr := newFakeKeyring()
	s := credentialStore{kind: "keyring", kr: kr}

	for _, region := range []string{"us-east-1", "ap-southeast-2"} {
		if err := s.put(region, testCred(region)); err != nil {
			t.Fatalf("put %s: %v", region, err)
		}
	}
	if cred, err := s.get("eu-west-1"); err != nil || cred.AccessKeyID != "" {
		t.Fatalf("get of a region with no entry = %+v, %v", cred, err)
	}
	creds, err := s.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(creds) != 2 || creds[0].Region != "ap-southeast-2" || creds[1].Region != "us-east-1" {
		t.Fatalf("list = %+v; want the two regions, sorted", creds)
	}
	// A region in the index whose entry was removed outside of spinloop is
	// skipped, not reported.
	if err := kr.delete("us-east-1"); err != nil {
		t.Fatal(err)
	}
	creds, err = s.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(creds) != 1 || creds[0].Region != "ap-southeast-2" {
		t.Fatalf("list after an out-of-band removal = %+v", creds)
	}
}

func TestCredStoreFileFallback(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keystore")
	s := fileStore(t, dir)

	if err := s.put("us-east-1", testCred("us-east-1")); err != nil {
		t.Fatalf("put: %v", err)
	}
	// Owner-only file in an owner-only directory: the store may hold a secret.
	fileInfo, err := os.Stat(s.fileFor("us-east-1"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := fileInfo.Mode().Perm(); mode != 0o600 {
		t.Fatalf("file mode = %o; want 0600", mode)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Fatalf("dir mode = %o; want 0700", mode)
	}

	got, err := s.get("us-east-1")
	if err != nil || got.AccessKeyID != testCred("us-east-1").AccessKeyID {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if cred, err := s.get("ap-southeast-2"); err != nil || cred.AccessKeyID != "" {
		t.Fatalf("get of a region with no entry = %+v, %v", cred, err)
	}
	creds, err := s.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(creds) != 1 || creds[0].Region != "us-east-1" {
		t.Fatalf("list = %+v", creds)
	}
	if err := s.delete("us-east-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if cred, err := s.get("us-east-1"); err != nil || cred.AccessKeyID != "" {
		t.Fatalf("entry still present after delete: %+v, %v", cred, err)
	}
}

func TestCredStoreFileBrokenEntry(t *testing.T) {
	dir := t.TempDir()
	s := fileStore(t, dir)
	if err := os.WriteFile(s.fileFor("us-east-1"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.get("us-east-1"); err == nil {
		t.Fatal("get of a broken entry returned no error")
	}
	if _, err := s.list(); err == nil {
		t.Fatal("list of a store with a broken entry returned no error")
	}
}

func TestStoredCredentialAPI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), ".config"))
	fake := newFakeKeyring()
	t.Cleanup(func() { openCredStoreFn = openCredStore })
	openCredStoreFn = func() (credentialStore, error) {
		return credentialStore{kind: "keyring", kr: fake}, nil
	}

	if _, ok := LookupStoredCredential("us-east-1"); ok {
		t.Fatal("lookup before any store reported an entry")
	}
	cred := testCred("us-east-1")
	if err := StoreCredential(cred); err != nil {
		t.Fatalf("StoreCredential: %v", err)
	}
	got, ok := LookupStoredCredential("us-east-1")
	if !ok || got.AccessKeyID != cred.AccessKeyID || got.Store != "keyring" {
		t.Fatalf("LookupStoredCredential = %+v, %v", got, ok)
	}
	if _, ok := LookupStoredCredential("ap-southeast-2"); ok {
		t.Fatal("lookup for a different region reported the stored entry")
	}
	all, err := ListStoredCredentials()
	if err != nil || len(all) != 1 || all[0].Region != "us-east-1" {
		t.Fatalf("ListStoredCredentials = %+v, %v", all, err)
	}
	if err := DeleteStoredCredential("us-east-1"); err != nil {
		t.Fatalf("DeleteStoredCredential: %v", err)
	}
	if _, ok := LookupStoredCredential("us-east-1"); ok {
		t.Fatal("entry still present after DeleteStoredCredential")
	}
}

func TestKeyStoreEnvVarForcesFileStore(t *testing.T) {
	// The env var must choose the file store even on a machine where a
	// keystore is reachable (here: regardless of keyringAvailable).
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("SPINLOOP_CONFIG_DIR", "")
	t.Setenv(keyStoreEnvVar, "file")
	t.Cleanup(func() { openCredStoreFn = openCredStore })

	if err := StoreCredential(testCred("us-east-1")); err != nil {
		t.Fatalf("StoreCredential: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "spinloop", "keystore", "cloud-us-east-1.json")); err != nil {
		t.Fatalf("file store not used under the forced config dir: %v", err)
	}
	got, ok := LookupStoredCredential("us-east-1")
	if !ok || got.Store != "file" {
		t.Fatalf("LookupStoredCredential = %+v, %v; want the file store", got, ok)
	}
	if err := DeleteStoredCredential("us-east-1"); err != nil {
		t.Fatalf("DeleteStoredCredential: %v", err)
	}
}

func TestStoredCredentialAPIFileFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Cleanup(func() { openCredStoreFn = openCredStore })
	openCredStoreFn = func() (credentialStore, error) {
		return fileStore(t, filepath.Join(home, ".config", "spinloop", "keystore")), nil
	}

	if err := StoreCredential(testCred("us-east-1")); err != nil {
		t.Fatalf("StoreCredential: %v", err)
	}
	got, ok := LookupStoredCredential("us-east-1")
	if !ok || got.SecretAccessKey != "secret-for-us-east-1" {
		t.Fatalf("LookupStoredCredential = %+v, %v", got, ok)
	}
	if err := DeleteStoredCredential("us-east-1"); err != nil {
		t.Fatalf("DeleteStoredCredential: %v", err)
	}
	if _, ok := LookupStoredCredential("us-east-1"); ok {
		t.Fatal("entry still present after DeleteStoredCredential")
	}
}
