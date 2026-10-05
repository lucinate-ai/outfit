package cloud

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestControlPlaneFromOutputs_MapsEveryStackOutput(t *testing.T) {
	// The keys are the outputs the control-plane stack (remote/lib/llm-stack.ts)
	// publishes for the config. If the template gains a new control-URL output,
	// the mapping in controlPlaneFromOutputs must take it on: an output landed
	// here but not mapped is dropped from every registered environment's
	// cloud.json without error — update_url was exactly that, which left
	// `spinloop cloud keep` unusable on freshly registered environments.
	outputs := map[string]string{
		"StartUrl":               "https://start.example.aws/",
		"StopUrl":                "https://stop.example.aws/",
		"DeployUrl":              "https://deploy.example.aws/",
		"StatsUrl":               "https://stats.example.aws/",
		"EnvUrl":                 "https://env.example.aws/",
		"UpdateUrl":              "https://update.example.aws/",
		"ScheduleUrl":            "https://schedule.example.aws/",
		"Region":                 "eu-west-1",
		"WeightsBucket":          "weights-bucket",
		"VpcId":                  "vpc-123", // not part of the config
		"SeedInstanceProfileArn": "arn:aws:iam::1:instance-profile/seed",
	}

	layer, err := controlPlaneFromOutputs("test-stack", outputs)
	if err != nil {
		t.Fatalf("controlPlaneFromOutputs: %v", err)
	}

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"StartURL", layer.Config.StartURL, outputs["StartUrl"]},
		{"StopURL", layer.Config.StopURL, outputs["StopUrl"]},
		{"DeployURL", layer.Config.DeployURL, outputs["DeployUrl"]},
		{"StatsURL", layer.Config.StatsURL, outputs["StatsUrl"]},
		{"EnvURL", layer.Config.EnvURL, outputs["EnvUrl"]},
		{"UpdateURL", layer.Config.UpdateURL, outputs["UpdateUrl"]},
		{"ScheduleURL", layer.Config.ScheduleURL, outputs["ScheduleUrl"]},
		{"Region", layer.Config.Region, outputs["Region"]},
		{"WeightsBucket", layer.WeightsBucket, outputs["WeightsBucket"]},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestControlPlaneFromOutputs_PredatesUpdateLambda(t *testing.T) {
	// A stack deployed before the update Lambda exists carries no UpdateUrl
	// output. Registration must still succeed — keep degrades to an error at
	// the point it is used, not at deploy time.
	outputs := map[string]string{
		"StartUrl":      "https://start.example.aws/",
		"StopUrl":       "https://stop.example.aws/",
		"DeployUrl":     "https://deploy.example.aws/",
		"Region":        "eu-west-1",
		"StatsUrl":      "https://stats.example.aws/",
		"EnvUrl":        "https://env.example.aws/",
		"WeightsBucket": "weights-bucket",
	}

	layer, err := controlPlaneFromOutputs("test-stack", outputs)
	if err != nil {
		t.Fatalf("controlPlaneFromOutputs: %v", err)
	}
	if layer.Config.UpdateURL != "" {
		t.Errorf("UpdateURL = %q, want empty for a stack without the output", layer.Config.UpdateURL)
	}
}

func TestControlPlaneFromOutputs_MissingRequired(t *testing.T) {
	for _, missing := range []string{"StartUrl", "StopUrl", "DeployUrl"} {
		outputs := map[string]string{
			"StartUrl":  "https://start.example.aws/",
			"StopUrl":   "https://stop.example.aws/",
			"DeployUrl": "https://deploy.example.aws/",
		}
		delete(outputs, missing)
		if _, err := controlPlaneFromOutputs("test-stack", outputs); err == nil {
			t.Errorf("missing %s: expected an error", missing)
		}
	}
}

func TestParseFloat(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		err  bool
	}{
		{"0.3580", 0.3580, false},
		{"1.5", 1.5, false},
		{"0", 0, false},
		{"NaN", 0, true},
		{"Inf", 0, true},
		{"", 0, true},
		{"abc", 0, true},
	}
	for _, tc := range cases {
		got, err := parseFloat(tc.in)
		if tc.err && err == nil {
			t.Errorf("parseFloat(%q) = %v, want error", tc.in, got)
		}
		if !tc.err && err != nil {
			t.Errorf("parseFloat(%q) error: %v", tc.in, err)
		}
		if !tc.err && math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("parseFloat(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestExtractPriceSimple(t *testing.T) {
	got, err := extractPriceSimple([]byte(`{"pricePerUnit":{"HOUR": "0.3580"}}`))
	if err != nil {
		t.Fatalf("extractPriceSimple: %v", err)
	}
	if math.Abs(got-0.3580) > 1e-4 {
		t.Errorf("extractPriceSimple = %v, want 0.3580", got)
	}

	_, err = extractPriceSimple([]byte(`{"pricePerUnit":{"SECOND": "0.0001"}}`))
	if err == nil {
		t.Error("expected error when HOUR key is absent")
	}
}

func TestExtractPrice(t *testing.T) {
	doc, _ := json.Marshal([]struct {
		Products map[string]struct {
			Attributes struct {
				InstanceType string `json:"instanceType"`
			} `json:"attributes"`
			PriceList map[string]struct {
				OnDemand map[string]struct {
					PricePerUnit map[string]struct {
						Hour string `json:"HOUR"`
					} `json:"pricePerUnit"`
				} `json:"OnDemand"`
			} `json:"priceList"`
		} `json:"products"`
	}{
		{
			Products: map[string]struct {
				Attributes struct {
					InstanceType string `json:"instanceType"`
				} `json:"attributes"`
				PriceList map[string]struct {
					OnDemand map[string]struct {
						PricePerUnit map[string]struct {
							Hour string `json:"HOUR"`
						} `json:"pricePerUnit"`
					} `json:"OnDemand"`
				} `json:"priceList"`
			}{
				"i": {
					Attributes: struct {
						InstanceType string `json:"instanceType"`
					}{InstanceType: "g6e.12xlarge"},
					PriceList: map[string]struct {
						OnDemand map[string]struct {
							PricePerUnit map[string]struct {
								Hour string `json:"HOUR"`
							} `json:"pricePerUnit"`
						} `json:"OnDemand"`
					}{
						"p": {OnDemand: map[string]struct {
							PricePerUnit map[string]struct {
								Hour string `json:"HOUR"`
							} `json:"pricePerUnit"`
						}{
							"o": {PricePerUnit: map[string]struct {
								Hour string `json:"HOUR"`
							}{
								"h": {Hour: "2.10"},
							}},
						}},
					},
				},
			},
		},
	})

	got, err := extractPrice(doc, "g6e.12xlarge")
	if err != nil {
		t.Fatalf("extractPrice: %v", err)
	}
	if math.Abs(got-2.10) > 1e-4 {
		t.Errorf("extractPrice = %v, want 2.10", got)
	}

	_, err = extractPrice(doc, "nonexistent.type")
	if err == nil {
		t.Error("expected error for unknown instance type")
	}
}

func TestExtractPrice_Fallback(t *testing.T) {
	// Malformed JSON that fails unmarshal falls back to extractPriceSimple.
	got, err := extractPrice([]byte(`{not valid json "HOUR": "0.3580"}`), "g6e.12xlarge")
	if err != nil {
		t.Fatalf("extractPrice fallback: %v", err)
	}
	if math.Abs(got-0.3580) > 1e-4 {
		t.Errorf("extractPrice fallback = %v, want 0.3580", got)
	}
}

// pinChainHermetically keeps the standard credential chain inside the test:
// the shared config and credentials files point at temp files, no explicit
// env credential or profile, and IMDS is off so an unresolvable chain fails
// fast instead of reaching for the metadata service.
func pinChainHermetically(t *testing.T, sharedCredsFile, configFile string) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", sharedCredsFile)
	t.Setenv("AWS_CONFIG_FILE", configFile)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

func writeAWSCredsFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "aws-creds")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// storeCredForTest parks a stored credential in a file-backed store behind the
// openCredStoreFn seam, so the tests never touch the machine's keystore.
func storeCredForTest(t *testing.T, cred StoredCredential) {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() { openCredStoreFn = openCredStore })
	openCredStoreFn = func() (credentialStore, error) {
		return fileStore(t, dir), nil
	}
	if err := StoreCredential(cred); err != nil {
		t.Fatalf("StoreCredential: %v", err)
	}
}

func resolvedAccessKeyID(t *testing.T, region string) string {
	t.Helper()
	cfg, err := LoadAWSConfig(context.Background(), region)
	if err != nil {
		t.Fatalf("LoadAWSConfig: %v", err)
	}
	creds, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	return creds.AccessKeyID
}

func TestLoadAWSConfigPrecedence(t *testing.T) {
	const region = "ap-southeast-2"
	stored := testCred(region)
	storeCredForTest(t, stored)
	chainFile := writeAWSCredsFile(t, t.TempDir(), `[default]
aws_access_key_id = AKIACHAINCHAINCHAIN
aws_secret_access_key = chain-secret
`)

	t.Run("env credentials override the stored key", func(t *testing.T) {
		pinChainHermetically(t, chainFile, chainFile)
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIAENVENVENVENVENV")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
		if got := resolvedAccessKeyID(t, region); got != "AKIAENVENVENVENVENV" {
			t.Fatalf("resolved %q; want the env credential", got)
		}
	})

	t.Run("a named profile overrides the stored key", func(t *testing.T) {
		profileFile := writeAWSCredsFile(t, t.TempDir(), `[worker]
aws_access_key_id = AKIAPROFILEPROFILE
aws_secret_access_key = profile-secret
`)
		pinChainHermetically(t, profileFile, profileFile)
		t.Setenv("AWS_PROFILE", "worker")
		if got := resolvedAccessKeyID(t, region); got != "AKIAPROFILEPROFILE" {
			t.Fatalf("resolved %q; want the profile credential", got)
		}
	})

	t.Run("the stored key wins over the shared chain", func(t *testing.T) {
		pinChainHermetically(t, chainFile, chainFile)
		if got := resolvedAccessKeyID(t, region); got != stored.AccessKeyID {
			t.Fatalf("resolved %q; want the stored credential", got)
		}
	})

	t.Run("no stored key falls back to the chain", func(t *testing.T) {
		// A region with no stored entry: the file store holds only ap-southeast-2.
		pinChainHermetically(t, chainFile, chainFile)
		if got := resolvedAccessKeyID(t, "eu-west-1"); got != "AKIACHAINCHAINCHAIN" {
			t.Fatalf("resolved %q; want the shared-chain credential", got)
		}
	})
}

func TestPricingConfigPrecedence(t *testing.T) {
	const region = "ap-southeast-2"
	stored := testCred(region)
	storeCredForTest(t, stored)
	chainFile := writeAWSCredsFile(t, t.TempDir(), `[default]
aws_access_key_id = AKIACHAINCHAINCHAIN
aws_secret_access_key = chain-secret
`)

	t.Run("the stored key authorises pricing, endpoint stays us-east-1", func(t *testing.T) {
		pinChainHermetically(t, chainFile, chainFile)
		cfg, err := pricingConfig(context.Background(), region)
		if err != nil {
			t.Fatalf("pricingConfig: %v", err)
		}
		if cfg.Region != "us-east-1" {
			t.Fatalf("endpoint region = %q; want us-east-1", cfg.Region)
		}
		creds, err := cfg.Credentials.Retrieve(context.Background())
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if creds.AccessKeyID != stored.AccessKeyID {
			t.Fatalf("resolved %q; want the stored credential", creds.AccessKeyID)
		}
	})

	t.Run("explicit env credentials override the stored key", func(t *testing.T) {
		pinChainHermetically(t, chainFile, chainFile)
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIAENVENVENVENVENV")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
		cfg, err := pricingConfig(context.Background(), region)
		if err != nil {
			t.Fatalf("pricingConfig: %v", err)
		}
		creds, err := cfg.Credentials.Retrieve(context.Background())
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if creds.AccessKeyID != "AKIAENVENVENVENVENV" {
			t.Fatalf("resolved %q; want the env credential", creds.AccessKeyID)
		}
	})
}

func TestLoadAmbientAWSConfigIgnoresStoredKey(t *testing.T) {
	const region = "ap-southeast-2"
	storeCredForTest(t, testCred(region))
	chainFile := writeAWSCredsFile(t, t.TempDir(), `[default]
aws_access_key_id = AKIACHAINCHAINCHAIN
aws_secret_access_key = chain-secret
`)
	pinChainHermetically(t, chainFile, chainFile)

	cfg, err := LoadAmbientAWSConfig(context.Background(), region)
	if err != nil {
		t.Fatalf("LoadAmbientAWSConfig: %v", err)
	}
	creds, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if creds.AccessKeyID != "AKIACHAINCHAINCHAIN" {
		t.Fatalf("resolved %q; want the ambient credential, not the stored key", creds.AccessKeyID)
	}
}
