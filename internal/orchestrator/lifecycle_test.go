package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/0xFredZhang/Hermes/internal/store"
)

func TestEnqueueStateActionMatrix(t *testing.T) {
	statuses := []string{
		store.EnvPending,
		store.EnvPreviewing,
		store.EnvDestroyPreviewing,
		store.EnvPreviewReady,
		store.EnvProvisioning,
		store.EnvUp,
		store.EnvRefreshing,
		store.EnvDestroyPreviewReady,
		store.EnvFailed,
		store.EnvDestroying,
		store.EnvDestroyed,
	}
	actions := []string{
		store.ActionPreview,
		store.ActionUp,
		store.ActionRefresh,
		store.ActionDestroyPreview,
		store.ActionDestroy,
	}
	allowed := map[string]string{
		matrixKey(store.EnvPending, store.ActionPreview):             store.EnvPreviewing,
		matrixKey(store.EnvPreviewReady, store.ActionUp):             store.EnvProvisioning,
		matrixKey(store.EnvUp, store.ActionRefresh):                  store.EnvRefreshing,
		matrixKey(store.EnvPreviewReady, store.ActionDestroyPreview): store.EnvDestroyPreviewing,
		matrixKey(store.EnvUp, store.ActionDestroyPreview):           store.EnvDestroyPreviewing,
		matrixKey(store.EnvFailed, store.ActionDestroyPreview):       store.EnvDestroyPreviewing,
		matrixKey(store.EnvDestroyPreviewReady, store.ActionDestroy): store.EnvDestroying,
	}

	for _, status := range statuses {
		for _, action := range actions {
			name := status + "_" + action
			t.Run(name, func(t *testing.T) {
				st, envID := newSeededStore(t)
				ctx := context.Background()
				if err := st.UpdateEnvironmentStatus(ctx, envID, status); err != nil {
					t.Fatalf("UpdateEnvironmentStatus: %v", err)
				}
				o := New(st, &fakeProvisioner{}, NewBroker(), 1)

				jobID, err := o.Enqueue(ctx, envID, action)
				wantTransient, ok := allowed[matrixKey(status, action)]
				if !ok {
					if !errors.Is(err, ErrInvalidTransition) {
						t.Fatalf("Enqueue(%q, %q) error = %v, want ErrInvalidTransition", status, action, err)
					}
					jobs, listErr := st.ListJobsByEnvironment(ctx, envID)
					if listErr != nil {
						t.Fatalf("ListJobsByEnvironment: %v", listErr)
					}
					if len(jobs) != 0 {
						t.Fatalf("invalid transition created jobs: %+v", jobs)
					}
					env, getErr := st.GetEnvironment(ctx, envID)
					if getErr != nil {
						t.Fatalf("GetEnvironment: %v", getErr)
					}
					if env.Status != status {
						t.Fatalf("invalid transition changed status to %q, want %q", env.Status, status)
					}
					return
				}

				if err != nil {
					t.Fatalf("Enqueue(%q, %q): %v", status, action, err)
				}
				job, getErr := st.GetJob(ctx, jobID)
				if getErr != nil {
					t.Fatalf("GetJob: %v", getErr)
				}
				if job.Action != action || job.Status != store.JobQueued {
					t.Fatalf("queued job = %+v", job)
				}
				env, getErr := st.GetEnvironment(ctx, envID)
				if getErr != nil {
					t.Fatalf("GetEnvironment: %v", getErr)
				}
				if env.Status != wantTransient {
					t.Fatalf("environment status = %q, want %q", env.Status, wantTransient)
				}
			})
		}
	}
}

func TestEnqueueRejectsUnknownAction(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	o := New(st, &fakeProvisioner{}, NewBroker(), 1)

	if _, err := o.Enqueue(ctx, envID, "unknown"); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("Enqueue unknown error = %v, want ErrInvalidAction", err)
	}
	assertNoNewLifecycleJob(t, st, envID, 0)
	env, err := st.GetEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if env.Status != store.EnvPending {
		t.Fatalf("unknown action changed environment to %q", env.Status)
	}
}

func TestEnqueueMapsActiveJobConflict(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	existingID, err := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionPreview})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	o := New(st, &fakeProvisioner{}, NewBroker(), 1)

	if _, err := o.Enqueue(ctx, envID, store.ActionPreview); !errors.Is(err, ErrEnvironmentBusy) {
		t.Fatalf("Enqueue active conflict error = %v, want ErrEnvironmentBusy", err)
	}
	jobs, err := st.ListJobsByEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("ListJobsByEnvironment: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != existingID {
		t.Fatalf("active conflict jobs = %+v, want only %d", jobs, existingID)
	}
	env, err := st.GetEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if env.Status != store.EnvPending {
		t.Fatalf("active conflict changed environment to %q", env.Status)
	}
}

func TestRetryRequiresFailedEnvironment(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	if _, err := st.CreateJob(ctx, store.Job{
		EnvironmentID: envID, Action: store.ActionPreview, Status: store.JobFailed,
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	o := New(st, &fakeProvisioner{}, NewBroker(), 1)

	if _, err := o.Retry(ctx, envID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Retry pending environment error = %v, want ErrInvalidTransition", err)
	}
	assertNoNewLifecycleJob(t, st, envID, 1)
}

func TestRetryUsesLatestFailedJob(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	for _, job := range []store.Job{
		{EnvironmentID: envID, Action: store.ActionPreview, Status: store.JobFailed},
		{EnvironmentID: envID, Action: store.ActionDestroy, Status: store.JobFailed},
		{EnvironmentID: envID, Action: store.ActionUp, Status: store.JobSucceeded},
	} {
		if _, err := st.CreateJob(ctx, job); err != nil {
			t.Fatalf("CreateJob(%s, %s): %v", job.Action, job.Status, err)
		}
	}
	if err := st.UpdateEnvironmentStatus(ctx, envID, store.EnvFailed); err != nil {
		t.Fatalf("UpdateEnvironmentStatus: %v", err)
	}
	o := New(st, &fakeProvisioner{}, NewBroker(), 1)

	jobID, err := o.Retry(ctx, envID)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	job, err := st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Action != store.ActionDestroy || job.Status != store.JobQueued {
		t.Fatalf("retry job = %+v, want queued destroy", job)
	}
	env, err := st.GetEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if env.Status != store.EnvDestroying {
		t.Fatalf("retry environment status = %q, want %q", env.Status, store.EnvDestroying)
	}
}

func TestRetryRequeuesExactKnownAction(t *testing.T) {
	tests := []struct {
		action    string
		transient string
	}{
		{store.ActionPreview, store.EnvPreviewing},
		{store.ActionUp, store.EnvProvisioning},
		{store.ActionRefresh, store.EnvRefreshing},
		{store.ActionDestroyPreview, store.EnvDestroyPreviewing},
		{store.ActionDestroy, store.EnvDestroying},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			st, envID := newSeededStore(t)
			ctx := context.Background()
			if _, err := st.CreateJob(ctx, store.Job{
				EnvironmentID: envID, Action: tt.action, Status: store.JobFailed,
			}); err != nil {
				t.Fatalf("CreateJob: %v", err)
			}
			if err := st.UpdateEnvironmentStatus(ctx, envID, store.EnvFailed); err != nil {
				t.Fatalf("UpdateEnvironmentStatus: %v", err)
			}
			o := New(st, &fakeProvisioner{}, NewBroker(), 1)

			jobID, err := o.Retry(ctx, envID)
			if err != nil {
				t.Fatalf("Retry: %v", err)
			}
			job, err := st.GetJob(ctx, jobID)
			if err != nil {
				t.Fatalf("GetJob: %v", err)
			}
			if job.Action != tt.action {
				t.Fatalf("retry action = %q, want %q", job.Action, tt.action)
			}
			env, err := st.GetEnvironment(ctx, envID)
			if err != nil {
				t.Fatalf("GetEnvironment: %v", err)
			}
			if env.Status != tt.transient {
				t.Fatalf("retry status = %q, want %q", env.Status, tt.transient)
			}
		})
	}
}

func TestRetryRejectsMissingFailedJob(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	if err := st.UpdateEnvironmentStatus(ctx, envID, store.EnvFailed); err != nil {
		t.Fatalf("UpdateEnvironmentStatus: %v", err)
	}
	if _, err := st.CreateJob(ctx, store.Job{
		EnvironmentID: envID, Action: store.ActionUp, Status: store.JobSucceeded,
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	o := New(st, &fakeProvisioner{}, NewBroker(), 1)

	if _, err := o.Retry(ctx, envID); !errors.Is(err, ErrNoFailedJob) {
		t.Fatalf("Retry missing failed job error = %v, want ErrNoFailedJob", err)
	}
	assertNoNewLifecycleJob(t, st, envID, 1)
}

func TestRetryRejectsUnknownFailedAction(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	if err := st.UpdateEnvironmentStatus(ctx, envID, store.EnvFailed); err != nil {
		t.Fatalf("UpdateEnvironmentStatus: %v", err)
	}
	if _, err := st.CreateJob(ctx, store.Job{
		EnvironmentID: envID, Action: "unknown", Status: store.JobFailed,
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	o := New(st, &fakeProvisioner{}, NewBroker(), 1)

	if _, err := o.Retry(ctx, envID); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("Retry unknown failed action error = %v, want ErrInvalidAction", err)
	}
	assertNoNewLifecycleJob(t, st, envID, 1)
}

func TestCancelDestroyPreviewUsesAtomicStoreTransition(t *testing.T) {
	t.Run("restores resume status", func(t *testing.T) {
		st, envID := newSeededStore(t)
		ctx := context.Background()
		setEnvironmentState(t, st, envID, store.EnvDestroyPreviewReady, store.EnvUp)
		o := New(st, &fakeProvisioner{}, NewBroker(), 1)

		if err := o.CancelDestroyPreview(ctx, envID); err != nil {
			t.Fatalf("CancelDestroyPreview: %v", err)
		}
		env, err := st.GetEnvironment(ctx, envID)
		if err != nil {
			t.Fatalf("GetEnvironment: %v", err)
		}
		if env.Status != store.EnvUp || env.ResumeStatus != "" {
			t.Fatalf("cancelled environment = %+v, want up with cleared resume status", env)
		}
	})

	t.Run("maps stale transition", func(t *testing.T) {
		st, envID := newSeededStore(t)
		o := New(st, &fakeProvisioner{}, NewBroker(), 1)
		if err := o.CancelDestroyPreview(context.Background(), envID); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("CancelDestroyPreview stale error = %v, want ErrInvalidTransition", err)
		}
	})

	t.Run("maps active destroy conflict", func(t *testing.T) {
		st, envID := newSeededStore(t)
		ctx := context.Background()
		setEnvironmentState(t, st, envID, store.EnvDestroyPreviewReady, store.EnvUp)
		if _, err := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionDestroy}); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		o := New(st, &fakeProvisioner{}, NewBroker(), 1)

		if err := o.CancelDestroyPreview(ctx, envID); !errors.Is(err, ErrEnvironmentBusy) {
			t.Fatalf("CancelDestroyPreview active error = %v, want ErrEnvironmentBusy", err)
		}
	})
}

func matrixKey(status, action string) string {
	return status + "\x00" + action
}

func assertNoNewLifecycleJob(t *testing.T, st *store.Store, environmentID int64, want int) {
	t.Helper()
	jobs, err := st.ListJobsByEnvironment(context.Background(), environmentID)
	if err != nil {
		t.Fatalf("ListJobsByEnvironment: %v", err)
	}
	if len(jobs) != want {
		t.Fatalf("jobs = %d, want %d: %+v", len(jobs), want, jobs)
	}
}

func setEnvironmentState(t *testing.T, st *store.Store, environmentID int64, status, resumeStatus string) {
	t.Helper()
	res, err := st.DB().ExecContext(context.Background(),
		`UPDATE environments SET status = ?, resume_status = ? WHERE id = ?`,
		status, resumeStatus, environmentID,
	)
	if err != nil {
		t.Fatalf("set environment state: %v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("set environment state rows = %d, err = %v", n, err)
	}
}

func TestLifecycleErrorsSupportErrorsIs(t *testing.T) {
	for _, err := range []error{
		ErrEnvironmentBusy,
		ErrInvalidAction,
		ErrInvalidTransition,
		ErrNoFailedJob,
		ErrOrchestratorDegraded,
	} {
		t.Run(fmt.Sprint(err), func(t *testing.T) {
			wrapped := fmt.Errorf("wrapped: %w", err)
			if !errors.Is(wrapped, err) {
				t.Fatalf("errors.Is(%v, %v) = false", wrapped, err)
			}
		})
	}
}

func TestAdmissionGateRejectsWaitingEnqueueAfterDegraded(t *testing.T) {
	st, envID := newSeededStore(t)
	o := New(st, &fakeProvisioner{}, NewBroker(), 1)
	o.admissionMu.Lock()
	result := make(chan error, 1)
	go func() {
		_, err := o.Enqueue(context.Background(), envID, store.ActionPreview)
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for len(o.queueSlots) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(o.queueSlots) != 1 {
		o.admissionMu.Unlock()
		t.Fatal("enqueue did not reserve a queue slot before admission")
	}
	o.degraded.Store(true)
	o.admissionMu.Unlock()

	select {
	case err := <-result:
		if !errors.Is(err, ErrOrchestratorDegraded) {
			t.Fatalf("waiting Enqueue error = %v, want ErrOrchestratorDegraded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting Enqueue did not return")
	}
	jobs, err := st.ListJobsByEnvironment(context.Background(), envID)
	if err != nil {
		t.Fatalf("ListJobsByEnvironment: %v", err)
	}
	if len(jobs) != 0 || len(o.queue) != 0 || len(o.queueSlots) != 0 {
		t.Fatalf("rejected admission left jobs=%+v queue=%d slots=%d", jobs, len(o.queue), len(o.queueSlots))
	}
}
