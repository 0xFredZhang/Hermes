package store

import (
	"context"
	"testing"
)

// TestForeignKeysEnforced locks in that ON DELETE RESTRICT actually fires — it
// only does when PRAGMA foreign_keys is ON for the connection. Without it, the
// project/blueprint delete guards in the API would silently orphan rows.
func TestForeignKeysEnforced(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	pid, aid := seedProjectAndAccount(t, s)
	bpID, err := s.CreateBlueprint(ctx, sampleBlueprint(pid, aid))
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}

	// A project with a referencing blueprint must not delete (RESTRICT).
	if err := s.DeleteProject(ctx, pid); err == nil {
		t.Fatal("expected FK RESTRICT error deleting a project that still has a blueprint")
	}

	// After removing the blueprint, the project deletes cleanly.
	if err := s.DeleteBlueprint(ctx, bpID); err != nil {
		t.Fatalf("DeleteBlueprint: %v", err)
	}
	if err := s.DeleteProject(ctx, pid); err != nil {
		t.Fatalf("DeleteProject after blueprint removed: %v", err)
	}
}

// TestJobsCascadeWithEnvironment verifies jobs are removed when their
// environment is deleted (ON DELETE CASCADE).
func TestJobsCascadeWithEnvironment(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)
	if _, err := s.CreateJob(ctx, Job{EnvironmentID: envID, Action: ActionPreview}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if _, err := s.DB().ExecContext(ctx, `DELETE FROM environments WHERE id = ?`, envID); err != nil {
		t.Fatalf("delete environment: %v", err)
	}
	jobs, err := s.ListJobsByEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("ListJobsByEnvironment: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected jobs to cascade-delete with environment, got %d", len(jobs))
	}
}
