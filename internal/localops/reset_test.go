package localops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResetRequiresConfirmation(t *testing.T) {
	repo := newTestRepository(t)
	db := filepath.Join(repo, "hermes.db")
	writeSentinel(t, db)

	err := ResetLocal(ResetOptions{Repository: repo, DBPath: db})

	if !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("ResetLocal error = %v, want ErrConfirmationRequired", err)
	}
	assertExists(t, db)
}

func TestResetRejectsUnsafeDatabasePaths(t *testing.T) {
	repo := newTestRepository(t)
	directory := filepath.Join(repo, "database.db")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatalf("Mkdir database directory: %v", err)
	}

	tests := []struct {
		name string
		path string
	}{
		{name: "repository root", path: repo},
		{name: "directory", path: directory},
		{name: "memory database", path: ":memory:"},
		{name: "sqlite URI", path: "file:hermes.db?mode=memory"},
		{name: "empty path", path: ""},
		{name: "source-like target", path: filepath.Join(repo, "main.go")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ResetLocal(ResetOptions{Repository: repo, DBPath: tt.path, Confirm: true}); err == nil {
				t.Fatalf("ResetLocal(%q) error = nil, want rejection", tt.path)
			}
		})
	}
}

func TestResetRejectsPathOutsideRepository(t *testing.T) {
	repo := newTestRepository(t)
	outside := filepath.Join(t.TempDir(), "outside.db")
	writeSentinel(t, outside)

	err := ResetLocal(ResetOptions{Repository: repo, DBPath: outside, Confirm: true})

	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "repository") {
		t.Fatalf("ResetLocal error = %v, want repository containment error", err)
	}
	assertExists(t, outside)
}

func TestResetRejectsSymlinkEscapeBeforeDeletingAnything(t *testing.T) {
	repo := newTestRepository(t)
	db := filepath.Join(repo, "hermes.db")
	writeSentinel(t, db)
	outside := filepath.Join(t.TempDir(), "outside-wal")
	writeSentinel(t, outside)
	if err := os.Symlink(outside, db+"-wal"); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	err := ResetLocal(ResetOptions{Repository: repo, DBPath: db, Confirm: true})

	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "symbolic link") {
		t.Fatalf("ResetLocal error = %v, want symbolic-link rejection", err)
	}
	assertExists(t, db)
	assertExists(t, outside)
}

func TestResetRejectsAncestorSymlinkEscape(t *testing.T) {
	repo := newTestRepository(t)
	outside := t.TempDir()
	outsideDB := filepath.Join(outside, "hermes.db")
	writeSentinel(t, outsideDB)
	if err := os.Symlink(outside, filepath.Join(repo, "data")); err != nil {
		t.Fatalf("Symlink data directory: %v", err)
	}

	err := ResetLocal(ResetOptions{
		Repository: repo,
		DBPath:     filepath.Join(repo, "data", "hermes.db"),
		Confirm:    true,
	})

	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "symbolic link") {
		t.Fatalf("ResetLocal error = %v, want ancestor symbolic-link rejection", err)
	}
	assertExists(t, outsideDB)
}

func TestResetRemovesDatabaseSidecarsButPreservesState(t *testing.T) {
	repo := newTestRepository(t)
	db := filepath.Join(repo, "data", "hermes.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	removed := []string{db, db + "-wal", db + "-shm", db + "-journal"}
	for _, path := range removed {
		writeSentinel(t, path)
	}
	preserved := []string{
		db + ".backup",
		db + "-backup",
		filepath.Join(repo, "data", "pulumi-state", "state.json"),
		filepath.Join(repo, "README.md"),
	}
	for _, path := range preserved {
		writeSentinel(t, path)
	}

	if err := ResetLocal(ResetOptions{Repository: repo, DBPath: db, Confirm: true}); err != nil {
		t.Fatalf("ResetLocal: %v", err)
	}

	for _, path := range removed {
		assertNotExists(t, path)
	}
	for _, path := range preserved {
		assertExists(t, path)
	}
}

func TestResetResolvesRelativeDatabaseFromRepository(t *testing.T) {
	repo := newTestRepository(t)
	db := filepath.Join(repo, "data", "hermes.db")
	writeSentinel(t, db)

	err := ResetLocal(ResetOptions{Repository: repo, DBPath: "data/hermes.db", Confirm: true})

	if err != nil {
		t.Fatalf("ResetLocal relative database: %v", err)
	}
	assertNotExists(t, db)
}

func TestPurgeStateRejectsMalformedFileBackends(t *testing.T) {
	tests := []struct {
		name    string
		backend func(repo, state string) string
	}{
		{name: "host", backend: func(_, _ string) string { return "file://data/pulumi-state" }},
		{name: "relative", backend: func(_, _ string) string { return "file:data/pulumi-state" }},
		{name: "empty", backend: func(_, _ string) string { return "file://" }},
		{name: "query", backend: func(_, state string) string { return "file://" + state + "?mode=test" }},
		{name: "fragment", backend: func(_, state string) string { return "file://" + state + "#backup" }},
		{
			name: "encoded traversal",
			backend: func(repo, _ string) string {
				return "file://" + filepath.ToSlash(filepath.Join(repo, "data", "%2e%2e", "internal"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepository(t)
			state := filepath.Join(repo, "data", "pulumi-state")
			sentinel := filepath.Join(state, "metadata.json")
			writeSentinel(t, sentinel)

			err := PurgeLocalState(PurgeStateOptions{
				Repository: repo,
				Backend:    tt.backend(repo, state),
				Confirm:    true,
				Force:      true,
			})

			if err == nil {
				t.Fatal("PurgeLocalState error = nil, want malformed file backend rejection")
			}
			assertExists(t, sentinel)
		})
	}
}

func TestPurgeStateGuardsCannotBeForced(t *testing.T) {
	repo := newTestRepository(t)
	outside := filepath.Join(t.TempDir(), "pulumi-state")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("MkdirAll outside: %v", err)
	}
	dataSource := filepath.Join(repo, "data", "source")
	dataSourceSentinel := filepath.Join(dataSource, "main.go")
	writeSentinel(t, dataSourceSentinel)

	tests := []struct {
		name    string
		options PurgeStateOptions
	}{
		{
			name: "confirmation",
			options: PurgeStateOptions{
				Repository: repo,
				Backend:    "file://" + filepath.Join(repo, "data", "pulumi-state"),
				Force:      true,
			},
		},
		{
			name: "S3 backend",
			options: PurgeStateOptions{
				Repository: repo,
				Backend:    "s3://bucket/state",
				Confirm:    true,
				Force:      true,
			},
		},
		{
			name: "non-file backend",
			options: PurgeStateOptions{
				Repository: repo,
				Backend:    "https://example.com/state",
				Confirm:    true,
				Force:      true,
			},
		},
		{
			name: "outside repository",
			options: PurgeStateOptions{
				Repository: repo,
				Backend:    "file://" + outside,
				Confirm:    true,
				Force:      true,
			},
		},
		{
			name: "repository root",
			options: PurgeStateOptions{
				Repository: repo,
				Backend:    "file://" + repo,
				Confirm:    true,
				Force:      true,
			},
		},
		{
			name: "source directory",
			options: PurgeStateOptions{
				Repository: repo,
				Backend:    "file://" + filepath.Join(repo, "internal"),
				Confirm:    true,
				Force:      true,
			},
		},
		{
			name: "source directory under data",
			options: PurgeStateOptions{
				Repository: repo,
				Backend:    "file://" + dataSource,
				Confirm:    true,
				Force:      true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := PurgeLocalState(tt.options); err == nil {
				t.Fatalf("PurgeLocalState(%+v) error = nil, want rejection", tt.options)
			}
			assertExists(t, dataSourceSentinel)
		})
	}
}

func TestPurgeStateRejectsSymlinkEscape(t *testing.T) {
	repo := newTestRepository(t)
	dataDir := filepath.Join(repo, "data")
	if err := os.Mkdir(dataDir, 0o755); err != nil {
		t.Fatalf("Mkdir data: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "external-state")
	writeSentinel(t, filepath.Join(outside, "state.json"))
	statePath := filepath.Join(dataDir, "pulumi-state")
	if err := os.Symlink(outside, statePath); err != nil {
		t.Fatalf("Symlink state: %v", err)
	}

	err := PurgeLocalState(PurgeStateOptions{
		Repository: repo,
		Backend:    "file://" + statePath,
		Confirm:    true,
		Force:      true,
	})

	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "symbolic link") {
		t.Fatalf("PurgeLocalState error = %v, want symbolic-link rejection", err)
	}
	assertExists(t, filepath.Join(outside, "state.json"))
}

func TestPurgeStateRejectsSymlinkInsideStateTree(t *testing.T) {
	repo := newTestRepository(t)
	statePath := filepath.Join(repo, "data", "pulumi-state")
	writeSentinel(t, filepath.Join(statePath, "metadata.json"))
	outside := filepath.Join(t.TempDir(), "outside.json")
	writeSentinel(t, outside)
	if err := os.Symlink(outside, filepath.Join(statePath, "linked.json")); err != nil {
		t.Fatalf("Symlink inside state: %v", err)
	}

	err := PurgeLocalState(PurgeStateOptions{
		Repository: repo,
		Backend:    "file://" + statePath,
		Confirm:    true,
		Force:      true,
	})

	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "symbolic link") {
		t.Fatalf("PurgeLocalState error = %v, want nested symbolic-link rejection", err)
	}
	assertExists(t, statePath)
	assertExists(t, outside)
}

func TestPurgeStateRequiresForceForNestedStackFiles(t *testing.T) {
	repo := newTestRepository(t)
	statePath := filepath.Join(repo, "data", "pulumi-state")
	stackBackup := filepath.Join(statePath, ".pulumi", "stacks", "team", "project", "dev.json.bak")
	writeSentinel(t, stackBackup)
	writeSentinel(t, filepath.Join(statePath, "metadata.json"))
	source := filepath.Join(repo, "internal", "app.go")
	writeSentinel(t, source)
	options := PurgeStateOptions{
		Repository: repo,
		Backend:    "file://" + statePath,
		Confirm:    true,
	}

	err := PurgeLocalState(options)
	if !errors.Is(err, ErrForceRequired) {
		t.Fatalf("PurgeLocalState error = %v, want ErrForceRequired", err)
	}
	assertExists(t, stackBackup)

	options.Force = true
	if err := PurgeLocalState(options); err != nil {
		t.Fatalf("PurgeLocalState with force: %v", err)
	}
	assertNotExists(t, statePath)
	assertExists(t, source)
}

func TestPurgeStateRequiresForceForCaseVariantStackPath(t *testing.T) {
	repo := newTestRepository(t)
	statePath := filepath.Join(repo, "data", "pulumi-state")
	stack := filepath.Join(statePath, ".PULUMI", "STACKS", "dev.json")
	writeSentinel(t, stack)

	err := PurgeLocalState(PurgeStateOptions{
		Repository: repo,
		Backend:    "file://" + statePath,
		Confirm:    true,
	})

	if !errors.Is(err, ErrForceRequired) {
		t.Fatalf("PurgeLocalState error = %v, want ErrForceRequired", err)
	}
	assertExists(t, stack)
}

func TestPurgeStateWithoutStacksRemovesOnlyStateDirectory(t *testing.T) {
	repo := newTestRepository(t)
	statePath := filepath.Join(repo, "data", "pulumi-state")
	writeSentinel(t, filepath.Join(statePath, ".pulumi", "meta.yaml"))
	preserved := filepath.Join(repo, "data", "backup", "state.tar")
	writeSentinel(t, preserved)

	err := PurgeLocalState(PurgeStateOptions{
		Repository: repo,
		Backend:    "file://" + statePath,
		Confirm:    true,
	})

	if err != nil {
		t.Fatalf("PurgeLocalState: %v", err)
	}
	assertNotExists(t, statePath)
	assertExists(t, preserved)
}

func newTestRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeSentinel(t, filepath.Join(repo, ".git"))
	return repo
}

func writeSentinel(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("WriteFile %q: %v", path, err)
	}
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("expected %q to exist: %v", path, err)
	}
}

func assertNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %q not to exist, Lstat error = %v", path, err)
	}
}
