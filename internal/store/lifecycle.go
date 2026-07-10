package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrStaleTransition   = errors.New("stale lifecycle transition")
	ErrJobNotQueued      = errors.New("job is not queued")
	ErrActiveJob         = errors.New("environment has an active job")
	ErrInvalidAction     = errors.New("invalid lifecycle action")
	ErrInvalidCompletion = errors.New("invalid lifecycle completion")
)

type EnqueueTransition struct {
	EnvironmentID       int64
	Action              string
	AllowedFrom         []string
	TransientStatus     string
	CaptureResumeStatus bool
}

type JobCompletion struct {
	JobID             int64
	EnvironmentID     int64
	JobStatus         string
	EnvironmentStatus string
	Logs              string
	Error             string
	Summary           map[string]any
	Outputs           map[string]any
	ClearResumeStatus bool
}

// StartedJobFailure is the narrow terminal transition used when a worker has
// claimed a persisted job whose action is not part of the lifecycle contract.
type StartedJobFailure struct {
	JobID                     int64
	EnvironmentID             int64
	ExpectedEnvironmentStatus string
	Logs                      string
	Error                     string
}

// EnqueueJobTransition creates the queued job and moves its environment to the
// action's transient state in one transaction. The partial unique index on
// active jobs is the final concurrency guard; a conflict rolls back both rows.
func (s *Store) EnqueueJobTransition(ctx context.Context, in EnqueueTransition) (Job, error) {
	if in.EnvironmentID == 0 || in.TransientStatus == "" || len(in.AllowedFrom) == 0 {
		return Job{}, errors.New("enqueue transition requires environment, allowed states, and transient state")
	}
	expected, ok := transientStatusForAction(in.Action)
	if !ok {
		return Job{}, fmt.Errorf("%w: %q", ErrInvalidAction, in.Action)
	}
	if expected != in.TransientStatus {
		return Job{}, fmt.Errorf("%w: action %q requires transient status %q", ErrInvalidAction, in.Action, expected)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var currentStatus, resumeStatus string
	if err := tx.QueryRowContext(ctx,
		`SELECT status, resume_status FROM environments WHERE id = ?`, in.EnvironmentID,
	).Scan(&currentStatus, &resumeStatus); err != nil {
		return Job{}, err
	}
	if !containsStatus(in.AllowedFrom, currentStatus) {
		return Job{}, fmt.Errorf("%w: environment %d is %q", ErrStaleTransition, in.EnvironmentID, currentStatus)
	}
	if in.CaptureResumeStatus && resumeStatus == "" && isResumeCaptureStatus(currentStatus) {
		resumeStatus = currentStatus
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE environments
		 SET status = ?, resume_status = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ?`,
		in.TransientStatus, resumeStatus, in.EnvironmentID, currentStatus,
	)
	if err != nil {
		return Job{}, err
	}
	if ok, err := changedExactlyOne(res); err != nil {
		return Job{}, err
	} else if !ok {
		return Job{}, fmt.Errorf("%w: environment %d changed while enqueueing", ErrStaleTransition, in.EnvironmentID)
	}

	res, err = tx.ExecContext(ctx,
		`INSERT INTO jobs (environment_id, action, status) VALUES (?, ?, ?)`,
		in.EnvironmentID, in.Action, JobQueued,
	)
	if err != nil {
		if isActiveJobConstraint(err) {
			return Job{}, fmt.Errorf("%w: environment %d", ErrActiveJob, in.EnvironmentID)
		}
		return Job{}, err
	}
	jobID, err := res.LastInsertId()
	if err != nil {
		return Job{}, err
	}
	job, err := scanJob(tx.QueryRowContext(ctx,
		`SELECT `+jobCols+` FROM jobs WHERE id = ?`, jobID))
	if err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

// StartJob claims a queued job. Returning the job and environment from the
// same transaction gives workers a consistent lifecycle snapshot.
func (s *Store) StartJob(ctx context.Context, jobID int64) (Job, Environment, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, Environment{}, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = ?, started_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ?`,
		JobRunning, jobID, JobQueued,
	)
	if err != nil {
		return Job{}, Environment{}, err
	}
	if ok, err := changedExactlyOne(res); err != nil {
		return Job{}, Environment{}, err
	} else if !ok {
		return Job{}, Environment{}, fmt.Errorf("%w: job %d", ErrJobNotQueued, jobID)
	}

	job, err := scanJob(tx.QueryRowContext(ctx,
		`SELECT `+jobCols+` FROM jobs WHERE id = ?`, jobID))
	if err != nil {
		return Job{}, Environment{}, err
	}
	env, err := scanEnvironment(tx.QueryRowContext(ctx,
		`SELECT `+environmentCols+` FROM environments WHERE id = ?`, job.EnvironmentID))
	if err != nil {
		return Job{}, Environment{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, Environment{}, err
	}
	return job, env, nil
}

// CompleteJob commits terminal job data and the corresponding environment
// state together. Nil summary or outputs mean "leave the stored JSON alone".
func (s *Store) CompleteJob(ctx context.Context, in JobCompletion) error {
	if in.JobStatus != JobSucceeded && in.JobStatus != JobFailed {
		return fmt.Errorf("%w: job status %q is not terminal", ErrInvalidCompletion, in.JobStatus)
	}
	if in.JobID == 0 || in.EnvironmentID == 0 || in.EnvironmentStatus == "" {
		return fmt.Errorf("%w: job, environment, and environment status are required", ErrInvalidCompletion)
	}
	summaryJSON, hasSummary, err := marshalOptionalJSON(in.Summary)
	if err != nil {
		return fmt.Errorf("marshal job summary: %w", err)
	}
	outputsJSON, hasOutputs, err := marshalOptionalJSON(in.Outputs)
	if err != nil {
		return fmt.Errorf("marshal environment outputs: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var environmentID int64
	var action, jobStatus string
	if err := tx.QueryRowContext(ctx,
		`SELECT environment_id, action, status FROM jobs WHERE id = ?`, in.JobID,
	).Scan(&environmentID, &action, &jobStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: job %d does not exist", ErrStaleTransition, in.JobID)
		}
		return err
	}
	if environmentID != in.EnvironmentID || jobStatus != JobRunning {
		return fmt.Errorf("%w: job %d no longer matches a running completion", ErrStaleTransition, in.JobID)
	}
	expectedEnvironmentStatus, ok := transientStatusForAction(action)
	if !ok {
		return fmt.Errorf("%w: job %d has unsupported action %q", ErrInvalidCompletion, in.JobID, action)
	}
	wantedEnvironmentStatus, ok := completedEnvironmentStatus(action, in.JobStatus)
	if !ok || in.EnvironmentStatus != wantedEnvironmentStatus {
		return fmt.Errorf(
			"%w: action %q with job status %q requires environment status %q, got %q",
			ErrInvalidCompletion, action, in.JobStatus, wantedEnvironmentStatus, in.EnvironmentStatus,
		)
	}

	environmentQuery := `UPDATE environments SET status = ?, updated_at = CURRENT_TIMESTAMP`
	environmentArgs := []any{in.EnvironmentStatus}
	if hasOutputs {
		environmentQuery += `, outputs_json = ?`
		environmentArgs = append(environmentArgs, outputsJSON)
	}
	if in.ClearResumeStatus {
		environmentQuery += `, resume_status = ''`
	}
	environmentQuery += ` WHERE id = ? AND status = ?`
	environmentArgs = append(environmentArgs, in.EnvironmentID, expectedEnvironmentStatus)
	res, err := tx.ExecContext(ctx, environmentQuery, environmentArgs...)
	if err != nil {
		return err
	}
	if ok, err := changedExactlyOne(res); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: environment %d is no longer %q", ErrStaleTransition, in.EnvironmentID, expectedEnvironmentStatus)
	}

	jobQuery := `UPDATE jobs
		SET status = ?, logs = ?, error = ?, finished_at = CURRENT_TIMESTAMP`
	jobArgs := []any{in.JobStatus, in.Logs, in.Error}
	if hasSummary {
		jobQuery += `, summary_json = ?`
		jobArgs = append(jobArgs, summaryJSON)
	}
	jobQuery += ` WHERE id = ? AND environment_id = ? AND status = ?`
	jobArgs = append(jobArgs, in.JobID, in.EnvironmentID, JobRunning)
	res, err = tx.ExecContext(ctx, jobQuery, jobArgs...)
	if err != nil {
		return err
	}
	if ok, err := changedExactlyOne(res); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: job %d changed while completing", ErrStaleTransition, in.JobID)
	}

	return tx.Commit()
}

// FailStartedJob atomically fails a running job and its environment without
// interpreting the job action. Normal known actions must use CompleteJob.
func (s *Store) FailStartedJob(ctx context.Context, in StartedJobFailure) error {
	if in.JobID == 0 || in.EnvironmentID == 0 || in.ExpectedEnvironmentStatus == "" || strings.TrimSpace(in.Error) == "" {
		return fmt.Errorf("%w: started job failure requires job, environment, expected state, and error", ErrInvalidCompletion)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`UPDATE environments SET status = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ?`,
		EnvFailed, in.EnvironmentID, in.ExpectedEnvironmentStatus,
	)
	if err != nil {
		return err
	}
	if ok, err := changedExactlyOne(res); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: environment %d is no longer %q", ErrStaleTransition, in.EnvironmentID, in.ExpectedEnvironmentStatus)
	}

	res, err = tx.ExecContext(ctx,
		`UPDATE jobs
		 SET status = ?, logs = ?, error = ?, finished_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND environment_id = ? AND status = ?`,
		JobFailed, in.Logs, in.Error, in.JobID, in.EnvironmentID, JobRunning,
	)
	if err != nil {
		return err
	}
	if ok, err := changedExactlyOne(res); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: job %d no longer matches a running failure", ErrStaleTransition, in.JobID)
	}

	return tx.Commit()
}

// CancelDestroyPreview restores the stable state captured when destroy preview
// started. Its conditional update races safely with a destroy enqueue: exactly
// one can move the environment out of destroy_preview_ready.
func (s *Store) CancelDestroyPreview(ctx context.Context, environmentID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`UPDATE environments
		 SET status = resume_status, resume_status = '', updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ? AND resume_status <> ''
		   AND NOT EXISTS (
		       SELECT 1 FROM jobs
		       WHERE environment_id = environments.id AND action = ?
		         AND status IN (?, ?)
		   )`,
		environmentID, EnvDestroyPreviewReady, ActionDestroy, JobQueued, JobRunning,
	)
	if err != nil {
		return err
	}
	if ok, err := changedExactlyOne(res); err != nil {
		return err
	} else if !ok {
		var active int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM jobs
			 WHERE environment_id = ? AND action = ? AND status IN (?, ?)`,
			environmentID, ActionDestroy, JobQueued, JobRunning,
		).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return fmt.Errorf("%w: environment %d has a destroy job", ErrActiveJob, environmentID)
		}
		return fmt.Errorf("%w: environment %d is not cancelable", ErrStaleTransition, environmentID)
	}
	return tx.Commit()
}

// FailOrphanJob terminates a queued or running job after a process restart and
// fails its environment only when the environment is still in that action's
// transient state. An empty logs argument preserves already-persisted logs.
func (s *Store) FailOrphanJob(ctx context.Context, jobID int64, message, logs string) error {
	if strings.TrimSpace(message) == "" {
		message = "interrupted before completion"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var environmentID int64
	var action, status string
	if err := tx.QueryRowContext(ctx,
		`SELECT environment_id, action, status FROM jobs WHERE id = ?`, jobID,
	).Scan(&environmentID, &action, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: orphan job %d does not exist", ErrStaleTransition, jobID)
		}
		return err
	}
	if status != JobQueued && status != JobRunning {
		return fmt.Errorf("%w: orphan job %d is %q", ErrStaleTransition, jobID, status)
	}

	jobQuery := `UPDATE jobs SET status = ?, error = ?, finished_at = CURRENT_TIMESTAMP`
	jobArgs := []any{JobFailed, message}
	if logs != "" {
		jobQuery += `, logs = ?`
		jobArgs = append(jobArgs, logs)
	}
	jobQuery += ` WHERE id = ? AND status IN (?, ?)`
	jobArgs = append(jobArgs, jobID, JobQueued, JobRunning)
	res, err := tx.ExecContext(ctx, jobQuery, jobArgs...)
	if err != nil {
		return err
	}
	if ok, err := changedExactlyOne(res); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: orphan job %d changed while failing", ErrStaleTransition, jobID)
	}

	transientStatus, ok := transientStatusForAction(action)
	if !ok {
		return fmt.Errorf("%w: orphan job %d has unsupported action %q", ErrStaleTransition, jobID, action)
	}
	res, err = tx.ExecContext(ctx,
		`UPDATE environments SET status = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ?`,
		EnvFailed, environmentID, transientStatus,
	)
	if err != nil {
		return err
	}
	if ok, err := changedExactlyOne(res); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: environment %d is no longer %q", ErrStaleTransition, environmentID, transientStatus)
	}
	return tx.Commit()
}

func containsStatus(statuses []string, want string) bool {
	for _, status := range statuses {
		if status == want {
			return true
		}
	}
	return false
}

func isResumeCaptureStatus(status string) bool {
	switch status {
	case EnvPending, EnvPreviewReady, EnvUp, EnvFailed:
		return true
	default:
		return false
	}
}

func transientStatusForAction(action string) (string, bool) {
	switch action {
	case ActionPreview:
		return EnvPreviewing, true
	case ActionDestroyPreview:
		return EnvDestroyPreviewing, true
	case ActionUp:
		return EnvProvisioning, true
	case ActionRefresh:
		return EnvRefreshing, true
	case ActionDestroy:
		return EnvDestroying, true
	default:
		return "", false
	}
}

func completedEnvironmentStatus(action, jobStatus string) (string, bool) {
	if _, ok := transientStatusForAction(action); !ok {
		return "", false
	}
	if jobStatus == JobFailed {
		return EnvFailed, true
	}
	if jobStatus != JobSucceeded {
		return "", false
	}
	switch action {
	case ActionPreview:
		return EnvPreviewReady, true
	case ActionDestroyPreview:
		return EnvDestroyPreviewReady, true
	case ActionUp, ActionRefresh:
		return EnvUp, true
	case ActionDestroy:
		return EnvDestroyed, true
	default:
		return "", false
	}
}

func changedExactlyOne(result sql.Result) (bool, error) {
	n, err := result.RowsAffected()
	return n == 1, err
}

func marshalOptionalJSON(value map[string]any) (string, bool, error) {
	if value == nil {
		return "", false, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", false, err
	}
	return string(raw), true, nil
}

func isActiveJobConstraint(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint failed") &&
		strings.Contains(message, "jobs.environment_id")
}
