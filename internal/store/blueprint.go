package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	ErrBlueprintReferenced       = errors.New("blueprint is referenced by an environment")
	ErrBlueprintOwnershipInvalid = errors.New("blueprint project or cloud account does not exist")
)

type Blueprint struct {
	ID             int64
	ProjectID      int64
	CloudAccountID int64
	Name           string
	Params         provisioner.BlueprintParams
	CreatedAt      time.Time
}

func (s *Store) UpdateBlueprint(ctx context.Context, b Blueprint) error {
	b.Params.ApplyDefaults()
	raw, err := json.Marshal(b.Params)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE blueprints
		 SET project_id = ?, name = ?, cloud_account_id = ?, params_json = ?
		 WHERE id = ?`,
		b.ProjectID, b.Name, b.CloudAccountID, string(raw), b.ID)
	if err != nil {
		if isSQLiteForeignKeyConstraint(err) {
			return ErrBlueprintOwnershipInvalid
		}
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
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
		if isSQLiteForeignKeyConstraint(err) {
			return 0, ErrBlueprintOwnershipInvalid
		}
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
	res, err := s.db.ExecContext(ctx, `DELETE FROM blueprints WHERE id = ?`, id)
	if err != nil {
		if isSQLiteForeignKeyConstraint(err) {
			return fmt.Errorf("%w: %v", ErrBlueprintReferenced, err)
		}
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func isSQLiteForeignKeyConstraint(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && (sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY ||
		sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_TRIGGER && strings.Contains(sqliteErr.Error(), "FOREIGN KEY constraint failed"))
}
