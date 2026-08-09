package remote

import (
	"encoding/json"
	"math"
	"testing"
)

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
