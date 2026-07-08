package pulumiengine

import (
	"testing"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

// Compile-time assertion that Provisioner satisfies the interface.
var _ provisioner.Provisioner = (*Provisioner)(nil)

func TestEnvVarsScopesCredentials(t *testing.T) {
	p := New("hermes", "file:///tmp/state", "pass123")
	ev := p.envVars(provisioner.Spec{
		Region: "ap-southeast-1",
		Creds:  provisioner.AWSCreds{AccessKeyID: "AKIA", SecretAccessKey: "SECRET"},
	})
	if ev["AWS_ACCESS_KEY_ID"] != "AKIA" || ev["AWS_SECRET_ACCESS_KEY"] != "SECRET" {
		t.Fatalf("aws creds not mapped: %v", ev)
	}
	if ev["AWS_REGION"] != "ap-southeast-1" {
		t.Fatalf("region not mapped: %v", ev)
	}
	if ev["PULUMI_BACKEND_URL"] != "file:///tmp/state" || ev["PULUMI_CONFIG_PASSPHRASE"] != "pass123" {
		t.Fatalf("backend/passphrase not mapped: %v", ev)
	}
}
