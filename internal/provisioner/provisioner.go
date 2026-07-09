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
	AMI          string `json:"ami"` // empty = auto-resolve latest Ubuntu LTS
	RootVolumeGB int    `json:"root_volume_gb"`
	KeyName      string `json:"key_name"`
}

type RDS struct {
	Enabled            bool   `json:"enabled"`
	Engine             string `json:"engine"`
	EngineVersion      string `json:"engine_version"`
	InstanceClass      string `json:"instance_class"`
	AllocatedStorageGB int    `json:"allocated_storage_gb"`
	DBName             string `json:"db_name"`
	Username           string `json:"username"`
	Port               int    `json:"port"`
}

type Redis struct {
	Enabled       bool   `json:"enabled"`
	Engine        string `json:"engine"`
	EngineVersion string `json:"engine_version"`
	NodeType      string `json:"node_type"`
	NodeCount     int    `json:"node_count"`
	Port          int    `json:"port"`
}

type BlueprintParams struct {
	Region        string        `json:"region"`
	SecurityGroup SecurityGroup `json:"security_group"`
	EC2           EC2           `json:"ec2"`
	RDS           RDS           `json:"rds,omitempty"`
	Redis         Redis         `json:"redis,omitempty"`
}

func (p *BlueprintParams) ApplyDefaults() {
	if p.RDS.Engine == "" {
		p.RDS.Engine = "mysql"
	}
	if p.RDS.EngineVersion == "" {
		p.RDS.EngineVersion = "8.0"
	}
	if p.RDS.InstanceClass == "" {
		p.RDS.InstanceClass = "db.t3.micro"
	}
	if p.RDS.AllocatedStorageGB == 0 {
		p.RDS.AllocatedStorageGB = 20
	}
	if p.RDS.DBName == "" {
		p.RDS.DBName = "app"
	}
	if p.RDS.Username == "" {
		p.RDS.Username = "admin"
	}
	if p.RDS.Port == 0 {
		p.RDS.Port = 3306
	}
	if p.Redis.Engine == "" {
		p.Redis.Engine = "redis"
	}
	if p.Redis.EngineVersion == "" {
		p.Redis.EngineVersion = "7.2"
	}
	if p.Redis.NodeType == "" {
		p.Redis.NodeType = "cache.t3.micro"
	}
	if p.Redis.NodeCount == 0 {
		p.Redis.NodeCount = 1
	}
	if p.Redis.Port == 0 {
		p.Redis.Port = 6379
	}
}

// Validate enforces blueprint rules for EC2 plus optional M3a resources.
func (p BlueprintParams) Validate() error {
	p.ApplyDefaults()
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
	if p.RDS.Enabled {
		if p.RDS.Engine != "mysql" {
			return fmt.Errorf("rds.engine must be mysql, got %q", p.RDS.Engine)
		}
		if p.RDS.EngineVersion == "" {
			return fmt.Errorf("rds.engine_version is required")
		}
		if p.RDS.InstanceClass == "" {
			return fmt.Errorf("rds.instance_class is required")
		}
		if p.RDS.AllocatedStorageGB < 20 {
			return fmt.Errorf("rds.allocated_storage_gb must be >= 20, got %d", p.RDS.AllocatedStorageGB)
		}
		if p.RDS.DBName == "" {
			return fmt.Errorf("rds.db_name is required")
		}
		if p.RDS.Username == "" {
			return fmt.Errorf("rds.username is required")
		}
		if p.RDS.Port < 1 || p.RDS.Port > 65535 {
			return fmt.Errorf("rds.port out of range: %d", p.RDS.Port)
		}
	}
	if p.Redis.Enabled {
		if p.Redis.Engine != "redis" {
			return fmt.Errorf("redis.engine must be redis, got %q", p.Redis.Engine)
		}
		if p.Redis.EngineVersion == "" {
			return fmt.Errorf("redis.engine_version is required")
		}
		if p.Redis.NodeType == "" {
			return fmt.Errorf("redis.node_type is required")
		}
		if p.Redis.NodeCount < 1 || p.Redis.NodeCount > 5 {
			return fmt.Errorf("redis.node_count must be between 1 and 5, got %d", p.Redis.NodeCount)
		}
		if p.Redis.Port < 1 || p.Redis.Port > 65535 {
			return fmt.Errorf("redis.port out of range: %d", p.Redis.Port)
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
