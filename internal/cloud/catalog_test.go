package cloud

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// fakeEC2 implements EC2API; each field, when set, backs one method.
type fakeEC2 struct {
	regions   func(*ec2.DescribeRegionsInput) (*ec2.DescribeRegionsOutput, error)
	offerings func(*ec2.DescribeInstanceTypeOfferingsInput) (*ec2.DescribeInstanceTypeOfferingsOutput, error)
	itypes    func(*ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error)
	images    func(*ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error)
}

func (f *fakeEC2) DescribeRegions(_ context.Context, in *ec2.DescribeRegionsInput, _ ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
	if f.regions != nil {
		return f.regions(in)
	}
	return &ec2.DescribeRegionsOutput{}, nil
}
func (f *fakeEC2) DescribeInstanceTypeOfferings(_ context.Context, in *ec2.DescribeInstanceTypeOfferingsInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error) {
	if f.offerings != nil {
		return f.offerings(in)
	}
	return &ec2.DescribeInstanceTypeOfferingsOutput{}, nil
}
func (f *fakeEC2) DescribeInstanceTypes(_ context.Context, in *ec2.DescribeInstanceTypesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	if f.itypes != nil {
		return f.itypes(in)
	}
	return &ec2.DescribeInstanceTypesOutput{}, nil
}
func (f *fakeEC2) DescribeImages(_ context.Context, in *ec2.DescribeImagesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	if f.images != nil {
		return f.images(in)
	}
	return &ec2.DescribeImagesOutput{}, nil
}

func catalogWith(f *fakeEC2) *Catalog {
	return &Catalog{NewClient: func(_, _, _ string) EC2API { return f }}
}

func TestRegionsSorted(t *testing.T) {
	c := catalogWith(&fakeEC2{
		regions: func(*ec2.DescribeRegionsInput) (*ec2.DescribeRegionsOutput, error) {
			return &ec2.DescribeRegionsOutput{Regions: []types.Region{
				{RegionName: aws.String("us-west-2")},
				{RegionName: aws.String("ap-southeast-1")},
			}}, nil
		},
	})
	got, err := c.Regions(context.Background(), "AK", "sk")
	if err != nil {
		t.Fatalf("Regions: %v", err)
	}
	if len(got) != 2 || got[0] != "ap-southeast-1" || got[1] != "us-west-2" {
		t.Fatalf("Regions = %v, want [ap-southeast-1 us-west-2]", got)
	}
}

func TestInstanceTypesPaginatesAndSorts(t *testing.T) {
	page := 0
	c := catalogWith(&fakeEC2{
		offerings: func(*ec2.DescribeInstanceTypeOfferingsInput) (*ec2.DescribeInstanceTypeOfferingsOutput, error) {
			page++
			if page == 1 {
				return &ec2.DescribeInstanceTypeOfferingsOutput{
					InstanceTypeOfferings: []types.InstanceTypeOffering{{InstanceType: types.InstanceType("t3.micro")}},
					NextToken:             aws.String("more"),
				}, nil
			}
			return &ec2.DescribeInstanceTypeOfferingsOutput{
				InstanceTypeOfferings: []types.InstanceTypeOffering{{InstanceType: types.InstanceType("c7g.large")}},
			}, nil
		},
	})
	got, err := c.InstanceTypes(context.Background(), "AK", "sk", "ap-southeast-1")
	if err != nil {
		t.Fatalf("InstanceTypes: %v", err)
	}
	if len(got) != 2 || got[0] != "c7g.large" || got[1] != "t3.micro" {
		t.Fatalf("InstanceTypes = %v, want [c7g.large t3.micro]", got)
	}
}
