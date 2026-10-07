package cloud

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	dbus "github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"
)

// StoredCredential is one long-lived control-plane credential the operator
// stored with `spinloop cloud auth --store`: an access key for the
// control-plane user, held in the OS keystore (or an owner-only file where no
// keystore exists) rather than in a shared AWS config.
type StoredCredential struct {
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	Account         string    `json:"account"`
	User            string    `json:"user"`
	Region          string    `json:"region"`
	StoredAt        time.Time `json:"stored_at"`
	Store           string    `json:"store"`
}

// credentialStore is one machine's store of stored control-plane credentials:
// the OS keystore where one is available, an owner-only directory under the
// user's spinloop config directory where it is not.
type credentialStore struct {
	kind string         // "keyring" or "file"
	kr   keyringBackend // set when kind is "keyring"
	dir  string         // set when kind is "file"
}

// keyringService is the service name this tool's entries sit under in the
// shared OS keystore; the region is the entry's user name.
const keyringService = "spinloop-remote"

// keyringIndexUser is the entry holding the regions this tool has stored, one
// per line: a non-secret index, since the OS keystores offer no way to list
// a service's entries.
const keyringIndexUser = "index"

// keyStoreEnvVar selects the file store over the OS keystore when set to
// "file": the opt-out for a headless macOS session whose keychain is locked
// or unreachable, and the way the test suite keeps stored credentials inside
// a temp config directory.
const keyStoreEnvVar = "SPINLOOP_CLOUD_KEYSTORE"

// keyringBackend is the slice of the OS keystore the store drives, so tests
// substitute an in-memory fake without touching the machine's real keystore.
type keyringBackend interface {
	set(user, data string) error
	get(user string) (string, error)
	delete(user string) error
}

// osKeyring is keyringBackend over the OS keystore: the security CLI on
// macOS, Credential Manager on Windows, the Secret Service over D-Bus on
// Linux. No cgo, so it works in the static release binaries.
type osKeyring struct{}

func (osKeyring) set(user, data string) error { return keyring.Set(keyringService, user, data) }
func (osKeyring) get(user string) (string, error) {
	return keyring.Get(keyringService, user)
}
func (osKeyring) delete(user string) error { return keyring.Delete(keyringService, user) }

// keyringBackendFn is the seam tests drive.
var keyringBackendFn = func() keyringBackend { return osKeyring{} }

// keyringAvailable reports whether an OS keystore is reachable on this
// machine. macOS ships the security CLI and Windows ships Credential Manager;
// Linux needs a D-Bus session bus with the Secret Service registered, which a
// headless machine lacks.
func keyringAvailable() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	obj := conn.Object("org.freedesktop.DBus", dbus.ObjectPath("/org/freedesktop/DBus"))
	var hasOwner bool
	err = obj.Call("org.freedesktop.DBus.NameHasOwner", 0, "org.freedesktop.secrets").Store(&hasOwner)
	return err == nil && hasOwner
}

// openCredStore opens the machine's credential store: the OS keystore where
// one is available, otherwise the owner-only file store. A machine with no
// keystore at all is the fallback case; SPINLOOP_CLOUD_KEYSTORE=file chooses
// the file store even where a keystore is reachable; a keystore that opened
// and then fails is reported, not papered over by silently switching stores.
func openCredStore() (credentialStore, error) {
	if os.Getenv(keyStoreEnvVar) != "file" && keyringAvailable() {
		return credentialStore{kind: "keyring", kr: keyringBackendFn()}, nil
	}
	home, err := ConfigHome()
	if err != nil {
		return credentialStore{}, err
	}
	dir := filepath.Join(home, "keystore")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return credentialStore{}, err
	}
	return credentialStore{kind: "file", dir: dir}, nil
}

// openCredStoreFn is the seam tests drive so they never touch the real
// keystore or the operator's config directory.
var openCredStoreFn = openCredStore

func (s credentialStore) fileFor(region string) string {
	return filepath.Join(s.dir, "cloud-"+region+".json")
}

func (s credentialStore) put(region string, cred StoredCredential) error {
	data, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	if s.kind == "keyring" {
		if err := s.kr.set(region, string(data)); err != nil {
			return err
		}
		return s.putIndex(append(s.indexRegions(), region))
	}
	return os.WriteFile(s.fileFor(region), append(data, '\n'), 0o600)
}

// indexRegions reads the stored-region index. A missing index is no
// regions; a broken one is reported, since an index that misleads the
// report misleads the operator about what is stored.
func (s credentialStore) indexRegions() []string {
	if s.kind != "keyring" {
		return nil
	}
	data, err := s.kr.get(keyringIndexUser)
	if err != nil {
		return nil
	}
	var regions []string
	for _, region := range strings.Split(data, "\n") {
		if region != "" {
			regions = append(regions, region)
		}
	}
	return regions
}

func (s credentialStore) putIndex(regions []string) error {
	if s.kind != "keyring" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, region := range regions {
		if region != "" && !seen[region] {
			seen[region] = true
			out = append(out, region)
		}
	}
	sort.Strings(out)
	return s.kr.set(keyringIndexUser, strings.Join(out, "\n"))
}

// get returns the stored credential for the region. A missing entry is
// (zero, nil), not an error: an absent credential is the normal case, and a
// broken one must not take down a command the ambient chain could still sign.
func (s credentialStore) get(region string) (StoredCredential, error) {
	if s.kind == "keyring" {
		data, err := s.kr.get(region)
		if err != nil {
			if errors.Is(err, keyring.ErrNotFound) {
				return StoredCredential{}, nil
			}
			return StoredCredential{}, err
		}
		var cred StoredCredential
		if err := json.Unmarshal([]byte(data), &cred); err != nil {
			return StoredCredential{}, fmt.Errorf("parsing the stored credential for %s: %w", region, err)
		}
		return cred, nil
	}
	data, err := os.ReadFile(s.fileFor(region))
	if err != nil {
		if os.IsNotExist(err) {
			return StoredCredential{}, nil
		}
		return StoredCredential{}, err
	}
	var cred StoredCredential
	if err := json.Unmarshal(data, &cred); err != nil {
		return StoredCredential{}, fmt.Errorf("parsing the stored credential for %s: %w", region, err)
	}
	return cred, nil
}

// delete removes the stored credential for the region. An absent entry is not
// an error, so a clear is idempotent.
func (s credentialStore) delete(region string) error {
	if s.kind == "keyring" {
		if err := s.kr.delete(region); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return err
		}
		regions := s.indexRegions()
		kept := regions[:0]
		for _, r := range regions {
			if r != region {
				kept = append(kept, r)
			}
		}
		return s.putIndex(kept)
	}
	if err := os.Remove(s.fileFor(region)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// list returns every stored credential, sorted by region. An entry the index
// names but the store no longer holds (removed outside of spinloop) is
// skipped rather than reported, since the index lags a removal; an entry that
// cannot be parsed is an error naming its region, since a report that hides a
// broken entry misleads the operator about what is stored.
func (s credentialStore) list() ([]StoredCredential, error) {
	var regions []string
	if s.kind == "keyring" {
		regions = s.indexRegions()
	} else {
		entries, err := os.ReadDir(s.dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || filepath.Ext(name) != ".json" || !strings.HasPrefix(name, "cloud-") {
				continue
			}
			region := name[len("cloud-") : len(name)-len(".json")]
			if region != "" {
				regions = append(regions, region)
			}
		}
	}
	sort.Strings(regions)
	creds := make([]StoredCredential, 0, len(regions))
	for _, region := range regions {
		cred, err := s.get(region)
		if err != nil {
			return nil, err
		}
		if cred.AccessKeyID == "" {
			continue
		}
		creds = append(creds, cred)
	}
	return creds, nil
}

// StoreCredential stores cred in the machine's credential store, recording
// which store holds it. The secret stays in the store; nothing prints it.
func StoreCredential(cred StoredCredential) error {
	s, err := openCredStoreFn()
	if err != nil {
		return err
	}
	cred.Store = s.kind
	return s.put(cred.Region, cred)
}

// LookupStoredCredential returns the stored control-plane credential for the
// region. A missing or unreadable entry is (zero, false), not an error.
func LookupStoredCredential(region string) (StoredCredential, bool) {
	s, err := openCredStoreFn()
	if err != nil {
		return StoredCredential{}, false
	}
	cred, err := s.get(region)
	if err != nil || cred.AccessKeyID == "" {
		return StoredCredential{}, false
	}
	return cred, true
}

// ListStoredCredentials returns every stored control-plane credential, sorted
// by region.
func ListStoredCredentials() ([]StoredCredential, error) {
	s, err := openCredStoreFn()
	if err != nil {
		return nil, err
	}
	return s.list()
}

// DeleteStoredCredential removes the stored control-plane credential for the
// region. An absent entry is not an error.
func DeleteStoredCredential(region string) error {
	s, err := openCredStoreFn()
	if err != nil {
		return err
	}
	return s.delete(region)
}
