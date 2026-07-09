package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
	"github.com/0xFredZhang/Hermes/internal/store"
)

var ErrEnvironmentBusy = errors.New("environment already has an active job")

type Orchestrator struct {
	store   *store.Store
	prov    provisioner.Provisioner
	broker  *Broker
	queue   chan int64
	workers int

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(st *store.Store, prov provisioner.Provisioner, broker *Broker, workers int) *Orchestrator {
	if workers < 1 {
		workers = 1
	}
	return &Orchestrator{
		store: st, prov: prov, broker: broker,
		queue: make(chan int64, 128), workers: workers,
	}
}

// Start recovers orphaned jobs from a prior run, then launches the worker pool.
func (o *Orchestrator) Start(ctx context.Context) {
	ctx, o.cancel = context.WithCancel(ctx)
	o.recoverOrphans(ctx)
	for i := 0; i < o.workers; i++ {
		o.wg.Add(1)
		go func() {
			defer o.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case jobID := <-o.queue:
					o.run(ctx, jobID)
				}
			}
		}()
	}
}

// Stop cancels workers and waits for the in-flight jobs to return.
func (o *Orchestrator) Stop() {
	if o.cancel != nil {
		o.cancel()
	}
	o.wg.Wait()
}

// Enqueue guards one active job per environment, creates a queued Job, and
// hands it to the worker pool.
func (o *Orchestrator) Enqueue(ctx context.Context, envID int64, action string) (int64, error) {
	active, err := o.store.HasActiveJob(ctx, envID)
	if err != nil {
		return 0, err
	}
	if active {
		return 0, ErrEnvironmentBusy
	}
	jobID, err := o.store.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: action})
	if err != nil {
		// The partial unique index (migration 0004) is the atomic backstop for
		// the check-then-act race above: a concurrent Enqueue that slips past
		// HasActiveJob fails here on the unique constraint.
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, ErrEnvironmentBusy
		}
		return 0, err
	}
	o.queue <- jobID
	return jobID, nil
}

func (o *Orchestrator) run(ctx context.Context, jobID int64) {
	logs := o.broker.Writer(jobID)
	var envID int64

	// Registered first, so it runs LAST: flush the trailing partial line, then
	// persist the complete log buffer to the DB (fixes losing the last line).
	defer func() {
		o.broker.Close(jobID)
		o.persistLogs(ctx, jobID)
	}()
	// Registered second, so it runs FIRST: a panic in the Pulumi path must fail
	// only this job, never crash the whole server (and every other in-flight job).
	defer func() {
		if r := recover(); r != nil {
			o.fail(ctx, jobID, envID, logs, fmt.Errorf("panic: %v", r))
		}
	}()

	job, err := o.store.GetJob(ctx, jobID)
	if err != nil {
		return
	}
	env, err := o.store.GetEnvironment(ctx, job.EnvironmentID)
	if err != nil {
		o.fail(ctx, jobID, 0, logs, err)
		return
	}
	envID = env.ID
	acct, err := o.store.GetCloudAccount(ctx, env.CloudAccountID) // decrypts secret
	if err != nil {
		o.fail(ctx, jobID, env.ID, logs, err)
		return
	}

	_ = o.store.UpdateJobStatus(ctx, jobID, store.JobRunning)
	_ = o.store.UpdateEnvironmentStatus(ctx, env.ID, transientStatus(job.Action))

	spec := provisioner.Spec{
		StackName: env.PulumiStack,
		Region:    env.Region,
		Params:    env.Snapshot,
		Creds:     provisioner.AWSCreds{AccessKeyID: acct.AccessKeyID, SecretAccessKey: acct.SecretAccessKey},
	}
	secrets, err := o.prepareRuntimeSecrets(ctx, env)
	if err != nil {
		o.fail(ctx, jobID, env.ID, logs, err)
		return
	}
	spec.Secrets = secrets

	switch job.Action {
	case store.ActionPreview:
		res, err := o.prov.Preview(ctx, spec, logs)
		if err != nil {
			o.fail(ctx, jobID, env.ID, logs, err)
			return
		}
		_ = o.store.SetJobSummary(ctx, jobID, map[string]any{
			"creates": res.Creates, "updates": res.Updates,
			"deletes": res.Deletes, "sames": res.Sames,
		})
		_ = o.store.UpdateEnvironmentStatus(ctx, env.ID, store.EnvPreviewReady)
	case store.ActionDestroyPreview:
		res, err := o.prov.PreviewDestroy(ctx, spec, logs)
		if err != nil {
			o.fail(ctx, jobID, env.ID, logs, err)
			return
		}
		_ = o.store.SetJobSummary(ctx, jobID, map[string]any{
			"creates": res.Creates, "updates": res.Updates,
			"deletes": res.Deletes, "sames": res.Sames,
		})
		_ = o.store.UpdateEnvironmentStatus(ctx, env.ID, store.EnvDestroyPreviewReady)
	case store.ActionRefresh:
		res, err := o.prov.Refresh(ctx, spec, logs)
		if err != nil {
			o.fail(ctx, jobID, env.ID, logs, err)
			return
		}
		_ = o.store.SetJobSummary(ctx, jobID, map[string]any{
			"creates": res.Creates, "updates": res.Updates,
			"deletes": res.Deletes, "sames": res.Sames,
		})
		_ = o.store.UpdateEnvironmentStatus(ctx, env.ID, store.EnvUp)
	case store.ActionUp:
		res, err := o.prov.Up(ctx, spec, logs)
		if err != nil {
			o.fail(ctx, jobID, env.ID, logs, err)
			return
		}
		_ = o.store.SetEnvironmentOutputs(ctx, env.ID, res.Outputs)
		if err := o.syncRDSSecretMetadata(ctx, env, res.Outputs); err != nil {
			o.fail(ctx, jobID, env.ID, logs, err)
			return
		}
		_ = o.store.UpdateEnvironmentStatus(ctx, env.ID, store.EnvUp)
	case store.ActionDestroy:
		if err := o.prov.Destroy(ctx, spec, logs); err != nil {
			o.fail(ctx, jobID, env.ID, logs, err)
			return
		}
		_ = o.store.UpdateEnvironmentStatus(ctx, env.ID, store.EnvDestroyed)
	}

	_ = o.store.UpdateJobStatus(ctx, jobID, store.JobSucceeded)
}

func transientStatus(action string) string {
	switch action {
	case store.ActionPreview:
		return store.EnvPreviewing
	case store.ActionDestroyPreview:
		return store.EnvPreviewing
	case store.ActionRefresh:
		return store.EnvRefreshing
	case store.ActionDestroy:
		return store.EnvDestroying
	default:
		return store.EnvProvisioning
	}
}

func (o *Orchestrator) fail(ctx context.Context, jobID, envID int64, logs io.Writer, cause error) {
	fmt.Fprintf(logs, "ERROR: %v\n", cause)
	// Log persistence is handled by the deferred flush-and-persist in run().
	_ = o.store.SetJobError(ctx, jobID, cause.Error())
	_ = o.store.UpdateJobStatus(ctx, jobID, store.JobFailed)
	if envID != 0 {
		_ = o.store.UpdateEnvironmentStatus(ctx, envID, store.EnvFailed)
	}
}

func (o *Orchestrator) persistLogs(ctx context.Context, jobID int64) {
	_ = o.store.SetJobLogs(ctx, jobID, o.broker.Snapshot(jobID))
}

func (o *Orchestrator) recoverOrphans(ctx context.Context) {
	orphans, err := o.store.ListOrphanJobs(ctx)
	if err != nil {
		return
	}
	for _, j := range orphans {
		_ = o.store.SetJobError(ctx, j.ID, "interrupted by restart")
		_ = o.store.UpdateJobStatus(ctx, j.ID, store.JobFailed)
		env, err := o.store.GetEnvironment(ctx, j.EnvironmentID)
		if err != nil {
			continue
		}
		switch env.Status {
		case store.EnvPreviewing, store.EnvProvisioning, store.EnvRefreshing, store.EnvDestroying:
			_ = o.store.UpdateEnvironmentStatus(ctx, env.ID, store.EnvFailed)
		}
	}
}
