package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/0xFredZhang/Hermes/internal/store"
)

func addEnvironmentRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /environments", func(w http.ResponseWriter, r *http.Request) {
		list, err := d.Store.ListEnvironments(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.Renderer.Render(w, "environments", map[string]any{"Environments": list})
	})
	mux.HandleFunc("GET /environments/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		env, err := d.Store.GetEnvironment(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		jobs, _ := d.Store.ListJobsByEnvironment(r.Context(), id)
		d.Renderer.Render(w, "environment_detail", envViewData(env, jobs))
	})
	mux.HandleFunc("GET /environments/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		env, err := d.Store.GetEnvironment(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		jobs, _ := d.Store.ListJobsByEnvironment(r.Context(), id)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = d.Renderer.RenderPartial(w, "env_status", envViewData(env, jobs))
	})
	mux.HandleFunc("POST /environments/{id}/up", enqueueHandler(d, store.ActionUp))
	mux.HandleFunc("POST /environments/{id}/retry", retryHandler(d))
	mux.HandleFunc("POST /environments/{id}/destroy", enqueueHandler(d, store.ActionDestroy))
}

// retryHandler re-runs the action that actually failed, rather than always
// running "up" — retrying a failed destroy must not re-create the resources
// the user was tearing down.
func retryHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		action := store.ActionUp
		if jobs, err := d.Store.ListJobsByEnvironment(r.Context(), id); err == nil && len(jobs) > 0 {
			action = jobs[0].Action // newest job (DESC) = the one that failed
		}
		_, _ = d.Orchestrator.Enqueue(r.Context(), id, action)
		http.Redirect(w, r, "/environments/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	}
}

func enqueueHandler(d Deps, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		// Enqueue guards one-active-job-per-env; on busy/error we still redirect
		// and the status fragment reflects the true state.
		_, _ = d.Orchestrator.Enqueue(r.Context(), id, action)
		http.Redirect(w, r, "/environments/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	}
}

// envViewData is the template payload shared by the detail page and the status
// fragment: the environment, the latest job id/logs (for the SSE pane), a
// preview plan string, and a formatted public-IP list.
func envViewData(env store.Environment, jobs []store.Job) map[string]any {
	// Initialize every key the templates reference so missing values render as
	// empty strings (a map miss would otherwise print "<no value>").
	data := map[string]any{
		"Env": env, "CurrentJobID": int64(0), "CurrentLogs": "", "Plan": "", "PublicIPs": "",
	}
	if len(jobs) > 0 {
		data["CurrentJobID"] = jobs[0].ID // DESC order → newest first
		data["CurrentLogs"] = jobs[0].Logs
	}
	for _, j := range jobs {
		if j.Action == store.ActionPreview && j.Summary != nil {
			data["Plan"] = fmt.Sprintf("%v 个待创建", j.Summary["creates"])
			break
		}
	}
	if env.Outputs != nil {
		data["PublicIPs"] = formatIPs(env.Outputs["public_ips"])
	}
	return data
}

func formatIPs(v any) string {
	arr, ok := v.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(arr))
	for _, x := range arr {
		parts = append(parts, fmt.Sprintf("%v", x))
	}
	return strings.Join(parts, ", ")
}
