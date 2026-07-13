package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/0xFredZhang/Hermes/internal/orchestrator"
	"github.com/0xFredZhang/Hermes/internal/store"
)

func addEnvironmentRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /environments", func(w http.ResponseWriter, r *http.Request) {
		list, err := d.Store.ListEnvironments(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.Renderer.Render(w, "environments", map[string]any{
			"PageTitle":    "环境",
			"ActiveNav":    "environments",
			"Environments": list,
		})
	})
	mux.HandleFunc("GET /environments/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePositivePathID(w, r)
		if !ok {
			return
		}
		env, err := d.Store.GetEnvironment(r.Context(), id)
		if err != nil {
			writeStoreReadError(w, r, err, "load environment")
			return
		}
		jobs, err := d.Store.ListJobSummariesByEnvironment(r.Context(), id)
		if err != nil {
			http.Error(w, "load job history", http.StatusInternalServerError)
			return
		}
		data, err := envViewData(r.Context(), d, env, jobs)
		if err != nil {
			http.Error(w, "load environment details", http.StatusInternalServerError)
			return
		}
		data["Error"] = r.URL.Query().Get("error")
		d.Renderer.Render(w, "environment_detail", data)
	})
	mux.HandleFunc("GET /environments/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePositivePathID(w, r)
		if !ok {
			return
		}
		env, err := d.Store.GetEnvironment(r.Context(), id)
		if err != nil {
			writeStoreReadError(w, r, err, "load environment")
			return
		}
		jobs, err := d.Store.ListJobSummariesByEnvironment(r.Context(), id)
		if err != nil {
			http.Error(w, "load job history", http.StatusInternalServerError)
			return
		}
		data, err := envViewData(r.Context(), d, env, jobs)
		if err != nil {
			http.Error(w, "load environment details", http.StatusInternalServerError)
			return
		}
		writeHTMLPartial(w, d, "env_status", data)
	})
	mux.HandleFunc("GET /environments/{id}/jobs", func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePositivePathID(w, r)
		if !ok {
			return
		}
		env, err := d.Store.GetEnvironment(r.Context(), id)
		if err != nil {
			writeStoreReadError(w, r, err, "load environment")
			return
		}
		jobs, err := d.Store.ListJobSummariesByEnvironment(r.Context(), id)
		if err != nil {
			http.Error(w, "load job history", http.StatusInternalServerError)
			return
		}
		writeHTMLPartial(w, d, "job_history", jobHistoryData(env, jobs))
	})
	mux.HandleFunc("POST /environments/{id}/preview", enqueueHandler(d, store.ActionPreview))
	mux.HandleFunc("POST /environments/{id}/up", enqueueHandler(d, store.ActionUp))
	mux.HandleFunc("POST /environments/{id}/retry", retryHandler(d))
	mux.HandleFunc("POST /environments/{id}/refresh", enqueueHandler(d, store.ActionRefresh))
	mux.HandleFunc("POST /environments/{id}/rds-credentials", revealRDSCredentialsHandler(d))
	mux.HandleFunc("POST /environments/{id}/redis-credentials", revealRedisCredentialsHandler(d))
	mux.HandleFunc("POST /environments/{id}/destroy-preview", enqueueHandler(d, store.ActionDestroyPreview))
	mux.HandleFunc("POST /environments/{id}/cancel-destroy", cancelDestroyHandler(d))
	mux.HandleFunc("POST /environments/{id}/destroy", destroyHandler(d))
}

// retryHandler delegates failed-action selection to the orchestrator so the
// HTTP layer cannot guess or substitute a lifecycle action.
func retryHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		_, err := d.Orchestrator.Retry(r.Context(), id)
		redirectLifecycleResult(w, r, environmentPath(id), err)
	}
}

func enqueueHandler(d Deps, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		_, err := d.Orchestrator.Enqueue(r.Context(), id, action)
		redirectLifecycleResult(w, r, environmentPath(id), err)
	}
}

func destroyHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		_, err := d.Orchestrator.Enqueue(r.Context(), id, store.ActionDestroy)
		redirectLifecycleResult(w, r, environmentPath(id), err)
	}
}

func cancelDestroyHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		err := d.Orchestrator.CancelDestroyPreview(r.Context(), id)
		redirectLifecycleResult(w, r, environmentPath(id), err)
	}
}

func environmentPath(id int64) string {
	return "/environments/" + strconv.FormatInt(id, 10)
}

func redirectLifecycleResult(w http.ResponseWriter, r *http.Request, target string, err error) {
	if err != nil {
		redirectErrorMessage(w, r, target, lifecycleErrorMessage(err))
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func redirectErrorMessage(w http.ResponseWriter, r *http.Request, target, message string) {
	query := url.Values{"error": {message}}
	http.Redirect(w, r, target+"?"+query.Encode(), http.StatusSeeOther)
}

func lifecycleErrorMessage(err error) string {
	switch {
	case errors.Is(err, orchestrator.ErrEnvironmentBusy), errors.Is(err, store.ErrActiveJob):
		return "环境正在执行其他任务，请稍后再试"
	case errors.Is(err, orchestrator.ErrNoFailedJob):
		return "没有可重试的失败任务"
	case errors.Is(err, orchestrator.ErrInvalidAction), errors.Is(err, store.ErrInvalidAction):
		return "不支持该操作，请刷新后重试"
	case errors.Is(err, orchestrator.ErrInvalidTransition), errors.Is(err, store.ErrStaleTransition), errors.Is(err, sql.ErrNoRows):
		return "当前环境状态不允许此操作，请刷新后重试"
	case errors.Is(err, orchestrator.ErrOrchestratorDegraded):
		return "任务服务暂不可用，请稍后重试"
	default:
		return "任务启动失败，请稍后重试"
	}
}

func revealRDSCredentialsHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if env, err := d.Store.GetEnvironment(r.Context(), id); err != nil || env.Status != store.EnvUp {
			http.Error(w, "RDS credentials are not available", http.StatusNotFound)
			return
		}
		secret, err := d.Store.GetEnvironmentSecret(r.Context(), id, store.SecretRDSMySQL)
		if err != nil {
			http.Error(w, "RDS credentials are not available", http.StatusNotFound)
			return
		}
		data := map[string]any{
			"Username": secret.Username,
			"Password": secret.Password,
			"Host":     formatScalar(secret.Metadata["host"]),
			"Port":     formatScalar(secret.Metadata["port"]),
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = d.Renderer.RenderPartial(w, "rds_credentials", data)
	}
}

func revealRedisCredentialsHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if env, err := d.Store.GetEnvironment(r.Context(), id); err != nil || env.Status != store.EnvUp {
			http.Error(w, "Redis credentials are not available", http.StatusNotFound)
			return
		}
		secret, err := d.Store.GetEnvironmentSecret(r.Context(), id, store.SecretRedisAuth)
		if err != nil {
			http.Error(w, "Redis credentials are not available", http.StatusNotFound)
			return
		}
		data := map[string]any{
			"Username": secret.Username,
			"Token":    secret.Password,
			"Host":     formatScalar(secret.Metadata["primary_endpoint"]),
			"Port":     formatScalar(secret.Metadata["port"]),
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = d.Renderer.RenderPartial(w, "redis_credentials", data)
	}
}

// envViewData is the template payload shared by the detail page and status
// fragment: the environment, lightweight job history, active stream metadata,
// plan summaries, and formatted outputs.
func envViewData(ctx context.Context, d Deps, env store.Environment, jobs []store.JobSummary) (map[string]any, error) {
	history := jobHistoryData(env, jobs)
	// Initialize every key the templates reference so missing values render as
	// empty strings (a map miss would otherwise print "<no value>").
	data := map[string]any{
		"PageTitle": "环境详情", "ActiveNav": "environments",
		"Env": env, "CurrentJobActive": false,
		"StatusPolling":       environmentStatusNeedsPolling(env.Status),
		"CurrentJobStreamURL": "", "CurrentJob": jobView{}, "Plan": "",
		"DestroyPlan": "",
		"RefreshPlan": "",
		"PublicIPs":   "", "PublicDNS": "",
		"VPCID": "", "SubnetIDs": "",
		"RDSEndpoint": "", "RDSAddress": "", "RDSPort": "", "RDSUsername": "",
		"HasRDSSecret":  false,
		"RedisEndpoint": "", "RedisReader": "", "RedisPort": "",
		"HasRedisSecret": false,
		"Jobs":           history["Jobs"],
		"HasActiveJobs":  history["HasActiveJobs"],
	}
	for _, job := range jobs {
		if job.Status == store.JobQueued || job.Status == store.JobRunning {
			data["CurrentJobActive"] = true
			data["StatusPolling"] = true
			data["CurrentJobStreamURL"] = "/jobs/" + strconv.FormatInt(job.ID, 10) + "/logs/stream"
			data["CurrentJob"] = jobViewFromSummary(job)
			break
		}
	}
	for _, j := range jobs {
		if j.Action == store.ActionPreview && j.Summary != nil {
			data["Plan"] = fmt.Sprintf("%v 个待创建", j.Summary["creates"])
			break
		}
	}
	for _, j := range jobs {
		if j.Action == store.ActionDestroyPreview && j.Summary != nil {
			data["DestroyPlan"] = fmt.Sprintf("%v 个待删除", j.Summary["deletes"])
			break
		}
	}
	for _, j := range jobs {
		if j.Action == store.ActionRefresh && j.Summary != nil {
			data["RefreshPlan"] = formatChangeSummary(j.Summary)
			break
		}
	}
	if env.Outputs != nil {
		data["PublicIPs"] = formatIPs(env.Outputs["public_ips"])
		data["PublicDNS"] = formatIPs(env.Outputs["public_dns"])
		data["VPCID"] = formatScalar(env.Outputs["vpc_id"])
		data["SubnetIDs"] = formatIPs(env.Outputs["subnet_ids"])
		data["RDSEndpoint"] = formatScalar(env.Outputs["rds_endpoint"])
		data["RDSAddress"] = formatScalar(env.Outputs["rds_address"])
		data["RDSPort"] = formatScalar(env.Outputs["rds_port"])
		data["RDSUsername"] = formatScalar(env.Outputs["rds_username"])
		data["RedisEndpoint"] = formatScalar(env.Outputs["redis_primary_endpoint"])
		data["RedisReader"] = formatScalar(env.Outputs["redis_reader_endpoint"])
		data["RedisPort"] = formatScalar(env.Outputs["redis_port"])
	}
	if env.Status == store.EnvUp && data["RDSEndpoint"] != "" {
		has, err := d.Store.HasEnvironmentSecret(ctx, env.ID, store.SecretRDSMySQL)
		if err != nil {
			return nil, err
		}
		data["HasRDSSecret"] = has
	}
	if env.Status == store.EnvUp && data["RedisEndpoint"] != "" {
		has, err := d.Store.HasEnvironmentSecret(ctx, env.ID, store.SecretRedisAuth)
		if err != nil {
			return nil, err
		}
		data["HasRedisSecret"] = has
	}
	return data, nil
}

func environmentStatusNeedsPolling(status string) bool {
	switch status {
	case store.EnvPreviewing,
		store.EnvDestroyPreviewing,
		store.EnvProvisioning,
		store.EnvRefreshing,
		store.EnvDestroying:
		return true
	default:
		return false
	}
}

func jobHistoryData(env store.Environment, jobs []store.JobSummary) map[string]any {
	views := jobViews(jobs)
	hasActiveJobs := false
	for _, job := range views {
		if job.Active {
			hasActiveJobs = true
			break
		}
	}
	return map[string]any{
		"Env":           env,
		"Jobs":          views,
		"HasActiveJobs": hasActiveJobs,
	}
}

func writeHTMLPartial(w http.ResponseWriter, d Deps, name string, data any) {
	var body bytes.Buffer
	if err := d.Renderer.RenderPartial(&body, name, data); err != nil {
		http.Error(w, "render response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body.Bytes())
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

func formatScalar(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func formatChangeSummary(summary map[string]any) string {
	if len(summary) == 0 {
		return ""
	}
	return fmt.Sprintf("%v 创建 / %v 更新 / %v 删除 / %v 不变",
		changeCount(summary, "creates"), changeCount(summary, "updates"),
		changeCount(summary, "deletes"), changeCount(summary, "sames"))
}

func changeCount(summary map[string]any, key string) any {
	value, ok := summary[key]
	if !ok {
		return 0
	}
	return value
}
