package pulumiengine

import (
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

type recordMocks struct {
	mu    sync.Mutex
	types []string
	calls []string
}

func (m *recordMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	m.types = append(m.types, args.TypeToken)
	m.mu.Unlock()
	outputs := args.Inputs.Mappable()
	if args.TypeToken == "aws:ec2/instance:Instance" {
		outputs["publicIp"] = "1.2.3.4"
		outputs["publicDns"] = "ec2-1-2-3-4.compute.amazonaws.com"
	}
	if args.TypeToken == "aws:ec2/eip:Eip" {
		outputs["publicIp"] = "52.1.2.3"
	}
	return args.Name + "_id", resource.NewPropertyMapFromMap(outputs), nil
}

func (m *recordMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	m.mu.Lock()
	m.calls = append(m.calls, args.Token)
	m.mu.Unlock()
	switch args.Token {
	case "aws:ec2/getVpc:getVpc":
		return resource.NewPropertyMapFromMap(map[string]any{"id": "vpc-123"}), nil
	case "aws:ec2/getSubnets:getSubnets":
		return resource.NewPropertyMapFromMap(map[string]any{"ids": []any{"subnet-123"}}), nil
	case "aws:ec2/getAmi:getAmi":
		return resource.NewPropertyMapFromMap(map[string]any{"id": "ami-0abc"}), nil
	case "aws:ec2/getInstanceType:getInstanceType":
		return resource.NewPropertyMapFromMap(map[string]any{
			"supportedArchitectures": []any{"x86_64"},
		}), nil
	}
	return args.Args, nil
}

func (m *recordMocks) MethodCall(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args, nil
}

func TestBuildProgramDeclaresResources(t *testing.T) {
	params := provisioner.BlueprintParams{
		Region: "ap-southeast-1",
		SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
			{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
		}},
		EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 2, RootVolumeGB: 8},
	}
	m := &recordMocks{}
	err := pulumi.RunErr(buildProgram(params), pulumi.WithMocks("hermes", "test", m))
	if err != nil {
		t.Fatalf("RunErr: %v", err)
	}
	count := func(tok string) int {
		n := 0
		for _, x := range m.types {
			if x == tok {
				n++
			}
		}
		return n
	}
	if got := count("aws:ec2/securityGroup:SecurityGroup"); got != 1 {
		t.Fatalf("security groups = %d, want 1", got)
	}
	if got := count("aws:ec2/instance:Instance"); got != 2 {
		t.Fatalf("instances = %d, want 2 (matches EC2.Count)", got)
	}
	if got := count("aws:ec2/eip:Eip"); got != 2 {
		t.Fatalf("eips = %d, want 2 (one per instance)", got)
	}

	called := func(tok string) bool {
		for _, x := range m.calls {
			if x == tok {
				return true
			}
		}
		return false
	}
	// AMI must resolve via ec2 getAmi (ec2:DescribeImages), not ssm getParameter
	// (which needs an extra ssm:GetParameter permission users often lack).
	if !called("aws:ec2/getAmi:getAmi") {
		t.Fatalf("expected AMI resolution via ec2 getAmi; calls=%v", m.calls)
	}
	if called("aws:ssm/getParameter:getParameter") {
		t.Fatalf("must not resolve AMI via ssm getParameter; calls=%v", m.calls)
	}
	if !called("aws:ec2/getInstanceType:getInstanceType") {
		t.Fatalf("expected arch resolution via getInstanceType; calls=%v", m.calls)
	}
}

func TestResolveArch(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"x86_64"}, "x86_64"},
		{[]string{"arm64"}, "arm64"},
		{[]string{"x86_64", "arm64"}, "x86_64"},
		{nil, "x86_64"},
	}
	for _, c := range cases {
		if got := resolveArch(c.in); got != c.want {
			t.Errorf("resolveArch(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUbuntuNameFilterArchAware(t *testing.T) {
	if got := ubuntuNameFilter("x86_64"); !strings.Contains(got, "26.04-amd64-server") {
		t.Errorf("x86_64 filter = %q, want amd64 token", got)
	}
	if got := ubuntuNameFilter("arm64"); !strings.Contains(got, "26.04-arm64-server") {
		t.Errorf("arm64 filter = %q, want arm64 token", got)
	}
}
