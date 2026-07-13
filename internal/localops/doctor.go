// Package localops provides local-only diagnostics and destructive maintenance
// operations. It deliberately has no dependency on cloud or provisioning code.
package localops

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// Status is the outcome of one doctor check.
type Status string

const (
	// StatusPass indicates that a prerequisite is available.
	StatusPass Status = "pass"
	// StatusFail indicates that a prerequisite is missing or unusable.
	StatusFail Status = "fail"
)

// Check is one local diagnostic result.
type Check struct {
	Name    string
	Status  Status
	Message string
}

// Report is the complete set of local diagnostic results.
type Report struct {
	Checks []Check
}

// HasFailures reports whether any doctor check failed.
func (r Report) HasFailures() bool {
	for _, check := range r.Checks {
		if check.Status == StatusFail {
			return true
		}
	}
	return false
}

// CommandRunner executes local commands without a shell.
type CommandRunner interface {
	LookPath(file string) (string, error)
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecCommandRunner uses the operating system PATH and direct argument-based
// process execution.
type ExecCommandRunner struct{}

// LookPath resolves an executable through PATH.
func (ExecCommandRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

// Output executes a command directly and returns its standard output.
func (ExecCommandRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

type pulumiPlugin struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Doctor checks local Pulumi prerequisites without loading Hermes config or
// contacting AWS.
func Doctor(ctx context.Context, runner CommandRunner) Report {
	pulumiPath, err := runner.LookPath("pulumi")
	if err != nil {
		return Report{Checks: []Check{{
			Name:    "Pulumi CLI",
			Status:  StatusFail,
			Message: fmt.Sprintf("not found: %v", err),
		}}}
	}

	checks := []Check{{
		Name:    "Pulumi CLI",
		Status:  StatusPass,
		Message: pulumiPath,
	}}
	output, err := runner.Output(ctx, pulumiPath, "plugin", "ls", "--json")
	if err != nil {
		checks = append(checks, Check{
			Name:    "Pulumi plugins",
			Status:  StatusFail,
			Message: fmt.Sprintf("could not list plugins: %v", err),
		})
		return Report{Checks: checks}
	}

	var plugins []pulumiPlugin
	if err := json.Unmarshal(output, &plugins); err != nil {
		checks = append(checks, Check{
			Name:    "Pulumi plugins",
			Status:  StatusFail,
			Message: fmt.Sprintf("invalid JSON output: %v", err),
		})
		return Report{Checks: checks}
	}
	if plugins == nil {
		checks = append(checks, Check{
			Name:    "Pulumi plugins",
			Status:  StatusFail,
			Message: "invalid JSON output: expected an array",
		})
		return Report{Checks: checks}
	}

	installed := make(map[string]bool, len(plugins))
	for _, plugin := range plugins {
		if plugin.Kind == "resource" {
			installed[plugin.Name] = true
		}
	}
	for _, name := range []string{"aws", "random"} {
		check := Check{Name: "Pulumi plugin " + name, Status: StatusPass, Message: "installed"}
		if !installed[name] {
			check.Status = StatusFail
			check.Message = "missing resource plugin"
		}
		checks = append(checks, check)
	}

	return Report{Checks: checks}
}
