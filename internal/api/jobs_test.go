package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xFredZhang/Hermes/internal/store"
)

func TestJobLogStreamReturnsPersistedTerminalLogs(t *testing.T) {
	for _, status := range []string{store.JobSucceeded, store.JobFailed} {
		t.Run(status, func(t *testing.T) {
			d := testDepsWithOrchestrator(t)
			envID := seedEnv(t, d)
			jobID, err := d.Store.CreateJob(context.Background(), store.Job{
				EnvironmentID: envID,
				Action:        store.ActionPreview,
				Status:        status,
			})
			if err != nil {
				t.Fatalf("CreateJob: %v", err)
			}
			if err := d.Store.SetJobLogs(context.Background(), jobID, "hello\nworld"); err != nil {
				t.Fatalf("SetJobLogs: %v", err)
			}

			rec := serveJobStream(t, d, jobID)
			body := rec.Body.String()
			if !strings.Contains(body, "data: hello") || !strings.Contains(body, "data: world") {
				t.Fatalf("missing persisted replay lines: %s", body)
			}
			if !strings.Contains(body, "event: done") {
				t.Fatalf("missing done event: %s", body)
			}
			if got := activeBrokerTopics(d.Broker); got != 0 {
				t.Fatalf("terminal stream created %d broker topics", got)
			}
		})
	}
}

func TestJobLogStreamRejectsUnknownJob(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	rec := serveJobStream(t, d, 99999)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := activeBrokerTopics(d.Broker); got != 0 {
		t.Fatalf("unknown stream created %d broker topics", got)
	}
}

func TestJobLogStreamSealedActiveTopicEmitsInterruptionNotDone(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	jobID, err := d.Store.CreateJob(context.Background(), store.Job{
		EnvironmentID: envID,
		Action:        store.ActionPreview,
		Status:        store.JobQueued,
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := d.Store.SetJobLogs(context.Background(), jobID, "stale persisted active log"); err != nil {
		t.Fatalf("SetJobLogs: %v", err)
	}
	writer := d.Broker.Writer(jobID)
	if _, err := writer.Write([]byte("live broker log\n")); err != nil {
		t.Fatalf("broker write: %v", err)
	}
	// A sealed topic gives this handler test a deterministic end-of-stream
	// boundary without changing the Job's active database state.
	d.Broker.Seal(jobID)

	rec := serveJobStream(t, d, jobID)
	body := rec.Body.String()
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("active stream Content-Type = %q, want text/event-stream", got)
	}
	if !strings.Contains(body, "data: live broker log") || strings.Contains(body, "stale persisted active log") {
		t.Fatalf("active stream did not use broker history exclusively: %q", body)
	}
	if !strings.Contains(body, "event: interrupted") {
		t.Fatalf("active sealed stream missing interrupted event: %q", body)
	}
	if strings.Contains(body, "event: done") {
		t.Fatalf("active sealed stream falsely emitted durable completion: %q", body)
	}
	if got := activeBrokerTopics(d.Broker); got != 1 {
		t.Fatalf("active stream topic count = %d, want 1 until terminal persistence", got)
	}
	d.Broker.Close(jobID)
	if got := activeBrokerTopics(d.Broker); got != 0 {
		t.Fatalf("active stream cleanup retained %d topics", got)
	}
}

func TestJobLogStreamChannelCloseWithActiveDatabaseStateEmitsInterruption(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	jobID, err := d.Store.CreateJob(context.Background(), store.Job{
		EnvironmentID: envID,
		Action:        store.ActionRefresh,
		Status:        store.JobRunning,
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	writer := d.Broker.Writer(jobID)
	if _, err := writer.Write([]byte("before seal\n")); err != nil {
		t.Fatalf("broker write: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/jobs/"+itoa(jobID)+"/logs/stream", nil).WithContext(ctx)
	req.AddCookie(d.Auth.IssueCookie())
	rec := &flushCallbackRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		onFirstFlush:     func() { d.Broker.Seal(jobID) },
	}
	done := make(chan struct{})
	go func() {
		NewRouter(d).ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("stream did not terminate after broker channel close")
	}

	body := rec.Body.String()
	if !strings.Contains(body, "data: before seal") || !strings.Contains(body, "event: interrupted") {
		t.Fatalf("channel-close stream body = %q", body)
	}
	if strings.Contains(body, "event: done") {
		t.Fatalf("active database state falsely emitted durable completion: %q", body)
	}
}

func TestJobLogStreamReadFailureAfterHeadersEmitsInterruption(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	jobID, err := d.Store.CreateJob(context.Background(), store.Job{
		EnvironmentID: envID,
		Action:        store.ActionUp,
		Status:        store.JobRunning,
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	writer := d.Broker.Writer(jobID)
	if _, err := writer.Write([]byte("persist attempt failed\n")); err != nil {
		t.Fatalf("broker write: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/jobs/"+itoa(jobID)+"/logs/stream", nil).WithContext(ctx)
	req.AddCookie(d.Auth.IssueCookie())
	var closeErr error
	rec := &flushCallbackRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		onFirstFlush: func() {
			closeErr = d.Store.Close()
			d.Broker.Seal(jobID)
		},
	}
	done := make(chan struct{})
	go func() {
		NewRouter(d).ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("stream did not terminate after store failure")
	}
	if closeErr != nil {
		t.Fatalf("close store: %v", closeErr)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: interrupted") {
		t.Fatalf("store-failure stream missing interrupted event: %q", body)
	}
	if strings.Contains(body, "event: done") {
		t.Fatalf("store failure after headers falsely emitted completion: %q", body)
	}
}

func TestJobLogStreamChannelCloseAfterDurableTerminalStateEmitsDone(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	ctx := context.Background()
	envID := seedEnv(t, d)
	jobID, err := d.Store.CreateJob(ctx, store.Job{
		EnvironmentID: envID,
		Action:        store.ActionPreview,
		Status:        store.JobRunning,
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	writer := d.Broker.Writer(jobID)
	if _, err := writer.Write([]byte("terminal line\n")); err != nil {
		t.Fatalf("broker write: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/jobs/"+itoa(jobID)+"/logs/stream", nil)
	req.AddCookie(d.Auth.IssueCookie())
	var persistErr error
	rec := &flushCallbackRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		onFirstFlush: func() {
			persistErr = d.Store.UpdateJobStatus(ctx, jobID, store.JobSucceeded)
			d.Broker.Seal(jobID)
		},
	}
	NewRouter(d).ServeHTTP(rec, req)
	if persistErr != nil {
		t.Fatalf("persist terminal status: %v", persistErr)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: done") || strings.Contains(body, "event: interrupted") {
		t.Fatalf("durable terminal stream body = %q", body)
	}
	if got := activeBrokerTopics(d.Broker); got != 0 {
		t.Fatalf("durable terminal stream retained %d broker topics", got)
	}
}

func TestJobLogStreamReleasesSealedTopicAfterPersistence(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	jobID, err := d.Store.CreateJob(context.Background(), store.Job{
		EnvironmentID: envID,
		Action:        store.ActionUp,
		Status:        store.JobFailed,
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := d.Store.SetJobLogs(context.Background(), jobID, "persisted terminal log"); err != nil {
		t.Fatalf("SetJobLogs: %v", err)
	}
	writer := d.Broker.Writer(jobID)
	if _, err := writer.Write([]byte("unpersisted exceptional log\n")); err != nil {
		t.Fatalf("broker write: %v", err)
	}
	d.Broker.Seal(jobID)

	rec := serveJobStream(t, d, jobID)
	if body := rec.Body.String(); !strings.Contains(body, "persisted terminal log") || strings.Contains(body, "unpersisted exceptional log") {
		t.Fatalf("terminal replay body = %q", body)
	}
	if got := activeBrokerTopics(d.Broker); got != 0 {
		t.Fatalf("terminal replay retained %d sealed topics", got)
	}
}

func TestJobDetailShowsMetadataSummaryErrorAndLogs(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	ctx := context.Background()
	envID := seedEnv(t, d)
	jobID, err := d.Store.CreateJob(ctx, store.Job{
		EnvironmentID: envID,
		Action:        store.ActionRefresh,
		Status:        store.JobFailed,
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := d.Store.SetJobSummary(ctx, jobID, map[string]any{
		"creates": 1, "updates": 2, "deletes": 3, "sames": 4,
	}); err != nil {
		t.Fatalf("SetJobSummary: %v", err)
	}
	if err := d.Store.SetJobError(ctx, jobID, "刷新失败：请检查 Pulumi 状态锁并返回环境重试"); err != nil {
		t.Fatalf("SetJobError: %v", err)
	}
	logs := "first diagnostic line\n<script>alert('unsafe')</script>\nlast diagnostic line"
	if err := d.Store.SetJobLogs(ctx, jobID, logs); err != nil {
		t.Fatalf("SetJobLogs: %v", err)
	}
	if _, err := d.Store.DB().ExecContext(ctx, `
		UPDATE jobs
		SET created_at = '2026-07-13 09:00:00',
		    started_at = '2026-07-13 09:00:02',
		    finished_at = '2026-07-13 09:02:05'
		WHERE id = ?
	`, jobID); err != nil {
		t.Fatalf("set Job timestamps: %v", err)
	}

	rec := authedGet(t, d, "/jobs/"+itoa(jobID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Job #" + itoa(jobID), "检测漂移", "refresh", "失败", "failed",
		"排队时间", "开始时间", "结束时间", "耗时", "2分3秒",
		"1 创建 / 2 更新 / 3 删除 / 4 不变",
		"刷新失败：请检查 Pulumi 状态锁并返回环境重试",
		"first diagnostic line", "last diagnostic line", "&lt;script&gt;alert(&#39;unsafe&#39;)&lt;/script&gt;",
		`href="/environments/` + itoa(envID) + `"`, `hidden data-copy-log`, `aria-controls="job-log"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("Job detail missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "<script>alert('unsafe')</script>") {
		t.Fatalf("Job logs were not contextually escaped: %s", body)
	}
}

func TestJobDetailReturns404ForUnknownJob(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	rec := authedGet(t, d, "/jobs/99999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestActiveJobDetailDoesNotDescribeLogsAsCompletedSnapshot(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	jobID, err := d.Store.CreateJob(context.Background(), store.Job{
		EnvironmentID: envID,
		Action:        store.ActionPreview,
		Status:        store.JobQueued,
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	body := authedGet(t, d, "/jobs/"+itoa(jobID)).Body.String()
	for _, want := range []string{"任务仍在执行", "暂无持久化日志"} {
		if !strings.Contains(body, want) {
			t.Fatalf("active Job detail missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "任务完成时持久化的日志快照") {
		t.Fatalf("active Job detail used terminal snapshot copy: %s", body)
	}
}

func TestJobDetailRejectsMalformedID(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	rec := authedGet(t, d, "/jobs/not-a-number")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestJobDetailReturnsSafe500ForStoreFailure(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	if _, err := d.Store.DB().ExecContext(context.Background(), `ALTER TABLE jobs RENAME TO unavailable_jobs`); err != nil {
		t.Fatalf("make jobs table unavailable: %v", err)
	}
	rec := authedGet(t, d, "/jobs/1")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "load job") || strings.Contains(body, "no such table") {
		t.Fatalf("unsafe operational error response: %s", body)
	}
}

func serveJobStream(t *testing.T, d Deps, jobID int64) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/jobs/"+itoa(jobID)+"/logs/stream", nil).WithContext(ctx)
	req.AddCookie(d.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		NewRouter(d).ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
		return rec
	case <-time.After(500 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("job log stream did not terminate")
		return nil
	}
}

func activeBrokerTopics(b any) int {
	return reflect.ValueOf(b).Elem().FieldByName("topics").Len()
}

type flushCallbackRecorder struct {
	*httptest.ResponseRecorder
	once         sync.Once
	onFirstFlush func()
}

func (r *flushCallbackRecorder) Flush() {
	r.once.Do(r.onFirstFlush)
	r.ResponseRecorder.Flush()
}
