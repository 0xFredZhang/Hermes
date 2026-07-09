package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

const (
	EnvPending      = "pending"
	EnvPreviewing   = "previewing"
	EnvPreviewReady = "preview_ready"
	EnvProvisioning = "provisioning"
	EnvUp           = "up"
	EnvFailed       = "failed"
	EnvDestroying   = "destroying"
	EnvDestroyed    = "destroyed"
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
		 (blueprint_id, cloud_account_id, name, pulumi_stack, region, blueprint_snapshot_json, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.BlueprintID, e.CloudAccountID, e.Name, e.PulumiStack, e.Region, string(snap), e.Status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetEnvironment(ctx context.Context, id int64) (Environment, error) {
	var e Environment
	var snap, outputs string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, blueprint_id, cloud_account_id, name, pulumi_stack, region,
		        blueprint_snapshot_json, status, outputs_json, created_at, updated_at
		 FROM environments WHERE id = ?`, id,
	).Scan(&e.ID, &e.BlueprintID, &e.CloudAccountID, &e.Name, &e.PulumiStack, &e.Region,
		&snap, &e.Status, &outputs, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
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

func (s *Store) ListEnvironments(ctx context.Context) ([]Environment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, blueprint_id, cloud_account_id, name, pulumi_stack, region,
		        blueprint_snapshot_json, status, outputs_json, created_at, updated_at
		 FROM environments ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Environment
	for rows.Next() {
		var e Environment
		var snap, outputs string
		if err := rows.Scan(&e.ID, &e.BlueprintID, &e.CloudAccountID, &e.Name, &e.PulumiStack,
			&e.Region, &snap, &e.Status, &outputs, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(snap), &e.Snapshot)
		e.Snapshot.ApplyDefaults()
		if outputs != "" {
			_ = json.Unmarshal([]byte(outputs), &e.Outputs)
		}
		out = append(out, e)
	}
	return out, rows.Err()
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
