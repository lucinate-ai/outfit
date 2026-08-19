package remote

import (
	"encoding/json"
	"math"
	"testing"
)

func TestControlPlaneFromOutputs_MapsEveryStackOutput(t *testing.T) {
	// The keys are the outputs the control-plane stack (remote/lib/llm-stack.ts)
	// publishes for the config. If the template gains a new control-URL output,
	// the mapping in controlPlaneFromOutputs must take it on: an output landed
	// here but not mapped is dropped from every registered environment's
	// remote.json without error — update_url was exactly that, which left
	// `outfit remote keep` unusable on freshly registered environments.
	outputs := map[string]string{
		"StartUrl":               "https://start.example.aws/",
		"StopUrl":                "https://stop.example.aws/",
		"DeployUrl":              "https://deploy.example.aws/",
		"StatsUrl":               "https://stats.example.aws/",
		"EnvUrl":                 "https://env.example.aws/",
		"UpdateUrl":              "https://update.example.aws/",
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
