package api

import (
	"context"
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
		d.Renderer.Render(w, "environment_detail", envViewData(r.Context(), d, env, jobs))
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
		_ = d.Renderer.RenderPartial(w, "env_status", envViewData(r.Context(), d, env, jobs))
	})
	mux.HandleFunc("POST /environments/{id}/up", enqueueHandler(d, store.ActionUp))
	mux.HandleFunc("POST /environments/{id}/retry", retryHandler(d))
	mux.HandleFunc("POST /environments/{id}/refresh", enqueueHandler(d, store.ActionRefresh))
	mux.HandleFunc("POST /environments/{id}/rds-credentials", revealRDSCredentialsHandler(d))
	mux.HandleFunc("POST /environments/{id}/redis-credentials", revealRedisCredentialsHandler(d))
	mux.HandleFunc("POST /environments/{id}/destroy-preview", enqueueHandler(d, store.ActionDestroyPreview))
	mux.HandleFunc("POST /environments/{id}/cancel-destroy", cancelDestroyHandler(d))
	mux.HandleFunc("POST /environments/{id}/destroy", destroyHandler(d))
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

func destroyHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		env, err := d.Store.GetEnvironment(r.Context(), id)
		if err == nil && env.Status == store.EnvUp {
			http.Redirect(w, r, "/environments/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
			return
		}
		_, _ = d.Orchestrator.Enqueue(r.Context(), id, store.ActionDestroy)
		http.Redirect(w, r, "/environments/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	}
}

func cancelDestroyHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if env, err := d.Store.GetEnvironment(r.Context(), id); err == nil && env.Status == store.EnvDestroyPreviewReady {
			_ = d.Store.UpdateEnvironmentStatus(r.Context(), id, store.EnvUp)
		}
		http.Redirect(w, r, "/environments/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
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

// envViewData is the template payload shared by the detail page and the status
// fragment: the environment, the latest job id/logs (for the SSE pane), a
// preview plan string, and a formatted public-IP list.
func envViewData(ctx context.Context, d Deps, env store.Environment, jobs []store.Job) map[string]any {
	// Initialize every key the templates reference so missing values render as
	// empty strings (a map miss would otherwise print "<no value>").
	data := map[string]any{
		"Env": env, "CurrentJobID": int64(0), "CurrentLogs": "", "Plan": "",
		"DestroyPlan": "",
		"RefreshPlan": "",
		"PublicIPs":   "", "PublicDNS": "",
		"VPCID": "", "SubnetIDs": "",
		"RDSEndpoint": "", "RDSAddress": "", "RDSPort": "", "RDSUsername": "",
		"HasRDSSecret":  false,
		"RedisEndpoint": "", "RedisReader": "", "RedisPort": "",
		"HasRedisSecret": false,
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
		if has, err := d.Store.HasEnvironmentSecret(ctx, env.ID, store.SecretRDSMySQL); err == nil {
			data["HasRDSSecret"] = has
		}
	}
	if env.Status == store.EnvUp && data["RedisEndpoint"] != "" {
		if has, err := d.Store.HasEnvironmentSecret(ctx, env.ID, store.SecretRedisAuth); err == nil {
			data["HasRedisSecret"] = has
		}
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

func formatScalar(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func formatChangeSummary(summary map[string]any) string {
	return fmt.Sprintf("%v 创建 / %v 更新 / %v 删除 / %v 不变",
		summary["creates"], summary["updates"], summary["deletes"], summary["sames"])
}
