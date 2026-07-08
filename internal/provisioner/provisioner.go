// Package provisioner defines the engine abstraction that turns a blueprint
// into real cloud resources, plus the structured blueprint parameter types.
package provisioner

import (
	"context"
	"fmt"
	"io"
	"net"
)

type Ingress struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	CIDR     string `json:"cidr"`
	Desc     string `json:"desc"`
}

type SecurityGroup struct {
	Ingress []Ingress `json:"ingress"`
}

type EC2 struct {
	InstanceType string `json:"instance_type"`
	Count        int    `json:"count"`
	AMI          string `json:"ami"` // empty = auto-resolve latest AL2023
	RootVolumeGB int    `json:"root_volume_gb"`
	KeyName      string `json:"key_name"`
}

type BlueprintParams struct {
	Region        string        `json:"region"`
	SecurityGroup SecurityGroup `json:"security_group"`
	EC2           EC2           `json:"ec2"`
}

// Validate enforces the M2 minimal-blueprint rules.
func (p BlueprintParams) Validate() error {
	if p.Region == "" {
		return fmt.Errorf("region is required")
	}
	if p.EC2.InstanceType == "" {
		return fmt.Errorf("ec2.instance_type is required")
	}
	if p.EC2.Count < 1 || p.EC2.Count > 10 {
		return fmt.Errorf("ec2.count must be between 1 and 10, got %d", p.EC2.Count)
	}
	if p.EC2.RootVolumeGB < 8 {
		return fmt.Errorf("ec2.root_volume_gb must be >= 8, got %d", p.EC2.RootVolumeGB)
	}
	for i, in := range p.SecurityGroup.Ingress {
		if in.Port < 1 || in.Port > 65535 {
			return fmt.Errorf("ingress[%d]: port out of range: %d", i, in.Port)
		}
		if in.Protocol != "tcp" && in.Protocol != "udp" {
			return fmt.Errorf("ingress[%d]: protocol must be tcp or udp, got %q", i, in.Protocol)
		}
		if _, _, err := net.ParseCIDR(in.CIDR); err != nil {
			return fmt.Errorf("ingress[%d]: invalid cidr %q: %w", i, in.CIDR, err)
		}
	}
	return nil
}

type AWSCreds struct {
	AccessKeyID     string
	SecretAccessKey string
}

// Spec is the per-execution input to a Provisioner. Process-level config
// (backend URL, passphrase, pulumi project) lives on the implementation, not here.
type Spec struct {
	StackName string
	Region    string
	Params    BlueprintParams
	Creds     AWSCreds
}

type PreviewResult struct {
	Creates, Updates, Deletes, Sames int
	Summary                          string
}

type UpResult struct {
	Outputs map[string]any
	Summary string
}

// Provisioner is the decoupling point between the orchestrator and the engine.
// logs receives streaming output (wired to the SSE broker).
type Provisioner interface {
	Preview(ctx context.Context, spec Spec, logs io.Writer) (PreviewResult, error)
	Up(ctx context.Context, spec Spec, logs io.Writer) (UpResult, error)
	Destroy(ctx context.Context, spec Spec, logs io.Writer) error
}
