package store

import (
	"context"
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
