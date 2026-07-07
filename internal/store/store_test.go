package store

import (
	"testing"

	"github.com/0xFredZhang/Hermes/internal/crypto"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	c, err := crypto.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	s, err := Open(":memory:", c)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenRunsMigrations(t *testing.T) {
	s := newTestStore(t)

	var name string
	err := s.DB().QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='cloud_accounts'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("cloud_accounts table not found: %v", err)
	}
	if name != "cloud_accounts" {
		t.Fatalf("got table %q", name)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	c, _ := crypto.NewCipher(make([]byte, 32))
	// A shared file: reopen should not error re-running migrations.
	path := t.TempDir() + "/h.db"
	s1, err := Open(path, c)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_ = s1.Close()
	s2, err := Open(path, c)
	if err != nil {
		t.Fatalf("second Open (idempotent migrate): %v", err)
	}
	_ = s2.Close()
}
