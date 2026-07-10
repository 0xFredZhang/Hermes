package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"

	ActionPreview        = "preview"
	ActionUp             = "up"
	ActionRefresh        = "refresh"
	ActionDestroyPreview = "destroy_preview"
	ActionDestroy        = "destroy"
)

type Job struct {
	ID            int64
	EnvironmentID int64
	Action        string
	Status        string
	Logs          string
	Error         string
	Summary       map[string]any
	StartedAt     sql.NullTime
	FinishedAt    sql.NullTime
	CreatedAt     time.Time
}

func (s *Store) CreateJob(ctx context.Context, j Job) (int64, error) {
	if j.Status == "" {
		j.Status = JobQueued
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (environment_id, action, status) VALUES (?, ?, ?)`,
		j.EnvironmentID, j.Action, j.Status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func scanJob(sc interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var summary string
	if err := sc.Scan(&j.ID, &j.EnvironmentID, &j.Action, &j.Status, &j.Logs, &summary,
		&j.Error, &j.StartedAt, &j.FinishedAt, &j.CreatedAt); err != nil {
		return Job{}, err
	}
	if summary != "" {
		if err := json.Unmarshal([]byte(summary), &j.Summary); err != nil {
			return Job{}, err
		}
	}
	return j, nil
}

const jobCols = `id, environment_id, action, status, logs, summary_json, error, started_at, finished_at, created_at`

func (s *Store) GetJob(ctx context.Context, id int64) (Job, error) {
	return scanJob(s.db.QueryRowContext(ctx,
		`SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
}

func (s *Store) GetLatestFailedJob(ctx context.Context, environmentID int64) (Job, error) {
	var job Job
	err := s.db.QueryRowContext(ctx,
		`SELECT id, environment_id, action, status FROM jobs
		 WHERE environment_id = ? AND status = ?
		 ORDER BY id DESC LIMIT 1`,
		environmentID, JobFailed,
	).Scan(&job.ID, &job.EnvironmentID, &job.Action, &job.Status)
	return job, err
}

func (s *Store) ListJobsByEnvironment(ctx context.Context, envID int64) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+jobCols+` FROM jobs WHERE environment_id = ? ORDER BY id DESC`, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) UpdateJobStatus(ctx context.Context, id int64, status string) error {
	var q string
	switch status {
	case JobRunning:
		q = `UPDATE jobs SET status = ?, started_at = CURRENT_TIMESTAMP WHERE id = ?`
	case JobSucceeded, JobFailed:
		q = `UPDATE jobs SET status = ?, finished_at = CURRENT_TIMESTAMP WHERE id = ?`
	default:
		q = `UPDATE jobs SET status = ? WHERE id = ?`
	}
	_, err := s.db.ExecContext(ctx, q, status, id)
	return err
}

func (s *Store) SetJobLogs(ctx context.Context, id int64, logs string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET logs = ? WHERE id = ?`, logs, id)
	return err
}

func (s *Store) SetJobSummary(ctx context.Context, id int64, summary map[string]any) error {
	raw, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE jobs SET summary_json = ? WHERE id = ?`, string(raw), id)
	return err
}

func (s *Store) SetJobError(ctx context.Context, id int64, msg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET error = ? WHERE id = ?`, msg, id)
	return err
}

func (s *Store) HasActiveJob(ctx context.Context, envID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE environment_id = ? AND status IN ('queued','running')`,
		envID).Scan(&n)
	return n > 0, err
}

func (s *Store) ListOrphanJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+jobCols+` FROM jobs WHERE status IN ('queued','running') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
