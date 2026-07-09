package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

type Blueprint struct {
	ID             int64
	ProjectID      int64
	CloudAccountID int64
	Name           string
	Params         provisioner.BlueprintParams
	CreatedAt      time.Time
}

func (s *Store) CreateBlueprint(ctx context.Context, b Blueprint) (int64, error) {
	b.Params.ApplyDefaults()
	raw, err := json.Marshal(b.Params)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO blueprints (project_id, name, cloud_account_id, params_json)
		 VALUES (?, ?, ?, ?)`,
		b.ProjectID, b.Name, b.CloudAccountID, string(raw))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetBlueprint(ctx context.Context, id int64) (Blueprint, error) {
	var b Blueprint
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, name, cloud_account_id, params_json, created_at
		 FROM blueprints WHERE id = ?`, id,
	).Scan(&b.ID, &b.ProjectID, &b.Name, &b.CloudAccountID, &raw, &b.CreatedAt)
	if err != nil {
		return Blueprint{}, err
	}
	if err := json.Unmarshal([]byte(raw), &b.Params); err != nil {
		return Blueprint{}, err
	}
	b.Params.ApplyDefaults()
	return b, nil
}

func (s *Store) ListBlueprints(ctx context.Context) ([]Blueprint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, name, cloud_account_id, params_json, created_at
		 FROM blueprints ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Blueprint
	for rows.Next() {
		var b Blueprint
		var raw string
		if err := rows.Scan(&b.ID, &b.ProjectID, &b.Name, &b.CloudAccountID, &raw, &b.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &b.Params); err != nil {
			return nil, err
		}
		b.Params.ApplyDefaults()
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) DeleteBlueprint(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM blueprints WHERE id = ?`, id)
	return err
}
