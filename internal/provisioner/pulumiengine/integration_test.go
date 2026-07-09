//go:build integration

package pulumiengine

import (
	"context"
	"os"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

// TestIntegrationUpDestroy runs a real up→destroy against AWS.
// Requires: pulumi CLI + AWS plugin installed, and AWS creds in the environment.
// Run: make test-integration   (or)
//
//	go test -tags integration ./internal/provisioner/pulumiengine/ -run TestIntegration -v
func TestIntegrationUpDestroy(t *testing.T) {
	ak, sk := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY")
	if ak == "" || sk == "" {
		t.Skip("set AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY to run the integration test")
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "ap-southeast-1"
	}

	p := New("hermes-it", "file://"+t.TempDir(), "integration-passphrase")
	spec := provisioner.Spec{
		StackName: "it-stack",
		Region:    region,
		Params: provisioner.BlueprintParams{
			Region: region,
			SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
				{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
			}},
			EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8},
		},
		Creds: provisioner.AWSCreds{AccessKeyID: ak, SecretAccessKey: sk},
	}
	if os.Getenv("HERMES_IT_RDS") != "" {
		spec.Params.RDS.Enabled = true
		spec.Secrets.RDSPassword = "HermesIntegrationPassword123!"
	}
	if os.Getenv("HERMES_IT_REDIS") != "" {
		spec.Params.Redis.Enabled = true
	}
	if os.Getenv("HERMES_IT_REDIS_AUTH") != "" {
		spec.Params.Redis.Enabled = true
		spec.Params.Redis.AuthEnabled = true
		spec.Secrets.RedisAuthToken = "HermesRedisAuthToken123!"
	}
	if os.Getenv("HERMES_IT_NETWORK") != "" {
		spec.Params.Network.Enabled = true
	}
	spec.Params.ApplyDefaults()
	ctx := context.Background()

	// Safety net: tear down even if an assertion fails before the explicit
	// destroy below. A second destroy on an already-removed stack is harmless.
	t.Cleanup(func() { _ = p.Destroy(ctx, spec, os.Stderr) })

	res, err := p.Up(ctx, spec, os.Stderr)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	// public_ips are now the instances' Elastic IPs (stable across reboots).
	if res.Outputs["public_ips"] == nil {
		t.Fatalf("expected public_ips output, got %+v", res.Outputs)
	}
	if spec.Params.Network.Enabled {
		if res.Outputs["vpc_id"] == nil || res.Outputs["subnet_ids"] == nil {
			t.Fatalf("expected managed network outputs, got %+v", res.Outputs)
		}
	}
	if spec.Params.RDS.Enabled {
		if res.Outputs["rds_endpoint"] == nil {
			t.Fatalf("expected rds_endpoint output, got %+v", res.Outputs)
		}
		if res.Outputs["rds_password"] != nil {
			t.Fatalf("must not export generated RDS password, got %+v", res.Outputs)
		}
	}
	if spec.Params.Redis.Enabled && res.Outputs["redis_primary_endpoint"] == nil {
		t.Fatalf("expected redis_primary_endpoint output, got %+v", res.Outputs)
	}
	if spec.Params.Redis.AuthEnabled && res.Outputs["redis_auth_token"] != nil {
		t.Fatalf("must not export generated Redis auth token, got %+v", res.Outputs)
	}

	// Destroy tears down the resources AND removes the now-empty stack.
	if err := p.Destroy(ctx, spec, os.Stderr); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	// The stack should no longer exist in the backend: selecting it must fail.
	if _, err := auto.SelectStackInlineSource(ctx, spec.StackName, p.project,
		buildProgram(spec), auto.EnvVars(p.envVars(spec))); err == nil {
		t.Errorf("stack %q still present after Destroy; RemoveStack did not run", spec.StackName)
	}
}
