package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/0xFredZhang/Hermes/internal/store"
)

func TestJobLogStreamReplaysTerminalLogsFromStore(t *testing.T) {
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

func TestJobLogStreamUnknownJobReturnsNotFoundWithoutTopic(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	rec := serveJobStream(t, d, 99999)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := activeBrokerTopics(d.Broker); got != 0 {
		t.Fatalf("unknown stream created %d broker topics", got)
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
