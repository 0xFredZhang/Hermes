package provisioner

import (
	"encoding/json"
	"testing"
)

func validParams() BlueprintParams {
	return BlueprintParams{
		Region: "ap-southeast-1",
		SecurityGroup: SecurityGroup{Ingress: []Ingress{
			{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
		}},
		EC2: EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*BlueprintParams)
		wantErr bool
	}{
		{"valid", func(*BlueprintParams) {}, false},
		{"empty region", func(p *BlueprintParams) { p.Region = "" }, true},
		{"empty instance type", func(p *BlueprintParams) { p.EC2.InstanceType = "" }, true},
		{"count zero", func(p *BlueprintParams) { p.EC2.Count = 0 }, true},
		{"count over max", func(p *BlueprintParams) { p.EC2.Count = 11 }, true},
		{"disk too small", func(p *BlueprintParams) { p.EC2.RootVolumeGB = 4 }, true},
		{"bad port", func(p *BlueprintParams) { p.SecurityGroup.Ingress[0].Port = 0 }, true},
		{"bad protocol", func(p *BlueprintParams) { p.SecurityGroup.Ingress[0].Protocol = "icmp" }, true},
		{"bad cidr", func(p *BlueprintParams) { p.SecurityGroup.Ingress[0].CIDR = "not-a-cidr" }, true},
		{"empty ami is allowed", func(p *BlueprintParams) { p.EC2.AMI = "" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validParams()
			tt.mutate(&p)
			err := p.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDefaultOptionalResources(t *testing.T) {
	p := validParams()
	p.ApplyDefaults()

	if p.Network.Enabled {
		t.Fatalf("managed network should default disabled: %+v", p.Network)
	}
	if p.Network.VPCCIDR != "10.0.0.0/16" {
		t.Fatalf("unexpected VPC CIDR default: %+v", p.Network)
	}
	if len(p.Network.PublicSubnetCIDRs) != 2 || p.Network.PublicSubnetCIDRs[0] != "10.0.1.0/24" || p.Network.PublicSubnetCIDRs[1] != "10.0.2.0/24" {
		t.Fatalf("unexpected subnet CIDR defaults: %+v", p.Network)
	}
	if !p.Network.MapPublicIPOnLaunch {
		t.Fatalf("managed public subnets should map public IPs by default: %+v", p.Network)
	}
	if p.RDS.Enabled || p.Redis.Enabled {
		t.Fatalf("optional resources should default disabled: rds=%+v redis=%+v", p.RDS, p.Redis)
	}
	if p.RDS.Engine != "mysql" || p.RDS.EngineVersion != "8.0" || p.RDS.InstanceClass != "db.t3.micro" {
		t.Fatalf("unexpected RDS defaults: %+v", p.RDS)
	}
	if p.RDS.AllocatedStorageGB != 20 || p.RDS.DBName != "app" || p.RDS.Username != "admin" || p.RDS.Port != 3306 {
		t.Fatalf("unexpected RDS defaults: %+v", p.RDS)
	}
	if p.Redis.Engine != "redis" || p.Redis.EngineVersion != "7.2" || p.Redis.NodeType != "cache.t3.micro" {
		t.Fatalf("unexpected Redis defaults: %+v", p.Redis)
	}
	if p.Redis.NodeCount != 1 || p.Redis.Port != 6379 {
		t.Fatalf("unexpected Redis defaults: %+v", p.Redis)
	}
}

func TestValidateManagedNetwork(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*BlueprintParams)
		wantErr bool
	}{
		{"disabled ignores empty network", func(p *BlueprintParams) {
			p.Network.VPCCIDR = ""
			p.Network.PublicSubnetCIDRs = nil
		}, false},
		{"enabled valid defaults", func(p *BlueprintParams) { p.Network.Enabled = true }, false},
		{"enabled bad vpc cidr", func(p *BlueprintParams) {
			p.Network.Enabled = true
			p.Network.VPCCIDR = "not-a-cidr"
		}, true},
		{"enabled no subnets", func(p *BlueprintParams) {
			p.Network.Enabled = true
			p.Network.PublicSubnetCIDRs = []string{}
		}, true},
		{"enabled bad subnet cidr", func(p *BlueprintParams) {
			p.Network.Enabled = true
			p.Network.PublicSubnetCIDRs = []string{"10.0.1.0/24", "bad"}
		}, true},
		{"enabled rds needs two managed subnets", func(p *BlueprintParams) {
			p.Network.Enabled = true
			p.Network.PublicSubnetCIDRs = []string{"10.0.1.0/24"}
			p.RDS.Enabled = true
		}, true},
		{"enabled redis needs two managed subnets", func(p *BlueprintParams) {
			p.Network.Enabled = true
			p.Network.PublicSubnetCIDRs = []string{"10.0.1.0/24"}
			p.Redis.Enabled = true
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validParams()
			p.ApplyDefaults()
			tt.mutate(&p)
			err := p.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateOptionalResources(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*BlueprintParams)
		wantErr bool
	}{
		{"rds valid defaults", func(p *BlueprintParams) { p.RDS.Enabled = true }, false},
		{"rds storage too small", func(p *BlueprintParams) { p.RDS.Enabled = true; p.RDS.AllocatedStorageGB = 5 }, true},
		{"rds bad engine", func(p *BlueprintParams) { p.RDS.Enabled = true; p.RDS.Engine = "postgres" }, true},
		{"rds bad port", func(p *BlueprintParams) { p.RDS.Enabled = true; p.RDS.Port = 70000 }, true},
		{"redis valid defaults", func(p *BlueprintParams) { p.Redis.Enabled = true }, false},
		{"redis bad engine", func(p *BlueprintParams) { p.Redis.Enabled = true; p.Redis.Engine = "valkey" }, true},
		{"redis bad node count", func(p *BlueprintParams) { p.Redis.Enabled = true; p.Redis.NodeCount = 6 }, true},
		{"redis bad port", func(p *BlueprintParams) { p.Redis.Enabled = true; p.Redis.Port = 70000 }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validParams()
			p.ApplyDefaults()
			tt.mutate(&p)
			err := p.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBlueprintParamsOldJSONDefaultsOptionalResources(t *testing.T) {
	var p BlueprintParams
	if err := json.Unmarshal([]byte(`{
		"region":"ap-southeast-1",
		"security_group":{"ingress":[{"port":22,"protocol":"tcp","cidr":"0.0.0.0/0","desc":"SSH"}]},
		"ec2":{"instance_type":"t3.micro","count":1,"ami":"","root_volume_gb":8,"key_name":""}
	}`), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("old blueprint JSON should validate after defaults: %v", err)
	}
	if p.RDS.Enabled || p.Redis.Enabled {
		t.Fatalf("old blueprint JSON should keep optional resources disabled: rds=%+v redis=%+v", p.RDS, p.Redis)
	}
	if p.Network.Enabled {
		t.Fatalf("old blueprint JSON should keep managed network disabled: %+v", p.Network)
	}
	if p.Network.VPCCIDR == "" || len(p.Network.PublicSubnetCIDRs) == 0 {
		t.Fatalf("old blueprint JSON should receive managed network defaults: %+v", p.Network)
	}
}
