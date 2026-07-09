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
	upErr              error
	outputs            map[string]any
	logLine            string
	previewDestroyLine string
	refreshLine        string
	previewSpecs       []provisioner.Spec
	upSpecs            []provisioner.Spec
}

func (f *fakeProvisioner) Preview(_ context.Context, spec provisioner.Spec, logs io.Writer) (provisioner.PreviewResult, error) {
	f.previewSpecs = append(f.previewSpecs, spec)
	if f.logLine != "" {
		fmt.Fprintln(logs, f.logLine)
	}
	return provisioner.PreviewResult{Creates: 3}, nil
}

func (f *fakeProvisioner) PreviewDestroy(_ context.Context, _ provisioner.Spec, logs io.Writer) (provisioner.PreviewResult, error) {
	if f.previewDestroyLine != "" {
		fmt.Fprintln(logs, f.previewDestroyLine)
	}
	return provisioner.PreviewResult{Deletes: 2}, nil
}

func (f *fakeProvisioner) Refresh(_ context.Context, _ provisioner.Spec, logs io.Writer) (provisioner.PreviewResult, error) {
	if f.refreshLine != "" {
		fmt.Fprintln(logs, f.refreshLine)
	}
	return provisioner.PreviewResult{Updates: 1, Sames: 3}, nil
}

func (f *fakeProvisioner) Up(_ context.Context, spec provisioner.Spec, logs io.Writer) (provisioner.UpResult, error) {
	f.upSpecs = append(f.upSpecs, spec)
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
	return newSeededStoreWithParams(t, provisioner.BlueprintParams{Region: "ap-southeast-1",
		EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8}})
}

func newSeededStoreWithParams(t *testing.T, params provisioner.BlueprintParams) (*store.Store, int64) {
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
		Params: params,
	})
	envID, _ := st.CreateEnvironment(ctx, store.Environment{
		BlueprintID: bpID, CloudAccountID: aid, Name: "e", PulumiStack: "e-1", Region: "ap-southeast-1",
		Snapshot: params,
	})
	return st, envID
}

func rdsParams() provisioner.BlueprintParams {
	p := provisioner.BlueprintParams{
		Region: "ap-southeast-1",
		SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
			{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
		}},
		EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8},
		RDS: provisioner.RDS{Enabled: true},
	}
	p.ApplyDefaults()
	return p
}

func redisAuthParams() provisioner.BlueprintParams {
	p := provisioner.BlueprintParams{
		Region: "ap-southeast-1",
		SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
			{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
		}},
		EC2:   provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8},
		Redis: provisioner.Redis{Enabled: true, AuthEnabled: true},
	}
	p.ApplyDefaults()
	return p
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

func TestRunPreviewWithRDSGeneratesAndStoresPassword(t *testing.T) {
	st, envID := newSeededStoreWithParams(t, rdsParams())
	ctx := context.Background()
	prov := &fakeProvisioner{logLine: "previewing rds"}
	o := New(st, prov, NewBroker(), 1)

	jobID, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionPreview})
	o.run(ctx, jobID)

	if len(prov.previewSpecs) != 1 {
		t.Fatalf("preview specs = %d, want 1", len(prov.previewSpecs))
	}
	gotPassword := prov.previewSpecs[0].Secrets.RDSPassword
	if len(gotPassword) != 24 {
		t.Fatalf("runtime RDS password length = %d, want 24", len(gotPassword))
	}
	secret, err := st.GetEnvironmentSecret(ctx, envID, store.SecretRDSMySQL)
	if err != nil {
		t.Fatalf("GetEnvironmentSecret: %v", err)
	}
	if secret.Username != "admin" || secret.Password != gotPassword {
		t.Fatalf("stored secret = %+v, want username admin and generated password", secret)
	}
	if secret.Metadata["db_name"] != "app" || secret.Metadata["port"] != float64(3306) {
		t.Fatalf("stored metadata = %+v, want db_name/port", secret.Metadata)
	}
}

func TestRunPreviewWithRedisAuthGeneratesAndStoresToken(t *testing.T) {
	st, envID := newSeededStoreWithParams(t, redisAuthParams())
	ctx := context.Background()
	prov := &fakeProvisioner{logLine: "previewing redis"}
	o := New(st, prov, NewBroker(), 1)

	jobID, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionPreview})
	o.run(ctx, jobID)

	if len(prov.previewSpecs) != 1 {
		t.Fatalf("preview specs = %d, want 1", len(prov.previewSpecs))
	}
	gotToken := prov.previewSpecs[0].Secrets.RedisAuthToken
	if len(gotToken) != 32 {
		t.Fatalf("runtime Redis auth token length = %d, want 32", len(gotToken))
	}
	secret, err := st.GetEnvironmentSecret(ctx, envID, store.SecretRedisAuth)
	if err != nil {
		t.Fatalf("GetEnvironmentSecret: %v", err)
	}
	if secret.Username != "default" || secret.Password != gotToken {
		t.Fatalf("stored Redis secret = %+v, want default user and generated token", secret)
	}
	if secret.Metadata["port"] != float64(6379) {
		t.Fatalf("stored Redis metadata = %+v, want port", secret.Metadata)
	}
}

func TestRunDestroyPreviewSucceeds(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	o := New(st, &fakeProvisioner{previewDestroyLine: "preview destroy"}, NewBroker(), 1)

	jobID, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionDestroyPreview})
	o.run(ctx, jobID)

	job, _ := st.GetJob(ctx, jobID)
	if job.Status != store.JobSucceeded {
		t.Fatalf("job status = %q, want succeeded", job.Status)
	}
	if job.Summary["deletes"] != float64(2) && job.Summary["deletes"] != 2 {
		t.Fatalf("delete summary not stored: %+v", job.Summary)
	}
	if !strings.Contains(job.Logs, "preview destroy") {
		t.Fatalf("logs not persisted: %q", job.Logs)
	}
	env, _ := st.GetEnvironment(ctx, envID)
	if env.Status != store.EnvDestroyPreviewReady {
		t.Fatalf("env status = %q, want destroy_preview_ready", env.Status)
	}
}

func TestRunRefreshSucceeds(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	o := New(st, &fakeProvisioner{refreshLine: "refreshing"}, NewBroker(), 1)

	jobID, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionRefresh})
	o.run(ctx, jobID)

	job, _ := st.GetJob(ctx, jobID)
	if job.Status != store.JobSucceeded {
		t.Fatalf("job status = %q, want succeeded", job.Status)
	}
	if job.Summary["updates"] != float64(1) && job.Summary["updates"] != 1 {
		t.Fatalf("update summary not stored: %+v", job.Summary)
	}
	if job.Summary["sames"] != float64(3) && job.Summary["sames"] != 3 {
		t.Fatalf("same summary not stored: %+v", job.Summary)
	}
	if !strings.Contains(job.Logs, "refreshing") {
		t.Fatalf("logs not persisted: %q", job.Logs)
	}
	env, _ := st.GetEnvironment(ctx, envID)
	if env.Status != store.EnvUp {
		t.Fatalf("env status = %q, want up", env.Status)
	}
}

func TestRunUpWithRDSReusesExistingPassword(t *testing.T) {
	st, envID := newSeededStoreWithParams(t, rdsParams())
	ctx := context.Background()
	if err := st.UpsertEnvironmentSecret(ctx, store.EnvironmentSecret{
		EnvironmentID: envID,
		Kind:          store.SecretRDSMySQL,
		Username:      "admin",
		Password:      "existing-rds-password",
		Metadata: map[string]any{
			"db_name": "app",
			"port":    float64(3306),
		},
	}); err != nil {
		t.Fatalf("UpsertEnvironmentSecret: %v", err)
	}
	prov := &fakeProvisioner{
		outputs: map[string]any{
			"rds_endpoint": "db.example:3306",
			"rds_address":  "db.example",
			"rds_port":     float64(3306),
		},
	}
	o := New(st, prov, NewBroker(), 1)

	jobID, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionUp})
	o.run(ctx, jobID)

	if len(prov.upSpecs) != 1 {
		t.Fatalf("up specs = %d, want 1", len(prov.upSpecs))
	}
	if got := prov.upSpecs[0].Secrets.RDSPassword; got != "existing-rds-password" {
		t.Fatalf("runtime RDS password = %q, want existing secret", got)
	}
	secret, err := st.GetEnvironmentSecret(ctx, envID, store.SecretRDSMySQL)
	if err != nil {
		t.Fatalf("GetEnvironmentSecret: %v", err)
	}
	if secret.Password != "existing-rds-password" {
		t.Fatalf("stored password = %q, want unchanged existing secret", secret.Password)
	}
	if secret.Metadata["host"] != "db.example" || secret.Metadata["endpoint"] != "db.example:3306" {
		t.Fatalf("stored metadata = %+v, want RDS endpoint details from outputs", secret.Metadata)
	}
}

func TestRunUpWithRedisAuthReusesExistingToken(t *testing.T) {
	st, envID := newSeededStoreWithParams(t, redisAuthParams())
	ctx := context.Background()
	if err := st.UpsertEnvironmentSecret(ctx, store.EnvironmentSecret{
		EnvironmentID: envID,
		Kind:          store.SecretRedisAuth,
		Username:      "default",
		Password:      "existing-redis-token",
		Metadata: map[string]any{
			"port": float64(6379),
		},
	}); err != nil {
		t.Fatalf("UpsertEnvironmentSecret: %v", err)
	}
	prov := &fakeProvisioner{
		outputs: map[string]any{
			"redis_primary_endpoint": "redis.example",
			"redis_reader_endpoint":  "redis-ro.example",
			"redis_port":             float64(6379),
		},
	}
	o := New(st, prov, NewBroker(), 1)

	jobID, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionUp})
	o.run(ctx, jobID)

	if len(prov.upSpecs) != 1 {
		t.Fatalf("up specs = %d, want 1", len(prov.upSpecs))
	}
	if got := prov.upSpecs[0].Secrets.RedisAuthToken; got != "existing-redis-token" {
		t.Fatalf("runtime Redis auth token = %q, want existing token", got)
	}
	secret, err := st.GetEnvironmentSecret(ctx, envID, store.SecretRedisAuth)
	if err != nil {
		t.Fatalf("GetEnvironmentSecret: %v", err)
	}
	if secret.Password != "existing-redis-token" {
		t.Fatalf("stored Redis token = %q, want unchanged existing token", secret.Password)
	}
	if secret.Metadata["primary_endpoint"] != "redis.example" || secret.Metadata["reader_endpoint"] != "redis-ro.example" {
		t.Fatalf("stored Redis metadata = %+v, want endpoint details from outputs", secret.Metadata)
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
