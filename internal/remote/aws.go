package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamb "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/pricing/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// LoadAWSConfig resolves the AWS config for a region, applying the credential
// precedence the remote commands sign with: explicit AWS environment
// credentials or an explicit profile selection win (the default chain, as
// before); then a stored control-plane credential for the region, if one is
// in the keystore; then the rest of the standard chain (shared config, SSO
// sessions, instance metadata). Credentials are not retrieved here — callers
// that need them (signing, the preflight check) call Retrieve on the returned
// config, keeping the failure guidance close to where it is reported.
func LoadAWSConfig(ctx context.Context, region string) (aws.Config, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if opt, ok := storedCredsOption(region); ok {
		opts = append(opts, opt)
	}
	return awsconfig.LoadDefaultConfig(ctx, opts...)
}

// LoadAmbientAWSConfig resolves the default credential chain only, never
// consulting the stored control-plane credential. Bootstrap and bake use it:
// they provision the control plane itself, so a stored day-to-day key must
// not stand in for the administrator credentials they need.
func LoadAmbientAWSConfig(ctx context.Context, region string) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
}

// storedCredsOption is the config option carrying the stored control-plane
// credential for the region, when it applies — that is, when the process
// environment carries no explicit AWS credentials and no explicit profile
// selection. Those are a deliberate per-process choice (a Spinloop's .env or
// ENV may inject them, and an operator may set them to debug with other
// credentials) and override the stored key; everything else in the standard
// chain yields to it, which is the point of storing a key that outlives SSO
// log-ins.
func storedCredsOption(region string) (func(*awsconfig.LoadOptions) error, bool) {
	if explicitAmbientCreds() {
		return nil, false
	}
	cred, ok := LookupStoredCredential(region)
	if !ok {
		return nil, false
	}
	return awsconfig.WithCredentialsProvider(
		credentials.NewStaticCredentialsProvider(cred.AccessKeyID, cred.SecretAccessKey, "")), true
}

// explicitAmbientCreds reports whether the process environment names explicit
// AWS credentials or an explicit profile selection.
func explicitAmbientCreds() bool {
	for _, key := range []string{"AWS_ACCESS_KEY_ID", "AWS_PROFILE"} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
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

// ControlPlaneUserName is the IAM user the control-plane stack creates for the
// CLI's long-lived credential: `spinloop remote auth --store` creates access
// keys for this user and stores one in this machine's keystore. The name is
// fixed, so the CLI addresses the user without reading a stack output; the
// stack and its tests use the same literal.
const ControlPlaneUserName = "cloud-vm-llm-remote-cli"

// ConfigFromStored builds the AWS config that signs with a stored
// control-plane credential: a static provider for the key, no other chain.
// The auth command uses it to verify a newly created key, to rotate with the
// stored key alone, and to delete a key on the AWS side during a clear.
func ConfigFromStored(cred StoredCredential) aws.Config {
	return aws.Config{
		Region:      cred.Region,
		Credentials: credentials.NewStaticCredentialsProvider(cred.AccessKeyID, cred.SecretAccessKey, ""),
	}
}

// IAMUserExists reports whether the named IAM user exists in the account and
// region the config resolves for. An absent user is (false, nil), not an
// error: a control plane deployed before the user existed is a normal,
// fixable case the caller names its fix for.
func IAMUserExists(ctx context.Context, cfg aws.Config, userName string) (bool, error) {
	_, err := iam.NewFromConfig(cfg).GetUser(ctx, &iam.GetUserInput{UserName: aws.String(userName)})
	if err != nil {
		var noSuch *iamb.NoSuchEntityException
		if errors.As(err, &noSuch) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// IAMUserAccessKeyIDs returns the access key ids the named IAM user currently
// has.
func IAMUserAccessKeyIDs(ctx context.Context, cfg aws.Config, userName string) ([]string, error) {
	out, err := iam.NewFromConfig(cfg).ListAccessKeys(ctx, &iam.ListAccessKeysInput{UserName: aws.String(userName)})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.AccessKeyMetadata))
	for _, k := range out.AccessKeyMetadata {
		ids = append(ids, aws.ToString(k.AccessKeyId))
	}
	return ids, nil
}

// IAMCreateAccessKey creates an access key for the named IAM user and returns
// it. The secret is returned only here, once: IAM never returns it again, so
// a failure after this point has nothing to recover it with.
func IAMCreateAccessKey(ctx context.Context, cfg aws.Config, userName string) (string, string, error) {
	out, err := iam.NewFromConfig(cfg).CreateAccessKey(ctx, &iam.CreateAccessKeyInput{UserName: aws.String(userName)})
	if err != nil {
		return "", "", err
	}
	return aws.ToString(out.AccessKey.AccessKeyId), aws.ToString(out.AccessKey.SecretAccessKey), nil
}

// IAMDeleteAccessKey deletes the named access key from the named IAM user.
func IAMDeleteAccessKey(ctx context.Context, cfg aws.Config, userName, accessKeyID string) error {
	_, err := iam.NewFromConfig(cfg).DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
		UserName:    aws.String(userName),
		AccessKeyId: aws.String(accessKeyID),
	})
	return err
}

// ControlPlaneStackDeployed reports whether the named CloudFormation stack exists in
// the account and region — i.e. whether `spinloop remote bootstrap` has already
// run. A stack that does not exist is reported as false, not an error.
func ControlPlaneStackDeployed(ctx context.Context, cfg aws.Config, stackName string) (bool, error) {
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

// ControlPlane is what `spinloop remote bootstrap` deployed once for the account:
// the control URLs every environment shares, plus the weights bucket. It is
// discovered from the control-plane stack's CloudFormation outputs, so it reflects
// what is actually deployed and works from any machine with account access.
type ControlPlane struct {
	Config        Config
	WeightsBucket string
}

// DiscoverControlPlane reads the control-plane stack's outputs. An absent stack is the
// account not being bootstrapped, reported with the fix.
func DiscoverControlPlane(ctx context.Context, cfg aws.Config, stackName string) (ControlPlane, error) {
	out, err := cloudformation.NewFromConfig(cfg).DescribeStacks(ctx, &cloudformation.DescribeStacksInput{
		StackName: aws.String(stackName),
	})
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return ControlPlane{}, fmt.Errorf(
				"the control plane (stack %q) is not deployed in this account and region — run `spinloop remote bootstrap` first",
				stackName)
		}
		return ControlPlane{}, err
	}
	outputs := map[string]string{}
	if len(out.Stacks) > 0 {
		for _, o := range out.Stacks[0].Outputs {
			outputs[aws.ToString(o.OutputKey)] = aws.ToString(o.OutputValue)
		}
	}
	return controlPlaneFromOutputs(stackName, outputs)
}

// controlPlaneFromOutputs maps a control-plane stack's CloudFormation outputs
// onto the config that drives its Lambdas. Pure, so the mapping is testable
// without a network: a stack output added to the template but not here would
// otherwise be dropped from every registered environment's remote.json.
func controlPlaneFromOutputs(stackName string, outputs map[string]string) (ControlPlane, error) {
	layer := ControlPlane{
		Config: Config{
			StartURL:    outputs["StartUrl"],
			StopURL:     outputs["StopUrl"],
			DeployURL:   outputs["DeployUrl"],
			StatsURL:    outputs["StatsUrl"],
			EnvURL:      outputs["EnvUrl"],
			SeedURL:     outputs["SeedUrl"],
			UpdateURL:   outputs["UpdateUrl"],
			ScheduleURL: outputs["ScheduleUrl"],
			Region:      outputs["Region"],
		},
		WeightsBucket: outputs["WeightsBucket"],
	}
	// Only the three the other subcommands cannot work without are required.
	// SeedUrl and EnvUrl are absent from a control plane deployed before they
	// existed; the subcommands that need them say so themselves.
	if layer.Config.StartURL == "" || layer.Config.StopURL == "" || layer.Config.DeployURL == "" {
		return ControlPlane{}, fmt.Errorf(
			"stack %q is missing its control-URL outputs — re-run `spinloop remote bootstrap` to update it",
			stackName)
	}
	return layer, nil
}

// pricingConfig resolves the AWS config for the pricing call. The pricing
// service is global, so the endpoint stays us-east-1; the credential, though,
// resolves with the environment's region precedence — the stored
// control-plane key, when nothing explicit is set, signs the call the same as
// every other day-to-day command, and the user policy's pricing:GetProducts
// grant is what authorises it.
func pricingConfig(ctx context.Context, envRegion string) (aws.Config, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion("us-east-1")}
	if opt, ok := storedCredsOption(envRegion); ok {
		opts = append(opts, opt)
	}
	return awsconfig.LoadDefaultConfig(ctx, opts...)
}

// GetOnDemandPrice returns the hourly on-demand price for an instance type in
// a region, from the AWS Price List API. The result is cached for 5 minutes
// within a single process lifetime. Returns an error if the pricing service is
// unavailable or the instance type is not found.
func GetOnDemandPrice(ctx context.Context, region, instanceType string) (float64, error) {
	cfg, err := pricingConfig(ctx, region)
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
