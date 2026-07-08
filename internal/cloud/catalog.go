package cloud

import (
	"context"
	"sort"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// discoveryRegion builds the EC2 client for DescribeRegions. Any valid region
// works since the call enumerates all enabled regions.
const discoveryRegion = "us-east-1"

// EC2API is the subset of the EC2 client the Catalog uses. *ec2.Client
// satisfies it; tests inject a fake.
type EC2API interface {
	DescribeRegions(ctx context.Context, in *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error)
	DescribeInstanceTypeOfferings(ctx context.Context, in *ec2.DescribeInstanceTypeOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error)
	DescribeInstanceTypes(ctx context.Context, in *ec2.DescribeInstanceTypesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error)
	DescribeImages(ctx context.Context, in *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
}

// Catalog fetches read-only AWS metadata (regions, instance types, images) for
// the blueprint form. Credentials are per-call and used only to build the SDK
// client — never logged, never placed in the process environment.
type Catalog struct {
	// NewClient builds an EC2 client for static credentials. Overridable in tests.
	NewClient func(accessKey, secret, region string) EC2API
}

func NewCatalog() *Catalog {
	return &Catalog{
		NewClient: func(accessKey, secret, region string) EC2API {
			return ec2.New(ec2.Options{
				Region:      region,
				Credentials: credentials.NewStaticCredentialsProvider(accessKey, secret, ""),
			})
		},
	}
}

// Regions returns the account's enabled regions, sorted.
func (c *Catalog) Regions(ctx context.Context, accessKey, secret string) ([]string, error) {
	out, err := c.NewClient(accessKey, secret, discoveryRegion).
		DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
	if err != nil {
		return nil, err
	}
	var regions []string
	for _, r := range out.Regions {
		if r.RegionName != nil {
			regions = append(regions, *r.RegionName)
		}
	}
	sort.Strings(regions)
	return regions, nil
}

// InstanceTypes returns the instance types offered in region, sorted and deduped.
func (c *Catalog) InstanceTypes(ctx context.Context, accessKey, secret, region string) ([]string, error) {
	client := c.NewClient(accessKey, secret, region)
	seen := map[string]bool{}
	var out []string
	var token *string
	for {
		page, err := client.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{
			LocationType: types.LocationTypeRegion,
			NextToken:    token,
		})
		if err != nil {
			return nil, err
		}
		for _, o := range page.InstanceTypeOfferings {
			s := string(o.InstanceType)
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
		if page.NextToken == nil || *page.NextToken == "" {
			break
		}
		token = page.NextToken
	}
	sort.Strings(out)
	return out, nil
}
