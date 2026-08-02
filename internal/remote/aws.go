package remote

import (
	"context"
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
