package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/0xFredZhang/Hermes/internal/localops"
)

var errDoctorFailed = errors.New("one or more doctor checks failed")

type commandDependencies struct {
	stdout    io.Writer
	stderr    io.Writer
	locations func() (repository string, workingDirectory string, err error)
	getenv    func(string) string
	doctor    func(context.Context) localops.Report
	reset     func(localops.ResetOptions) error
	purge     func(localops.PurgeStateOptions) error
	serve     func(context.Context) error
}

func defaultCommandDependencies() commandDependencies {
	return commandDependencies{
		stdout: os.Stdout,
		stderr: os.Stderr,
		locations: func() (string, string, error) {
			workingDirectory, err := os.Getwd()
			if err != nil {
				return "", "", fmt.Errorf("working directory: %w", err)
			}
			repository, err := findRepositoryRoot(workingDirectory)
			if err != nil {
				return "", "", err
			}
			return repository, workingDirectory, nil
		},
		getenv: os.Getenv,
		doctor: func(ctx context.Context) localops.Report {
			return localops.Doctor(ctx, localops.ExecCommandRunner{})
		},
		reset: localops.ResetLocal,
		purge: localops.PurgeLocalState,
		serve: runServer,
	}
}

func run(ctx context.Context, args []string, deps commandDependencies) error {
	if len(args) == 0 {
		return deps.serve(ctx)
	}

	switch args[0] {
	case "doctor":
		return runDoctor(ctx, args[1:], deps)
	case "reset-local":
		return runResetLocal(args[1:], deps)
	case "reset-local-state":
		return runResetLocalState(args[1:], deps)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runDoctor(ctx context.Context, args []string, deps commandDependencies) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(deps.stderr)
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("doctor flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("doctor accepts no arguments, got %q", flags.Args())
	}

	report := deps.doctor(ctx)
	for _, check := range report.Checks {
		if _, err := fmt.Fprintf(
			deps.stdout,
			"%s %s: %s\n",
			strings.ToUpper(string(check.Status)),
			check.Name,
			check.Message,
		); err != nil {
			return fmt.Errorf("write doctor report: %w", err)
		}
	}
	if report.HasFailures() {
		return errDoctorFailed
	}
	return nil
}

func runResetLocal(args []string, deps commandDependencies) error {
	flags := flag.NewFlagSet("reset-local", flag.ContinueOnError)
	flags.SetOutput(deps.stderr)
	confirm := flags.Bool("confirm", false, "confirm removal of the repository-local SQLite database")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("reset-local flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("reset-local accepts no arguments, got %q", flags.Args())
	}
	if !*confirm {
		return localops.ErrConfirmationRequired
	}

	repository, workingDirectory, err := deps.locations()
	if err != nil {
		return fmt.Errorf("resolve local paths: %w", err)
	}
	database := deps.getenv("HERMES_DB_PATH")
	if database == "" {
		database = "hermes.db"
	}
	if !filepath.IsAbs(database) && database != ":memory:" &&
		!strings.HasPrefix(strings.ToLower(database), "file:") {
		database = filepath.Join(workingDirectory, database)
	}
	if err := deps.reset(localops.ResetOptions{
		Repository: repository,
		DBPath:     database,
		Confirm:    true,
	}); err != nil {
		return fmt.Errorf("reset local database: %w", err)
	}
	return nil
}

func runResetLocalState(args []string, deps commandDependencies) error {
	flags := flag.NewFlagSet("reset-local-state", flag.ContinueOnError)
	flags.SetOutput(deps.stderr)
	confirm := flags.Bool("confirm", false, "confirm removal of repository-local Pulumi state")
	force := flags.Bool("force", false, "allow removal when local Pulumi stack files exist")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("reset-local-state flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("reset-local-state accepts no arguments, got %q", flags.Args())
	}
	if !*confirm {
		return localops.ErrConfirmationRequired
	}

	repository, workingDirectory, err := deps.locations()
	if err != nil {
		return fmt.Errorf("resolve local paths: %w", err)
	}
	backend := deps.getenv("HERMES_PULUMI_BACKEND")
	if backend == "" {
		statePath := filepath.Join(workingDirectory, "data", "pulumi-state")
		backend = (&url.URL{Scheme: "file", Path: filepath.ToSlash(statePath)}).String()
	}
	if err := deps.purge(localops.PurgeStateOptions{
		Repository: repository,
		Backend:    backend,
		Confirm:    true,
		Force:      *force,
	}); err != nil {
		return fmt.Errorf("reset local state: %w", err)
	}
	return nil
}

func findRepositoryRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("absolute start path: %w", err)
	}
	info, err := os.Stat(current)
	if err != nil {
		return "", fmt.Errorf("stat start path: %w", err)
	}
	if !info.IsDir() {
		current = filepath.Dir(current)
	}

	for {
		marker := filepath.Join(current, ".git")
		markerInfo, markerErr := os.Lstat(marker)
		if markerErr == nil {
			if markerInfo.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("repository marker %q is a symbolic link", marker)
			}
			if markerInfo.IsDir() || markerInfo.Mode().IsRegular() {
				return current, nil
			}
			return "", fmt.Errorf("repository marker %q has unsafe mode %s", marker, markerInfo.Mode())
		}
		if !errors.Is(markerErr, os.ErrNotExist) {
			return "", fmt.Errorf("inspect repository marker %q: %w", marker, markerErr)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no .git repository marker found above %q", start)
		}
		current = parent
	}
}
