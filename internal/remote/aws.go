package remote

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
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
