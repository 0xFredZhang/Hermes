package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
	"github.com/0xFredZhang/Hermes/internal/store"
)

const (
	orchestratorQueueCapacity   = 128
	terminalPersistenceTimeout  = 5 * time.Second
	terminalPersistenceAttempts = 3
)

type Orchestrator struct {
	store               *store.Store
	prov                provisioner.Provisioner
	broker              *Broker
	queue               chan int64
	queueSlots          chan struct{}
	workers             int
	afterHealthyDequeue func(int64)

	admissionMu sync.Mutex
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	degraded    atomic.Bool
}

func New(st *store.Store, prov provisioner.Provisioner, broker *Broker, workers int) *Orchestrator {
	if workers < 1 {
		workers = 1
	}
	return &Orchestrator{
		store: st, prov: prov, broker: broker,
		queue:      make(chan int64, orchestratorQueueCapacity),
		queueSlots: make(chan struct{}, orchestratorQueueCapacity),
		workers:    workers,
	}
}

// Start recovers orphaned jobs from a prior run, then launches the worker pool.
func (o *Orchestrator) Start(ctx context.Context) error {
	if err := o.recoverOrphans(ctx); err != nil {
		return fmt.Errorf("recover orphan jobs: %w", err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	o.cancel = cancel
	for i := 0; i < o.workers; i++ {
		o.wg.Add(1)
		go func() {
			defer o.wg.Done()
			for {
				select {
				case <-workerCtx.Done():
					return
				default:
				}
				select {
				case <-workerCtx.Done():
					return
				case jobID := <-o.queue:
					if o.afterHealthyDequeue != nil {
						o.afterHealthyDequeue(jobID)
					}
					stop, err := o.runDequeued(workerCtx, jobID)
					if err != nil {
						log.Printf("orchestrator job %d: %v", jobID, err)
					}
					if stop {
						return
					}
				}
			}
		}()
	}
	return nil
}

// Stop cancels workers and waits until in-flight jobs persist their terminal
// state using the detached completion context.
func (o *Orchestrator) Stop() {
	if o.cancel != nil {
		o.cancel()
	}
	o.wg.Wait()
}

type actionResult struct {
	environmentStatus string
	summary           map[string]any
	outputs           map[string]any
	clearResumeStatus bool
}

func (o *Orchestrator) run(ctx context.Context, jobID int64) error {
	job, env, _, err := o.claimJob(ctx, jobID, false)
	if err != nil {
		return err
	}
	return o.runClaimed(ctx, job, env)
}

func (o *Orchestrator) runDequeued(ctx context.Context, jobID int64) (bool, error) {
	job, env, stop, err := o.claimJob(ctx, jobID, true)
	if err != nil || stop {
		return stop, err
	}
	return false, o.runClaimed(ctx, job, env)
}

func (o *Orchestrator) claimJob(
	ctx context.Context,
	jobID int64,
	ownsQueueSlot bool,
) (store.Job, store.Environment, bool, error) {
	o.admissionMu.Lock()
	if err := o.healthError(); err != nil {
		if ownsQueueSlot {
			o.releaseQueueSlot()
		}
		o.admissionMu.Unlock()
		if reconcileErr := o.reconcileQueuedJob(ctx, jobID); reconcileErr != nil {
			return store.Job{}, store.Environment{}, false, errors.Join(err, reconcileErr)
		}
		return store.Job{}, store.Environment{}, false, err
	}
	if ownsQueueSlot && ctx.Err() != nil {
		// Preserve normal shutdown semantics: startup recovery owns queued work
		// that was never claimed. The queue slot remains reserved.
		o.queue <- jobID
		o.admissionMu.Unlock()
		return store.Job{}, store.Environment{}, true, nil
	}
	job, env, err := o.startJob(ctx, jobID)
	if err != nil && ownsQueueSlot && !errors.Is(err, store.ErrJobNotQueued) {
		var panicErr *startJobPanicError
		if ctx.Err() != nil && !errors.As(err, &panicErr) {
			o.queue <- jobID
			o.admissionMu.Unlock()
			return store.Job{}, store.Environment{}, true, nil
		}
		cancel, transitioned := o.transitionToDegradedLocked()
		o.admissionMu.Unlock()
		if transitioned {
			o.cancelAndDrain(ctx, cancel)
		}
		o.releaseQueueSlot()
		claimErr := fmt.Errorf("start job %d: %w", jobID, err)
		if reconcileErr := o.reconcileQueuedJob(ctx, jobID); reconcileErr != nil {
			return store.Job{}, store.Environment{}, false, errors.Join(claimErr, reconcileErr)
		}
		return store.Job{}, store.Environment{}, false, claimErr
	}
	if ownsQueueSlot {
		o.releaseQueueSlot()
	}
	o.admissionMu.Unlock()
	if err != nil {
		return store.Job{}, store.Environment{}, false, fmt.Errorf("start job %d: %w", jobID, err)
	}
	return job, env, false, nil
}

type startJobPanicError struct {
	recovered any
}

func (e *startJobPanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.recovered)
}

func (o *Orchestrator) startJob(ctx context.Context, jobID int64) (job store.Job, env store.Environment, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &startJobPanicError{recovered: recovered}
		}
	}()
	return o.store.StartJob(ctx, jobID)
}

func (o *Orchestrator) runClaimed(ctx context.Context, job store.Job, env store.Environment) (runErr error) {
	logs := o.broker.Writer(job.ID)
	terminalPersisted := false
	defer func() {
		if recovered := recover(); recovered != nil {
			terminalPersisted, runErr = o.completeFailure(ctx, job, env, logs, fmt.Errorf("panic: %v", recovered))
		}
		if terminalPersisted {
			o.broker.Close(job.ID)
		} else {
			o.broker.Seal(job.ID)
		}
	}()
	fail := func(cause error) error {
		terminalPersisted, runErr = o.completeFailure(ctx, job, env, logs, cause)
		return runErr
	}

	if _, ok := actionPolicy[job.Action]; !ok {
		terminalPersisted, runErr = o.failUnknownAction(ctx, job, env, logs)
		return runErr
	}

	account, err := o.store.GetCloudAccount(ctx, env.CloudAccountID)
	if err != nil {
		return fail(fmt.Errorf("load cloud account: %w", err))
	}
	spec := provisioner.Spec{
		StackName: env.PulumiStack,
		Region:    env.Region,
		Params:    env.Snapshot,
		Creds: provisioner.AWSCreds{
			AccessKeyID:     account.AccessKeyID,
			SecretAccessKey: account.SecretAccessKey,
		},
	}
	secrets, err := o.prepareRuntimeSecrets(ctx, env)
	if err != nil {
		return fail(fmt.Errorf("prepare runtime secrets: %w", err))
	}
	spec.Secrets = secrets

	result, err := o.executeAction(ctx, job.Action, env, spec, logs)
	if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}

	completion := store.JobCompletion{
		JobID:             job.ID,
		EnvironmentID:     env.ID,
		JobStatus:         store.JobSucceeded,
		EnvironmentStatus: result.environmentStatus,
		Logs:              o.broker.Snapshot(job.ID),
		Summary:           result.summary,
		Outputs:           result.outputs,
		ClearResumeStatus: result.clearResumeStatus,
	}
	if err := o.persistTerminal(ctx, func(completionCtx context.Context) error {
		return o.store.CompleteJob(completionCtx, completion)
	}); err != nil {
		return fmt.Errorf("complete job %d: %w", job.ID, err)
	}
	terminalPersisted = true
	return nil
}

func (o *Orchestrator) executeAction(
	ctx context.Context,
	action string,
	env store.Environment,
	spec provisioner.Spec,
	logs io.Writer,
) (actionResult, error) {
	switch action {
	case store.ActionPreview:
		result, err := o.prov.Preview(ctx, spec, logs)
		if err != nil {
			return actionResult{}, err
		}
		return actionResult{
			environmentStatus: store.EnvPreviewReady,
			summary:           previewSummary(result),
		}, nil
	case store.ActionDestroyPreview:
		result, err := o.prov.PreviewDestroy(ctx, spec, logs)
		if err != nil {
			return actionResult{}, err
		}
		return actionResult{
			environmentStatus: store.EnvDestroyPreviewReady,
			summary:           previewSummary(result),
		}, nil
	case store.ActionRefresh:
		result, err := o.prov.Refresh(ctx, spec, logs)
		if err != nil {
			return actionResult{}, err
		}
		return actionResult{
			environmentStatus: store.EnvUp,
			summary:           previewSummary(result),
		}, nil
	case store.ActionUp:
		result, err := o.prov.Up(ctx, spec, logs)
		if err != nil {
			return actionResult{}, err
		}
		if err := o.syncRDSSecretMetadata(ctx, env, result.Outputs); err != nil {
			return actionResult{}, fmt.Errorf("sync RDS secret metadata: %w", err)
		}
		if err := o.syncRedisAuthSecretMetadata(ctx, env, result.Outputs); err != nil {
			return actionResult{}, fmt.Errorf("sync Redis secret metadata: %w", err)
		}
		return actionResult{
			environmentStatus: store.EnvUp,
			outputs:           result.Outputs,
		}, nil
	case store.ActionDestroy:
		if err := o.prov.Destroy(ctx, spec, logs); err != nil {
			return actionResult{}, err
		}
		return actionResult{
			environmentStatus: store.EnvDestroyed,
			clearResumeStatus: true,
		}, nil
	default:
		return actionResult{}, fmt.Errorf("%w: %q", ErrInvalidAction, action)
	}
}

func (o *Orchestrator) completeFailure(
	ctx context.Context,
	job store.Job,
	env store.Environment,
	logs io.Writer,
	cause error,
) (bool, error) {
	if _, err := fmt.Fprintf(logs, "ERROR: %v\n", cause); err != nil {
		cause = errors.Join(cause, fmt.Errorf("write terminal log: %w", err))
	}
	completion := store.JobCompletion{
		JobID:             job.ID,
		EnvironmentID:     env.ID,
		JobStatus:         store.JobFailed,
		EnvironmentStatus: store.EnvFailed,
		Logs:              o.broker.Snapshot(job.ID),
		Error:             cause.Error(),
	}
	if err := o.persistTerminal(ctx, func(completionCtx context.Context) error {
		return o.store.CompleteJob(completionCtx, completion)
	}); err != nil {
		return false, errors.Join(cause, fmt.Errorf("persist failed job %d: %w", job.ID, err))
	}
	return true, cause
}

func (o *Orchestrator) failUnknownAction(
	ctx context.Context,
	job store.Job,
	env store.Environment,
	logs io.Writer,
) (bool, error) {
	cause := fmt.Errorf("%w: persisted job %d has action %q", ErrInvalidAction, job.ID, job.Action)
	if _, err := fmt.Fprintf(logs, "ERROR: %v\n", cause); err != nil {
		cause = errors.Join(cause, fmt.Errorf("write terminal log: %w", err))
	}
	failure := store.StartedJobFailure{
		JobID:                     job.ID,
		EnvironmentID:             env.ID,
		ExpectedEnvironmentStatus: env.Status,
		Logs:                      o.broker.Snapshot(job.ID),
		Error:                     cause.Error(),
	}
	if err := o.persistTerminal(ctx, func(completionCtx context.Context) error {
		return o.store.FailStartedJob(completionCtx, failure)
	}); err != nil {
		return false, errors.Join(cause, fmt.Errorf("persist invalid job %d: %w", job.ID, err))
	}
	return true, cause
}

func (o *Orchestrator) recoverOrphans(ctx context.Context) error {
	orphans, err := o.store.ListOrphanJobs(ctx)
	if err != nil {
		return err
	}
	for _, job := range orphans {
		if err := o.store.FailOrphanJob(ctx, job.ID, "interrupted by restart", ""); err != nil {
			return fmt.Errorf("fail orphan job %d: %w", job.ID, err)
		}
	}
	return nil
}

func (o *Orchestrator) persistTerminal(ctx context.Context, persist func(context.Context) error) error {
	err := retryTerminalPersistence(ctx, persist)
	if err != nil {
		o.markDegraded(ctx)
	}
	return err
}

func retryTerminalPersistence(ctx context.Context, persist func(context.Context) error) error {
	var lastErr error
	for attempt := 0; attempt < terminalPersistenceAttempts; attempt++ {
		completionCtx, cancel := terminalContext(ctx)
		lastErr = persist(completionCtx)
		cancel()
		if lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("terminal persistence failed after %d attempts: %w", terminalPersistenceAttempts, lastErr)
}

func (o *Orchestrator) markDegraded(ctx context.Context) {
	o.admissionMu.Lock()
	cancel, transitioned := o.transitionToDegradedLocked()
	o.admissionMu.Unlock()
	if !transitioned {
		return
	}
	o.cancelAndDrain(ctx, cancel)
}

func (o *Orchestrator) transitionToDegradedLocked() (context.CancelFunc, bool) {
	if !o.degraded.CompareAndSwap(false, true) {
		return nil, false
	}
	return o.cancel, true
}

func (o *Orchestrator) cancelAndDrain(ctx context.Context, cancel context.CancelFunc) {
	if cancel != nil {
		cancel()
	}
	o.reconcileQueuedJobs(ctx)
}

func (o *Orchestrator) reconcileQueuedJobs(ctx context.Context) {
	for {
		select {
		case jobID := <-o.queue:
			o.releaseQueueSlot()
			if err := o.reconcileQueuedJob(ctx, jobID); err != nil {
				log.Printf("reconcile queued job %d: %v", jobID, err)
			}
		default:
			return
		}
	}
}

func (o *Orchestrator) reconcileQueuedJob(ctx context.Context, jobID int64) error {
	const message = "orchestrator degraded before queued job could start"
	logs := "ERROR: " + message + "\n"
	writer := o.broker.Writer(jobID)
	if _, err := io.WriteString(writer, logs); err == nil {
		logs = o.broker.Snapshot(jobID)
	}

	err := retryTerminalPersistence(ctx, func(completionCtx context.Context) error {
		return o.store.FailOrphanJob(completionCtx, jobID, message, logs)
	})
	if err != nil {
		o.broker.Seal(jobID)
		return fmt.Errorf("persist queued job reconciliation: %w", err)
	}
	o.broker.Close(jobID)
	return nil
}

func terminalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), terminalPersistenceTimeout)
}

func previewSummary(result provisioner.PreviewResult) map[string]any {
	return map[string]any{
		"creates": result.Creates,
		"updates": result.Updates,
		"deletes": result.Deletes,
		"sames":   result.Sames,
	}
}
