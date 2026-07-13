package localops

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type commandCall struct {
	name string
	args []string
}

type fakeCommandRunner struct {
	path      string
	lookErr   error
	output    []byte
	outputErr error
	lookups   []string
	commands  []commandCall
}

func (f *fakeCommandRunner) LookPath(file string) (string, error) {
	f.lookups = append(f.lookups, file)
	return f.path, f.lookErr
}

func (f *fakeCommandRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	f.commands = append(f.commands, commandCall{name: name, args: append([]string(nil), args...)})
	return f.output, f.outputErr
}

func TestDoctorReportsMissingPulumi(t *testing.T) {
	runner := &fakeCommandRunner{lookErr: errors.New("not found")}

	report := Doctor(context.Background(), runner)

	assertFailedCheck(t, report, "pulumi", "not found")
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %#v, want none when pulumi is missing", runner.commands)
	}
}

func TestDoctorReportsMissingAWSAndRandomPlugins(t *testing.T) {
	runner := &fakeCommandRunner{
		path:   "/usr/local/bin/pulumi",
		output: []byte(`[]`),
	}

	report := Doctor(context.Background(), runner)

	assertFailedCheck(t, report, "plugin aws", "missing")
	assertFailedCheck(t, report, "plugin random", "missing")
}

func TestDoctorParsesPluginJSONStructurally(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "malformed output",
			output: `not-json aws random`,
			want:   "invalid",
		},
		{
			name:   "null instead of array",
			output: `null`,
			want:   "invalid",
		},
		{
			name:   "decoy strings",
			output: `[{"name":"decoy","kind":"resource","version":"aws random"}]`,
			want:   "missing",
		},
		{
			name:   "wrong plugin kind",
			output: `[{"name":"aws","kind":"language"},{"name":"random","kind":"analyzer"}]`,
			want:   "missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeCommandRunner{path: "/usr/local/bin/pulumi", output: []byte(tt.output)}

			report := Doctor(context.Background(), runner)

			if tt.name == "malformed output" || tt.name == "null instead of array" {
				assertFailedCheck(t, report, "pulumi plugins", tt.want)
				return
			}
			assertFailedCheck(t, report, "plugin aws", tt.want)
			assertFailedCheck(t, report, "plugin random", tt.want)
		})
	}
}

func TestDoctorUsesOnlyLocalPulumiPluginList(t *testing.T) {
	t.Setenv("HERMES_MASTER_KEY", "")
	t.Setenv("HERMES_LOGIN_PASSWORD", "")
	runner := &fakeCommandRunner{
		path: "/opt/pulumi/bin/pulumi",
		output: []byte(`[
			{"name":"aws","kind":"resource","version":"6.0.0"},
			{"name":"random","kind":"resource","version":"4.0.0"}
		]`),
	}

	report := Doctor(context.Background(), runner)

	if report.HasFailures() {
		t.Fatalf("Doctor returned failures on a fresh checkout: %#v", report.Checks)
	}
	if !reflect.DeepEqual(runner.lookups, []string{"pulumi"}) {
		t.Fatalf("LookPath calls = %#v, want only pulumi", runner.lookups)
	}
	wantCommands := []commandCall{{
		name: "/opt/pulumi/bin/pulumi",
		args: []string{"plugin", "ls", "--json"},
	}}
	if !reflect.DeepEqual(runner.commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, wantCommands)
	}
}

func assertFailedCheck(t *testing.T, report Report, nameContains, messageContains string) {
	t.Helper()
	for _, check := range report.Checks {
		if strings.Contains(strings.ToLower(check.Name), strings.ToLower(nameContains)) &&
			strings.Contains(strings.ToLower(check.Message), strings.ToLower(messageContains)) {
			if check.Status != StatusFail {
				t.Fatalf("check %q status = %q, want %q", check.Name, check.Status, StatusFail)
			}
			return
		}
	}
	t.Fatalf("no failed check matching name %q and message %q in %#v", nameContains, messageContains, report.Checks)
}
