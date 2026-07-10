package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/0xFredZhang/Hermes/internal/store"
)

func addJobRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /jobs/{id}/logs/stream", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		job, err := d.Store.GetJob(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "load job", http.StatusInternalServerError)
			return
		}
		if isTerminalJob(job.Status) {
			d.Broker.Close(id)
			writeTerminalJobStream(w, job.Logs)
			return
		}
		if job.Status != store.JobQueued && job.Status != store.JobRunning {
			http.Error(w, "job is not streamable", http.StatusConflict)
			return
		}

		prepareEventStream(w)
		history, ch, done, cancel := d.Broker.Subscribe(id)
		defer cancel()

		// Completion can commit between the first DB read and Subscribe. Recheck
		// so a topic created after the worker released its topic cannot hang.
		latest, err := d.Store.GetJob(r.Context(), id)
		if err == nil && isTerminalJob(latest.Status) {
			cancel()
			d.Broker.Close(id)
			writeTerminalJobEvents(w, latest.Logs)
			return
		}

		for _, line := range history {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
		rc := http.NewResponseController(w)
		_ = rc.Flush()
		if done {
			writeDoneEvent(w, rc)
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case line, ok := <-ch:
				if !ok {
					writeDoneEvent(w, rc)
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
				_ = rc.Flush()
			}
		}
	})
}

func prepareEventStream(w http.ResponseWriter) *http.ResponseController {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	rc := http.NewResponseController(w)
	// SSE is long-lived: clear the server's write deadline for this connection.
	// ResponseRecorder returns ErrNotSupported here, which is safe to ignore.
	_ = rc.SetWriteDeadline(time.Time{})
	return rc
}

func writeTerminalJobStream(w http.ResponseWriter, logs string) {
	prepareEventStream(w)
	writeTerminalJobEvents(w, logs)
}

func writeTerminalJobEvents(w http.ResponseWriter, logs string) {
	if logs != "" {
		for _, line := range strings.Split(strings.TrimSuffix(logs, "\n"), "\n") {
			fmt.Fprintf(w, "data: %s\n\n", strings.TrimSuffix(line, "\r"))
		}
	}
	rc := http.NewResponseController(w)
	writeDoneEvent(w, rc)
}

func writeDoneEvent(w http.ResponseWriter, rc *http.ResponseController) {
	fmt.Fprint(w, "event: done\ndata: end\n\n")
	_ = rc.Flush()
}

func isTerminalJob(status string) bool {
	return status == store.JobSucceeded || status == store.JobFailed
}
