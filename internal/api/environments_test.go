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
		"rds_endpoint":           "db.example:3306",
		"rds_address":            "db.example",
		"rds_port":               float64(3306),
		"rds_username":           "admin",
		"redis_primary_endpoint": "redis.example",
		"redis_reader_endpoint":  "redis-ro.example",
		"redis_port":             float64(6379),
	})

	rec := authedGet(t, d, "/environments/"+itoa(envID)+"/status")
	body := rec.Body.String()
	for _, want := range []string{
		"EC2",
		"52.1.2.3",
		"db.example:3306",
		"admin",
		"redis.example",
		"6379",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("status fragment missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "password") || strings.Contains(body, "密码") {
		t.Fatalf("status fragment must not expose generated DB password: %s", body)
	}
}
