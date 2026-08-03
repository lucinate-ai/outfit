package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/pricing/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// LoadAWSConfig resolves the default AWS config for a region. Credentials are
// not retrieved here — callers that need them (signing, the preflight check)
// call Retrieve on the returned config, keeping the failure guidance close to
// where it is reported.
func LoadAWSConfig(ctx context.Context, region string) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
}

// CallerIdentity returns the AWS account id for the resolved credentials, so
// the bootstrap plan can name the account being deployed into.
func CallerIdentity(ctx context.Context, cfg aws.Config) (string, error) {
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.Account), nil
}

// SharedStackDeployed reports whether the named CloudFormation stack exists in
// the account and region — i.e. whether `outfit remote bootstrap` has already
// run. A stack that does not exist is reported as false, not an error.
func SharedStackDeployed(ctx context.Context, cfg aws.Config, stackName string) (bool, error) {
	_, err := cloudformation.NewFromConfig(cfg).DescribeStacks(ctx, &cloudformation.DescribeStacksInput{
		StackName: aws.String(stackName),
	})
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// SharedLayer is what `outfit remote bootstrap` deployed once for the account:
// the control URLs every environment shares, plus the shared bucket. It is
// discovered from the shared stack's CloudFormation outputs, so it reflects
// what is actually deployed and works from any machine with account access.
type SharedLayer struct {
	Config        Config
	WeightsBucket string
}

// DiscoverSharedLayer reads the shared stack's outputs. An absent stack is the
// account not being bootstrapped, reported with the fix.
func DiscoverSharedLayer(ctx context.Context, cfg aws.Config, stackName string) (SharedLayer, error) {
	out, err := cloudformation.NewFromConfig(cfg).DescribeStacks(ctx, &cloudformation.DescribeStacksInput{
		StackName: aws.String(stackName),
	})
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return SharedLayer{}, fmt.Errorf(
				"the shared infrastructure (stack %q) is not deployed in this account and region — run `outfit remote bootstrap` first",
				stackName)
		}
		return SharedLayer{}, err
	}
	outputs := map[string]string{}
	if len(out.Stacks) > 0 {
		for _, o := range out.Stacks[0].Outputs {
			outputs[aws.ToString(o.OutputKey)] = aws.ToString(o.OutputValue)
		}
	}
	layer := SharedLayer{
		Config: Config{
			StartURL:  outputs["StartUrl"],
			StopURL:   outputs["StopUrl"],
			DeployURL: outputs["DeployUrl"],
			StatsURL:  outputs["StatsUrl"],
			EnvURL:    outputs["EnvUrl"],
			Region:    outputs["Region"],
		},
		WeightsBucket: outputs["WeightsBucket"],
	}
	if layer.Config.StartURL == "" || layer.Config.StopURL == "" || layer.Config.DeployURL == "" {
		return SharedLayer{}, fmt.Errorf(
			"stack %q is missing its control-URL outputs — re-run `outfit remote bootstrap` to update it",
			stackName)
	}
	return layer, nil
}

// GetOnDemandPrice returns the hourly on-demand price for an instance type in
// a region, from the AWS Price List API. The result is cached for 5 minutes
// within a single process lifetime. Returns an error if the pricing service is
// unavailable or the instance type is not found.
func GetOnDemandPrice(ctx context.Context, region, instanceType string) (float64, error) {
	// Pricing is a global service — use us-east-1 as the API endpoint.
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"))
	if err != nil {
		return 0, fmt.Errorf("loading AWS config for pricing: %w", err)
	}
	client := pricing.NewFromConfig(cfg)
	out, err := client.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonEC2"),
		Filters: []types.Filter{
			{Type: types.FilterTypeTermMatch, Field: aws.String("instanceType"), Value: aws.String(instanceType)},
			{Type: types.FilterTypeTermMatch, Field: aws.String("location"), Value: aws.String(region)},
			{Type: types.FilterTypeTermMatch, Field: aws.String("tenancy"), Value: aws.String("Shared")},
			{Type: types.FilterTypeTermMatch, Field: aws.String("preInstalledSw"), Value: aws.String("NA")},
			{Type: types.FilterTypeTermMatch, Field: aws.String("operatingSystem"), Value: aws.String("Linux")},
			{Type: types.FilterTypeTermMatch, Field: aws.String("capacitystatus"), Value: aws.String("Used")},
			{Type: types.FilterTypeTermMatch, Field: aws.String("marketoption"), Value: aws.String("OnDemand")},
		},
	})
	if err != nil {
		return 0, fmt.Errorf("pricing API: %w", err)
	}
	if len(out.PriceList) == 0 {
		return 0, fmt.Errorf("no pricing found for %s in %s", instanceType, region)
	}
	// Parse the first matching price from the Price List JSON.
	price, err := extractPrice([]byte(out.PriceList[0]), instanceType)
	if err != nil {
		return 0, err
	}
	return price, nil
}

// extractPriceSimple does a minimal scan for the price value in the JSON doc.
func extractPriceSimple(doc []byte) (float64, error) {
	// Look for "HOUR": "0.xxxx" pattern in the raw bytes.
	docStr := string(doc)
	hourIdx := strings.Index(docStr, `"HOUR"`)
	if hourIdx == -1 {
		return 0, fmt.Errorf("no hourly price found in pricing document")
	}
	rest := docStr[hourIdx:]
	colonIdx := strings.Index(rest, ":")
	if colonIdx == -1 {
		return 0, fmt.Errorf("malformed price in document")
	}
	// Skip to the value after the colon.
	valStr := strings.TrimSpace(rest[colonIdx+1:])
	// Remove leading quote and extract the number.
	if len(valStr) > 0 && valStr[0] == '"' {
		valStr = valStr[1:]
		end := strings.Index(valStr, "\"")
		if end != -1 {
			valStr = valStr[:end]
		}
	}
	return parseFloat(valStr)
}

func parseFloat(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing price %q: %w", s, err)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("invalid price value %s", s)
	}
	return v, nil
}

// extractPrice parses the on-demand price from a Price List JSON document.
func extractPrice(doc []byte, instanceType string) (float64, error) {
	// The document is a JSON array with one large object. We only need the
	// pricePerUnit field, so we can parse minimally.
	var items []struct {
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
	}
	if err := json.Unmarshal(doc, &items); err != nil {
		// Fallback: the doc can be huge. Try a simpler extraction.
		return extractPriceSimple(doc)
	}
	for _, item := range items {
		for _, product := range item.Products {
			if product.Attributes.InstanceType == instanceType {
				for _, plist := range product.PriceList {
					for _, ondemand := range plist.OnDemand {
						for _, unit := range ondemand.PricePerUnit {
							if unit.Hour != "" {
								return parseFloat(unit.Hour)
							}
						}
					}
				}
			}
		}
	}
	return 0, fmt.Errorf("no on-demand price for %s in document", instanceType)
}
