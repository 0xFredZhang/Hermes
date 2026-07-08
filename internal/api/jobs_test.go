package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/store"
)

func TestJobLogStreamReplaysAndSignalsDone(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	jobID, _ := d.Store.CreateJob(context.Background(), store.Job{EnvironmentID: envID, Action: store.ActionPreview})

	w := d.Broker.Writer(jobID)
	fmt.Fprintln(w, "hello")
	fmt.Fprintln(w, "world")
	d.Broker.Close(jobID) // closed before request → handler replays history + done immediately

	req := httptest.NewRequest(http.MethodGet, "/jobs/"+itoa(jobID)+"/logs/stream", nil)
	req.AddCookie(d.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	NewRouter(d).ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "data: hello") || !strings.Contains(body, "data: world") {
		t.Fatalf("missing replayed lines: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("missing done event: %s", body)
	}
}
