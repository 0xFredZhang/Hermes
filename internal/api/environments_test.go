package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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
	if err := d.Store.UpdateEnvironmentStatus(context.Background(), envID, store.EnvPreviewReady); err != nil {
		t.Fatalf("UpdateEnvironmentStatus: %v", err)
	}

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/up", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	jobs, _ := d.Store.ListJobsByEnvironment(context.Background(), envID)
	if len(jobs) != 1 || jobs[0].Action != store.ActionUp {
		t.Fatalf("up job not enqueued: %+v", jobs)
	}
}

func TestPendingEnvironmentPreviewRecoveryEnqueuesJob(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/preview", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
	jobs, err := d.Store.ListJobsByEnvironment(context.Background(), envID)
	if err != nil {
		t.Fatalf("ListJobsByEnvironment: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Action != store.ActionPreview || jobs[0].Status != store.JobQueued {
		t.Fatalf("pending recovery did not enqueue preview job: %+v", jobs)
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

func TestEnvironmentDestroyPreviewFromPreviewReadyAndFailedEnqueuesJob(t *testing.T) {
	for _, status := range []string{store.EnvPreviewReady, store.EnvFailed} {
		t.Run(status, func(t *testing.T) {
			d := testDepsWithOrchestrator(t)
			envID := seedEnv(t, d)
			if err := d.Store.UpdateEnvironmentStatus(context.Background(), envID, status); err != nil {
				t.Fatalf("UpdateEnvironmentStatus: %v", err)
			}

			rec := authedPost(t, d, "/environments/"+itoa(envID)+"/destroy-preview", url.Values{})
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", rec.Code)
			}
			jobs, err := d.Store.ListJobsByEnvironment(context.Background(), envID)
			if err != nil {
				t.Fatalf("ListJobsByEnvironment: %v", err)
			}
			if len(jobs) != 1 || jobs[0].Action != store.ActionDestroyPreview || jobs[0].Status != store.JobQueued {
				t.Fatalf("destroy preview job from %s not enqueued: %+v", status, jobs)
			}
		})
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

func TestEnvironmentActionsRejectInvalidStates(t *testing.T) {
	tests := []struct {
		name   string
		status string
		path   string
	}{
		{name: "up before preview", status: store.EnvPending, path: "/up"},
		{name: "refresh before up", status: store.EnvPending, path: "/refresh"},
		{name: "destroy before destroy preview", status: store.EnvUp, path: "/destroy"},
		{name: "destroy preview while preview runs", status: store.EnvPreviewing, path: "/destroy-preview"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := testDepsWithOrchestrator(t)
			envID := seedEnv(t, d)
			if err := d.Store.UpdateEnvironmentStatus(context.Background(), envID, tt.status); err != nil {
				t.Fatalf("UpdateEnvironmentStatus: %v", err)
			}

			rec := authedPost(t, d, "/environments/"+itoa(envID)+tt.path, url.Values{})
			assertActionRedirectError(t, rec, envID, "当前环境状态不允许此操作，请刷新后重试")
			jobs, err := d.Store.ListJobsByEnvironment(context.Background(), envID)
			if err != nil {
				t.Fatalf("ListJobsByEnvironment: %v", err)
			}
			if len(jobs) != 0 {
				t.Fatalf("invalid action created jobs: %+v", jobs)
			}
		})
	}
}

func TestEnvironmentActionShowsBusyReason(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	ctx := context.Background()
	if err := d.Store.UpdateEnvironmentStatus(ctx, envID, store.EnvPreviewReady); err != nil {
		t.Fatalf("UpdateEnvironmentStatus: %v", err)
	}
	if _, err := d.Store.CreateJob(ctx, store.Job{
		EnvironmentID: envID,
		Action:        store.ActionRefresh,
		Status:        store.JobQueued,
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/up", url.Values{})
	assertActionRedirectError(t, rec, envID, "环境正在执行其他任务，请稍后再试")
	page := authedGet(t, d, rec.Header().Get("Location"))
	if !strings.Contains(page.Body.String(), "环境正在执行其他任务，请稍后再试") {
		t.Fatalf("redirected detail page did not show busy recovery message: %s", page.Body.String())
	}
	jobs, err := d.Store.ListJobsByEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("ListJobsByEnvironment: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Action != store.ActionRefresh {
		t.Fatalf("busy action changed jobs: %+v", jobs)
	}
	env, err := d.Store.GetEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if env.Status != store.EnvPreviewReady {
		t.Fatalf("busy action changed environment status to %q", env.Status)
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
	setEnvironmentLifecycleState(t, d, envID, store.EnvDestroyPreviewReady, store.EnvUp)

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

func TestCancelDestroyPreviewCannotRaceQueuedDestroy(t *testing.T) {
	t.Run("queued destroy blocks cancellation", func(t *testing.T) {
		d := testDepsWithOrchestrator(t)
		envID := seedEnv(t, d)
		ctx := context.Background()
		setEnvironmentLifecycleState(t, d, envID, store.EnvDestroyPreviewReady, store.EnvUp)
		if _, err := d.Store.CreateJob(ctx, store.Job{
			EnvironmentID: envID,
			Action:        store.ActionDestroy,
			Status:        store.JobQueued,
		}); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}

		rec := authedPost(t, d, "/environments/"+itoa(envID)+"/cancel-destroy", url.Values{})
		assertActionRedirectError(t, rec, envID, "环境正在执行其他任务，请稍后再试")
		env, err := d.Store.GetEnvironment(ctx, envID)
		if err != nil {
			t.Fatalf("GetEnvironment: %v", err)
		}
		if env.Status != store.EnvDestroyPreviewReady || env.ResumeStatus != store.EnvUp {
			t.Fatalf("cancel raced queued destroy and changed environment: %+v", env)
		}
		jobs, err := d.Store.ListJobsByEnvironment(ctx, envID)
		if err != nil {
			t.Fatalf("ListJobsByEnvironment: %v", err)
		}
		if len(jobs) != 1 || jobs[0].Action != store.ActionDestroy || jobs[0].Status != store.JobQueued {
			t.Fatalf("queued destroy changed during cancellation: %+v", jobs)
		}
	})

	t.Run("concurrent handler calls are linearizable", func(t *testing.T) {
		d := testDepsWithOrchestrator(t)
		envID := seedEnv(t, d)
		ctx := context.Background()
		setEnvironmentLifecycleState(t, d, envID, store.EnvDestroyPreviewReady, store.EnvUp)

		start := make(chan struct{})
		var destroyRec, cancelRec *httptest.ResponseRecorder
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			destroyRec = authedPost(t, d, "/environments/"+itoa(envID)+"/destroy", url.Values{})
		}()
		go func() {
			defer wg.Done()
			<-start
			cancelRec = authedPost(t, d, "/environments/"+itoa(envID)+"/cancel-destroy", url.Values{})
		}()
		close(start)
		wg.Wait()

		if destroyRec.Code != http.StatusSeeOther || cancelRec.Code != http.StatusSeeOther {
			t.Fatalf("handler statuses: destroy=%d cancel=%d, want both 303", destroyRec.Code, cancelRec.Code)
		}
		env, err := d.Store.GetEnvironment(ctx, envID)
		if err != nil {
			t.Fatalf("GetEnvironment: %v", err)
		}
		jobs, err := d.Store.ListJobsByEnvironment(ctx, envID)
		if err != nil {
			t.Fatalf("ListJobsByEnvironment: %v", err)
		}
		switch env.Status {
		case store.EnvUp:
			if len(jobs) != 0 {
				t.Fatalf("cancel won but destroy job exists: env=%+v jobs=%+v", env, jobs)
			}
		case store.EnvDestroying:
			if len(jobs) != 1 || jobs[0].Action != store.ActionDestroy || jobs[0].Status != store.JobQueued {
				t.Fatalf("destroy won without one queued destroy job: env=%+v jobs=%+v", env, jobs)
			}
		default:
			t.Fatalf("concurrent handlers left non-linearizable state: env=%+v jobs=%+v", env, jobs)
		}
	})
}

func TestRetryReusesFailedAction(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	ctx := context.Background()
	// A failed destroy is the most recent job for the environment.
	if err := d.Store.UpdateEnvironmentStatus(ctx, envID, store.EnvFailed); err != nil {
		t.Fatalf("UpdateEnvironmentStatus: %v", err)
	}
	if _, err := d.Store.CreateJob(ctx, store.Job{
		EnvironmentID: envID,
		Action:        store.ActionDestroy,
		Status:        store.JobFailed,
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/retry", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	jobs, _ := d.Store.ListJobsByEnvironment(ctx, envID)
	if jobs[0].Action != store.ActionDestroy || jobs[0].Status != store.JobQueued {
		t.Fatalf("retry enqueued %+v, want a queued destroy (reuse failed action)", jobs[0])
	}
}

func TestRetryDoesNotDefaultToUp(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	ctx := context.Background()
	if err := d.Store.UpdateEnvironmentStatus(ctx, envID, store.EnvFailed); err != nil {
		t.Fatalf("UpdateEnvironmentStatus: %v", err)
	}

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/retry", url.Values{})
	assertActionRedirectError(t, rec, envID, "没有可重试的失败任务")
	jobs, err := d.Store.ListJobsByEnvironment(ctx, envID)
	if err != nil {
		t.Fatalf("ListJobsByEnvironment: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("retry without failed history defaulted to a new action: %+v", jobs)
	}
}

func TestEnvironmentDetailStreamsOnlyActiveJob(t *testing.T) {
	tests := []struct {
		name       string
		jobStatus  string
		wantStream bool
	}{
		{name: "no job"},
		{name: "queued", jobStatus: store.JobQueued, wantStream: true},
		{name: "running", jobStatus: store.JobRunning, wantStream: true},
		{name: "succeeded", jobStatus: store.JobSucceeded},
		{name: "failed", jobStatus: store.JobFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := testDepsWithOrchestrator(t)
			envID := seedEnv(t, d)
			var jobID int64
			if tt.jobStatus != "" {
				var err error
				jobID, err = d.Store.CreateJob(context.Background(), store.Job{
					EnvironmentID: envID,
					Action:        store.ActionPreview,
					Status:        tt.jobStatus,
				})
				if err != nil {
					t.Fatalf("CreateJob: %v", err)
				}
				if err := d.Store.SetJobLogs(context.Background(), jobID, "persisted job log"); err != nil {
					t.Fatalf("SetJobLogs: %v", err)
				}
				environmentStatus := store.EnvPreviewing
				if tt.jobStatus == store.JobSucceeded {
					environmentStatus = store.EnvPreviewReady
				} else if tt.jobStatus == store.JobFailed {
					environmentStatus = store.EnvFailed
				}
				if err := d.Store.UpdateEnvironmentStatus(context.Background(), envID, environmentStatus); err != nil {
					t.Fatalf("UpdateEnvironmentStatus: %v", err)
				}
			}

			body := authedGet(t, d, "/environments/"+itoa(envID)).Body.String()
			streamMarker := `new EventSource("/jobs/` + itoa(jobID) + `/logs/stream")`
			escapedStreamMarker := strings.ReplaceAll(streamMarker, "/", `\/`)
			streamPresent := strings.Contains(body, streamMarker) || strings.Contains(body, escapedStreamMarker)
			if streamPresent != tt.wantStream {
				t.Fatalf("EventSource present = %v, want %v for %s Job: %s", streamPresent, tt.wantStream, tt.jobStatus, body)
			}
			wantPersistedLogs := 0
			if tt.jobStatus == store.JobSucceeded || tt.jobStatus == store.JobFailed {
				wantPersistedLogs = 1
			}
			if got := strings.Count(body, "persisted job log"); got != wantPersistedLogs {
				t.Fatalf("persisted log count = %d, want %d for %s Job: %s", got, wantPersistedLogs, tt.jobStatus, body)
			}
		})
	}
}

func assertActionRedirectError(t *testing.T, rec *httptest.ResponseRecorder, envID int64, want string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect Location: %v", err)
	}
	if location.Path != "/environments/"+itoa(envID) {
		t.Fatalf("redirect path = %q, want environment detail", location.Path)
	}
	if got := location.Query().Get("error"); got != want {
		t.Fatalf("redirect error = %q, want %q", got, want)
	}
}

func setEnvironmentLifecycleState(t *testing.T, d Deps, envID int64, status, resumeStatus string) {
	t.Helper()
	if _, err := d.Store.DB().ExecContext(context.Background(),
		`UPDATE environments SET status = ?, resume_status = ? WHERE id = ?`,
		status, resumeStatus, envID,
	); err != nil {
		t.Fatalf("set environment lifecycle state: %v", err)
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

func TestPendingEnvironmentStatusOffersPreviewRecovery(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	body := authedGet(t, d, "/environments/"+itoa(envID)+"/status").Body.String()

	if !strings.Contains(body, `action="/environments/`+itoa(envID)+`/preview"`) || !strings.Contains(body, "重试预演") {
		t.Fatalf("pending status lacks preview recovery action: %s", body)
	}
	if strings.Contains(body, `action="/environments/`+itoa(envID)+`/destroy-preview"`) || strings.Contains(body, `action="/environments/`+itoa(envID)+`/destroy"`) {
		t.Fatalf("pending status exposed an action rejected by policy: %s", body)
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
	_ = d.Store.UpsertEnvironmentSecret(ctx, store.EnvironmentSecret{
		EnvironmentID: envID,
		Kind:          store.SecretRedisAuth,
		Username:      "default",
		Password:      "do-not-leak-redis-token",
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
		`/environments/` + itoa(envID) + `/redis-credentials`,
		"显示凭据",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("status fragment missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "do-not-leak-in-status") {
		t.Fatalf("status fragment must not expose generated DB secret: %s", body)
	}
	if strings.Contains(body, "do-not-leak-redis-token") {
		t.Fatalf("status fragment must not expose generated Redis token: %s", body)
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

func TestRevealRedisCredentialsReturnsStoredSecretNoStore(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	ctx := context.Background()
	_ = d.Store.UpdateEnvironmentStatus(ctx, envID, store.EnvUp)
	if err := d.Store.UpsertEnvironmentSecret(ctx, store.EnvironmentSecret{
		EnvironmentID: envID,
		Kind:          store.SecretRedisAuth,
		Username:      "default",
		Password:      "stored-redis-token",
		Metadata: map[string]any{
			"primary_endpoint": "redis.example",
			"port":             float64(6379),
		},
	}); err != nil {
		t.Fatalf("UpsertEnvironmentSecret: %v", err)
	}

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/redis-credentials", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	body := rec.Body.String()
	for _, want := range []string{"default", "stored-redis-token", "redis.example", "6379"} {
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

func TestEnvironmentStatusFragmentUsesDestroyPreviewForPreDestroyStates(t *testing.T) {
	for _, status := range []string{store.EnvPreviewReady, store.EnvFailed} {
		t.Run(status, func(t *testing.T) {
			d := testDepsWithOrchestrator(t)
			envID := seedEnv(t, d)
			if err := d.Store.UpdateEnvironmentStatus(context.Background(), envID, status); err != nil {
				t.Fatalf("UpdateEnvironmentStatus: %v", err)
			}

			body := authedGet(t, d, "/environments/"+itoa(envID)+"/status").Body.String()
			if !strings.Contains(body, `action="/environments/`+itoa(envID)+`/destroy-preview"`) || !strings.Contains(body, "预演销毁") {
				t.Fatalf("%s status must expose destroy preview action: %s", status, body)
			}
			if strings.Contains(body, `action="/environments/`+itoa(envID)+`/destroy"`) {
				t.Fatalf("%s status exposed direct destroy before destroy preview: %s", status, body)
			}
		})
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
