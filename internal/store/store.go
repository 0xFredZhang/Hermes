package store

import (
	"database/sql"
	"embed"
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
		var exists string
		err := s.db.QueryRow(
			`SELECT version FROM schema_migrations WHERE version = ?`, name,
		).Scan(&exists)
		if err == nil {
			continue // already applied
		}
		if err != sql.ErrNoRows {
			return err
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := s.db.Exec(
			`INSERT INTO schema_migrations (version) VALUES (?)`, name,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DB() *sql.DB { return s.db }
func (s *Store) Close() error { return s.db.Close() }
