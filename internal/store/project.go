package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Project struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
}

// ErrProjectReferenced is returned when a blueprint still belongs to the
// project and the database refuses its deletion.
var ErrProjectReferenced = errors.New("project is referenced")

func (s *Store) CreateProject(ctx context.Context, p Project) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (name, description) VALUES (?, ?)`,
		p.Name, p.Description)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetProject(ctx context.Context, id int64) (Project, error) {
	var p Project
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, description, created_at FROM projects WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt)
	return p, err
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, description, created_at FROM projects ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeleteProject(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		if isSQLiteForeignKeyConstraint(err) {
			return fmt.Errorf("%w: %v", ErrProjectReferenced, err)
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
