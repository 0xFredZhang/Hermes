package cloud

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
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

// Image is one selectable OS image resolved for a region+architecture.
type Image struct {
	ID      string // ami-...
	Name    string // friendly, e.g. "Ubuntu 26.04 LTS"
	Default bool   // the form pre-selects the default image
}

const canonicalOwner = "099720109477" // Canonical (Ubuntu) AWS account

// osSpec is one entry in the curated OS catalog. pattern has a single %s for the
// architecture token produced by archToken.
type osSpec struct {
	name      string
	owner     string
	pattern   string
	archToken func(arch string) string
	isDefault bool
}

func ubuntuArchToken(arch string) string {
	if arch == "arm64" {
		return "arm64"
	}
	return "amd64"
}

func al2023ArchToken(arch string) string {
	if arch == "arm64" {
		return "arm64"
	}
	return "x86_64"
}

// osCatalog is the curated image list. Add a distro by adding an entry.
// hvm-ssd* matches both the hvm-ssd and hvm-ssd-gp3 Ubuntu storage variants.
var osCatalog = []osSpec{
	{name: "Ubuntu 26.04 LTS", owner: canonicalOwner, pattern: "ubuntu/images/hvm-ssd*/ubuntu-*-26.04-%s-server-*", archToken: ubuntuArchToken, isDefault: true},
	{name: "Ubuntu 24.04 LTS", owner: canonicalOwner, pattern: "ubuntu/images/hvm-ssd*/ubuntu-*-24.04-%s-server-*", archToken: ubuntuArchToken},
	{name: "Amazon Linux 2023", owner: "amazon", pattern: "al2023-ami-2023.*-%s", archToken: al2023ArchToken},
}

// Architecture reports the CPU architecture ("x86_64" or "arm64") of instanceType,
// defaulting to x86_64 when the type reports both or is unknown.
func (c *Catalog) Architecture(ctx context.Context, accessKey, secret, region, instanceType string) (string, error) {
	out, err := c.NewClient(accessKey, secret, region).DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []types.InstanceType{types.InstanceType(instanceType)},
	})
	if err != nil {
		return "", err
	}
	if len(out.InstanceTypes) == 0 || out.InstanceTypes[0].ProcessorInfo == nil {
		return "x86_64", nil
	}
	return archOf(out.InstanceTypes[0].ProcessorInfo.SupportedArchitectures), nil
}

// archOf collapses AWS's SupportedArchitectures to one token, preferring x86_64
// when a type supports both.
func archOf(supported []types.ArchitectureType) string {
	hasArm, hasX86 := false, false
	for _, a := range supported {
		switch a {
		case types.ArchitectureTypeArm64:
			hasArm = true
		case types.ArchitectureTypeX8664:
			hasX86 = true
		}
	}
	if hasArm && !hasX86 {
		return "arm64"
	}
	return "x86_64"
}

// Images resolves the curated OS catalog to the newest AMI per entry for the
// given region and architecture. Entries that resolve to no image are skipped.
func (c *Catalog) Images(ctx context.Context, accessKey, secret, region, arch string) ([]Image, error) {
	client := c.NewClient(accessKey, secret, region)
	var out []Image
	for _, spec := range osCatalog {
		name := fmt.Sprintf(spec.pattern, spec.archToken(arch))
		res, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
			Owners: []string{spec.owner},
			Filters: []types.Filter{
				{Name: aws.String("name"), Values: []string{name}},
				{Name: aws.String("state"), Values: []string{"available"}},
			},
		})
		if err != nil {
			return nil, err
		}
		if newest := newestImage(res.Images); newest != nil {
			out = append(out, Image{ID: aws.ToString(newest.ImageId), Name: spec.name, Default: spec.isDefault})
		}
	}
	return out, nil
}

// newestImage returns the image with the latest CreationDate (ISO-8601 sorts
// lexicographically), or nil for an empty slice.
func newestImage(imgs []types.Image) *types.Image {
	var best *types.Image
	for i := range imgs {
		if best == nil || aws.ToString(imgs[i].CreationDate) > aws.ToString(best.CreationDate) {
			best = &imgs[i]
		}
	}
	return best
}
