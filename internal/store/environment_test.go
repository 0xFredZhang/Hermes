package store

import (
	"context"
	"database/sql"
	"errors"
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

func TestDeletePendingEnvironment(t *testing.T) {
	t.Run("deletes pending environment without jobs", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		bpID, aid := seedBlueprint(t, s)
		envID, err := s.CreateEnvironment(ctx, Environment{
			BlueprintID: bpID, CloudAccountID: aid, Name: "pending", PulumiStack: "pending-1", Region: "ap-southeast-1",
		})
		if err != nil {
			t.Fatalf("CreateEnvironment: %v", err)
		}

		if err := s.DeletePendingEnvironment(ctx, envID); err != nil {
			t.Fatalf("DeletePendingEnvironment: %v", err)
		}
		if _, err := s.GetEnvironment(ctx, envID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("GetEnvironment after delete error = %v, want sql.ErrNoRows", err)
		}
	})

	t.Run("preserves environment after status change", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		bpID, aid := seedBlueprint(t, s)
		envID, err := s.CreateEnvironment(ctx, Environment{
			BlueprintID: bpID, CloudAccountID: aid, Name: "ready", PulumiStack: "ready-1", Region: "ap-southeast-1",
		})
		if err != nil {
			t.Fatalf("CreateEnvironment: %v", err)
		}
		if err := s.UpdateEnvironmentStatus(ctx, envID, EnvPreviewReady); err != nil {
			t.Fatalf("UpdateEnvironmentStatus: %v", err)
		}

		if err := s.DeletePendingEnvironment(ctx, envID); !errors.Is(err, ErrStaleTransition) {
			t.Fatalf("DeletePendingEnvironment error = %v, want ErrStaleTransition", err)
		}
		if _, err := s.GetEnvironment(ctx, envID); err != nil {
			t.Fatalf("protected environment was deleted: %v", err)
		}
	})

	t.Run("preserves pending environment once any job exists", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		bpID, aid := seedBlueprint(t, s)
		envID, err := s.CreateEnvironment(ctx, Environment{
			BlueprintID: bpID, CloudAccountID: aid, Name: "started", PulumiStack: "started-1", Region: "ap-southeast-1",
		})
		if err != nil {
			t.Fatalf("CreateEnvironment: %v", err)
		}
		if _, err := s.CreateJob(ctx, Job{
			EnvironmentID: envID, Action: ActionPreview, Status: JobFailed,
		}); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}

		if err := s.DeletePendingEnvironment(ctx, envID); !errors.Is(err, ErrStaleTransition) {
			t.Fatalf("DeletePendingEnvironment error = %v, want ErrStaleTransition", err)
		}
		jobs, err := s.ListJobsByEnvironment(ctx, envID)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("protected environment jobs = %+v, err = %v", jobs, err)
		}
	})
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
