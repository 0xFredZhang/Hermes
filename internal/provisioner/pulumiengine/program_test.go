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
	mu        sync.Mutex
	types     []string
	calls     []string
	resources []resourceRecord
}

type resourceRecord struct {
	typ  string
	name string
	raw  resource.PropertyMap
}

func (m *recordMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	m.types = append(m.types, args.TypeToken)
	m.resources = append(m.resources, resourceRecord{
		typ:  args.TypeToken,
		name: args.Name,
		raw:  args.Inputs,
	})
	m.mu.Unlock()
	outputs := args.Inputs.Mappable()
	if args.TypeToken == "aws:ec2/instance:Instance" {
		outputs["publicIp"] = "1.2.3.4"
		outputs["publicDns"] = "ec2-1-2-3-4.compute.amazonaws.com"
	}
	if args.TypeToken == "aws:ec2/eip:Eip" {
		outputs["publicIp"] = "52.1.2.3"
	}
	if args.TypeToken == "aws:ec2/vpc:Vpc" {
		outputs["id"] = "vpc-managed"
	}
	if args.TypeToken == "aws:ec2/subnet:Subnet" {
		outputs["id"] = args.Name + "_id"
	}
	if args.TypeToken == "aws:rds/instance:Instance" {
		outputs["address"] = "db.example"
		outputs["endpoint"] = "db.example:3306"
		outputs["port"] = 3306
		outputs["username"] = "admin"
	}
	if args.TypeToken == "aws:elasticache/replicationGroup:ReplicationGroup" {
		outputs["primaryEndpointAddress"] = "redis.example"
		outputs["readerEndpointAddress"] = "redis-ro.example"
		outputs["port"] = 6379
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
	case "aws:index/getAvailabilityZones:getAvailabilityZones":
		return resource.NewPropertyMapFromMap(map[string]any{"names": []any{"ap-southeast-1a", "ap-southeast-1b"}}), nil
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
	err := pulumi.RunErr(buildProgram(provisioner.Spec{Params: params}), pulumi.WithMocks("hermes", "test", m))
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
	for _, tok := range []string{
		"aws:ec2/vpc:Vpc",
		"aws:ec2/internetGateway:InternetGateway",
		"aws:ec2/subnet:Subnet",
		"aws:ec2/routeTable:RouteTable",
		"aws:ec2/routeTableAssociation:RouteTableAssociation",
		"aws:rds/subnetGroup:SubnetGroup",
		"aws:rds/instance:Instance",
		"aws:elasticache/subnetGroup:SubnetGroup",
		"aws:elasticache/replicationGroup:ReplicationGroup",
	} {
		if got := count(tok); got != 0 {
			t.Fatalf("%s = %d, want 0 when optional resources are disabled", tok, got)
		}
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

func TestBuildProgramDeclaresManagedNetwork(t *testing.T) {
	params := provisioner.BlueprintParams{
		Region: "ap-southeast-1",
		SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
			{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
		}},
		EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8},
		Network: provisioner.Network{
			Enabled:             true,
			VPCCIDR:             "10.42.0.0/16",
			PublicSubnetCIDRs:   []string{"10.42.1.0/24", "10.42.2.0/24"},
			MapPublicIPOnLaunch: true,
		},
	}
	m := &recordMocks{}
	var outputs map[string]any
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		if err := buildProgram(provisioner.Spec{Params: params})(ctx); err != nil {
			return err
		}
		outputs = map[string]any{}
		for key := range ctx.GetCurrentExportMap() {
			outputs[key] = true
		}
		return nil
	}, pulumi.WithMocks("hermes", "test", m))
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
	wantCounts := map[string]int{
		"aws:ec2/vpc:Vpc":                                     1,
		"aws:ec2/internetGateway:InternetGateway":             1,
		"aws:ec2/subnet:Subnet":                               2,
		"aws:ec2/routeTable:RouteTable":                       1,
		"aws:ec2/routeTableAssociation:RouteTableAssociation": 2,
		"aws:ec2/securityGroup:SecurityGroup":                 1,
		"aws:ec2/instance:Instance":                           1,
	}
	for tok, want := range wantCounts {
		if got := count(tok); got != want {
			t.Fatalf("%s = %d, want %d; all resources=%v", tok, got, want, m.types)
		}
	}
	if outputs["vpc_id"] == nil || outputs["subnet_ids"] == nil {
		t.Fatalf("missing managed network outputs: %+v", outputs)
	}
	for _, tok := range []string{"aws:ec2/getVpc:getVpc", "aws:ec2/getSubnets:getSubnets"} {
		for _, call := range m.calls {
			if call == tok {
				t.Fatalf("managed network should not look up default network; calls=%v", m.calls)
			}
		}
	}
}

func TestBuildProgramDeclaresOptionalRDSAndRedis(t *testing.T) {
	params := provisioner.BlueprintParams{
		Region: "ap-southeast-1",
		SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
			{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
		}},
		EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8},
		RDS: provisioner.RDS{
			Enabled:            true,
			Engine:             "mysql",
			EngineVersion:      "8.0",
			InstanceClass:      "db.t3.micro",
			AllocatedStorageGB: 20,
			DBName:             "app",
			Username:           "admin",
			Port:               3306,
		},
		Redis: provisioner.Redis{
			Enabled:       true,
			Engine:        "redis",
			EngineVersion: "7.2",
			NodeType:      "cache.t3.micro",
			NodeCount:     1,
			Port:          6379,
		},
	}
	m := &recordMocks{}
	var outputs map[string]any
	const rdsPassword = "HermesStoredPassword123!"
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		if err := buildProgram(provisioner.Spec{
			Params:  params,
			Secrets: provisioner.RuntimeSecrets{RDSPassword: rdsPassword},
		})(ctx); err != nil {
			return err
		}
		outputs = map[string]any{}
		for key := range ctx.GetCurrentExportMap() {
			outputs[key] = true
		}
		return nil
	}, pulumi.WithMocks("hermes", "test", m))
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
	wantCounts := map[string]int{
		"aws:rds/subnetGroup:SubnetGroup":                   1,
		"aws:rds/instance:Instance":                         1,
		"aws:elasticache/subnetGroup:SubnetGroup":           1,
		"aws:elasticache/replicationGroup:ReplicationGroup": 1,
		"aws:ec2/securityGroupRule:SecurityGroupRule":       2,
		"aws:ec2/securityGroup:SecurityGroup":               3,
		"random:index/randomPassword:RandomPassword":        0,
	}
	for tok, want := range wantCounts {
		if got := count(tok); got != want {
			t.Fatalf("%s = %d, want %d; all resources=%v", tok, got, want, m.types)
		}
	}
	rdsInput, ok := resourceInputs(m.resources, "aws:rds/instance:Instance", "hermes-rds")
	if !ok {
		t.Fatalf("RDS instance inputs not recorded: %+v", m.resources)
	}
	passwordValue, ok := rdsInput.raw["password"]
	if !ok {
		t.Fatalf("RDS password input missing: %+v", rdsInput.raw)
	}
	if !passwordValue.ContainsSecrets() {
		t.Fatalf("RDS password input must be marked secret: %v", passwordValue)
	}
	if got := passwordValue.SecretValue().Element.StringValue(); got != rdsPassword {
		t.Fatalf("RDS password input = %q, want runtime secret password", got)
	}
	for _, key := range []string{
		"rds_endpoint",
		"rds_address",
		"rds_port",
		"rds_username",
		"redis_primary_endpoint",
		"redis_reader_endpoint",
		"redis_port",
	} {
		if outputs[key] == nil {
			t.Fatalf("missing output %q; outputs=%+v", key, outputs)
		}
	}
	if outputs["rds_password"] != nil {
		t.Fatalf("RDS password must not be exported: %+v", outputs)
	}
}

func TestBuildProgramRequiresRuntimeRDSPassword(t *testing.T) {
	params := provisioner.BlueprintParams{
		Region: "ap-southeast-1",
		SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
			{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
		}},
		EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8},
		RDS: provisioner.RDS{Enabled: true},
	}
	m := &recordMocks{}
	err := pulumi.RunErr(buildProgram(provisioner.Spec{Params: params}), pulumi.WithMocks("hermes", "test", m))
	if err == nil || !strings.Contains(err.Error(), "rds password") {
		t.Fatalf("RunErr err = %v, want missing rds password error", err)
	}
	if got := countResourceType(m.types, "random:index/randomPassword:RandomPassword"); got != 0 {
		t.Fatalf("random password resources = %d, want 0", got)
	}
}

func TestBuildProgramDeclaresRedisAuthToken(t *testing.T) {
	params := provisioner.BlueprintParams{
		Region: "ap-southeast-1",
		SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
			{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
		}},
		EC2:   provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8},
		Redis: provisioner.Redis{Enabled: true, AuthEnabled: true},
	}
	params.ApplyDefaults()
	m := &recordMocks{}
	var outputs map[string]any
	const redisToken = "RedisAuthTokenForHermes123!"
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		if err := buildProgram(provisioner.Spec{
			Params:  params,
			Secrets: provisioner.RuntimeSecrets{RedisAuthToken: redisToken},
		})(ctx); err != nil {
			return err
		}
		outputs = map[string]any{}
		for key := range ctx.GetCurrentExportMap() {
			outputs[key] = true
		}
		return nil
	}, pulumi.WithMocks("hermes", "test", m))
	if err != nil {
		t.Fatalf("RunErr: %v", err)
	}
	redisInput, ok := resourceInputs(m.resources, "aws:elasticache/replicationGroup:ReplicationGroup", "hermes-redis")
	if !ok {
		t.Fatalf("Redis replication group inputs not recorded: %+v", m.resources)
	}
	authToken, ok := redisInput.raw["authToken"]
	if !ok {
		t.Fatalf("Redis authToken input missing: %+v", redisInput.raw)
	}
	if !authToken.ContainsSecrets() {
		t.Fatalf("Redis authToken must be marked secret: %v", authToken)
	}
	if got := authToken.SecretValue().Element.StringValue(); got != redisToken {
		t.Fatalf("Redis authToken input = %q, want runtime auth token", got)
	}
	if got := redisInput.raw["transitEncryptionEnabled"].BoolValue(); !got {
		t.Fatalf("transitEncryptionEnabled = false, want true when Redis auth is enabled")
	}
	if got := redisInput.raw["authTokenUpdateStrategy"].StringValue(); got != "SET" {
		t.Fatalf("authTokenUpdateStrategy = %q, want SET", got)
	}
	if outputs["redis_auth_token"] != nil {
		t.Fatalf("Redis auth token must not be exported: %+v", outputs)
	}
}

func TestBuildProgramRequiresRuntimeRedisAuthToken(t *testing.T) {
	params := provisioner.BlueprintParams{
		Region: "ap-southeast-1",
		SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
			{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
		}},
		EC2:   provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8},
		Redis: provisioner.Redis{Enabled: true, AuthEnabled: true},
	}
	m := &recordMocks{}
	err := pulumi.RunErr(buildProgram(provisioner.Spec{Params: params}), pulumi.WithMocks("hermes", "test", m))
	if err == nil || !strings.Contains(err.Error(), "redis auth token") {
		t.Fatalf("RunErr err = %v, want missing redis auth token error", err)
	}
}

func resourceInputs(records []resourceRecord, typ, name string) (resourceRecord, bool) {
	for _, r := range records {
		if r.typ == typ && r.name == name {
			return r, true
		}
	}
	return resourceRecord{}, false
}

func countResourceType(types []string, tok string) int {
	n := 0
	for _, x := range types {
		if x == tok {
			n++
		}
	}
	return n
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
