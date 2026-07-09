package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
	"github.com/0xFredZhang/Hermes/internal/store"
)

func addBlueprintRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /blueprints", func(w http.ResponseWriter, r *http.Request) {
		renderBlueprints(w, r, d, "")
	})
	mux.HandleFunc("POST /blueprints", func(w http.ResponseWriter, r *http.Request) {
		handleCreateBlueprint(w, r, d)
	})
	mux.HandleFunc("DELETE /blueprints/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err := d.Store.DeleteBlueprint(r.Context(), id); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<tr><td colspan="4" class="err">无法删除:该蓝图已有环境引用</td></tr>`))
			return
		}
		list, _ := d.Store.ListBlueprints(r.Context())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = d.Renderer.RenderPartial(w, "blueprint_rows", list)
	})
	mux.HandleFunc("POST /blueprints/{id}/deploy", func(w http.ResponseWriter, r *http.Request) {
		handleDeploy(w, r, d)
	})
}

func renderBlueprints(w http.ResponseWriter, r *http.Request, d Deps, errMsg string) {
	blueprints, err := d.Store.ListBlueprints(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	projects, _ := d.Store.ListProjects(r.Context())
	accounts, _ := d.Store.ListCloudAccounts(r.Context())
	d.Renderer.Render(w, "blueprints", map[string]any{
		"Blueprints": blueprints, "Projects": projects, "Accounts": accounts, "Error": errMsg,
	})
}

func handleCreateBlueprint(w http.ResponseWriter, r *http.Request, d Deps) {
	count, _ := strconv.Atoi(r.FormValue("count"))
	disk, _ := strconv.Atoi(r.FormValue("root_volume_gb"))
	port, _ := strconv.Atoi(r.FormValue("ingress_port"))
	rdsStorage, _ := strconv.Atoi(r.FormValue("rds_allocated_storage_gb"))
	redisNodes, _ := strconv.Atoi(r.FormValue("redis_node_count"))
	params := provisioner.BlueprintParams{
		Region: r.FormValue("region"),
		SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
			{Port: port, Protocol: r.FormValue("ingress_protocol"), CIDR: r.FormValue("ingress_cidr"), Desc: "ingress"},
		}},
		EC2: provisioner.EC2{
			InstanceType: r.FormValue("instance_type"), Count: count,
			AMI: r.FormValue("ami"), RootVolumeGB: disk, KeyName: r.FormValue("key_name"),
		},
		RDS: provisioner.RDS{
			Enabled:            r.FormValue("rds_enabled") != "",
			Engine:             "mysql",
			EngineVersion:      r.FormValue("rds_engine_version"),
			InstanceClass:      r.FormValue("rds_instance_class"),
			AllocatedStorageGB: rdsStorage,
			DBName:             r.FormValue("rds_db_name"),
			Username:           r.FormValue("rds_username"),
			Port:               3306,
		},
		Redis: provisioner.Redis{
			Enabled:       r.FormValue("redis_enabled") != "",
			Engine:        "redis",
			EngineVersion: r.FormValue("redis_engine_version"),
			NodeType:      r.FormValue("redis_node_type"),
			NodeCount:     redisNodes,
			Port:          6379,
		},
	}
	params.ApplyDefaults()
	if err := params.Validate(); err != nil {
		w.WriteHeader(http.StatusOK)
		renderBlueprints(w, r, d, "参数无效:"+err.Error())
		return
	}
	projectID, _ := strconv.ParseInt(r.FormValue("project_id"), 10, 64)
	accountID, _ := strconv.ParseInt(r.FormValue("cloud_account_id"), 10, 64)
	_, err := d.Store.CreateBlueprint(r.Context(), store.Blueprint{
		ProjectID: projectID, CloudAccountID: accountID, Name: r.FormValue("name"), Params: params,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/blueprints", http.StatusSeeOther)
}

func handleDeploy(w http.ResponseWriter, r *http.Request, d Deps) {
	bpID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	bp, err := d.Store.GetBlueprint(r.Context(), bpID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	name := r.FormValue("env_name")
	stack := slug(name) + "-" + uuid.NewString()[:8]
	envID, err := d.Store.CreateEnvironment(r.Context(), store.Environment{
		BlueprintID: bp.ID, CloudAccountID: bp.CloudAccountID, Name: name,
		PulumiStack: stack, Region: bp.Params.Region, Snapshot: bp.Params,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := d.Orchestrator.Enqueue(r.Context(), envID, store.ActionPreview); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/environments/"+strconv.FormatInt(envID, 10), http.StatusSeeOther)
}

// slug reduces a name to lowercase alphanumerics and hyphens for a stack name.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == ' ' || c == '-' || c == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "env"
	}
	return out
}
