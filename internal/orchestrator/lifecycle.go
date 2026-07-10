package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/0xFredZhang/Hermes/internal/store"
)

var (
	ErrEnvironmentBusy      = errors.New("environment already has an active job")
	ErrInvalidAction        = errors.New("unknown environment action")
	ErrInvalidTransition    = errors.New("action is not allowed in the current environment state")
	ErrNoFailedJob          = errors.New("environment has no failed job to retry")
	ErrOrchestratorDegraded = errors.New("orchestrator is degraded after a terminal persistence failure")
)

type actionRule struct {
	allowedFrom         []string
	transientStatus     string
	captureResumeStatus bool
}

var actionPolicy = map[string]actionRule{
	store.ActionPreview: {
		allowedFrom:     []string{store.EnvPending},
		transientStatus: store.EnvPreviewing,
	},
	store.ActionUp: {
		allowedFrom:     []string{store.EnvPreviewReady},
		transientStatus: store.EnvProvisioning,
	},
	store.ActionRefresh: {
		allowedFrom:     []string{store.EnvUp},
		transientStatus: store.EnvRefreshing,
	},
	store.ActionDestroyPreview: {
		allowedFrom: []string{
			store.EnvPreviewReady,
			store.EnvUp,
			store.EnvFailed,
		},
		transientStatus:     store.EnvDestroyPreviewing,
		captureResumeStatus: true,
	},
	store.ActionDestroy: {
		allowedFrom:     []string{store.EnvDestroyPreviewReady},
		transientStatus: store.EnvDestroying,
	},
}

// Enqueue validates the public lifecycle contract and atomically queues the
// action while moving its environment into the action's transient state.
func (o *Orchestrator) Enqueue(ctx context.Context, environmentID int64, action string) (int64, error) {
	if err := o.healthError(); err != nil {
		return 0, err
	}
	rule, ok := actionPolicy[action]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAction, action)
	}
	return o.enqueueTransition(ctx, environmentID, action, rule)
}

// Retry requeues the most recent failed action for a failed environment.
func (o *Orchestrator) Retry(ctx context.Context, environmentID int64) (int64, error) {
	if err := o.healthError(); err != nil {
		return 0, err
	}
	env, err := o.store.GetEnvironment(ctx, environmentID)
	if err != nil {
		return 0, mapLifecycleStoreError(err)
	}
	if env.Status != store.EnvFailed {
		return 0, fmt.Errorf("%w: environment %d is %q", ErrInvalidTransition, environmentID, env.Status)
	}

	failed, err := o.store.GetLatestFailedJob(ctx, environmentID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: environment %d", ErrNoFailedJob, environmentID)
	}
	if err != nil {
		return 0, err
	}
	rule, ok := actionPolicy[failed.Action]
	if !ok {
		return 0, fmt.Errorf("%w: failed job %d has action %q", ErrInvalidAction, failed.ID, failed.Action)
	}
	rule.allowedFrom = []string{store.EnvFailed}
	return o.enqueueTransition(ctx, environmentID, failed.Action, rule)
}

// CancelDestroyPreview atomically restores the state captured when the
// destroy preview began.
func (o *Orchestrator) CancelDestroyPreview(ctx context.Context, environmentID int64) error {
	return mapLifecycleStoreError(o.store.CancelDestroyPreview(ctx, environmentID))
}

func (o *Orchestrator) enqueueTransition(
	ctx context.Context,
	environmentID int64,
	action string,
	rule actionRule,
) (int64, error) {
	if err := o.healthError(); err != nil {
		return 0, err
	}
	if err := o.reserveQueueSlot(ctx); err != nil {
		return 0, err
	}
	slotReserved := true
	defer func() {
		if slotReserved {
			o.releaseQueueSlot()
		}
	}()

	o.admissionMu.Lock()
	if err := o.healthError(); err != nil {
		o.admissionMu.Unlock()
		return 0, err
	}
	job, err := o.store.EnqueueJobTransition(ctx, store.EnqueueTransition{
		EnvironmentID:       environmentID,
		Action:              action,
		AllowedFrom:         rule.allowedFrom,
		TransientStatus:     rule.transientStatus,
		CaptureResumeStatus: rule.captureResumeStatus,
	})
	if err != nil {
		o.admissionMu.Unlock()
		return 0, mapLifecycleStoreError(err)
	}
	// The slot was reserved before taking admissionMu, so publication cannot
	// block while the mutex closes the commit-to-publish race with degradation.
	select {
	case o.queue <- job.ID:
		slotReserved = false
		o.admissionMu.Unlock()
	default:
		o.admissionMu.Unlock()
		err := errors.New("orchestrator queue slot invariant violated")
		o.markDegraded(ctx)
		if reconcileErr := o.reconcileQueuedJob(ctx, job.ID); reconcileErr != nil {
			return 0, errors.Join(err, reconcileErr)
		}
		return 0, err
	}
	return job.ID, nil
}

func (o *Orchestrator) reserveQueueSlot(ctx context.Context) error {
	select {
	case o.queueSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *Orchestrator) releaseQueueSlot() {
	select {
	case <-o.queueSlots:
	default:
	}
}

func (o *Orchestrator) healthError() error {
	if o.degraded.Load() {
		return ErrOrchestratorDegraded
	}
	return nil
}

func mapLifecycleStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrActiveJob):
		return fmt.Errorf("%w: %w", ErrEnvironmentBusy, err)
	case errors.Is(err, store.ErrStaleTransition), errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %w", ErrInvalidTransition, err)
	case errors.Is(err, store.ErrInvalidAction):
		return fmt.Errorf("%w: %w", ErrInvalidAction, err)
	default:
		return err
	}
}
