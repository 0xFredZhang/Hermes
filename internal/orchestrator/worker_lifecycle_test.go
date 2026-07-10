package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
	"github.com/0xFredZhang/Hermes/internal/store"
)

type lifecycleProvisioner struct {
	mu          sync.Mutex
	calls       []string
	failAction  string
	panicAction string
	blockAction string
	started     chan struct{}
	startedOnce sync.Once
	release     chan struct{}
	upHook      func()
	outputs     map[string]any
}

func (p *lifecycleProvisioner) Preview(ctx context.Context, _ provisioner.Spec, logs io.Writer) (provisioner.PreviewResult, error) {
	if err := p.invoke(ctx, store.ActionPreview, logs); err != nil {
		return provisioner.PreviewResult{}, err
	}
	return provisioner.PreviewResult{Creates: 1, Sames: 2}, nil
}

func (p *lifecycleProvisioner) PreviewDestroy(ctx context.Context, _ provisioner.Spec, logs io.Writer) (provisioner.PreviewResult, error) {
	if err := p.invoke(ctx, store.ActionDestroyPreview, logs); err != nil {
		return provisioner.PreviewResult{}, err
	}
	return provisioner.PreviewResult{Deletes: 3}, nil
}

func (p *lifecycleProvisioner) Refresh(ctx context.Context, _ provisioner.Spec, logs io.Writer) (provisioner.PreviewResult, error) {
	if err := p.invoke(ctx, store.ActionRefresh, logs); err != nil {
		return provisioner.PreviewResult{}, err
	}
	return provisioner.PreviewResult{Updates: 1, Sames: 4}, nil
}

func (p *lifecycleProvisioner) Up(ctx context.Context, _ provisioner.Spec, logs io.Writer) (provisioner.UpResult, error) {
	if err := p.invoke(ctx, store.ActionUp, logs); err != nil {
		return provisioner.UpResult{}, err
	}
	if p.upHook != nil {
		p.upHook()
	}
	return provisioner.UpResult{Outputs: p.outputs}, nil
}

func (p *lifecycleProvisioner) Destroy(ctx context.Context, _ provisioner.Spec, logs io.Writer) error {
	return p.invoke(ctx, store.ActionDestroy, logs)
}

func (p *lifecycleProvisioner) invoke(ctx context.Context, action string, logs io.Writer) error {
	p.mu.Lock()
	p.calls = append(p.calls, action)
	p.mu.Unlock()
	if _, err := fmt.Fprintf(logs, "%s started\n", action); err != nil {
		return err
	}
	if p.started != nil {
		p.startedOnce.Do(func() { close(p.started) })
	}
	if p.release != nil {
		<-p.release
	}
	if p.panicAction == action {
		panic("provisioner panic")
	}
	if p.blockAction == action {
		<-ctx.Done()
		return ctx.Err()
	}
	if p.failAction == action {
		return errors.New("provisioner failed")
	}
	return nil
}

func (p *lifecycleProvisioner) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func TestRunDoesNotCallProvisionerWhenStartTransitionFails(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	queued, err := st.EnqueueJobTransition(ctx, store.EnqueueTransition{
		EnvironmentID: envID, Action: store.ActionPreview,
		AllowedFrom: []string{store.EnvPending}, TransientStatus: store.EnvPreviewing,
	})
	if err != nil {
		t.Fatalf("EnqueueJobTransition: %v", err)
	}
	if err := st.UpdateJobStatus(ctx, queued.ID, store.JobSucceeded); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}
	prov := &lifecycleProvisioner{}
	o := New(st, prov, NewBroker(), 1)

	err = o.run(ctx, queued.ID)
	if !errors.Is(err, store.ErrJobNotQueued) {
		t.Fatalf("run start error = %v, want ErrJobNotQueued", err)
	}
	if prov.callCount() != 0 {
		t.Fatalf("provisioner calls = %d, want 0", prov.callCount())
	}
}

func TestRunCompletesKnownActionsAtomically(t *testing.T) {
	tests := []struct {
		name           string
		action         string
		from           string
		resume         string
		wantEnv        string
		wantSummaryKey string
		wantOutputKey  string
	}{
		{"preview", store.ActionPreview, store.EnvPending, "", store.EnvPreviewReady, "creates", ""},
		{"destroy preview", store.ActionDestroyPreview, store.EnvUp, "", store.EnvDestroyPreviewReady, "deletes", ""},
		{"refresh", store.ActionRefresh, store.EnvUp, "", store.EnvUp, "updates", ""},
		{"up", store.ActionUp, store.EnvPreviewReady, "", store.EnvUp, "", "public_ip"},
		{"destroy", store.ActionDestroy, store.EnvDestroyPreviewReady, store.EnvUp, store.EnvDestroyed, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, envID := newSeededStore(t)
			ctx := context.Background()
			setEnvironmentState(t, st, envID, tt.from, tt.resume)
			prov := &lifecycleProvisioner{outputs: map[string]any{"public_ip": "1.2.3.4"}}
			o := New(st, prov, NewBroker(), 1)
			jobID, err := o.Enqueue(ctx, envID, tt.action)
			if err != nil {
				t.Fatalf("Enqueue: %v", err)
			}

			if err := o.run(ctx, jobID); err != nil {
				t.Fatalf("run: %v", err)
			}
			job, err := st.GetJob(ctx, jobID)
			if err != nil {
				t.Fatalf("GetJob: %v", err)
			}
			if job.Status != store.JobSucceeded || !job.FinishedAt.Valid || !strings.Contains(job.Logs, tt.action+" started") {
				t.Fatalf("completed job = %+v", job)
			}
			if tt.wantSummaryKey != "" && job.Summary[tt.wantSummaryKey] == nil {
				t.Fatalf("summary = %+v, want %q", job.Summary, tt.wantSummaryKey)
			}
			env, err := st.GetEnvironment(ctx, envID)
			if err != nil {
				t.Fatalf("GetEnvironment: %v", err)
			}
			if env.Status != tt.wantEnv {
				t.Fatalf("environment status = %q, want %q", env.Status, tt.wantEnv)
			}
			if tt.wantOutputKey != "" && env.Outputs[tt.wantOutputKey] == nil {
				t.Fatalf("outputs = %+v, want %q", env.Outputs, tt.wantOutputKey)
			}
			if tt.action == store.ActionDestroy && env.ResumeStatus != "" {
				t.Fatalf("destroyed environment retained resume status %q", env.ResumeStatus)
			}
			if got := o.broker.Snapshot(jobID); got != "" {
				t.Fatalf("persisted terminal job retained broker logs %q", got)
			}
		})
	}
}

func TestRunRejectsUnknownPersistedAction(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	const corruptStatus = "unknown_action_running"
	if err := st.UpdateEnvironmentStatus(ctx, envID, corruptStatus); err != nil {
		t.Fatalf("UpdateEnvironmentStatus: %v", err)
	}
	jobID, err := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: "unknown"})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	prov := &lifecycleProvisioner{}
	o := New(st, prov, NewBroker(), 1)

	if err := o.run(ctx, jobID); err == nil {
		t.Fatal("run unknown action error = nil")
	}
	if prov.callCount() != 0 {
		t.Fatalf("provisioner calls = %d, want 0", prov.callCount())
	}
	job, err := st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != store.JobFailed || !strings.Contains(job.Error, "unknown") || !strings.Contains(job.Logs, "ERROR:") {
		t.Fatalf("unknown action job = %+v, want durable failure", job)
	}
	env, err := st.GetEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if env.Status != store.EnvFailed {
		t.Fatalf("unknown action environment = %q, want failed", env.Status)
	}
}

func TestRunFailurePersistsFullTerminalState(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	setEnvironmentState(t, st, envID, store.EnvPreviewReady, "")
	prov := &lifecycleProvisioner{failAction: store.ActionUp}
	o := New(st, prov, NewBroker(), 1)
	jobID, err := o.Enqueue(ctx, envID, store.ActionUp)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if err := o.run(ctx, jobID); err == nil {
		t.Fatal("run failure error = nil")
	}
	assertFailedTerminalState(t, st, envID, jobID, "provisioner failed", "up started")
}

func TestRunPanicPersistsFailureAndFinalLogs(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	setEnvironmentState(t, st, envID, store.EnvPreviewReady, "")
	prov := &lifecycleProvisioner{panicAction: store.ActionUp}
	o := New(st, prov, NewBroker(), 1)
	jobID, err := o.Enqueue(ctx, envID, store.ActionUp)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if err := o.run(ctx, jobID); err == nil {
		t.Fatal("run panic error = nil")
	}
	assertFailedTerminalState(t, st, envID, jobID, "panic: provisioner panic", "ERROR: panic: provisioner panic")
}

func TestRunSecretPreparationFailureIsTerminal(t *testing.T) {
	st, envID := newSeededStoreWithParams(t, rdsParams())
	ctx := context.Background()
	if err := st.UpsertEnvironmentSecret(ctx, store.EnvironmentSecret{
		EnvironmentID: envID, Kind: store.SecretRDSMySQL,
		Username: "admin", Password: "valid-password",
	}); err != nil {
		t.Fatalf("UpsertEnvironmentSecret: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE environment_secrets SET password_enc = 'not-valid-ciphertext' WHERE environment_id = ?`,
		envID,
	); err != nil {
		t.Fatalf("corrupt environment secret: %v", err)
	}
	setEnvironmentState(t, st, envID, store.EnvPreviewReady, "")
	prov := &lifecycleProvisioner{}
	o := New(st, prov, NewBroker(), 1)
	jobID, err := o.Enqueue(ctx, envID, store.ActionUp)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if err := o.run(ctx, jobID); err == nil {
		t.Fatal("run secret preparation error = nil")
	}
	if prov.callCount() != 0 {
		t.Fatalf("provisioner calls = %d, want 0", prov.callCount())
	}
	assertFailedTerminalState(t, st, envID, jobID, "illegal base64", "ERROR:")
}

func TestRunMetadataSyncFailureRemainsRetryable(t *testing.T) {
	st, envID := newSeededStoreWithParams(t, rdsParams())
	ctx := context.Background()
	if err := st.UpsertEnvironmentSecret(ctx, store.EnvironmentSecret{
		EnvironmentID: envID, Kind: store.SecretRDSMySQL,
		Username: "admin", Password: "valid-password",
	}); err != nil {
		t.Fatalf("UpsertEnvironmentSecret: %v", err)
	}
	setEnvironmentState(t, st, envID, store.EnvPreviewReady, "")
	prov := &lifecycleProvisioner{
		outputs: map[string]any{"rds_address": "db.example"},
		upHook: func() {
			if _, err := st.DB().ExecContext(context.Background(),
				`UPDATE environment_secrets SET password_enc = 'not-valid-ciphertext' WHERE environment_id = ?`,
				envID,
			); err != nil {
				panic(err)
			}
		},
	}
	o := New(st, prov, NewBroker(), 1)
	jobID, err := o.Enqueue(ctx, envID, store.ActionUp)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if err := o.run(ctx, jobID); err == nil {
		t.Fatal("run metadata sync error = nil")
	}
	assertFailedTerminalState(t, st, envID, jobID, "illegal base64", "up started")
	env, err := st.GetEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if env.Outputs != nil {
		t.Fatalf("failed metadata sync stored outputs: %+v", env.Outputs)
	}
	retryID, err := o.Retry(ctx, envID)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	retry, err := st.GetJob(ctx, retryID)
	if err != nil {
		t.Fatalf("GetJob retry: %v", err)
	}
	if retry.Action != store.ActionUp || retry.Status != store.JobQueued {
		t.Fatalf("retry job = %+v, want queued up", retry)
	}
	env, err = st.GetEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("GetEnvironment retry: %v", err)
	}
	if env.Status != store.EnvProvisioning {
		t.Fatalf("retry environment status = %q, want provisioning", env.Status)
	}
}

func TestRunTerminalPersistenceFailureSealsBrokerLogs(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	setEnvironmentState(t, st, envID, store.EnvPreviewReady, "")
	prov := &lifecycleProvisioner{
		outputs: map[string]any{"public_ip": "1.2.3.4"},
		upHook: func() {
			if err := st.UpdateEnvironmentStatus(context.Background(), envID, store.EnvPending); err != nil {
				panic(err)
			}
		},
	}
	o := New(st, prov, NewBroker(), 1)
	jobID, err := o.Enqueue(ctx, envID, store.ActionUp)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	_, current, done, cancel := o.broker.Subscribe(jobID)
	defer cancel()
	if done {
		t.Fatal("active subscription unexpectedly done")
	}

	err = o.run(ctx, jobID)
	if !errors.Is(err, store.ErrStaleTransition) {
		t.Fatalf("run error = %v, want ErrStaleTransition", err)
	}
	var currentLines []string
	for line := range current {
		currentLines = append(currentLines, line)
	}
	if !containsLine(currentLines, "up started") {
		t.Fatalf("current subscriber lines = %v, want provisioning log", currentLines)
	}

	history, future, done, _ := o.broker.Subscribe(jobID)
	if !done || future != nil {
		t.Fatalf("future subscription done=%v channel=%v, want explicit terminal signal", done, future)
	}
	if !containsLine(history, "up started") {
		t.Fatalf("future history = %v, want retained provisioning log", history)
	}
	if got := o.broker.Snapshot(jobID); !strings.Contains(got, "up started") {
		t.Fatalf("retained snapshot = %q, want provisioning log", got)
	}
	job, err := st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != store.JobRunning {
		t.Fatalf("job status = %q, want running after rolled-back completion", job.Status)
	}
	env, err := st.GetEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	otherEnvID, err := st.CreateEnvironment(ctx, store.Environment{
		BlueprintID: env.BlueprintID, CloudAccountID: env.CloudAccountID,
		Name: "other-after-degraded", PulumiStack: "other-after-degraded-1",
		Region: env.Region, Snapshot: env.Snapshot,
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	if _, err := o.Enqueue(ctx, otherEnvID, store.ActionPreview); !errors.Is(err, ErrOrchestratorDegraded) {
		t.Fatalf("Enqueue after persistence failure error = %v, want ErrOrchestratorDegraded", err)
	}
	if _, err := o.Retry(ctx, otherEnvID); !errors.Is(err, ErrOrchestratorDegraded) {
		t.Fatalf("Retry after persistence failure error = %v, want ErrOrchestratorDegraded", err)
	}
	jobs, err := st.ListJobsByEnvironment(ctx, otherEnvID)
	if err != nil {
		t.Fatalf("ListJobsByEnvironment: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("degraded orchestrator accepted jobs: %+v", jobs)
	}
}

func TestRetryTerminalPersistenceIsBounded(t *testing.T) {
	t.Run("exhausts three attempts", func(t *testing.T) {
		cause := errors.New("database unavailable")
		attempts := 0
		err := retryTerminalPersistence(context.Background(), func(context.Context) error {
			attempts++
			return cause
		})
		if !errors.Is(err, cause) {
			t.Fatalf("retry error = %v, want cause", err)
		}
		if attempts != 3 {
			t.Fatalf("attempts = %d, want 3", attempts)
		}
	})

	t.Run("stops after success", func(t *testing.T) {
		attempts := 0
		err := retryTerminalPersistence(context.Background(), func(context.Context) error {
			attempts++
			if attempts == 1 {
				return errors.New("transient failure")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("retry error: %v", err)
		}
		if attempts != 2 {
			t.Fatalf("attempts = %d, want 2", attempts)
		}
	})
}

func TestDegradedOrchestratorReconcilesAcceptedQueuedJobs(t *testing.T) {
	st, failingEnvID := newSeededStore(t)
	ctx := context.Background()
	setEnvironmentState(t, st, failingEnvID, store.EnvPreviewReady, "")
	failingEnv, err := st.GetEnvironment(ctx, failingEnvID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	createEnv := func(name string) int64 {
		t.Helper()
		id, err := st.CreateEnvironment(ctx, store.Environment{
			BlueprintID: failingEnv.BlueprintID, CloudAccountID: failingEnv.CloudAccountID,
			Name: name, PulumiStack: name + "-1", Region: failingEnv.Region,
			Snapshot: failingEnv.Snapshot, Status: store.EnvPreviewReady,
		})
		if err != nil {
			t.Fatalf("CreateEnvironment %s: %v", name, err)
		}
		return id
	}
	reconciledEnvID := createEnv("queued-reconciled")
	tombstoneEnvID := createEnv("queued-tombstone")
	rejectedEnvID := createEnv("rejected-after-degraded")

	release := make(chan struct{})
	prov := &lifecycleProvisioner{
		started: make(chan struct{}),
		release: release,
		upHook: func() {
			if err := st.UpdateEnvironmentStatus(context.Background(), failingEnvID, store.EnvPending); err != nil {
				panic(err)
			}
		},
	}
	o := New(st, prov, NewBroker(), 1)
	if err := o.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer o.Stop()
	if _, err := o.Enqueue(ctx, failingEnvID, store.ActionUp); err != nil {
		t.Fatalf("Enqueue failing job: %v", err)
	}
	select {
	case <-prov.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first job to start")
	}
	reconciledJobID, err := o.Enqueue(ctx, reconciledEnvID, store.ActionUp)
	if err != nil {
		t.Fatalf("Enqueue reconciled job: %v", err)
	}
	_, current, done, cancel := o.broker.Subscribe(reconciledJobID)
	defer cancel()
	if done {
		t.Fatal("queued job subscription unexpectedly done")
	}
	tombstoneJobID, err := o.Enqueue(ctx, tombstoneEnvID, store.ActionUp)
	if err != nil {
		t.Fatalf("Enqueue tombstone job: %v", err)
	}
	if err := st.UpdateEnvironmentStatus(ctx, tombstoneEnvID, store.EnvUp); err != nil {
		t.Fatalf("make queued reconciliation stale: %v", err)
	}
	close(release)

	select {
	case <-current:
		for range current {
		}
	case <-time.After(2 * time.Second):
		t.Fatal("accepted queued job subscriber did not terminate after degraded reconciliation")
	}
	reconciledJob, err := st.GetJob(ctx, reconciledJobID)
	if err != nil {
		t.Fatalf("GetJob reconciled: %v", err)
	}
	if reconciledJob.Status != store.JobFailed || !strings.Contains(reconciledJob.Logs, "orchestrator degraded") {
		t.Fatalf("reconciled queued job = %+v", reconciledJob)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !brokerTopicClosed(o.broker, tombstoneJobID) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	history, future, done, _ := o.broker.Subscribe(tombstoneJobID)
	if !done || future != nil || !containsLine(history, "orchestrator degraded") {
		t.Fatalf("queued tombstone subscription = history %v channel %v done %v", history, future, done)
	}
	tombstoneJob, err := st.GetJob(ctx, tombstoneJobID)
	if err != nil {
		t.Fatalf("GetJob tombstone: %v", err)
	}
	if tombstoneJob.Status != store.JobQueued {
		t.Fatalf("stale reconciliation job status = %q, want queued with tombstone", tombstoneJob.Status)
	}

	if _, err := o.Enqueue(ctx, rejectedEnvID, store.ActionUp); !errors.Is(err, ErrOrchestratorDegraded) {
		t.Fatalf("Enqueue after degraded error = %v, want ErrOrchestratorDegraded", err)
	}
	if _, err := o.Retry(ctx, rejectedEnvID); !errors.Is(err, ErrOrchestratorDegraded) {
		t.Fatalf("Retry after degraded error = %v, want ErrOrchestratorDegraded", err)
	}
	jobs, err := st.ListJobsByEnvironment(ctx, rejectedEnvID)
	if err != nil {
		t.Fatalf("ListJobsByEnvironment rejected: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("degraded admission created jobs: %+v", jobs)
	}
}

func TestDegradedClaimGateOwnsDequeuedJobAcrossCancel(t *testing.T) {
	st, failingEnvID := newSeededStore(t)
	ctx := context.Background()
	setEnvironmentState(t, st, failingEnvID, store.EnvPreviewReady, "")
	failingEnv, err := st.GetEnvironment(ctx, failingEnvID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	queuedEnvID, err := st.CreateEnvironment(ctx, store.Environment{
		BlueprintID: failingEnv.BlueprintID, CloudAccountID: failingEnv.CloudAccountID,
		Name: "dequeued-before-degraded", PulumiStack: "dequeued-before-degraded-1",
		Region: failingEnv.Region, Snapshot: failingEnv.Snapshot, Status: store.EnvPreviewReady,
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	provisionerRelease := make(chan struct{})
	claimRelease := make(chan struct{})
	var provisionerReleaseOnce, claimReleaseOnce sync.Once
	prov := &lifecycleProvisioner{
		started: make(chan struct{}),
		release: provisionerRelease,
		upHook: func() {
			if err := st.UpdateEnvironmentStatus(context.Background(), failingEnvID, store.EnvPending); err != nil {
				panic(err)
			}
		},
	}
	o := New(st, prov, NewBroker(), 2)
	var claimTarget atomic.Int64
	dequeued := make(chan struct{})
	var dequeuedOnce sync.Once
	o.afterHealthyDequeue = func(jobID int64) {
		if jobID != claimTarget.Load() {
			return
		}
		dequeuedOnce.Do(func() { close(dequeued) })
		<-claimRelease
	}
	if err := o.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		provisionerReleaseOnce.Do(func() { close(provisionerRelease) })
		claimReleaseOnce.Do(func() { close(claimRelease) })
		o.Stop()
	})
	failingJobID, err := o.Enqueue(ctx, failingEnvID, store.ActionUp)
	if err != nil {
		t.Fatalf("Enqueue failing job: %v", err)
	}
	select {
	case <-prov.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for failing job start")
	}
	claimTarget.Store(failingJobID + 1)
	queuedJobID, err := o.Enqueue(ctx, queuedEnvID, store.ActionUp)
	if err != nil {
		t.Fatalf("Enqueue queued job: %v", err)
	}
	if queuedJobID != claimTarget.Load() {
		t.Fatalf("queued job id = %d, want deterministic target %d", queuedJobID, claimTarget.Load())
	}
	select {
	case <-dequeued:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for second worker dequeue")
	}
	_, current, done, cancel := o.broker.Subscribe(queuedJobID)
	defer cancel()
	if done {
		t.Fatal("queued job subscription unexpectedly done")
	}
	provisionerReleaseOnce.Do(func() { close(provisionerRelease) })
	deadline := time.Now().Add(2 * time.Second)
	for !o.degraded.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !o.degraded.Load() {
		t.Fatal("orchestrator did not enter degraded state")
	}
	claimReleaseOnce.Do(func() { close(claimRelease) })

	if !waitForClosed(current, 2*time.Second) {
		t.Fatal("dequeued queued job subscriber did not terminate")
	}
	job, err := st.GetJob(ctx, queuedJobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != store.JobFailed || !strings.Contains(job.Logs, "orchestrator degraded") {
		t.Fatalf("dequeued queued job = %+v", job)
	}
	if len(o.queue) != 0 || len(o.queueSlots) != 0 {
		t.Fatalf("degraded claim left queue=%d slots=%d", len(o.queue), len(o.queueSlots))
	}
}

func TestStopDuringClaimRequeuesDequeuedJob(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	setEnvironmentState(t, st, envID, store.EnvPreviewReady, "")
	o := New(st, &lifecycleProvisioner{}, NewBroker(), 1)
	dequeued := make(chan struct{})
	claimRelease := make(chan struct{})
	var dequeueOnce, claimReleaseOnce sync.Once
	o.afterHealthyDequeue = func(int64) {
		dequeueOnce.Do(func() { close(dequeued) })
		<-claimRelease
	}
	if err := o.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		claimReleaseOnce.Do(func() { close(claimRelease) })
		o.Stop()
	})
	jobID, err := o.Enqueue(ctx, envID, store.ActionUp)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	select {
	case <-dequeued:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for worker dequeue")
	}

	conn, err := st.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("reserve store connection: %v", err)
	}
	defer conn.Close()
	claimReleaseOnce.Do(func() { close(claimRelease) })
	deadline := time.Now().Add(2 * time.Second)
	for {
		if !o.admissionMu.TryLock() {
			break
		}
		o.admissionMu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("worker did not enter blocked claim")
		}
		time.Sleep(time.Millisecond)
	}
	stopped := make(chan struct{})
	go func() {
		o.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not cancel blocked claim")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("release store connection: %v", err)
	}

	job, err := st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != store.JobQueued {
		t.Fatalf("job status = %q, want queued", job.Status)
	}
	if len(o.queue) != 1 || len(o.queueSlots) != 1 {
		t.Fatalf("stopped claim left queue=%d slots=%d, want 1/1", len(o.queue), len(o.queueSlots))
	}
}

func TestClaimStoreFailureDegradesAndReconcilesOwnedJob(t *testing.T) {
	st, failingEnvID := newSeededStore(t)
	ctx := context.Background()
	setEnvironmentState(t, st, failingEnvID, store.EnvPreviewReady, "")
	failingEnv, err := st.GetEnvironment(ctx, failingEnvID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	queuedEnvID, err := st.CreateEnvironment(ctx, store.Environment{
		BlueprintID: failingEnv.BlueprintID, CloudAccountID: failingEnv.CloudAccountID,
		Name: "queued-after-claim-failure", PulumiStack: "queued-after-claim-failure-1",
		Region: failingEnv.Region, Snapshot: failingEnv.Snapshot, Status: store.EnvPreviewReady,
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	claimRelease := make(chan struct{})
	dequeued := make(chan struct{})
	var dequeueOnce, claimReleaseOnce sync.Once
	o := New(st, &lifecycleProvisioner{}, NewBroker(), 1)
	o.afterHealthyDequeue = func(int64) {
		dequeueOnce.Do(func() {
			close(dequeued)
			<-claimRelease
		})
	}
	if err := o.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		claimReleaseOnce.Do(func() { close(claimRelease) })
		o.Stop()
	})
	failingJobID, err := o.Enqueue(ctx, failingEnvID, store.ActionUp)
	if err != nil {
		t.Fatalf("Enqueue failing job: %v", err)
	}
	select {
	case <-dequeued:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for failing job dequeue")
	}
	_, failingCurrent, done, failingCancel := o.broker.Subscribe(failingJobID)
	defer failingCancel()
	if done {
		t.Fatal("failing job subscription unexpectedly done")
	}
	queuedJobID, err := o.Enqueue(ctx, queuedEnvID, store.ActionUp)
	if err != nil {
		t.Fatalf("Enqueue queued job: %v", err)
	}
	_, queuedCurrent, done, queuedCancel := o.broker.Subscribe(queuedJobID)
	defer queuedCancel()
	if done {
		t.Fatal("queued job subscription unexpectedly done")
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE environments SET blueprint_snapshot_json = '{' WHERE id = ?`, failingEnvID,
	); err != nil {
		t.Fatalf("corrupt environment snapshot: %v", err)
	}
	claimReleaseOnce.Do(func() { close(claimRelease) })

	if !waitForClosed(failingCurrent, 2*time.Second) {
		t.Fatal("claim failure did not terminate owned job subscriber")
	}
	if !waitForClosed(queuedCurrent, 2*time.Second) {
		t.Fatal("claim failure did not terminate drained job subscriber")
	}
	for _, jobID := range []int64{failingJobID, queuedJobID} {
		job, err := st.GetJob(ctx, jobID)
		if err != nil {
			t.Fatalf("GetJob %d: %v", jobID, err)
		}
		if job.Status != store.JobFailed || !strings.Contains(job.Logs, "orchestrator degraded") {
			t.Fatalf("reconciled job %d = %+v", jobID, job)
		}
		// Future SSE requests observe the durable terminal state before consulting
		// the broker, whose successfully persisted topic must be released.
		if brokerHasTopic(o.broker, jobID) {
			t.Fatalf("persisted reconciled job %d retained broker topic", jobID)
		}
	}
	if !o.degraded.Load() {
		t.Fatal("claim store failure did not degrade orchestrator")
	}
	if len(o.queue) != 0 || len(o.queueSlots) != 0 {
		t.Fatalf("claim failure left queue=%d slots=%d", len(o.queue), len(o.queueSlots))
	}
}

func TestStopPersistsInterruptedJobBeforeReturning(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	setEnvironmentState(t, st, envID, store.EnvPreviewReady, "")
	prov := &lifecycleProvisioner{
		blockAction: store.ActionUp,
		started:     make(chan struct{}),
	}
	o := New(st, prov, NewBroker(), 1)
	if err := o.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	jobID, err := o.Enqueue(ctx, envID, store.ActionUp)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	select {
	case <-prov.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for provisioner start")
	}

	o.Stop()
	assertFailedTerminalState(t, st, envID, jobID, "context canceled", "ERROR: context canceled")
}

func TestRecoverOrphansUsesAtomicFailure(t *testing.T) {
	for _, initialStatus := range []string{store.JobQueued, store.JobRunning} {
		t.Run(initialStatus, func(t *testing.T) {
			st, envID := newSeededStore(t)
			ctx := context.Background()
			setEnvironmentState(t, st, envID, store.EnvPreviewReady, "")
			queued, err := st.EnqueueJobTransition(ctx, store.EnqueueTransition{
				EnvironmentID: envID, Action: store.ActionUp,
				AllowedFrom: []string{store.EnvPreviewReady}, TransientStatus: store.EnvProvisioning,
			})
			if err != nil {
				t.Fatalf("EnqueueJobTransition: %v", err)
			}
			if initialStatus == store.JobRunning {
				if _, _, err := st.StartJob(ctx, queued.ID); err != nil {
					t.Fatalf("StartJob: %v", err)
				}
			}
			o := New(st, &lifecycleProvisioner{}, NewBroker(), 1)

			if err := o.recoverOrphans(ctx); err != nil {
				t.Fatalf("recoverOrphans: %v", err)
			}
			assertFailedTerminalState(t, st, envID, queued.ID, "interrupted by restart", "")
		})
	}
}

func TestStartReturnsRecoveryErrorWithoutLaunchingWorkers(t *testing.T) {
	st, staleEnvID := newSeededStore(t)
	ctx := context.Background()
	setEnvironmentState(t, st, staleEnvID, store.EnvPreviewReady, "")
	stale, err := st.EnqueueJobTransition(ctx, store.EnqueueTransition{
		EnvironmentID: staleEnvID, Action: store.ActionUp,
		AllowedFrom: []string{store.EnvPreviewReady}, TransientStatus: store.EnvProvisioning,
	})
	if err != nil {
		t.Fatalf("EnqueueJobTransition stale: %v", err)
	}
	if _, _, err := st.StartJob(ctx, stale.ID); err != nil {
		t.Fatalf("StartJob stale: %v", err)
	}
	if err := st.UpdateEnvironmentStatus(ctx, staleEnvID, store.EnvUp); err != nil {
		t.Fatalf("make orphan stale: %v", err)
	}

	staleEnv, err := st.GetEnvironment(ctx, staleEnvID)
	if err != nil {
		t.Fatalf("GetEnvironment stale: %v", err)
	}
	otherEnvID, err := st.CreateEnvironment(ctx, store.Environment{
		BlueprintID: staleEnv.BlueprintID, CloudAccountID: staleEnv.CloudAccountID,
		Name: "other", PulumiStack: "other-1", Region: staleEnv.Region,
		Snapshot: staleEnv.Snapshot, Status: store.EnvPreviewReady,
	})
	if err != nil {
		t.Fatalf("CreateEnvironment other: %v", err)
	}
	queued, err := st.EnqueueJobTransition(ctx, store.EnqueueTransition{
		EnvironmentID: otherEnvID, Action: store.ActionUp,
		AllowedFrom: []string{store.EnvPreviewReady}, TransientStatus: store.EnvProvisioning,
	})
	if err != nil {
		t.Fatalf("EnqueueJobTransition other: %v", err)
	}
	prov := &lifecycleProvisioner{}
	o := New(st, prov, NewBroker(), 1)
	o.queue <- queued.ID

	if err := o.Start(ctx); err == nil {
		t.Fatal("Start recovery error = nil")
	}
	time.Sleep(25 * time.Millisecond)
	if prov.callCount() != 0 {
		t.Fatalf("workers launched after recovery error; provisioner calls = %d", prov.callCount())
	}
	if o.cancel != nil {
		t.Fatal("Start installed worker cancellation after recovery error")
	}
}

func assertFailedTerminalState(
	t *testing.T,
	st *store.Store,
	environmentID, jobID int64,
	wantError, wantLog string,
) {
	t.Helper()
	job, err := st.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != store.JobFailed || !job.FinishedAt.Valid || !strings.Contains(job.Error, wantError) {
		t.Fatalf("failed job = %+v, want error containing %q", job, wantError)
	}
	if wantLog != "" && !strings.Contains(job.Logs, wantLog) {
		t.Fatalf("job logs = %q, want %q", job.Logs, wantLog)
	}
	env, err := st.GetEnvironment(context.Background(), environmentID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if env.Status != store.EnvFailed {
		t.Fatalf("environment status = %q, want failed", env.Status)
	}
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

func brokerTopicClosed(b *Broker, jobID int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	topic := b.topics[jobID]
	if topic == nil {
		return false
	}
	topic.mu.Lock()
	defer topic.mu.Unlock()
	return topic.closed
}

func brokerHasTopic(b *Broker, jobID int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.topics[jobID]
	return ok
}

func waitForClosed(ch <-chan string, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return true
			}
		case <-timer.C:
			return false
		}
	}
}
