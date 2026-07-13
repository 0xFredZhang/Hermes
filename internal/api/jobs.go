package api

import (
	"context"
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
	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePositivePathID(w, r)
		if !ok {
			return
		}
		job, err := d.Store.GetJob(r.Context(), id)
		if err != nil {
			writeStoreReadError(w, r, err, "load job")
			return
		}
		environment, err := d.Store.GetEnvironment(r.Context(), job.EnvironmentID)
		if err != nil {
			writeStoreReadError(w, r, err, "load environment")
			return
		}
		d.Renderer.Render(w, "job_detail", map[string]any{
			"PageTitle":   "Job 详情",
			"ActiveNav":   "environments",
			"Environment": environment,
			"Job":         jobViewFromJob(job),
			"Logs":        job.Logs,
		})
	})

	mux.HandleFunc("GET /jobs/{id}/logs/stream", func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePositivePathID(w, r)
		if !ok {
			return
		}
		job, err := d.Store.GetJob(r.Context(), id)
		if err != nil {
			writeStoreReadError(w, r, err, "load job")
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

		history, ch, done, cancel := d.Broker.Subscribe(id)
		defer cancel()

		// Completion can commit between the first DB read and Subscribe. Recheck
		// so a topic created after the worker released its topic cannot hang.
		latest, err := d.Store.GetJob(r.Context(), id)
		if err != nil {
			cancel()
			writeStoreReadError(w, r, err, "reload job")
			return
		}
		if isTerminalJob(latest.Status) {
			cancel()
			d.Broker.Close(id)
			writeTerminalJobStream(w, latest.Logs)
			return
		}

		prepareEventStream(w)
		for _, line := range history {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
		rc := http.NewResponseController(w)
		_ = rc.Flush()
		if done {
			writeJobStreamEnd(r.Context(), w, rc, d, id)
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case line, ok := <-ch:
				if !ok {
					writeJobStreamEnd(r.Context(), w, rc, d, id)
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
				_ = rc.Flush()
			}
		}
	})
}

type jobView struct {
	ID            int64
	EnvironmentID int64
	Action        string
	ActionLabel   string
	Status        string
	StatusLabel   string
	StatusTone    string
	Summary       string
	Error         string
	ErrorExcerpt  string
	QueuedAt      string
	StartedAt     string
	FinishedAt    string
	Duration      string
	Active        bool
}

func jobViewFromSummary(job store.JobSummary) jobView {
	return newJobView(
		job.ID,
		job.EnvironmentID,
		job.Action,
		job.Status,
		job.Summary,
		job.Error,
		job.StartedAt,
		job.FinishedAt,
		job.CreatedAt,
	)
}

func jobViewFromJob(job store.Job) jobView {
	return newJobView(
		job.ID,
		job.EnvironmentID,
		job.Action,
		job.Status,
		job.Summary,
		job.Error,
		job.StartedAt,
		job.FinishedAt,
		job.CreatedAt,
	)
}

func newJobView(
	id int64,
	environmentID int64,
	action string,
	status string,
	summary map[string]any,
	jobError string,
	startedAt sql.NullTime,
	finishedAt sql.NullTime,
	createdAt time.Time,
) jobView {
	return jobView{
		ID:            id,
		EnvironmentID: environmentID,
		Action:        action,
		ActionLabel:   jobActionLabel(action),
		Status:        status,
		StatusLabel:   jobStatusLabel(status),
		StatusTone:    jobStatusTone(status),
		Summary:       formatChangeSummary(summary),
		Error:         jobError,
		ErrorExcerpt:  truncateRunes(jobError, 120),
		QueuedAt:      formatJobTime(createdAt),
		StartedAt:     formatNullJobTime(startedAt),
		FinishedAt:    formatNullJobTime(finishedAt),
		Duration:      formatJobDuration(startedAt, finishedAt, status),
		Active:        status == store.JobQueued || status == store.JobRunning,
	}
}

func jobViews(summaries []store.JobSummary) []jobView {
	views := make([]jobView, 0, len(summaries))
	for _, summary := range summaries {
		views = append(views, jobViewFromSummary(summary))
	}
	return views
}

func jobActionLabel(action string) string {
	switch action {
	case store.ActionPreview:
		return "预演创建"
	case store.ActionUp:
		return "创建资源"
	case store.ActionRefresh:
		return "检测漂移"
	case store.ActionDestroyPreview:
		return "预演销毁"
	case store.ActionDestroy:
		return "销毁资源"
	default:
		return "未知操作"
	}
}

func jobStatusLabel(status string) string {
	switch status {
	case store.JobQueued:
		return "排队中"
	case store.JobRunning:
		return "执行中"
	case store.JobSucceeded:
		return "成功"
	case store.JobFailed:
		return "失败"
	default:
		return "未知状态"
	}
}

func jobStatusTone(status string) string {
	switch status {
	case store.JobQueued, store.JobRunning:
		return "active"
	case store.JobSucceeded:
		return "success"
	case store.JobFailed:
		return "danger"
	default:
		return "neutral"
	}
}

func formatJobTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Format("2006-01-02 15:04:05")
}

func formatNullJobTime(value sql.NullTime) string {
	if !value.Valid {
		return "-"
	}
	return formatJobTime(value.Time)
}

func formatJobDuration(startedAt, finishedAt sql.NullTime, status string) string {
	if !startedAt.Valid {
		return "-"
	}
	if !finishedAt.Valid {
		if status == store.JobRunning {
			return "进行中"
		}
		return "-"
	}
	duration := finishedAt.Time.Sub(startedAt.Time)
	if duration < 0 {
		return "-"
	}
	seconds := int64(duration.Round(time.Second) / time.Second)
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	remainingSeconds := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%d时%d分%d秒", hours, minutes, remainingSeconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%d分%d秒", minutes, remainingSeconds)
	}
	return fmt.Sprintf("%d秒", remainingSeconds)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func parsePositivePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func writeStoreReadError(w http.ResponseWriter, r *http.Request, err error, operation string) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	http.Error(w, operation, http.StatusInternalServerError)
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

func writeJobStreamEnd(ctx context.Context, w http.ResponseWriter, rc *http.ResponseController, d Deps, jobID int64) {
	job, err := d.Store.GetJob(ctx, jobID)
	if err == nil && isTerminalJob(job.Status) {
		d.Broker.Close(jobID)
		writeDoneEvent(w, rc)
		return
	}
	writeInterruptedEvent(w, rc)
}

func writeInterruptedEvent(w http.ResponseWriter, rc *http.ResponseController) {
	fmt.Fprint(w, "event: interrupted\ndata: stream interrupted\n\n")
	_ = rc.Flush()
}

func isTerminalJob(status string) bool {
	return status == store.JobSucceeded || status == store.JobFailed
}
