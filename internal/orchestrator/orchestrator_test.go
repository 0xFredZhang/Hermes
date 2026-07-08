package orchestrator

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/crypto"
	"github.com/0xFredZhang/Hermes/internal/provisioner"
	"github.com/0xFredZhang/Hermes/internal/store"
)

type fakeProvisioner struct {
	upErr   error
	outputs map[string]any
	logLine string
}

func (f *fakeProvisioner) Preview(_ context.Context, _ provisioner.Spec, logs io.Writer) (provisioner.PreviewResult, error) {
	if f.logLine != "" {
		fmt.Fprintln(logs, f.logLine)
	}
	return provisioner.PreviewResult{Creates: 3}, nil
}

func (f *fakeProvisioner) Up(_ context.Context, _ provisioner.Spec, logs io.Writer) (provisioner.UpResult, error) {
	if f.logLine != "" {
		fmt.Fprintln(logs, f.logLine)
	}
	if f.upErr != nil {
		return provisioner.UpResult{}, f.upErr
	}
	return provisioner.UpResult{Outputs: f.outputs}, nil
}

func (f *fakeProvisioner) Destroy(_ context.Context, _ provisioner.Spec, logs io.Writer) error {
	if f.logLine != "" {
		fmt.Fprintln(logs, f.logLine)
	}
	return nil
}

func newSeededStore(t *testing.T) (*store.Store, int64) {
	t.Helper()
	c, err := crypto.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	st, err := store.Open(":memory:", c)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	pid, _ := st.CreateProject(ctx, store.Project{Name: "p"})
	aid, err := st.CreateCloudAccount(ctx, store.CloudAccount{
		Name: "a", DefaultRegion: "ap-southeast-1", AccessKeyID: "AKIA",
		SecretAccessKey: "sec", AWSAccountID: "111111111111", ARN: "arn:aws:iam::111111111111:user/x",
	})
	if err != nil {
		t.Fatalf("CreateCloudAccount: %v", err)
	}
	bpID, _ := st.CreateBlueprint(ctx, store.Blueprint{
		ProjectID: pid, CloudAccountID: aid, Name: "bp",
		Params: provisioner.BlueprintParams{Region: "ap-southeast-1",
			EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8}},
	})
	envID, _ := st.CreateEnvironment(ctx, store.Environment{
		BlueprintID: bpID, CloudAccountID: aid, Name: "e", PulumiStack: "e-1", Region: "ap-southeast-1",
	})
	return st, envID
}

func TestRunPreviewSucceeds(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	o := New(st, &fakeProvisioner{logLine: "previewing"}, NewBroker(), 1)

	jobID, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionPreview})
	o.run(ctx, jobID)

	job, _ := st.GetJob(ctx, jobID)
	if job.Status != store.JobSucceeded {
		t.Fatalf("job status = %q, want succeeded", job.Status)
	}
	if !strings.Contains(job.Logs, "previewing") {
		t.Fatalf("logs not persisted: %q", job.Logs)
	}
	env, _ := st.GetEnvironment(ctx, envID)
	if env.Status != store.EnvPreviewReady {
		t.Fatalf("env status = %q, want preview_ready", env.Status)
	}
}

func TestRunUpStoresOutputs(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	o := New(st, &fakeProvisioner{outputs: map[string]any{"public_ips": []any{"1.2.3.4"}}}, NewBroker(), 1)

	jobID, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionUp})
	o.run(ctx, jobID)

	env, _ := st.GetEnvironment(ctx, envID)
	if env.Status != store.EnvUp {
		t.Fatalf("env status = %q, want up", env.Status)
	}
	if env.Outputs["public_ips"] == nil {
		t.Fatalf("outputs not stored: %+v", env.Outputs)
	}
}

func TestRunUpFailureMarksFailed(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	o := New(st, &fakeProvisioner{upErr: fmt.Errorf("boom")}, NewBroker(), 1)

	jobID, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionUp})
	o.run(ctx, jobID)

	job, _ := st.GetJob(ctx, jobID)
	if job.Status != store.JobFailed || job.Error == "" {
		t.Fatalf("job = %+v, want failed with error", job)
	}
	env, _ := st.GetEnvironment(ctx, envID)
	if env.Status != store.EnvFailed {
		t.Fatalf("env status = %q, want failed", env.Status)
	}
}

func TestEnqueueRejectsBusyEnvironment(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	o := New(st, &fakeProvisioner{}, NewBroker(), 1)

	if _, err := o.Enqueue(ctx, envID, store.ActionPreview); err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	if _, err := o.Enqueue(ctx, envID, store.ActionUp); err != ErrEnvironmentBusy {
		t.Fatalf("second Enqueue err = %v, want ErrEnvironmentBusy", err)
	}
}

func TestRecoverOrphans(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	_ = st.UpdateEnvironmentStatus(ctx, envID, store.EnvProvisioning)
	orphan, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionUp})
	_ = st.UpdateJobStatus(ctx, orphan, store.JobRunning)

	o := New(st, &fakeProvisioner{}, NewBroker(), 1)
	o.recoverOrphans(ctx)

	job, _ := st.GetJob(ctx, orphan)
	if job.Status != store.JobFailed {
		t.Fatalf("orphan job status = %q, want failed", job.Status)
	}
	env, _ := st.GetEnvironment(ctx, envID)
	if env.Status != store.EnvFailed {
		t.Fatalf("env status = %q, want failed", env.Status)
	}
}
