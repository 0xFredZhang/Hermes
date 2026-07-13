package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/localops"
)

type commandRecorder struct {
	doctorCalls int
	resetCalls  []localops.ResetOptions
	purgeCalls  []localops.PurgeStateOptions
	serveCalls  int
}

func newCommandTestDependencies(recorder *commandRecorder) (commandDependencies, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return commandDependencies{
		stdout: stdout,
		stderr: stderr,
		locations: func() (string, string, error) {
			return "/repo", "/repo/cmd/hermes", nil
		},
		getenv: func(string) string { return "" },
		doctor: func(context.Context) localops.Report {
			recorder.doctorCalls++
			return localops.Report{Checks: []localops.Check{{
				Name: "Pulumi CLI", Status: localops.StatusPass, Message: "installed",
			}}}
		},
		reset: func(options localops.ResetOptions) error {
			recorder.resetCalls = append(recorder.resetCalls, options)
			return nil
		},
		purge: func(options localops.PurgeStateOptions) error {
			recorder.purgeCalls = append(recorder.purgeCalls, options)
			return nil
		},
		serve: func(context.Context) error {
			recorder.serveCalls++
			return nil
		},
	}, stdout, stderr
}

func TestCLISelectsDoctorBeforeServerBootstrap(t *testing.T) {
	recorder := &commandRecorder{}
	deps, stdout, _ := newCommandTestDependencies(recorder)
	deps.locations = func() (string, string, error) {
		t.Fatal("doctor unexpectedly looked up the repository")
		return "", "", nil
	}
	deps.getenv = func(string) string {
		t.Fatal("doctor unexpectedly loaded application configuration")
		return ""
	}
	deps.serve = func(context.Context) error {
		t.Fatal("doctor unexpectedly bootstrapped the server")
		return nil
	}

	err := run(context.Background(), []string{"doctor"}, deps)

	if err != nil {
		t.Fatalf("run doctor: %v", err)
	}
	if recorder.doctorCalls != 1 {
		t.Fatalf("doctor calls = %d, want 1", recorder.doctorCalls)
	}
	if !strings.Contains(stdout.String(), "PASS") || !strings.Contains(stdout.String(), "Pulumi CLI") {
		t.Fatalf("doctor output = %q, want status and check name", stdout.String())
	}
}

func TestCLISelectsResetCommands(t *testing.T) {
	t.Run("database", func(t *testing.T) {
		recorder := &commandRecorder{}
		deps, _, _ := newCommandTestDependencies(recorder)
		deps.getenv = func(name string) string {
			if name == "HERMES_DB_PATH" {
				return "data/custom.sqlite"
			}
			return ""
		}

		if err := run(context.Background(), []string{"reset-local", "--confirm"}, deps); err != nil {
			t.Fatalf("run reset-local: %v", err)
		}
		want := []localops.ResetOptions{{
			Repository: "/repo",
			DBPath:     "/repo/cmd/hermes/data/custom.sqlite",
			Confirm:    true,
		}}
		if !reflect.DeepEqual(recorder.resetCalls, want) {
			t.Fatalf("reset calls = %#v, want %#v", recorder.resetCalls, want)
		}
		if recorder.serveCalls != 0 {
			t.Fatalf("serve calls = %d, want 0", recorder.serveCalls)
		}
	})

	t.Run("state", func(t *testing.T) {
		recorder := &commandRecorder{}
		deps, _, _ := newCommandTestDependencies(recorder)

		if err := run(context.Background(), []string{"reset-local-state", "--confirm", "--force"}, deps); err != nil {
			t.Fatalf("run reset-local-state: %v", err)
		}
		want := []localops.PurgeStateOptions{{
			Repository: "/repo",
			Backend:    "file:///repo/cmd/hermes/data/pulumi-state",
			Confirm:    true,
			Force:      true,
		}}
		if !reflect.DeepEqual(recorder.purgeCalls, want) {
			t.Fatalf("purge calls = %#v, want %#v", recorder.purgeCalls, want)
		}
		if recorder.serveCalls != 0 {
			t.Fatalf("serve calls = %d, want 0", recorder.serveCalls)
		}
	})
}

func TestCLINoArgumentsPreservesServerBehavior(t *testing.T) {
	recorder := &commandRecorder{}
	deps, _, _ := newCommandTestDependencies(recorder)

	if err := run(context.Background(), nil, deps); err != nil {
		t.Fatalf("run server: %v", err)
	}
	if recorder.serveCalls != 1 {
		t.Fatalf("serve calls = %d, want 1", recorder.serveCalls)
	}
	if recorder.doctorCalls != 0 || len(recorder.resetCalls) != 0 || len(recorder.purgeCalls) != 0 {
		t.Fatalf("local operation unexpectedly called: %+v", recorder)
	}
}

func TestCLIStateResetMatchesNestedWorkingDirectoryDefault(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: test"), 0o600); err != nil {
		t.Fatalf("WriteFile .git: %v", err)
	}
	workingDirectory := filepath.Join(repo, "cmd", "hermes")
	statePath := filepath.Join(workingDirectory, "data", "pulumi-state")
	if err := os.MkdirAll(statePath, 0o755); err != nil {
		t.Fatalf("MkdirAll state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(statePath, "metadata.json"), []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("WriteFile state sentinel: %v", err)
	}
	recorder := &commandRecorder{}
	deps, _, _ := newCommandTestDependencies(recorder)
	deps.locations = func() (string, string, error) {
		return repo, workingDirectory, nil
	}
	deps.purge = localops.PurgeLocalState

	if err := run(context.Background(), []string{"reset-local-state", "--confirm"}, deps); err != nil {
		t.Fatalf("run reset-local-state: %v", err)
	}
	if _, err := os.Lstat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state path still exists after reset, Lstat error = %v", err)
	}
}

func TestCLIRejectsMissingConfirmUnknownFlagsAndExtraArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "reset missing confirm", args: []string{"reset-local"}},
		{name: "purge missing confirm", args: []string{"reset-local-state", "--force"}},
		{name: "doctor unknown flag", args: []string{"doctor", "--aws"}},
		{name: "reset unknown flag", args: []string{"reset-local", "--all"}},
		{name: "purge unknown flag", args: []string{"reset-local-state", "--yes"}},
		{name: "doctor extra argument", args: []string{"doctor", "now"}},
		{name: "reset extra argument", args: []string{"reset-local", "--confirm", "now"}},
		{name: "unknown command", args: []string{"serve"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &commandRecorder{}
			deps, _, _ := newCommandTestDependencies(recorder)

			if err := run(context.Background(), tt.args, deps); err == nil {
				t.Fatalf("run(%#v) error = nil, want rejection", tt.args)
			}
			if recorder.serveCalls != 0 || recorder.doctorCalls != 0 || len(recorder.resetCalls) != 0 || len(recorder.purgeCalls) != 0 {
				t.Fatalf("operation called for rejected args: %+v", recorder)
			}
		})
	}
}

func TestCLIDoctorFailureReturnsError(t *testing.T) {
	recorder := &commandRecorder{}
	deps, _, _ := newCommandTestDependencies(recorder)
	deps.doctor = func(context.Context) localops.Report {
		return localops.Report{Checks: []localops.Check{{
			Name: "Pulumi CLI", Status: localops.StatusFail, Message: "not found",
		}}}
	}

	if err := run(context.Background(), []string{"doctor"}, deps); !errors.Is(err, errDoctorFailed) {
		t.Fatalf("run doctor error = %v, want errDoctorFailed", err)
	}
}

func TestFindRepositoryRootAcceptsGitFile(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: /tmp/worktree"), 0o600); err != nil {
		t.Fatalf("WriteFile .git: %v", err)
	}
	nested := filepath.Join(repo, "cmd", "hermes")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll nested: %v", err)
	}

	got, err := findRepositoryRoot(nested)
	if err != nil {
		t.Fatalf("findRepositoryRoot: %v", err)
	}
	if got != repo {
		t.Fatalf("findRepositoryRoot = %q, want %q", got, repo)
	}
}

func TestCheckTargetClearsGOFLAGS(t *testing.T) {
	makefile, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("ReadFile Makefile: %v", err)
	}
	contents := string(makefile)
	for _, command := range []string{
		"GOENV=off GOFLAGS=-tags= go test ./...",
		"GOENV=off GOFLAGS=-tags= go vet ./...",
		"GOENV=off GOFLAGS=-tags= go build ./...",
	} {
		if !strings.Contains(contents, command) {
			t.Errorf("Makefile check target must contain %q", command)
		}
	}
}
