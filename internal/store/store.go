package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"

	"github.com/0xFredZhang/Hermes/internal/crypto"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db     *sql.DB
	cipher crypto.Cipher
}

func Open(dbPath string, c crypto.Cipher) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	// SQLite has a single writer; cap the pool at one connection. This also
	// keeps a ":memory:" database on one connection so migrations and queries
	// share the same schema (relied on by tests).
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db, cipher: c}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(
		`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`,
	); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := applyMigration(context.Background(), s.db, name, body); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}

// applyMigration keeps the migration body and its version marker in the same
// transaction. A failed statement therefore leaves neither schema changes nor
// a misleading schema_migrations row behind.
func applyMigration(ctx context.Context, db *sql.DB, name string, body []byte) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var exists string
	err = tx.QueryRowContext(ctx,
		`SELECT version FROM schema_migrations WHERE version = ?`, name,
	).Scan(&exists)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version) VALUES (?)`, name,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DB() *sql.DB  { return s.db }
func (s *Store) Close() error { return s.db.Close() }
