package localops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/0xFredZhang/Hermes/internal/config"
)

var (
	// ErrConfirmationRequired indicates that a destructive local operation was
	// requested without its explicit confirmation flag.
	ErrConfirmationRequired = errors.New("explicit confirmation is required")
	// ErrForceRequired indicates that local Pulumi stack files were found.
	ErrForceRequired = errors.New("local Pulumi stack files exist; force is required")
)

// ResetOptions configures removal of a repository-local SQLite database and
// its exact SQLite sidecars.
type ResetOptions struct {
	Repository string
	DBPath     string
	Confirm    bool
}

// PurgeStateOptions configures removal of a repository-local Pulumi file
// backend. Force only permits removal when stack files exist.
type PurgeStateOptions struct {
	Repository string
	Backend    string
	Confirm    bool
	Force      bool
}

type repositoryRoot struct {
	input    string
	resolved string
}

// ResetLocal removes only the configured SQLite database and the exact -wal,
// -shm, and -journal sidecars. It never removes Pulumi state.
func ResetLocal(options ResetOptions) error {
	if !options.Confirm {
		return ErrConfirmationRequired
	}
	if !isSQLitePath(options.DBPath) {
		return fmt.Errorf("unsafe SQLite database path %q", options.DBPath)
	}

	repository, err := openRepository(options.Repository)
	if err != nil {
		return err
	}
	database, err := repository.target(options.DBPath)
	if err != nil {
		return fmt.Errorf("database path: %w", err)
	}

	targets := make([]string, 0, 4)
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		target := database + suffix
		if err := repository.preflightRegularFile(target); err != nil {
			return fmt.Errorf("preflight %q: %w", target, err)
		}
		targets = append(targets, target)
	}

	root, err := os.OpenRoot(repository.resolved)
	if err != nil {
		return fmt.Errorf("open repository root: %w", err)
	}
	defer root.Close()

	for _, target := range targets {
		relative, err := filepath.Rel(repository.resolved, target)
		if err != nil {
			return fmt.Errorf("relative database target: %w", err)
		}
		if err := root.Remove(relative); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %q: %w", target, err)
		}
	}
	return nil
}

// PurgeLocalState removes one repository-local Pulumi file backend. It never
// contacts AWS or deletes cloud resources.
func PurgeLocalState(options PurgeStateOptions) error {
	if !options.Confirm {
		return ErrConfirmationRequired
	}
	statePath, isFile, err := config.LocalPulumiBackendPath(options.Backend)
	if err != nil {
		return fmt.Errorf("invalid local state backend: %w", err)
	}
	if !isFile {
		return fmt.Errorf("local state purge requires a file:/// backend, got %q", options.Backend)
	}

	repository, err := openRepository(options.Repository)
	if err != nil {
		return err
	}
	state, err := repository.target(statePath)
	if err != nil {
		return fmt.Errorf("state backend path: %w", err)
	}
	relative, err := filepath.Rel(repository.resolved, state)
	if err != nil {
		return fmt.Errorf("relative state path: %w", err)
	}
	if !isKnownLocalStatePath(relative) {
		return fmt.Errorf("unsafe state path %q: local state must end in data/pulumi-state", state)
	}

	hasStacks, err := repository.preflightStateDirectory(state)
	if err != nil {
		return fmt.Errorf("preflight state backend: %w", err)
	}
	if hasStacks && !options.Force {
		return ErrForceRequired
	}

	root, err := os.OpenRoot(repository.resolved)
	if err != nil {
		return fmt.Errorf("open repository root: %w", err)
	}
	defer root.Close()
	if err := root.RemoveAll(relative); err != nil {
		return fmt.Errorf("remove local Pulumi state %q: %w", state, err)
	}
	return nil
}

func openRepository(path string) (repositoryRoot, error) {
	if path == "" {
		return repositoryRoot{}, errors.New("repository path is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return repositoryRoot{}, fmt.Errorf("absolute repository path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return repositoryRoot{}, fmt.Errorf("resolve repository path: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return repositoryRoot{}, fmt.Errorf("stat repository path: %w", err)
	}
	if !info.IsDir() {
		return repositoryRoot{}, fmt.Errorf("repository path %q is not a directory", resolved)
	}
	gitInfo, err := os.Lstat(filepath.Join(resolved, ".git"))
	if err != nil {
		return repositoryRoot{}, fmt.Errorf("repository marker: %w", err)
	}
	if gitInfo.Mode()&os.ModeSymlink != 0 || (!gitInfo.IsDir() && !gitInfo.Mode().IsRegular()) {
		return repositoryRoot{}, fmt.Errorf("unsafe repository marker at %q", filepath.Join(resolved, ".git"))
	}
	return repositoryRoot{input: absolute, resolved: resolved}, nil
}

func (r repositoryRoot) target(path string) (string, error) {
	var absolute string
	if filepath.IsAbs(path) {
		var err error
		absolute, err = filepath.Abs(filepath.Clean(path))
		if err != nil {
			return "", fmt.Errorf("absolute target path: %w", err)
		}
		if relative, ok := containedRelative(r.input, absolute); ok {
			absolute = filepath.Join(r.resolved, relative)
		}
	} else {
		absolute = filepath.Join(r.resolved, filepath.Clean(path))
	}
	absolute = filepath.Clean(absolute)
	if relative, ok := containedRelative(r.resolved, absolute); !ok || relative == "." {
		return "", fmt.Errorf("target %q must be below repository %q", absolute, r.resolved)
	}
	if err := rejectSymlinkComponents(r.resolved, absolute); err != nil {
		return "", err
	}
	return absolute, nil
}

func (r repositoryRoot) preflightRegularFile(path string) error {
	if err := rejectSymlinkComponents(r.resolved, path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("target is a directory")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("target is not a regular file (mode %s)", info.Mode())
	}
	return nil
}

func (r repositoryRoot) preflightStateDirectory(path string) (bool, error) {
	if err := rejectSymlinkComponents(r.resolved, path); err != nil {
		return false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, errors.New("state backend is not a directory")
	}

	hasStacks := false
	err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link %q is not allowed in local state", current)
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return fmt.Errorf("special file %q is not allowed in local state", current)
		}
		if entry.IsDir() {
			return nil
		}
		if isPulumiStackPath(path, current) {
			hasStacks = true
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return hasStacks, nil
}

func isPulumiStackPath(stateRoot, path string) bool {
	relative, ok := containedRelative(stateRoot, path)
	if !ok {
		return false
	}
	components := strings.Split(relative, string(filepath.Separator))
	return len(components) >= 2 &&
		strings.EqualFold(components[0], ".pulumi") &&
		strings.EqualFold(components[1], "stacks")
}

func isKnownLocalStatePath(relative string) bool {
	components := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	return len(components) >= 2 &&
		strings.EqualFold(components[len(components)-2], "data") &&
		strings.EqualFold(components[len(components)-1], "pulumi-state")
}

func containedRelative(root, target string) (string, bool) {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return relative, true
}

func rejectSymlinkComponents(root, target string) error {
	relative, ok := containedRelative(root, target)
	if !ok {
		return fmt.Errorf("target %q is outside repository %q", target, root)
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link %q is not allowed", current)
		}
	}
	return nil
}

func isSQLitePath(path string) bool {
	if path == "" || path == ":memory:" || strings.HasPrefix(strings.ToLower(path), "file:") {
		return false
	}
	base := strings.ToLower(filepath.Base(filepath.Clean(path)))
	return strings.HasSuffix(base, ".db") || strings.HasSuffix(base, ".sqlite") || strings.HasSuffix(base, ".sqlite3")
}
