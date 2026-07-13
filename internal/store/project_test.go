package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestProjectCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.CreateProject(ctx, Project{Name: "web", Description: "web stack"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetProject(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "web" || got.Description != "web stack" {
		t.Fatalf("unexpected project: %+v", got)
	}

	list, err := s.ListProjects(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v len=%d", err, len(list))
	}

	if err := s.DeleteProject(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetProject(ctx, id); err == nil {
		t.Fatal("expected error getting deleted project")
	}
}

func TestDeleteProjectReportsMissingAndReferencedRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.DeleteProject(ctx, 999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("DeleteProject missing error = %v, want sql.ErrNoRows", err)
	}

	projectID, accountID := seedProjectAndAccount(t, s)
	if _, err := s.CreateBlueprint(ctx, sampleBlueprint(projectID, accountID)); err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	if err := s.DeleteProject(ctx, projectID); !errors.Is(err, ErrProjectReferenced) {
		t.Fatalf("DeleteProject referenced error = %v, want ErrProjectReferenced", err)
	}
}
