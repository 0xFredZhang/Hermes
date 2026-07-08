package store

import (
	"context"
	"testing"
)

func seedEnvironment(t *testing.T, s *Store) int64 {
	t.Helper()
	bpID, aid := seedBlueprint(t, s)
	id, err := s.CreateEnvironment(context.Background(), Environment{
		BlueprintID: bpID, CloudAccountID: aid, Name: "e", PulumiStack: "e-1", Region: "ap-southeast-1",
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	return id
}

func TestJobLifecycleAndActiveGuard(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)

	id, err := s.CreateJob(ctx, Job{EnvironmentID: envID, Action: ActionPreview})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, _ := s.GetJob(ctx, id)
	if got.Status != JobQueued || got.Action != ActionPreview {
		t.Fatalf("unexpected job: %+v", got)
	}

	active, err := s.HasActiveJob(ctx, envID)
	if err != nil || !active {
		t.Fatalf("HasActiveJob = %v, %v; want true", active, err)
	}

	if err := s.UpdateJobStatus(ctx, id, JobRunning); err != nil {
		t.Fatalf("UpdateStatus running: %v", err)
	}
	_ = s.SetJobLogs(ctx, id, "line1\nline2")
	_ = s.SetJobSummary(ctx, id, map[string]any{"creates": 3})
	if err := s.UpdateJobStatus(ctx, id, JobSucceeded); err != nil {
		t.Fatalf("UpdateStatus succeeded: %v", err)
	}

	got, _ = s.GetJob(ctx, id)
	if got.Status != JobSucceeded || !got.StartedAt.Valid || !got.FinishedAt.Valid {
		t.Fatalf("timestamps/status not set: %+v", got)
	}
	if got.Logs != "line1\nline2" || got.Summary["creates"] == nil {
		t.Fatalf("logs/summary not persisted: %+v", got)
	}

	active, _ = s.HasActiveJob(ctx, envID)
	if active {
		t.Fatal("HasActiveJob should be false after job succeeded")
	}

	byEnv, err := s.ListJobsByEnvironment(ctx, envID)
	if err != nil || len(byEnv) != 1 {
		t.Fatalf("ListJobsByEnvironment: %v len=%d", err, len(byEnv))
	}
}

func TestListOrphanJobs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)

	q, _ := s.CreateJob(ctx, Job{EnvironmentID: envID, Action: ActionUp})
	r, _ := s.CreateJob(ctx, Job{EnvironmentID: envID, Action: ActionUp})
	_ = s.UpdateJobStatus(ctx, r, JobRunning)
	done, _ := s.CreateJob(ctx, Job{EnvironmentID: envID, Action: ActionUp})
	_ = s.UpdateJobStatus(ctx, done, JobSucceeded)

	orphans, err := s.ListOrphanJobs(ctx)
	if err != nil {
		t.Fatalf("ListOrphanJobs: %v", err)
	}
	if len(orphans) != 2 {
		t.Fatalf("orphans = %d, want 2 (queued %d + running %d)", len(orphans), q, r)
	}
}
