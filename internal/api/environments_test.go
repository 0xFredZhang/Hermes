package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/store"
)

func seedEnv(t *testing.T, d Deps) int64 {
	t.Helper()
	pid, aid := seedProjectAccount(t, d)
	bpID, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{
		ProjectID: pid, CloudAccountID: aid, Name: "bp", Params: validBPParams(),
	})
	envID, _ := d.Store.CreateEnvironment(context.Background(), store.Environment{
		BlueprintID: bpID, CloudAccountID: aid, Name: "e", PulumiStack: "e-1", Region: "ap-southeast-1",
	})
	return envID
}

func TestEnvironmentUpEnqueuesJob(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/up", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	jobs, _ := d.Store.ListJobsByEnvironment(context.Background(), envID)
	if len(jobs) != 1 || jobs[0].Action != store.ActionUp {
		t.Fatalf("up job not enqueued: %+v", jobs)
	}
}

func TestEnvironmentDestroyPreviewEnqueuesJob(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	_ = d.Store.UpdateEnvironmentStatus(context.Background(), envID, store.EnvUp)

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/destroy-preview", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	jobs, _ := d.Store.ListJobsByEnvironment(context.Background(), envID)
	if len(jobs) != 1 || jobs[0].Action != store.ActionDestroyPreview {
		t.Fatalf("destroy preview job not enqueued: %+v", jobs)
	}
}

func TestEnvironmentRefreshEnqueuesJob(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	_ = d.Store.UpdateEnvironmentStatus(context.Background(), envID, store.EnvUp)

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/refresh", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	jobs, _ := d.Store.ListJobsByEnvironment(context.Background(), envID)
	if len(jobs) != 1 || jobs[0].Action != store.ActionRefresh {
		t.Fatalf("refresh job not enqueued: %+v", jobs)
	}
}

func TestEnvironmentDestroyRequiresPreviewWhenUp(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	ctx := context.Background()
	_ = d.Store.UpdateEnvironmentStatus(ctx, envID, store.EnvUp)

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/destroy", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	jobs, _ := d.Store.ListJobsByEnvironment(ctx, envID)
	if len(jobs) != 0 {
		t.Fatalf("direct destroy from up should not enqueue before preview: %+v", jobs)
	}

	_ = d.Store.UpdateEnvironmentStatus(ctx, envID, store.EnvDestroyPreviewReady)
	rec = authedPost(t, d, "/environments/"+itoa(envID)+"/destroy", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	jobs, _ = d.Store.ListJobsByEnvironment(ctx, envID)
	if len(jobs) != 1 || jobs[0].Action != store.ActionDestroy {
		t.Fatalf("confirmed destroy not enqueued: %+v", jobs)
	}
}

func TestCancelDestroyPreviewReturnsEnvironmentToUp(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	ctx := context.Background()
	_ = d.Store.UpdateEnvironmentStatus(ctx, envID, store.EnvDestroyPreviewReady)

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/cancel-destroy", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	env, _ := d.Store.GetEnvironment(ctx, envID)
	if env.Status != store.EnvUp {
		t.Fatalf("env status = %q, want up", env.Status)
	}
	jobs, _ := d.Store.ListJobsByEnvironment(ctx, envID)
	if len(jobs) != 0 {
		t.Fatalf("cancel destroy preview should not enqueue a job: %+v", jobs)
	}
}

func TestRetryReusesFailedAction(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	ctx := context.Background()
	// A failed destroy is the most recent job for the environment.
	jid, _ := d.Store.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionDestroy})
	_ = d.Store.UpdateJobStatus(ctx, jid, store.JobFailed)

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/retry", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	jobs, _ := d.Store.ListJobsByEnvironment(ctx, envID)
	if jobs[0].Action != store.ActionDestroy || jobs[0].Status != store.JobQueued {
		t.Fatalf("retry enqueued %+v, want a queued destroy (reuse failed action)", jobs[0])
	}
}

func TestEnvironmentStatusFragmentShowsConfirmButton(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	_ = d.Store.UpdateEnvironmentStatus(context.Background(), envID, store.EnvPreviewReady)

	req := httptest.NewRequest(http.MethodGet, "/environments/"+itoa(envID)+"/status", nil)
	req.AddCookie(d.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	NewRouter(d).ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "确认创建") {
		t.Fatalf("preview_ready status should show confirm button: %s", rec.Body.String())
	}
}

func TestEnvironmentStatusFragmentShowsRichOutputs(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	ctx := context.Background()
	_ = d.Store.UpdateEnvironmentStatus(ctx, envID, store.EnvUp)
	_ = d.Store.SetEnvironmentOutputs(ctx, envID, map[string]any{
		"public_ips":             []any{"52.1.2.3"},
		"public_dns":             []any{"ec2-52-1-2-3.compute.amazonaws.com"},
		"vpc_id":                 "vpc-123",
		"subnet_ids":             []any{"subnet-1", "subnet-2"},
		"rds_endpoint":           "db.example:3306",
		"rds_address":            "db.example",
		"rds_port":               float64(3306),
		"rds_username":           "admin",
		"redis_primary_endpoint": "redis.example",
		"redis_reader_endpoint":  "redis-ro.example",
		"redis_port":             float64(6379),
	})
	_ = d.Store.UpsertEnvironmentSecret(ctx, store.EnvironmentSecret{
		EnvironmentID: envID,
		Kind:          store.SecretRDSMySQL,
		Username:      "admin",
		Password:      "do-not-leak-in-status",
	})

	rec := authedGet(t, d, "/environments/"+itoa(envID)+"/status")
	body := rec.Body.String()
	for _, want := range []string{
		"EC2",
		"52.1.2.3",
		"网络",
		"vpc-123",
		"subnet-1",
		"db.example:3306",
		"admin",
		"redis.example",
		"6379",
		`/environments/` + itoa(envID) + `/rds-credentials`,
		"显示凭据",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("status fragment missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "do-not-leak-in-status") {
		t.Fatalf("status fragment must not expose generated DB secret: %s", body)
	}
	if strings.Contains(body, "password") || strings.Contains(body, "密码") {
		t.Fatalf("status fragment must not expose generated DB password: %s", body)
	}
}

func TestRevealRDSCredentialsReturnsStoredSecretNoStore(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	ctx := context.Background()
	_ = d.Store.UpdateEnvironmentStatus(ctx, envID, store.EnvUp)
	if err := d.Store.UpsertEnvironmentSecret(ctx, store.EnvironmentSecret{
		EnvironmentID: envID,
		Kind:          store.SecretRDSMySQL,
		Username:      "admin",
		Password:      "stored-rds-secret",
		Metadata: map[string]any{
			"host": "db.example",
			"port": float64(3306),
		},
	}); err != nil {
		t.Fatalf("UpsertEnvironmentSecret: %v", err)
	}

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/rds-credentials", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	body := rec.Body.String()
	for _, want := range []string{"admin", "stored-rds-secret", "db.example", "3306"} {
		if !strings.Contains(body, want) {
			t.Fatalf("credential reveal missing %q: %s", want, body)
		}
	}
}

func TestEnvironmentStatusFragmentShowsDestroyPreviewGate(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	ctx := context.Background()
	_ = d.Store.UpdateEnvironmentStatus(ctx, envID, store.EnvUp)

	rec := authedGet(t, d, "/environments/"+itoa(envID)+"/status")
	body := rec.Body.String()
	if !strings.Contains(body, "/destroy-preview") || !strings.Contains(body, "预演销毁") {
		t.Fatalf("up status should show destroy preview action: %s", body)
	}
	if strings.Contains(body, `action="/environments/`+itoa(envID)+`/destroy"`) {
		t.Fatalf("up status must not expose direct destroy action before preview: %s", body)
	}

	jobID, _ := d.Store.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionDestroyPreview})
	_ = d.Store.SetJobSummary(ctx, jobID, map[string]any{"deletes": 4})
	_ = d.Store.UpdateJobStatus(ctx, jobID, store.JobSucceeded)
	_ = d.Store.UpdateEnvironmentStatus(ctx, envID, store.EnvDestroyPreviewReady)

	rec = authedGet(t, d, "/environments/"+itoa(envID)+"/status")
	body = rec.Body.String()
	for _, want := range []string{"销毁预演", "4 个待删除", "确认销毁", "保留资源", `action="/environments/` + itoa(envID) + `/destroy"`, `action="/environments/` + itoa(envID) + `/cancel-destroy"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("destroy preview ready status missing %q: %s", want, body)
		}
	}
}

func TestEnvironmentStatusFragmentShowsRefreshActionAndSummary(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	ctx := context.Background()
	_ = d.Store.UpdateEnvironmentStatus(ctx, envID, store.EnvUp)

	jobID, _ := d.Store.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionRefresh})
	_ = d.Store.SetJobSummary(ctx, jobID, map[string]any{"creates": 0, "updates": 2, "deletes": 1, "sames": 4})
	_ = d.Store.UpdateJobStatus(ctx, jobID, store.JobSucceeded)

	rec := authedGet(t, d, "/environments/"+itoa(envID)+"/status")
	body := rec.Body.String()
	for _, want := range []string{"检测漂移", `action="/environments/` + itoa(envID) + `/refresh"`, "最近漂移检测", "0 创建 / 2 更新 / 1 删除 / 4 不变"} {
		if !strings.Contains(body, want) {
			t.Fatalf("refresh status missing %q: %s", want, body)
		}
	}
}
