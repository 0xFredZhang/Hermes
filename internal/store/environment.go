package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

const (
	EnvPending             = "pending"
	EnvPreviewing          = "previewing"
	EnvDestroyPreviewing   = "destroy_previewing"
	EnvPreviewReady        = "preview_ready"
	EnvProvisioning        = "provisioning"
	EnvUp                  = "up"
	EnvRefreshing          = "refreshing"
	EnvDestroyPreviewReady = "destroy_preview_ready"
	EnvFailed              = "failed"
	EnvDestroying          = "destroying"
	EnvDestroyed           = "destroyed"
)

type Environment struct {
	ID             int64
	BlueprintID    int64
	CloudAccountID int64
	Name           string
	PulumiStack    string
	Region         string
	Snapshot       provisioner.BlueprintParams
	Status         string
	ResumeStatus   string
	Outputs        map[string]any
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (s *Store) CreateEnvironment(ctx context.Context, e Environment) (int64, error) {
	e.Snapshot.ApplyDefaults()
	snap, err := json.Marshal(e.Snapshot)
	if err != nil {
		return 0, err
	}
	if e.Status == "" {
		e.Status = EnvPending
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO environments
		 (blueprint_id, cloud_account_id, name, pulumi_stack, region, blueprint_snapshot_json, status, resume_status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.BlueprintID, e.CloudAccountID, e.Name, e.PulumiStack, e.Region, string(snap), e.Status, e.ResumeStatus)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeletePendingEnvironment compensates for a failed initial enqueue. The
// conditional delete cannot remove an environment after lifecycle work starts.
func (s *Store) DeletePendingEnvironment(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM environments
		 WHERE id = ? AND status = ?
		   AND NOT EXISTS (
		       SELECT 1 FROM jobs WHERE environment_id = environments.id
		   )`,
		id, EnvPending,
	)
	if err != nil {
		return fmt.Errorf("delete pending environment: %w", err)
	}
	if ok, err := changedExactlyOne(res); err != nil {
		return fmt.Errorf("delete pending environment: %w", err)
	} else if !ok {
		return fmt.Errorf("%w: environment %d is no longer an unused pending environment", ErrStaleTransition, id)
	}
	return nil
}

func (s *Store) GetEnvironment(ctx context.Context, id int64) (Environment, error) {
	return scanEnvironment(s.db.QueryRowContext(ctx,
		`SELECT `+environmentCols+` FROM environments WHERE id = ?`, id))
}

func (s *Store) ListEnvironments(ctx context.Context) ([]Environment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+environmentCols+` FROM environments ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Environment
	for rows.Next() {
		e, err := scanEnvironment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const environmentCols = `id, blueprint_id, cloud_account_id, name, pulumi_stack, region,
	blueprint_snapshot_json, status, resume_status, outputs_json, created_at, updated_at`

func scanEnvironment(sc interface{ Scan(...any) error }) (Environment, error) {
	var e Environment
	var snap, outputs string
	if err := sc.Scan(
		&e.ID, &e.BlueprintID, &e.CloudAccountID, &e.Name, &e.PulumiStack, &e.Region,
		&snap, &e.Status, &e.ResumeStatus, &outputs, &e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		return Environment{}, err
	}
	if err := json.Unmarshal([]byte(snap), &e.Snapshot); err != nil {
		return Environment{}, err
	}
	e.Snapshot.ApplyDefaults()
	if outputs != "" {
		if err := json.Unmarshal([]byte(outputs), &e.Outputs); err != nil {
			return Environment{}, err
		}
	}
	return e, nil
}

func (s *Store) UpdateEnvironmentStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE environments SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, id)
	return err
}

func (s *Store) SetEnvironmentOutputs(ctx context.Context, id int64, outputs map[string]any) error {
	raw, err := json.Marshal(outputs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE environments SET outputs_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		string(raw), id)
	return err
}
