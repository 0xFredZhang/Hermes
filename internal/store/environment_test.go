package store

import (
	"context"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

func seedBlueprint(t *testing.T, s *Store) (blueprintID, accountID int64) {
	t.Helper()
	pid, aid := seedProjectAndAccount(t, s)
	id, err := s.CreateBlueprint(context.Background(), sampleBlueprint(pid, aid))
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	return id, aid
}

func TestEnvironmentLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	bpID, aid := seedBlueprint(t, s)

	id, err := s.CreateEnvironment(ctx, Environment{
		BlueprintID:    bpID,
		CloudAccountID: aid,
		Name:           "prod",
		PulumiStack:    "prod-abc123",
		Region:         "ap-southeast-1",
		Snapshot:       provisioner.BlueprintParams{Region: "ap-southeast-1", EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetEnvironment(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != EnvPending {
		t.Fatalf("status = %q, want pending", got.Status)
	}
	if got.Snapshot.EC2.InstanceType != "t3.micro" {
		t.Fatalf("snapshot did not round-trip: %+v", got.Snapshot)
	}

	if err := s.UpdateEnvironmentStatus(ctx, id, EnvUp); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if err := s.SetEnvironmentOutputs(ctx, id, map[string]any{"public_ips": []any{"1.2.3.4"}}); err != nil {
		t.Fatalf("SetOutputs: %v", err)
	}
	got, _ = s.GetEnvironment(ctx, id)
	if got.Status != EnvUp {
		t.Fatalf("status = %q, want up", got.Status)
	}
	if got.Outputs["public_ips"] == nil {
		t.Fatalf("outputs did not persist: %+v", got.Outputs)
	}

	list, err := s.ListEnvironments(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v len=%d", err, len(list))
	}
}
