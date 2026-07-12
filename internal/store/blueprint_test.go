package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

// seedProjectAndAccount creates a project + cloud account and returns their ids,
// so blueprint FKs resolve.
func seedProjectAndAccount(t *testing.T, s *Store) (projectID, accountID int64) {
	t.Helper()
	ctx := context.Background()
	pid, err := s.CreateProject(ctx, Project{Name: "p"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	aid, err := s.CreateCloudAccount(ctx, sampleAccount())
	if err != nil {
		t.Fatalf("CreateCloudAccount: %v", err)
	}
	return pid, aid
}

func TestUpdateBlueprintRoundTripsParams(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	pid, aid := seedProjectAndAccount(t, s)
	bp := sampleBlueprint(pid, aid)
	id, err := s.CreateBlueprint(ctx, bp)
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}

	bp.ID = id
	bp.Name = "updated-blueprint"
	bp.Params.Region = "eu-west-1"
	bp.Params.EC2.InstanceType = "c7g.large"
	bp.Params.EC2.Count = 3
	bp.Params.Redis.Enabled = true
	bp.Params.Redis.AuthEnabled = true
	if err := s.UpdateBlueprint(ctx, bp); err != nil {
		t.Fatalf("UpdateBlueprint: %v", err)
	}

	got, err := s.GetBlueprint(ctx, id)
	if err != nil {
		t.Fatalf("GetBlueprint: %v", err)
	}
	if got.Name != bp.Name || got.ProjectID != pid || got.CloudAccountID != aid {
		t.Fatalf("updated ownership fields = %+v, want %+v", got, bp)
	}
	if got.Params.Region != "eu-west-1" || got.Params.EC2.InstanceType != "c7g.large" || got.Params.EC2.Count != 3 || !got.Params.Redis.AuthEnabled {
		t.Fatalf("updated params did not round-trip: %+v", got.Params)
	}
}

func TestUpdateBlueprintNotFound(t *testing.T) {
	s := newTestStore(t)
	pid, aid := seedProjectAndAccount(t, s)
	bp := sampleBlueprint(pid, aid)
	bp.ID = 999

	if err := s.UpdateBlueprint(context.Background(), bp); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("UpdateBlueprint error = %v, want sql.ErrNoRows", err)
	}
}

func TestBlueprintEditDoesNotMutateEnvironmentSnapshot(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	pid, aid := seedProjectAndAccount(t, s)
	bp := sampleBlueprint(pid, aid)
	bpID, err := s.CreateBlueprint(ctx, bp)
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	envID, err := s.CreateEnvironment(ctx, Environment{
		BlueprintID: bpID, CloudAccountID: aid, Name: "before-edit", PulumiStack: "before-edit-1",
		Region: bp.Params.Region, Snapshot: bp.Params,
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	bp.ID = bpID
	bp.Params.Region = "us-west-2"
	bp.Params.EC2.Count = 9
	if err := s.UpdateBlueprint(ctx, bp); err != nil {
		t.Fatalf("UpdateBlueprint: %v", err)
	}

	env, err := s.GetEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if env.Snapshot.Region != "ap-southeast-1" || env.Snapshot.EC2.Count != 2 {
		t.Fatalf("environment snapshot changed after blueprint edit: %+v", env.Snapshot)
	}
}

func sampleBlueprint(projectID, accountID int64) Blueprint {
	return Blueprint{
		ProjectID:      projectID,
		CloudAccountID: accountID,
		Name:           "web-bp",
		Params: provisioner.BlueprintParams{
			Region: "ap-southeast-1",
			SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
				{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
			}},
			EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 2, RootVolumeGB: 8},
		},
	}
}

func TestBlueprintCRUD_RoundTripsParams(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	pid, aid := seedProjectAndAccount(t, s)

	id, err := s.CreateBlueprint(ctx, sampleBlueprint(pid, aid))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetBlueprint(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "web-bp" || got.Params.EC2.Count != 2 || got.Params.Region != "ap-southeast-1" {
		t.Fatalf("params did not round-trip: %+v", got)
	}
	if len(got.Params.SecurityGroup.Ingress) != 1 || got.Params.SecurityGroup.Ingress[0].Port != 22 {
		t.Fatalf("ingress did not round-trip: %+v", got.Params.SecurityGroup)
	}

	list, err := s.ListBlueprints(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v len=%d", err, len(list))
	}

	if err := s.DeleteBlueprint(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestDeleteBlueprintNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.DeleteBlueprint(context.Background(), 999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("DeleteBlueprint error = %v, want sql.ErrNoRows", err)
	}
}

func TestDeleteBlueprintClassifiesForeignKeyReference(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	bpID, aid := seedBlueprint(t, s)
	if _, err := s.CreateEnvironment(ctx, Environment{BlueprintID: bpID, CloudAccountID: aid, Name: "env", PulumiStack: "env-1", Region: "ap-southeast-1"}); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	if err := s.DeleteBlueprint(ctx, bpID); !errors.Is(err, ErrBlueprintReferenced) {
		t.Fatalf("DeleteBlueprint error = %v, want ErrBlueprintReferenced", err)
	}
}

func TestDeleteBlueprintDoesNotMisclassifyOperationalConstraint(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	bpID, _ := seedBlueprint(t, s)
	if _, err := s.DB().ExecContext(ctx, `CREATE TRIGGER reject_blueprint_delete BEFORE DELETE ON blueprints BEGIN SELECT RAISE(ABORT, 'sensitive operational failure'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	err := s.DeleteBlueprint(ctx, bpID)
	if err == nil || errors.Is(err, ErrBlueprintReferenced) {
		t.Fatalf("DeleteBlueprint error = %v, want unclassified operational error", err)
	}
}

func TestCreateAndUpdateBlueprintClassifyOwnershipConstraint(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, aid := seedProjectAndAccount(t, s)
	bp := sampleBlueprint(999, aid)
	if _, err := s.CreateBlueprint(ctx, bp); !errors.Is(err, ErrBlueprintOwnershipInvalid) {
		t.Fatalf("CreateBlueprint error = %v, want ErrBlueprintOwnershipInvalid", err)
	}
	pid, _ := s.CreateProject(ctx, Project{Name: "valid"})
	bp.ProjectID = pid
	id, err := s.CreateBlueprint(ctx, bp)
	if err != nil {
		t.Fatalf("CreateBlueprint valid: %v", err)
	}
	bp.ID = id
	bp.CloudAccountID = 999
	if err := s.UpdateBlueprint(ctx, bp); !errors.Is(err, ErrBlueprintOwnershipInvalid) {
		t.Fatalf("UpdateBlueprint error = %v, want ErrBlueprintOwnershipInvalid", err)
	}
}
