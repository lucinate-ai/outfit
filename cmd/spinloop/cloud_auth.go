package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/spf13/cobra"
	"github.com/spinloop-ai/spinloop/internal/cloud"
)

// Seams: package variables so tests drive the auth flow without AWS. The IAM
// and STS functions take the config they resolve from, so a test passes an
// ambient config for a first store and a stored-credential config for a
// rotation, and records which credential each call resolved with.
var (
	iamUserExistsFn      = cloud.IAMUserExists
	iamUserAccessKeysFn  = cloud.IAMUserAccessKeyIDs
	iamCreateAccessKeyFn = cloud.IAMCreateAccessKey
	iamDeleteAccessKeyFn = cloud.IAMDeleteAccessKey
	authCallerIdentityFn = cloud.CallerIdentity
)

// cloudAuthCmd is `spinloop cloud auth`: store, report, or clear the
// long-lived control-plane credential this machine signs day-to-day commands
// with. With no flag it reports what is stored, from the local store only.
func cloudAuthCmd() *cobra.Command {
	var (
		store  bool
		clear  bool
		region string
	)
	c := &cobra.Command{
		Use:   "auth",
		Short: "store, report, or clear the control-plane credential",
		Long: `stores a long-lived control-plane credential in this machine's OS
keystore (keychain, credential manager, or secret service) so the day-to-day
cloud commands sign without a fresh SSO log-in. With no flag it reports what
is stored; --store stores the credential for a region, rotating it when one
is already stored; --clear removes it and deletes the access key.`,
		Args:              cobra.ArbitraryArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		ValidArgsFunction: noPositionals,
		RunE: func(c *cobra.Command, _ []string) error {
			resolve(c)
			return runCloudAuth(store, clear, region)
		},
	}
	fs := c.Flags()
	fs.BoolVar(&store, "store", false, "store the credential for the region, rotating it when one is stored")
	fs.BoolVar(&clear, "clear", false, "remove the stored credential and delete its access key")
	fs.StringVar(&region, "region", "", "AWS region (default: AWS_REGION or us-east-1)")
	c.MarkFlagsMutuallyExclusive("store", "clear")
	fs.SetInterspersed(false)
	return c
}

// runCloudAuth is the body of `spinloop cloud auth`.
func runCloudAuth(store, clear bool, regionFlag string) error {
	region := resolveRegion(regionFlag)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch {
	case clear:
		return runCloudAuthClear(ctx, region)
	case store:
		return runCloudAuthStore(ctx, region)
	default:
		return runCloudAuthReport()
	}
}

// runCloudAuthReport lists every stored credential from the local store. It
// makes no AWS call: the report is this machine's own book, and it must work
// on a machine whose credentials are expired or absent — which is the point
// of the stored key.
func runCloudAuthReport() error {
	creds, err := cloud.ListStoredCredentials()
	if err != nil {
		return fmt.Errorf("reading the stored credentials: %w", err)
	}
	if len(creds) == 0 {
		fmt.Println("No stored credential. Store one with `spinloop cloud auth --store`.")
		return nil
	}
	w := os.Stdout
	fmt.Fprintln(w, "region\taccount\tuser\tkey id\tstored at\tstore")
	for _, c := range creds {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			c.Region, c.Account, c.User, c.AccessKeyID, c.StoredAt.UTC().Format(time.RFC3339), c.Store)
	}
	return nil
}

// runCloudAuthStore stores a credential for the region. With nothing stored
// it is a first store: it runs on the caller's ambient credentials, which are
// the administrator's, and verifies the new key against the caller's account.
// With an entry already stored it is a rotation: it runs on the stored
// credential alone — no administrator or other ambient credential required —
// and deletes the superseded key on the AWS side once the new one is verified
// and swapped in.
func runCloudAuthStore(ctx context.Context, region string) error {
	existing, rotating := cloud.LookupStoredCredential(region)

	var cfg aws.Config
	var expectedAccount string
	if rotating {
		cfg = cloud.ConfigFromStored(existing)
		expectedAccount = existing.Account
	} else {
		var err error
		cfg, err = loadCreds(ctx, region)
		if err != nil {
			return fmt.Errorf("resolving AWS credentials: %w (configure env credentials, a profile or an SSO session)", err)
		}
		var err2 error
		expectedAccount, err2 = authCallerIdentityFn(ctx, cfg)
		if err2 != nil {
			return fmt.Errorf("confirming the AWS account: %w", err2)
		}
	}

	exists, err := iamUserExistsFn(ctx, cfg, cloud.ControlPlaneUserName)
	if err != nil {
		return fmt.Errorf("checking the control-plane user: %w", err)
	}
	if !exists {
		return fmt.Errorf("the control-plane user %q does not exist in this account — re-run `spinloop cloud bootstrap` to create it",
			cloud.ControlPlaneUserName)
	}

	if !rotating {
		// IAM allows two access keys per user. A first store would be the
		// third, so check before creating rather than after a failure.
		keys, err := iamUserAccessKeysFn(ctx, cfg, cloud.ControlPlaneUserName)
		if err != nil {
			return fmt.Errorf("listing the control-plane user's access keys: %w", err)
		}
		if len(keys) >= 2 {
			return fmt.Errorf("the control-plane user already has two access keys — delete one and run `spinloop cloud auth --store` again")
		}
	}

	keyID, secret, err := iamCreateAccessKeyFn(ctx, cfg, cloud.ControlPlaneUserName)
	if err != nil {
		return fmt.Errorf("creating the access key: %w", err)
	}

	// Verify the new key resolves to the expected account before anything is
	// stored. A key that cannot be verified, or that resolves elsewhere, is
	// deleted on the AWS side rather than left behind.
	probe := cloud.ConfigFromStored(cloud.StoredCredential{AccessKeyID: keyID, SecretAccessKey: secret, Region: region})
	account, err := verifyNewKey(ctx, probe)
	if err != nil {
		deleteStrayKey(ctx, cfg, keyID)
		return fmt.Errorf("verifying the new access key: %w", err)
	}
	if account != expectedAccount {
		deleteStrayKey(ctx, cfg, keyID)
		return fmt.Errorf("the new access key resolves to account %s, not %s — it was not stored and has been deleted on the AWS side",
			account, expectedAccount)
	}

	cred := cloud.StoredCredential{
		AccessKeyID:     keyID,
		SecretAccessKey: secret,
		Account:         account,
		User:            cloud.ControlPlaneUserName,
		Region:          region,
		StoredAt:        time.Now().UTC(),
	}
	if err := cloud.StoreCredential(cred); err != nil {
		return fmt.Errorf("storing the credential: %w", err)
	}
	// Read the entry back: it carries the store kind the write recorded, and
	// a store that cannot hand back what it was given has not stored it.
	cred, ok := cloud.LookupStoredCredential(region)
	if !ok {
		return fmt.Errorf("the credential was stored but cannot be read back")
	}

	w := os.Stderr
	fmt.Fprintf(w, "Stored the control-plane credential for %s:\n", region)
	fmt.Fprintf(w, "  Account:  %s\n", account)
	fmt.Fprintf(w, "  Region:   %s\n", region)
	fmt.Fprintf(w, "  User:     %s\n", cloud.ControlPlaneUserName)
	fmt.Fprintf(w, "  Key id:   %s\n", keyID)
	fmt.Fprintf(w, "  Store:    %s\n", cred.Store)
	if rotating {
		if err := iamDeleteAccessKeyFn(ctx, probe, cloud.ControlPlaneUserName, existing.AccessKeyID); err != nil {
			fmt.Fprintf(w, "The superseded key %s could not be deleted on the AWS side (%v) — it may still exist.\n",
				existing.AccessKeyID, err)
		} else {
			fmt.Fprintf(w, "The superseded key %s was deleted on the AWS side.\n", existing.AccessKeyID)
		}
	}
	return nil
}

// verifyAttempts is how many times the identity probe runs while verifying a
// freshly created key.
const verifyAttempts = 6

// verifyProbeBackoff is the wait before the first retry of a verification
// that has not succeeded yet; each later retry waits twice as long, up to
// verifyProbeMaxBackoff. Tests set it to zero, which zeroes every wait.
var verifyProbeBackoff = time.Second

// verifyProbeMaxBackoff caps the exponential growth, so the whole retry
// window stays around half a minute.
const verifyProbeMaxBackoff = 16 * time.Second

// verifyNewKey resolves the account of the freshly created key. It retries
// while STS rejects the key id itself: after IAM issues a key, STS can lag
// several seconds before it resolves the key, and in that window every call
// reports the key as an invalid client token. Any other failure returns at
// once — the caller deletes the key, as with a verification that never
// succeeds.
func verifyNewKey(ctx context.Context, probe aws.Config) (string, error) {
	backoff := verifyProbeBackoff
	for attempt := 1; ; attempt++ {
		account, err := authCallerIdentityFn(ctx, probe)
		if err == nil || !invalidClientToken(err) || attempt >= verifyAttempts {
			return account, err
		}
		fmt.Fprintf(os.Stderr, "The new access key is not resolvable yet — retry %d of %d...\n", attempt, verifyAttempts-1)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > verifyProbeMaxBackoff {
			backoff = verifyProbeMaxBackoff
		}
	}
}

// invalidClientToken reports whether the error is STS rejecting the access
// key id itself — the shape of the failure while a freshly issued key is
// still propagating. The pinned SDK version has no typed error for it, so
// the match is on the API error code in the message.
func invalidClientToken(err error) bool {
	return err != nil && strings.Contains(err.Error(), "InvalidClientTokenId")
}

// deleteStrayKey removes an access key this command created and then failed
// to verify, so a failed store leaves no live key on the AWS side. A failure
// here is reported, not returned: the store has already failed.
func deleteStrayKey(ctx context.Context, cfg aws.Config, keyID string) {
	if err := iamDeleteAccessKeyFn(ctx, cfg, cloud.ControlPlaneUserName, keyID); err != nil {
		fmt.Fprintf(os.Stderr, "The new key %s could not be deleted on the AWS side (%v) — delete it manually.\n", keyID, err)
	}
}

// runCloudAuthClear removes the stored credential for the region and deletes
// its access key on the AWS side, using the stored credential, so a cleared
// key does not linger in the account. If the AWS-side deletion cannot be
// made, the local entry is still removed and the failure reported.
func runCloudAuthClear(ctx context.Context, region string) error {
	cred, ok := cloud.LookupStoredCredential(region)
	if !ok {
		fmt.Printf("No stored credential for %s.\n", region)
		return nil
	}
	cfg := cloud.ConfigFromStored(cred)
	deleteErr := iamDeleteAccessKeyFn(ctx, cfg, cred.User, cred.AccessKeyID)
	if err := cloud.DeleteStoredCredential(region); err != nil {
		return fmt.Errorf("removing the stored credential: %w", err)
	}
	w := os.Stderr
	if deleteErr != nil {
		fmt.Fprintf(w, "Removed the stored credential for %s, but deleting its access key %s on the AWS side failed (%v) — the key may still exist.\n",
			region, cred.AccessKeyID, deleteErr)
	} else {
		fmt.Fprintf(w, "Removed the stored credential for %s and deleted its access key %s on the AWS side.\n",
			region, cred.AccessKeyID)
	}
	return nil
}
