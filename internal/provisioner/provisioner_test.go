package provisioner

import "testing"

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
