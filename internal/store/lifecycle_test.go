package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestTransientStatusForAction(t *testing.T) {
	tests := []struct {
		action string
		want   string
	}{
		{ActionPreview, EnvPreviewing},
		{ActionDestroyPreview, EnvDestroyPreviewing},
		{ActionUp, EnvProvisioning},
		{ActionRefresh, EnvRefreshing},
		{ActionDestroy, EnvDestroying},
	}
	for _, tt := range tests {
		got, ok := transientStatusForAction(tt.action)
		if !ok || got != tt.want {
			t.Errorf("transientStatusForAction(%q) = %q, %v; want %q, true", tt.action, got, ok, tt.want)
		}
	}
	if got, ok := transientStatusForAction("unknown"); ok || got != "" {
		t.Fatalf("transientStatusForAction(unknown) = %q, %v; want empty, false", got, ok)
	}
}

func TestEnqueueJobTransitionsEnvironmentAtomically(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)

	job, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID:   envID,
		Action:          ActionPreview,
		AllowedFrom:     []string{EnvPending},
		TransientStatus: EnvPreviewing,
	})
	if err != nil {
		t.Fatalf("EnqueueJobTransition: %v", err)
	}
	if job.ID == 0 || job.EnvironmentID != envID || job.Action != ActionPreview || job.Status != JobQueued {
		t.Fatalf("unexpected job: %+v", job)
	}

	env, err := s.GetEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if env.Status != EnvPreviewing {
		t.Fatalf("environment status = %q, want %q", env.Status, EnvPreviewing)
	}
	jobs, err := s.ListJobsByEnvironment(ctx, envID)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("ListJobsByEnvironment: err=%v len=%d", err, len(jobs))
	}
}

func TestEnqueueJobRejectsStaleEnvironmentStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)

	_, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID:   envID,
		Action:          ActionRefresh,
		AllowedFrom:     []string{EnvUp},
		TransientStatus: EnvRefreshing,
	})
	if !errors.Is(err, ErrStaleTransition) {
		t.Fatalf("error = %v, want ErrStaleTransition", err)
	}

	env, _ := s.GetEnvironment(ctx, envID)
	if env.Status != EnvPending {
		t.Fatalf("environment status = %q, want unchanged %q", env.Status, EnvPending)
	}
	jobs, err := s.ListJobsByEnvironment(ctx, envID)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("stale transition created jobs: err=%v len=%d", err, len(jobs))
	}
}

func TestEnqueueJobRejectsConcurrentActiveJob(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)
	existingID, err := s.CreateJob(ctx, Job{EnvironmentID: envID, Action: ActionPreview})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	_, err = s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID:   envID,
		Action:          ActionUp,
		AllowedFrom:     []string{EnvPending},
		TransientStatus: EnvProvisioning,
	})
	if !errors.Is(err, ErrActiveJob) {
		t.Fatalf("error = %v, want ErrActiveJob", err)
	}

	env, _ := s.GetEnvironment(ctx, envID)
	if env.Status != EnvPending {
		t.Fatalf("environment status = %q, transition was not rolled back", env.Status)
	}
	jobs, _ := s.ListJobsByEnvironment(ctx, envID)
	if len(jobs) != 1 || jobs[0].ID != existingID {
		t.Fatalf("jobs = %+v, want only existing job %d", jobs, existingID)
	}
}

func TestEnqueueJobRejectsUnknownAction(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)

	_, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID: envID, Action: "unknown",
		AllowedFrom: []string{EnvPending}, TransientStatus: EnvProvisioning,
	})
	if !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("EnqueueJobTransition error = %v, want ErrInvalidAction", err)
	}
	env, _ := s.GetEnvironment(ctx, envID)
	if env.Status != EnvPending || env.ResumeStatus != "" {
		t.Fatalf("unknown action changed environment: %+v", env)
	}
	jobs, _ := s.ListJobsByEnvironment(ctx, envID)
	if len(jobs) != 0 {
		t.Fatalf("unknown action created jobs: %+v", jobs)
	}
}

func TestEnqueueJobPreservesResumeStatusOnDestroyPreviewRetry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)
	if _, err := s.DB().ExecContext(ctx,
		`UPDATE environments SET status = ?, resume_status = ? WHERE id = ?`,
		EnvFailed, EnvUp, envID,
	); err != nil {
		t.Fatalf("seed failed destroy preview: %v", err)
	}

	_, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID: envID, Action: ActionDestroyPreview,
		AllowedFrom: []string{EnvFailed}, TransientStatus: EnvDestroyPreviewing,
		CaptureResumeStatus: true,
	})
	if err != nil {
		t.Fatalf("retry destroy preview: %v", err)
	}
	env, _ := s.GetEnvironment(ctx, envID)
	if env.Status != EnvDestroyPreviewing || env.ResumeStatus != EnvUp {
		t.Fatalf("retry environment = %+v, want destroy previewing with preserved up resume", env)
	}
}

func TestDestroyPreviewFromFailedCapturesResumeStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)
	if err := s.UpdateEnvironmentStatus(ctx, envID, EnvFailed); err != nil {
		t.Fatalf("UpdateEnvironmentStatus: %v", err)
	}

	queued, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID: envID, Action: ActionDestroyPreview,
		AllowedFrom: []string{EnvFailed}, TransientStatus: EnvDestroyPreviewing,
		CaptureResumeStatus: true,
	})
	if err != nil {
		t.Fatalf("enqueue destroy preview: %v", err)
	}
	if _, _, err := s.StartJob(ctx, queued.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	if err := s.CompleteJob(ctx, JobCompletion{
		JobID: queued.ID, EnvironmentID: envID,
		JobStatus: JobSucceeded, EnvironmentStatus: EnvDestroyPreviewReady,
	}); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}

	if err := s.CancelDestroyPreview(ctx, envID); err != nil {
		t.Fatalf("CancelDestroyPreview: %v", err)
	}
	env, _ := s.GetEnvironment(ctx, envID)
	if env.Status != EnvFailed || env.ResumeStatus != "" {
		t.Fatalf("environment = %+v, want failed with cleared resume status", env)
	}
}

func TestDestroyAndCancelAreAtomic(t *testing.T) {
	t.Run("destroy commits before cancel", func(t *testing.T) {
		s, ctx, envID := destroyPreviewReadyEnvironment(t)

		job, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
			EnvironmentID:   envID,
			Action:          ActionDestroy,
			AllowedFrom:     []string{EnvDestroyPreviewReady},
			TransientStatus: EnvDestroying,
		})
		if err != nil {
			t.Fatalf("enqueue destroy: %v", err)
		}
		if err := s.CancelDestroyPreview(ctx, envID); !errors.Is(err, ErrActiveJob) {
			t.Fatalf("cancel error = %v, want ErrActiveJob", err)
		}

		env, _ := s.GetEnvironment(ctx, envID)
		if env.Status != EnvDestroying || env.ResumeStatus != EnvUp {
			t.Fatalf("environment = %+v, want destroying with up resume", env)
		}
		got, _ := s.GetJob(ctx, job.ID)
		if got.Status != JobQueued || got.Action != ActionDestroy {
			t.Fatalf("destroy job = %+v", got)
		}
	})

	t.Run("cancel commits before destroy", func(t *testing.T) {
		s, ctx, envID := destroyPreviewReadyEnvironment(t)

		if err := s.CancelDestroyPreview(ctx, envID); err != nil {
			t.Fatalf("cancel: %v", err)
		}
		_, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
			EnvironmentID:   envID,
			Action:          ActionDestroy,
			AllowedFrom:     []string{EnvDestroyPreviewReady},
			TransientStatus: EnvDestroying,
		})
		if !errors.Is(err, ErrStaleTransition) {
			t.Fatalf("destroy error = %v, want ErrStaleTransition", err)
		}

		env, _ := s.GetEnvironment(ctx, envID)
		if env.Status != EnvUp || env.ResumeStatus != "" {
			t.Fatalf("environment = %+v, want restored up with cleared resume", env)
		}
		jobs, _ := s.ListJobsByEnvironment(ctx, envID)
		if len(jobs) != 1 || jobs[0].Action != ActionDestroyPreview || jobs[0].Status != JobSucceeded {
			t.Fatalf("unexpected jobs after cancel wins: %+v", jobs)
		}
	})

	t.Run("cancel refuses an active destroy job", func(t *testing.T) {
		s, ctx, envID := destroyPreviewReadyEnvironment(t)
		if _, err := s.CreateJob(ctx, Job{EnvironmentID: envID, Action: ActionDestroy}); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}

		if err := s.CancelDestroyPreview(ctx, envID); !errors.Is(err, ErrActiveJob) {
			t.Fatalf("cancel error = %v, want ErrActiveJob", err)
		}
		env, _ := s.GetEnvironment(ctx, envID)
		if env.Status != EnvDestroyPreviewReady || env.ResumeStatus != EnvUp {
			t.Fatalf("environment changed despite active destroy job: %+v", env)
		}
	})
}

func TestDestroyAndCancelRaceCommitsExactlyOneTransition(t *testing.T) {
	const attempts = 20
	for attempt := 0; attempt < attempts; attempt++ {
		s, ctx, envID := destroyPreviewReadyEnvironment(t)
		start := make(chan struct{})
		type operationResult struct {
			operation string
			job       Job
			err       error
		}
		results := make(chan operationResult, 2)
		go func() {
			<-start
			job, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
				EnvironmentID: envID, Action: ActionDestroy,
				AllowedFrom: []string{EnvDestroyPreviewReady}, TransientStatus: EnvDestroying,
			})
			results <- operationResult{operation: "destroy", job: job, err: err}
		}()
		go func() {
			<-start
			results <- operationResult{operation: "cancel", err: s.CancelDestroyPreview(ctx, envID)}
		}()
		close(start)

		first, second := <-results, <-results
		outcomes := map[string]operationResult{first.operation: first, second.operation: second}
		successes := 0
		for _, outcome := range outcomes {
			if outcome.err == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Fatalf("attempt %d committed %d operations: destroy=%v cancel=%v", attempt, successes, outcomes["destroy"].err, outcomes["cancel"].err)
		}

		env, err := s.GetEnvironment(ctx, envID)
		if err != nil {
			t.Fatalf("attempt %d GetEnvironment: %v", attempt, err)
		}
		jobs, err := s.ListJobsByEnvironment(ctx, envID)
		if err != nil {
			t.Fatalf("attempt %d ListJobsByEnvironment: %v", attempt, err)
		}
		destroyJobs := 0
		for _, job := range jobs {
			if job.Action == ActionDestroy {
				destroyJobs++
			}
		}
		switch env.Status {
		case EnvUp:
			if env.ResumeStatus != "" || destroyJobs != 0 || outcomes["cancel"].err != nil || !errors.Is(outcomes["destroy"].err, ErrStaleTransition) {
				t.Fatalf("attempt %d invalid cancel winner: env=%+v destroyJobs=%d destroyErr=%v cancelErr=%v", attempt, env, destroyJobs, outcomes["destroy"].err, outcomes["cancel"].err)
			}
		case EnvDestroying:
			if env.ResumeStatus != EnvUp || destroyJobs != 1 || outcomes["destroy"].err != nil || !errors.Is(outcomes["cancel"].err, ErrActiveJob) {
				t.Fatalf("attempt %d invalid destroy winner: env=%+v destroyJobs=%d destroyErr=%v cancelErr=%v", attempt, env, destroyJobs, outcomes["destroy"].err, outcomes["cancel"].err)
			}
		default:
			t.Fatalf("attempt %d unexpected final environment: %+v", attempt, env)
		}
	}
}

func TestStartJobRequiresQueuedStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)
	queued, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID: envID, Action: ActionPreview,
		AllowedFrom: []string{EnvPending}, TransientStatus: EnvPreviewing,
	})
	if err != nil {
		t.Fatalf("EnqueueJobTransition: %v", err)
	}

	running, env, err := s.StartJob(ctx, queued.ID)
	if err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	if running.Status != JobRunning || !running.StartedAt.Valid || env.Status != EnvPreviewing || env.ID != envID {
		t.Fatalf("inconsistent start result: job=%+v env=%+v", running, env)
	}
	if _, _, err := s.StartJob(ctx, queued.ID); !errors.Is(err, ErrJobNotQueued) {
		t.Fatalf("second StartJob error = %v, want ErrJobNotQueued", err)
	}
}

func TestCompleteJobUpdatesJobAndEnvironmentAtomically(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)
	if err := s.SetEnvironmentOutputs(ctx, envID, map[string]any{"existing": "kept"}); err != nil {
		t.Fatalf("SetEnvironmentOutputs: %v", err)
	}
	queued, _ := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID: envID, Action: ActionPreview,
		AllowedFrom: []string{EnvPending}, TransientStatus: EnvPreviewing,
	})
	if _, _, err := s.StartJob(ctx, queued.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}

	err := s.CompleteJob(ctx, JobCompletion{
		JobID: queued.ID, EnvironmentID: envID,
		JobStatus: JobSucceeded, EnvironmentStatus: EnvPreviewReady,
		Logs: "preview complete\n", Summary: map[string]any{"creates": 2},
		Outputs: map[string]any{"public_ip": "1.2.3.4"},
	})
	if err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}

	job, _ := s.GetJob(ctx, queued.ID)
	if job.Status != JobSucceeded || !job.FinishedAt.Valid || job.Logs != "preview complete\n" || job.Summary["creates"] != float64(2) {
		t.Fatalf("completed job = %+v", job)
	}
	env, _ := s.GetEnvironment(ctx, envID)
	if env.Status != EnvPreviewReady || env.Outputs["public_ip"] != "1.2.3.4" || env.Outputs["existing"] != nil {
		t.Fatalf("completed environment = %+v", env)
	}
}

func TestCompleteJobClearsResumeStatus(t *testing.T) {
	s, ctx, envID := destroyPreviewReadyEnvironment(t)
	queued, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID: envID, Action: ActionDestroy,
		AllowedFrom: []string{EnvDestroyPreviewReady}, TransientStatus: EnvDestroying,
	})
	if err != nil {
		t.Fatalf("enqueue destroy: %v", err)
	}
	if _, _, err := s.StartJob(ctx, queued.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}

	if err := s.CompleteJob(ctx, JobCompletion{
		JobID: queued.ID, EnvironmentID: envID,
		JobStatus: JobSucceeded, EnvironmentStatus: EnvDestroyed,
		ClearResumeStatus: true,
	}); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	env, _ := s.GetEnvironment(ctx, envID)
	if env.Status != EnvDestroyed || env.ResumeStatus != "" {
		t.Fatalf("destroyed environment = %+v, want cleared resume status", env)
	}
}

func TestCompleteJobRollsBackOnStaleEnvironment(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)
	queued, _ := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID: envID, Action: ActionPreview,
		AllowedFrom: []string{EnvPending}, TransientStatus: EnvPreviewing,
	})
	_, _, _ = s.StartJob(ctx, queued.ID)
	if err := s.UpdateEnvironmentStatus(ctx, envID, EnvUp); err != nil {
		t.Fatalf("make environment stale: %v", err)
	}

	err := s.CompleteJob(ctx, JobCompletion{
		JobID: queued.ID, EnvironmentID: envID,
		JobStatus: JobSucceeded, EnvironmentStatus: EnvPreviewReady,
		Logs: "must roll back", Error: "must roll back",
		Summary: map[string]any{"creates": 99}, Outputs: map[string]any{"must": "roll back"},
	})
	if !errors.Is(err, ErrStaleTransition) {
		t.Fatalf("CompleteJob error = %v, want ErrStaleTransition", err)
	}

	job, _ := s.GetJob(ctx, queued.ID)
	if job.Status != JobRunning || job.FinishedAt.Valid || job.Logs != "" || job.Error != "" || job.Summary != nil {
		t.Fatalf("job was partially completed: %+v", job)
	}
	env, _ := s.GetEnvironment(ctx, envID)
	if env.Status != EnvUp || env.Outputs != nil {
		t.Fatalf("environment was partially completed: %+v", env)
	}
}

func TestCompleteJobNilJSONFieldsPreserveExistingValues(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)
	if err := s.SetEnvironmentOutputs(ctx, envID, map[string]any{"ip": "1.2.3.4"}); err != nil {
		t.Fatalf("SetEnvironmentOutputs: %v", err)
	}
	queued, _ := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID: envID, Action: ActionRefresh,
		AllowedFrom: []string{EnvPending}, TransientStatus: EnvRefreshing,
	})
	_, _, _ = s.StartJob(ctx, queued.ID)
	if err := s.SetJobSummary(ctx, queued.ID, map[string]any{"before": 1}); err != nil {
		t.Fatalf("SetJobSummary: %v", err)
	}

	if err := s.CompleteJob(ctx, JobCompletion{
		JobID: queued.ID, EnvironmentID: envID,
		JobStatus: JobSucceeded, EnvironmentStatus: EnvUp,
		Summary: nil, Outputs: nil,
	}); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	job, _ := s.GetJob(ctx, queued.ID)
	env, _ := s.GetEnvironment(ctx, envID)
	if job.Summary["before"] != float64(1) {
		t.Fatalf("nil summary overwrote existing summary: %+v", job.Summary)
	}
	if env.Outputs["ip"] != "1.2.3.4" {
		t.Fatalf("nil outputs overwrote existing outputs: %+v", env.Outputs)
	}
}

func TestCompleteJobRejectsNonTerminalStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)
	queued, _ := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID: envID, Action: ActionPreview,
		AllowedFrom: []string{EnvPending}, TransientStatus: EnvPreviewing,
	})
	_, _, _ = s.StartJob(ctx, queued.ID)

	err := s.CompleteJob(ctx, JobCompletion{
		JobID: queued.ID, EnvironmentID: envID,
		JobStatus: JobRunning, EnvironmentStatus: EnvPreviewReady,
	})
	if !errors.Is(err, ErrInvalidCompletion) {
		t.Fatalf("CompleteJob error = %v, want ErrInvalidCompletion", err)
	}
	job, _ := s.GetJob(ctx, queued.ID)
	env, _ := s.GetEnvironment(ctx, envID)
	if job.Status != JobRunning || env.Status != EnvPreviewing {
		t.Fatalf("invalid completion changed state: job=%+v env=%+v", job, env)
	}
}

func TestCompleteJobAcceptsValidActionResults(t *testing.T) {
	successes := []struct {
		name              string
		action            string
		environmentStatus string
	}{
		{"preview", ActionPreview, EnvPreviewReady},
		{"destroy preview", ActionDestroyPreview, EnvDestroyPreviewReady},
		{"up", ActionUp, EnvUp},
		{"refresh", ActionRefresh, EnvUp},
		{"destroy", ActionDestroy, EnvDestroyed},
	}
	for _, tt := range successes {
		t.Run("succeeded "+tt.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			envID, jobID := startLifecycleJob(t, s, tt.action)
			if err := s.CompleteJob(ctx, JobCompletion{
				JobID: jobID, EnvironmentID: envID,
				JobStatus: JobSucceeded, EnvironmentStatus: tt.environmentStatus,
			}); err != nil {
				t.Fatalf("CompleteJob: %v", err)
			}
			assertCompletedLifecycle(t, s, jobID, envID, JobSucceeded, tt.environmentStatus)
		})
	}

	for _, action := range []string{ActionPreview, ActionDestroyPreview, ActionUp, ActionRefresh, ActionDestroy} {
		t.Run("failed "+action, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			envID, jobID := startLifecycleJob(t, s, action)
			if err := s.CompleteJob(ctx, JobCompletion{
				JobID: jobID, EnvironmentID: envID,
				JobStatus: JobFailed, EnvironmentStatus: EnvFailed,
				Error: "operation failed",
			}); err != nil {
				t.Fatalf("CompleteJob: %v", err)
			}
			assertCompletedLifecycle(t, s, jobID, envID, JobFailed, EnvFailed)
		})
	}
}

func TestCompleteJobRejectsInvalidActionResultsAtomically(t *testing.T) {
	tests := []struct {
		name              string
		action            string
		jobStatus         string
		environmentStatus string
	}{
		{"preview cannot finish up", ActionPreview, JobSucceeded, EnvUp},
		{"destroy preview cannot finish preview ready", ActionDestroyPreview, JobSucceeded, EnvPreviewReady},
		{"up cannot finish destroyed", ActionUp, JobSucceeded, EnvDestroyed},
		{"refresh cannot finish preview ready", ActionRefresh, JobSucceeded, EnvPreviewReady},
		{"destroy cannot finish up", ActionDestroy, JobSucceeded, EnvUp},
		{"failed job requires failed environment", ActionPreview, JobFailed, EnvPreviewReady},
		{"successful job cannot finish failed", ActionPreview, JobSucceeded, EnvFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			envID, jobID := startLifecycleJob(t, s, tt.action)
			beforeJob, _ := s.GetJob(ctx, jobID)
			beforeEnv, _ := s.GetEnvironment(ctx, envID)

			err := s.CompleteJob(ctx, JobCompletion{
				JobID: jobID, EnvironmentID: envID,
				JobStatus: tt.jobStatus, EnvironmentStatus: tt.environmentStatus,
				Logs: "must roll back", Error: "must roll back",
				Summary: map[string]any{"must": "roll back"}, Outputs: map[string]any{"must": "roll back"},
			})
			if !errors.Is(err, ErrInvalidCompletion) {
				t.Fatalf("CompleteJob error = %v, want ErrInvalidCompletion", err)
			}
			afterJob, _ := s.GetJob(ctx, jobID)
			afterEnv, _ := s.GetEnvironment(ctx, envID)
			if afterJob.Status != beforeJob.Status || afterJob.Logs != beforeJob.Logs || afterJob.Error != beforeJob.Error || afterJob.FinishedAt.Valid != beforeJob.FinishedAt.Valid || afterJob.Summary != nil {
				t.Fatalf("invalid completion changed job: before=%+v after=%+v", beforeJob, afterJob)
			}
			if afterEnv.Status != beforeEnv.Status || afterEnv.ResumeStatus != beforeEnv.ResumeStatus || afterEnv.Outputs != nil {
				t.Fatalf("invalid completion changed environment: before=%+v after=%+v", beforeEnv, afterEnv)
			}
		})
	}
}

func TestCompleteJobRejectsUnknownActionAtomically(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)
	if err := s.UpdateEnvironmentStatus(ctx, envID, EnvProvisioning); err != nil {
		t.Fatalf("UpdateEnvironmentStatus: %v", err)
	}
	jobID, err := s.CreateJob(ctx, Job{EnvironmentID: envID, Action: "unknown"})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := s.UpdateJobStatus(ctx, jobID, JobRunning); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}

	err = s.CompleteJob(ctx, JobCompletion{
		JobID: jobID, EnvironmentID: envID,
		JobStatus: JobSucceeded, EnvironmentStatus: EnvUp,
		Logs: "must roll back", Outputs: map[string]any{"must": "roll back"},
	})
	if !errors.Is(err, ErrInvalidCompletion) {
		t.Fatalf("CompleteJob error = %v, want ErrInvalidCompletion", err)
	}
	job, _ := s.GetJob(ctx, jobID)
	env, _ := s.GetEnvironment(ctx, envID)
	if job.Status != JobRunning || job.Logs != "" || job.FinishedAt.Valid {
		t.Fatalf("unknown completion changed job: %+v", job)
	}
	if env.Status != EnvProvisioning || env.Outputs != nil {
		t.Fatalf("unknown completion changed environment: %+v", env)
	}
}

func TestFailOrphanCompletesQueuedOrRunningJob(t *testing.T) {
	for _, initialJobStatus := range []string{JobQueued, JobRunning} {
		t.Run(initialJobStatus, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			envID := seedEnvironment(t, s)
			queued, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
				EnvironmentID: envID, Action: ActionUp,
				AllowedFrom: []string{EnvPending}, TransientStatus: EnvProvisioning,
			})
			if err != nil {
				t.Fatalf("EnqueueJobTransition: %v", err)
			}
			if initialJobStatus == JobRunning {
				if _, _, err := s.StartJob(ctx, queued.ID); err != nil {
					t.Fatalf("StartJob: %v", err)
				}
			}
			if err := s.SetJobLogs(ctx, queued.ID, "already persisted\n"); err != nil {
				t.Fatalf("SetJobLogs: %v", err)
			}
			logs := ""
			if initialJobStatus == JobRunning {
				logs = "latest broker snapshot\n"
			}

			if err := s.FailOrphanJob(ctx, queued.ID, "interrupted by restart", logs); err != nil {
				t.Fatalf("FailOrphanJob: %v", err)
			}
			job, _ := s.GetJob(ctx, queued.ID)
			wantLogs := "already persisted\n"
			if logs != "" {
				wantLogs = logs
			}
			if job.Status != JobFailed || job.Error != "interrupted by restart" || job.Logs != wantLogs || !job.FinishedAt.Valid {
				t.Fatalf("failed orphan job = %+v", job)
			}
			env, _ := s.GetEnvironment(ctx, envID)
			if env.Status != EnvFailed {
				t.Fatalf("orphan environment status = %q, want %q", env.Status, EnvFailed)
			}
		})
	}
}

func TestFailOrphanRollsBackOnStaleEnvironment(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)
	queued, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID: envID, Action: ActionUp,
		AllowedFrom: []string{EnvPending}, TransientStatus: EnvProvisioning,
	})
	if err != nil {
		t.Fatalf("EnqueueJobTransition: %v", err)
	}
	if _, _, err := s.StartJob(ctx, queued.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	if err := s.UpdateEnvironmentStatus(ctx, envID, EnvUp); err != nil {
		t.Fatalf("make environment stale: %v", err)
	}

	err = s.FailOrphanJob(ctx, queued.ID, "interrupted by restart", "must roll back\n")
	if !errors.Is(err, ErrStaleTransition) {
		t.Fatalf("FailOrphanJob error = %v, want ErrStaleTransition", err)
	}
	job, _ := s.GetJob(ctx, queued.ID)
	if job.Status != JobRunning || job.Error != "" || job.Logs != "" || job.FinishedAt.Valid {
		t.Fatalf("job changed despite stale environment: %+v", job)
	}
	env, _ := s.GetEnvironment(ctx, envID)
	if env.Status != EnvUp {
		t.Fatalf("environment changed despite stale transition: %+v", env)
	}
}

func TestMigrationApplicationIsAtomic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const name = "9999_atomic_failure.sql"
	body := []byte(`
CREATE TABLE migration_atomic_probe (id INTEGER PRIMARY KEY);
THIS IS NOT VALID SQL;
`)

	if err := applyMigration(ctx, s.DB(), name, body); err == nil {
		t.Fatal("applyMigration succeeded, want invalid SQL error")
	}

	var tableName string
	err := s.DB().QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'migration_atomic_probe'`,
	).Scan(&tableName)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("migration body was not rolled back: table=%q err=%v", tableName, err)
	}
	var version string
	err = s.DB().QueryRowContext(ctx,
		`SELECT version FROM schema_migrations WHERE version = ?`, name,
	).Scan(&version)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("migration version was recorded after rollback: version=%q err=%v", version, err)
	}
}

func destroyPreviewReadyEnvironment(t *testing.T) (*Store, context.Context, int64) {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)
	if err := s.UpdateEnvironmentStatus(ctx, envID, EnvUp); err != nil {
		t.Fatalf("UpdateEnvironmentStatus: %v", err)
	}
	queued, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID: envID, Action: ActionDestroyPreview,
		AllowedFrom: []string{EnvUp}, TransientStatus: EnvDestroyPreviewing,
		CaptureResumeStatus: true,
	})
	if err != nil {
		t.Fatalf("enqueue destroy preview: %v", err)
	}
	if _, _, err := s.StartJob(ctx, queued.ID); err != nil {
		t.Fatalf("start destroy preview: %v", err)
	}
	if err := s.CompleteJob(ctx, JobCompletion{
		JobID: queued.ID, EnvironmentID: envID,
		JobStatus: JobSucceeded, EnvironmentStatus: EnvDestroyPreviewReady,
		Summary: map[string]any{"deletes": 1},
	}); err != nil {
		t.Fatalf("complete destroy preview: %v", err)
	}
	env, err := s.GetEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if env.Status != EnvDestroyPreviewReady || env.ResumeStatus != EnvUp {
		t.Fatalf("destroy preview environment = %+v", env)
	}
	return s, ctx, envID
}

func startLifecycleJob(t *testing.T, s *Store, action string) (environmentID, jobID int64) {
	t.Helper()
	ctx := context.Background()
	environmentID = seedEnvironment(t, s)
	transientStatus, ok := transientStatusForAction(action)
	if !ok {
		t.Fatalf("unsupported test action %q", action)
	}
	queued, err := s.EnqueueJobTransition(ctx, EnqueueTransition{
		EnvironmentID: environmentID, Action: action,
		AllowedFrom: []string{EnvPending}, TransientStatus: transientStatus,
	})
	if err != nil {
		t.Fatalf("EnqueueJobTransition: %v", err)
	}
	if _, _, err := s.StartJob(ctx, queued.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	return environmentID, queued.ID
}

func assertCompletedLifecycle(t *testing.T, s *Store, jobID, environmentID int64, jobStatus, environmentStatus string) {
	t.Helper()
	ctx := context.Background()
	job, err := s.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != jobStatus || !job.FinishedAt.Valid {
		t.Fatalf("job = %+v, want status %q with finished timestamp", job, jobStatus)
	}
	env, err := s.GetEnvironment(ctx, environmentID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if env.Status != environmentStatus {
		t.Fatalf("environment status = %q, want %q", env.Status, environmentStatus)
	}
}
