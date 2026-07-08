package cloud

import (
	"context"
	"strings"
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
		itypes: func(in *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
			var out []types.InstanceTypeInfo
			for _, it := range in.InstanceTypes {
				switch string(it) {
				case "t3.micro":
					out = append(out, types.InstanceTypeInfo{
						InstanceType: types.InstanceType("t3.micro"),
						VCpuInfo:     &types.VCpuInfo{DefaultVCpus: aws.Int32(2)},
						MemoryInfo:   &types.MemoryInfo{SizeInMiB: aws.Int64(1024)},
					})
				case "c7g.large":
					out = append(out, types.InstanceTypeInfo{
						InstanceType: types.InstanceType("c7g.large"),
						VCpuInfo:     &types.VCpuInfo{DefaultVCpus: aws.Int32(2)},
						MemoryInfo:   &types.MemoryInfo{SizeInMiB: aws.Int64(4096)},
					})
				}
			}
			return &ec2.DescribeInstanceTypesOutput{InstanceTypes: out}, nil
		},
	})
	got, err := c.InstanceTypes(context.Background(), "AK", "sk", "ap-southeast-1")
	if err != nil {
		t.Fatalf("InstanceTypes: %v", err)
	}
	want := []InstanceType{
		{Name: "c7g.large", VCPUs: 2, MemoryMiB: 4096},
		{Name: "t3.micro", VCPUs: 2, MemoryMiB: 1024},
	}
	if len(got) != len(want) {
		t.Fatalf("InstanceTypes len = %d, want %d (%+v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("InstanceTypes[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestArchitectureDetectsArm(t *testing.T) {
	c := catalogWith(&fakeEC2{
		itypes: func(*ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
			return &ec2.DescribeInstanceTypesOutput{InstanceTypes: []types.InstanceTypeInfo{
				{ProcessorInfo: &types.ProcessorInfo{SupportedArchitectures: []types.ArchitectureType{types.ArchitectureTypeArm64}}},
			}}, nil
		},
	})
	arch, err := c.Architecture(context.Background(), "AK", "sk", "ap-southeast-1", "t4g.micro")
	if err != nil {
		t.Fatalf("Architecture: %v", err)
	}
	if arch != "arm64" {
		t.Fatalf("arch = %q, want arm64", arch)
	}
}

func TestArchitectureDefaultsX86(t *testing.T) {
	arch, _ := catalogWith(&fakeEC2{}).Architecture(context.Background(), "AK", "sk", "r", "t3.micro")
	if arch != "x86_64" {
		t.Fatalf("arch = %q, want x86_64 (empty result → default)", arch)
	}
}

func TestImagesResolvesCatalogArchAwareWithDefault(t *testing.T) {
	var nameFilters []string
	c := catalogWith(&fakeEC2{
		images: func(in *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error) {
			for _, fl := range in.Filters {
				if aws.ToString(fl.Name) == "name" {
					nameFilters = append(nameFilters, fl.Values[0])
				}
			}
			return &ec2.DescribeImagesOutput{Images: []types.Image{
				{ImageId: aws.String("ami-old"), CreationDate: aws.String("2024-01-01T00:00:00Z")},
				{ImageId: aws.String("ami-new"), CreationDate: aws.String("2026-05-01T00:00:00Z")},
			}}, nil
		},
	})
	imgs, err := c.Images(context.Background(), "AK", "sk", "ap-southeast-1", "arm64")
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if len(imgs) != 3 {
		t.Fatalf("want 3 catalog images, got %d (%+v)", len(imgs), imgs)
	}
	if imgs[0].Name != "Ubuntu 26.04 LTS" || !imgs[0].Default {
		t.Fatalf("first image must be default Ubuntu 26.04 LTS, got %+v", imgs[0])
	}
	if imgs[0].ID != "ami-new" {
		t.Fatalf("must pick newest by CreationDate, got %q", imgs[0].ID)
	}
	if !strings.Contains(nameFilters[0], "26.04-arm64-server") {
		t.Fatalf("ubuntu 26.04 filter must use arm64 token, got %q", nameFilters[0])
	}
	if !strings.Contains(nameFilters[2], "al2023-ami-2023.*-arm64") {
		t.Fatalf("al2023 filter must use arm64 token, got %q", nameFilters[2])
	}
}
