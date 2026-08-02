package remote

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// BakedRunners reports, for the account's own images, which runners already
// have a runtime AMI baked — read from the tags the Image Builder distribution
// applies (`cloud-vm-llm:role=runtime-ami`, `cloud-vm-llm:runner=<runner>`). It
// lets `outfit remote bootstrap --wait` tell when a bake has finished.
func BakedRunners(ctx context.Context, cfg aws.Config) (map[string]bool, error) {
	out, err := ec2.NewFromConfig(cfg).DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{"self"},
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:cloud-vm-llm:role"), Values: []string{"runtime-ami"}},
			{Name: aws.String("state"), Values: []string{"available"}},
		},
	})
	if err != nil {
		return nil, err
	}
	baked := map[string]bool{}
	for _, img := range out.Images {
		for _, tag := range img.Tags {
			if aws.ToString(tag.Key) == "cloud-vm-llm:runner" {
				baked[aws.ToString(tag.Value)] = true
			}
		}
	}
	return baked, nil
}
