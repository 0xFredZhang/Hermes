package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
	"github.com/0xFredZhang/Hermes/internal/store"
)

const (
	maxBlueprintNameLength   = 128
	maxPulumiStackBaseLength = 91
)

type blueprintFormData struct {
	PageTitle         string
	ActiveNav         string
	HideNav           bool
	Mode              string
	FormAction        string
	SubmitLabel       string
	SourceBlueprintID int64
	Projects          []store.Project
	Accounts          []store.CloudAccount
	Form              store.Blueprint
	Error             string
	FieldErrors       map[string]string
	ParamErrorField   string
	HasIngress        bool
	IngressPort       string
	IngressProtocol   string
	IngressCIDR       string
}

func addBlueprintRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /blueprints", func(w http.ResponseWriter, r *http.Request) {
		renderBlueprints(w, r, d, r.URL.Query().Get("error"))
	})
	mux.HandleFunc("GET /blueprints/new", func(w http.ResponseWriter, r *http.Request) {
		data, err := newBlueprintForm(r.Context(), d, "new")
		if err != nil {
			blueprintInternalError(w, "load new blueprint form", "无法加载蓝图表单", err)
			return
		}
		renderBlueprintForm(w, r, d, http.StatusOK, data)
	})
	mux.HandleFunc("POST /blueprints", func(w http.ResponseWriter, r *http.Request) {
		handleCreateBlueprint(w, r, d)
	})
	mux.HandleFunc("GET /blueprints/{id}", func(w http.ResponseWriter, r *http.Request) {
		handleBlueprintDetail(w, r, d)
	})
	mux.HandleFunc("GET /blueprints/{id}/edit", func(w http.ResponseWriter, r *http.Request) {
		handleBlueprintForm(w, r, d, "edit")
	})
	mux.HandleFunc("POST /blueprints/{id}", func(w http.ResponseWriter, r *http.Request) {
		handleUpdateBlueprint(w, r, d)
	})
	mux.HandleFunc("GET /blueprints/{id}/duplicate", func(w http.ResponseWriter, r *http.Request) {
		handleBlueprintForm(w, r, d, "duplicate")
	})
	mux.HandleFunc("GET /blueprints/{id}/deploy", func(w http.ResponseWriter, r *http.Request) {
		handleBlueprintDeployPage(w, r, d)
	})
	mux.HandleFunc("GET /blueprints/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		handleBlueprintDeleteConfirmation(w, r, d)
	})
	mux.HandleFunc("DELETE /blueprints/{id}", func(w http.ResponseWriter, r *http.Request) {
		handleDeleteBlueprint(w, r, d, false)
	})
	mux.HandleFunc("POST /blueprints/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		handleDeleteBlueprint(w, r, d, true)
	})
	mux.HandleFunc("POST /blueprints/{id}/deploy", func(w http.ResponseWriter, r *http.Request) {
		handleBlueprintDeployPage(w, r, d)
	})
}

func handleBlueprintDeleteConfirmation(w http.ResponseWriter, r *http.Request, d Deps) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "蓝图 ID 无效", http.StatusBadRequest)
		return
	}
	b, err := d.Store.GetBlueprint(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		blueprintInternalError(w, "load blueprint delete confirmation", "无法读取蓝图", err)
		return
	}
	d.Renderer.Render(w, "blueprint_delete", map[string]any{
		"PageTitle": "删除蓝图 · " + b.Name,
		"ActiveNav": "blueprints",
		"Blueprint": b,
	})
}

func handleDeleteBlueprint(w http.ResponseWriter, r *http.Request, d Deps, redirect bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeBlueprintDeleteError(w, r, "蓝图 ID 无效", http.StatusBadRequest)
		return
	}
	if err := d.Store.DeleteBlueprint(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeBlueprintDeleteError(w, r, "蓝图不存在或已被删除", http.StatusNotFound)
		case errors.Is(err, store.ErrBlueprintReferenced):
			writeBlueprintDeleteError(w, r, "该蓝图已有环境引用，无法删除", http.StatusConflict)
		default:
			log.Printf("delete blueprint %d: %v", id, err)
			writeBlueprintDeleteError(w, r, "无法删除蓝图", http.StatusInternalServerError)
		}
		return
	}
	if redirect {
		http.Redirect(w, r, "/blueprints", http.StatusSeeOther)
		return
	}
	list, err := d.Store.ListBlueprints(r.Context())
	if err != nil {
		log.Printf("list blueprints after deleting %d: %v", id, err)
		writeBlueprintDeleteError(w, r, "蓝图已删除，但列表刷新失败，请重新加载页面", http.StatusInternalServerError)
		return
	}
	var body bytes.Buffer
	if err := d.Renderer.RenderPartial(&body, "blueprint_rows", list); err != nil {
		log.Printf("render blueprints after deleting %d: %v", id, err)
		writeBlueprintDeleteError(w, r, "蓝图已删除，但列表刷新失败，请重新加载页面", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body.Bytes())
}

func writeBlueprintDeleteError(w http.ResponseWriter, r *http.Request, message string, status int) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", `{"blueprint-delete-error":{"message":`+strconv.QuoteToASCII(message)+`}}`)
	}
	http.Error(w, message, status)
}

func renderBlueprints(w http.ResponseWriter, r *http.Request, d Deps, errMsg string) {
	blueprints, err := d.Store.ListBlueprints(r.Context())
	if err != nil {
		blueprintInternalError(w, "list blueprints", "无法读取蓝图", err)
		return
	}
	d.Renderer.Render(w, "blueprints", map[string]any{
		"PageTitle": "蓝图", "ActiveNav": "blueprints", "Blueprints": blueprints, "Error": errMsg,
	})
}

func newBlueprintForm(ctx context.Context, d Deps, mode string) (blueprintFormData, error) {
	params := provisioner.BlueprintParams{Region: "ap-southeast-1", SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "ingress"}}}, EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8}}
	params.ApplyDefaults()
	projects, err := d.Store.ListProjects(ctx)
	if err != nil {
		return blueprintFormData{}, err
	}
	accounts, err := d.Store.ListCloudAccounts(ctx)
	if err != nil {
		return blueprintFormData{}, err
	}
	data := blueprintFormData{PageTitle: "新建蓝图", ActiveNav: "blueprints", Mode: mode, Projects: projects, Accounts: accounts, Form: store.Blueprint{Params: params}, FieldErrors: map[string]string{}, HasIngress: true, IngressPort: "22", IngressProtocol: "tcp", IngressCIDR: "0.0.0.0/0"}
	setBlueprintFormPresentation(&data)
	return data, nil
}

func formForBlueprint(ctx context.Context, d Deps, b store.Blueprint, mode string) (blueprintFormData, error) {
	data, err := newBlueprintForm(ctx, d, mode)
	if err != nil {
		return blueprintFormData{}, err
	}
	setBlueprintForm(&data, b)
	if mode == "duplicate" {
		data.SourceBlueprintID = b.ID
		data.Form.ID = 0
		data.Form.Name = b.Name + " 副本"
	}
	setBlueprintFormPresentation(&data)
	return data, nil
}

func setBlueprintForm(data *blueprintFormData, b store.Blueprint) {
	b.Params.Redis.AuthEnabled = b.Params.Redis.Enabled && b.Params.Redis.AuthEnabled
	data.Form = b
	data.HasIngress = len(b.Params.SecurityGroup.Ingress) > 0
	data.IngressPort = ""
	data.IngressProtocol = ""
	data.IngressCIDR = ""
	if data.HasIngress {
		data.IngressPort = strconv.Itoa(b.Params.SecurityGroup.Ingress[0].Port)
		data.IngressProtocol = b.Params.SecurityGroup.Ingress[0].Protocol
		data.IngressCIDR = b.Params.SecurityGroup.Ingress[0].CIDR
	}
	setBlueprintFormPresentation(data)
}

func setBlueprintFormPresentation(data *blueprintFormData) {
	switch data.Mode {
	case "edit":
		data.PageTitle = "编辑蓝图"
		data.FormAction = "/blueprints/" + strconv.FormatInt(data.Form.ID, 10)
		data.SubmitLabel = "保存更改"
	case "duplicate":
		data.PageTitle = "复制蓝图"
		data.FormAction = "/blueprints"
		data.SubmitLabel = "创建副本"
	default:
		data.PageTitle = "新建蓝图"
		data.FormAction = "/blueprints"
		data.SubmitLabel = "创建蓝图"
	}
}

func mergeUnexposedBlueprintParams(base, submitted provisioner.BlueprintParams) provisioner.BlueprintParams {
	// The form intentionally exposes one ingress rule and omits engine/port
	// internals. Preserve those server-owned values while applying submitted
	// choices to the fields the operator can edit.
	submitted.Network.MapPublicIPOnLaunch = base.Network.MapPublicIPOnLaunch
	submitted.RDS.Engine = base.RDS.Engine
	submitted.RDS.Port = base.RDS.Port
	submitted.Redis.Engine = base.Redis.Engine
	submitted.Redis.Port = base.Redis.Port
	if len(submitted.SecurityGroup.Ingress) > 0 && len(base.SecurityGroup.Ingress) > 0 {
		ingress := make([]provisioner.Ingress, 0, len(base.SecurityGroup.Ingress))
		first := submitted.SecurityGroup.Ingress[0]
		first.Desc = base.SecurityGroup.Ingress[0].Desc
		ingress = append(ingress, first)
		ingress = append(ingress, base.SecurityGroup.Ingress[1:]...)
		submitted.SecurityGroup.Ingress = ingress
	}
	return submitted
}

func setIngressFormFromRequest(data *blueprintFormData, r *http.Request) {
	data.IngressPort = strings.TrimSpace(r.FormValue("ingress_port"))
	data.IngressProtocol = strings.TrimSpace(r.FormValue("ingress_protocol"))
	data.IngressCIDR = strings.TrimSpace(r.FormValue("ingress_cidr"))
	_, hasMode := r.Form["ingress_mode"]
	data.HasIngress = r.FormValue("ingress_mode") == "rule"
	if !hasMode {
		data.HasIngress = data.IngressPort != "" || data.IngressProtocol != "" || data.IngressCIDR != ""
	}
}

func parseBlueprintForm(r *http.Request) (store.Blueprint, map[string]string) {
	count, _ := strconv.Atoi(r.FormValue("count"))
	disk, _ := strconv.Atoi(r.FormValue("root_volume_gb"))
	port := 0
	rdsStorage, _ := strconv.Atoi(r.FormValue("rds_allocated_storage_gb"))
	redisNodes, _ := strconv.Atoi(r.FormValue("redis_node_count"))
	redisEnabled := r.FormValue("redis_enabled") != ""
	ingressPort := strings.TrimSpace(r.FormValue("ingress_port"))
	ingressProtocol := strings.TrimSpace(r.FormValue("ingress_protocol"))
	ingressCIDR := strings.TrimSpace(r.FormValue("ingress_cidr"))
	_, hasIngressMode := r.Form["ingress_mode"]
	hasIngress := r.FormValue("ingress_mode") == "rule"
	if !hasIngressMode {
		hasIngress = ingressPort != "" || ingressProtocol != "" || ingressCIDR != ""
	}
	ingress := []provisioner.Ingress(nil)
	if hasIngress {
		port, _ = strconv.Atoi(ingressPort)
		ingress = []provisioner.Ingress{{Port: port, Protocol: ingressProtocol, CIDR: ingressCIDR, Desc: "ingress"}}
	}
	params := provisioner.BlueprintParams{
		Region:        strings.TrimSpace(r.FormValue("region")),
		SecurityGroup: provisioner.SecurityGroup{Ingress: ingress},
		EC2: provisioner.EC2{
			InstanceType: strings.TrimSpace(r.FormValue("instance_type")), Count: count,
			AMI: strings.TrimSpace(r.FormValue("ami")), RootVolumeGB: disk, KeyName: strings.TrimSpace(r.FormValue("key_name")),
		},
		Network: provisioner.Network{
			Enabled:             r.FormValue("network_enabled") != "",
			VPCCIDR:             strings.TrimSpace(r.FormValue("network_vpc_cidr")),
			PublicSubnetCIDRs:   splitCIDRs(r.FormValue("network_public_subnet_cidrs")),
			MapPublicIPOnLaunch: r.FormValue("network_map_public_ip_launch") != "",
		},
		RDS: provisioner.RDS{
			Enabled:            r.FormValue("rds_enabled") != "",
			Engine:             "mysql",
			EngineVersion:      strings.TrimSpace(r.FormValue("rds_engine_version")),
			InstanceClass:      strings.TrimSpace(r.FormValue("rds_instance_class")),
			AllocatedStorageGB: rdsStorage,
			DBName:             strings.TrimSpace(r.FormValue("rds_db_name")),
			Username:           strings.TrimSpace(r.FormValue("rds_username")),
			Port:               3306,
		},
		Redis: provisioner.Redis{
			Enabled:       redisEnabled,
			Engine:        "redis",
			EngineVersion: strings.TrimSpace(r.FormValue("redis_engine_version")),
			NodeType:      strings.TrimSpace(r.FormValue("redis_node_type")),
			NodeCount:     redisNodes,
			Port:          6379,
			AuthEnabled:   redisEnabled && r.FormValue("redis_auth_enabled") != "",
		},
	}
	params.ApplyDefaults()
	b := store.Blueprint{ProjectID: projectIDValue(r.FormValue("project_id")), CloudAccountID: projectIDValue(r.FormValue("cloud_account_id")), Name: strings.TrimSpace(r.FormValue("name")), Params: params}
	errs := make(map[string]string)
	if b.Name == "" {
		errs["name"] = "请输入蓝图名称。"
	} else if utf8.RuneCountInString(b.Name) > maxBlueprintNameLength {
		errs["name"] = "蓝图名称不能超过 128 个字符。"
	}
	if b.ProjectID < 1 {
		errs["project_id"] = "请选择项目。"
	}
	if b.CloudAccountID < 1 {
		errs["cloud_account_id"] = "请选择云账号。"
	}
	if err := params.Validate(); err != nil {
		errs["params"] = "参数无效: " + err.Error()
	}
	return b, errs
}

func projectIDValue(raw string) int64 {
	id, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return id
}

func validateBlueprintOwnership(ctx context.Context, d Deps, b store.Blueprint, fieldErrors map[string]string) error {
	if _, invalid := fieldErrors["project_id"]; !invalid {
		if _, err := d.Store.GetProject(ctx, b.ProjectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				fieldErrors["project_id"] = "所选项目已不存在，请重新选择。"
			} else {
				return err
			}
		}
	}
	if _, invalid := fieldErrors["cloud_account_id"]; !invalid {
		if _, err := d.Store.GetCloudAccount(ctx, b.CloudAccountID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				fieldErrors["cloud_account_id"] = "所选云账号已不存在，请重新选择。"
			} else {
				return err
			}
		}
	}
	return nil
}

func handleCreateBlueprint(w http.ResponseWriter, r *http.Request, d Deps) {
	b, fieldErrors := parseBlueprintForm(r)
	mode := strings.TrimSpace(r.FormValue("blueprint_mode"))
	if mode != "duplicate" {
		mode = "new"
	}
	var sourceID int64
	if mode == "duplicate" {
		var err error
		sourceID, err = strconv.ParseInt(strings.TrimSpace(r.FormValue("source_blueprint_id")), 10, 64)
		if err != nil || sourceID < 1 {
			http.Error(w, "复制来源 ID 无效", http.StatusBadRequest)
			return
		}
		source, err := d.Store.GetBlueprint(r.Context(), sourceID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			blueprintInternalError(w, "load duplicate source", "无法加载复制来源", err)
			return
		}
		b.Params = mergeUnexposedBlueprintParams(source.Params, b.Params)
	}
	if err := b.Params.Validate(); err != nil {
		fieldErrors["params"] = "参数无效: " + err.Error()
	}
	if err := validateBlueprintOwnership(r.Context(), d, b, fieldErrors); err != nil {
		blueprintInternalError(w, "validate blueprint ownership", "无法验证蓝图归属", err)
		return
	}
	if len(fieldErrors) > 0 {
		data, err := newBlueprintForm(r.Context(), d, mode)
		if err != nil {
			blueprintInternalError(w, "reload blueprint form", "无法加载蓝图表单", err)
			return
		}
		setBlueprintForm(&data, b)
		data.SourceBlueprintID = sourceID
		if mode == "duplicate" {
			data.Form.ID = 0
		}
		setBlueprintFormPresentation(&data)
		setIngressFormFromRequest(&data, r)
		data.FieldErrors = fieldErrors
		data.Error = "请检查标出的字段。"
		renderBlueprintForm(w, r, d, http.StatusUnprocessableEntity, data)
		return
	}
	_, err := d.Store.CreateBlueprint(r.Context(), b)
	if err != nil {
		if errors.Is(err, store.ErrBlueprintOwnershipInvalid) {
			http.Error(w, "项目或云账号已不存在，请刷新后重试", http.StatusConflict)
			return
		}
		log.Printf("create blueprint: %v", err)
		http.Error(w, "无法保存蓝图", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/blueprints", http.StatusSeeOther)
}

func handleBlueprintForm(w http.ResponseWriter, r *http.Request, d Deps, mode string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	b, err := d.Store.GetBlueprint(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		blueprintInternalError(w, "load blueprint form", "无法读取蓝图", err)
		return
	}
	data, err := formForBlueprint(r.Context(), d, b, mode)
	if err != nil {
		blueprintInternalError(w, "load blueprint form prerequisites", "无法加载蓝图表单", err)
		return
	}
	renderBlueprintForm(w, r, d, http.StatusOK, data)
}

func handleUpdateBlueprint(w http.ResponseWriter, r *http.Request, d Deps) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	existing, err := d.Store.GetBlueprint(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		blueprintInternalError(w, "load blueprint for update", "无法读取蓝图", err)
		return
	}
	b, fieldErrors := parseBlueprintForm(r)
	b.ID = id
	b.Params = mergeUnexposedBlueprintParams(existing.Params, b.Params)
	if err := b.Params.Validate(); err != nil {
		fieldErrors["params"] = "参数无效: " + err.Error()
	}
	if err := validateBlueprintOwnership(r.Context(), d, b, fieldErrors); err != nil {
		blueprintInternalError(w, "validate updated blueprint ownership", "无法验证蓝图归属", err)
		return
	}
	if len(fieldErrors) > 0 {
		data, err := newBlueprintForm(r.Context(), d, "edit")
		if err != nil {
			blueprintInternalError(w, "reload blueprint edit form", "无法加载蓝图表单", err)
			return
		}
		setBlueprintForm(&data, b)
		setBlueprintFormPresentation(&data)
		setIngressFormFromRequest(&data, r)
		data.FieldErrors = fieldErrors
		data.Error = "请检查标出的字段。"
		renderBlueprintForm(w, r, d, http.StatusUnprocessableEntity, data)
		return
	}
	if err := d.Store.UpdateBlueprint(r.Context(), b); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, store.ErrBlueprintOwnershipInvalid) {
			http.Error(w, "项目或云账号已不存在，请刷新后重试", http.StatusConflict)
			return
		}
		log.Printf("update blueprint %d: %v", id, err)
		http.Error(w, "无法更新蓝图", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/blueprints/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func handleBlueprintDetail(w http.ResponseWriter, r *http.Request, d Deps) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	b, err := d.Store.GetBlueprint(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		blueprintInternalError(w, "load blueprint detail", "无法读取蓝图", err)
		return
	}
	d.Renderer.Render(w, "blueprint_detail", map[string]any{"PageTitle": b.Name, "ActiveNav": "blueprints", "Blueprint": b})
}

func handleBlueprintDeployPage(w http.ResponseWriter, r *http.Request, d Deps) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	b, err := d.Store.GetBlueprint(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		blueprintInternalError(w, "load blueprint deploy page", "无法读取蓝图", err)
		return
	}
	data := map[string]any{"PageTitle": "部署 · " + b.Name, "ActiveNav": "blueprints", "Blueprint": b, "EnvironmentName": strings.TrimSpace(r.FormValue("env_name")), "EnvironmentNameError": ""}
	if r.Method == http.MethodGet {
		d.Renderer.Render(w, "blueprint_deploy", data)
		return
	}
	name := strings.TrimSpace(r.FormValue("env_name"))
	if name == "" {
		data["EnvironmentNameError"] = "请输入环境名。"
		d.Renderer.RenderStatus(w, "blueprint_deploy", http.StatusUnprocessableEntity, data)
		return
	}
	r.Form.Set("env_name", name)
	handleDeploy(w, r, d, b)
}

func renderBlueprintForm(w http.ResponseWriter, r *http.Request, d Deps, status int, data blueprintFormData) {
	data.ParamErrorField = blueprintParamErrorField(data.FieldErrors["params"])
	d.Renderer.RenderStatus(w, "blueprint_form", status, data)
}

func blueprintParamErrorField(message string) string {
	switch {
	case strings.Contains(message, "region is required"):
		return "region"
	case strings.Contains(message, "ec2.instance_type"):
		return "instance_type"
	case strings.Contains(message, "ec2.count"):
		return "count"
	case strings.Contains(message, "ec2.root_volume_gb"):
		return "root_volume_gb"
	case strings.Contains(message, "ingress[") && strings.Contains(message, "port"):
		return "ingress_port"
	case strings.Contains(message, "ingress[") && strings.Contains(message, "protocol"):
		return "ingress_protocol"
	case strings.Contains(message, "ingress[") && strings.Contains(message, "cidr"):
		return "ingress_cidr"
	case strings.Contains(message, "network.vpc_cidr"):
		return "network_vpc_cidr"
	case strings.Contains(message, "network.public_subnet_cidrs"):
		return "network_public_subnet_cidrs"
	case strings.Contains(message, "rds.engine_version"):
		return "rds_engine_version"
	case strings.Contains(message, "rds.instance_class"):
		return "rds_instance_class"
	case strings.Contains(message, "rds.allocated_storage_gb"):
		return "rds_allocated_storage_gb"
	case strings.Contains(message, "rds.db_name"):
		return "rds_db_name"
	case strings.Contains(message, "rds.username"):
		return "rds_username"
	case strings.Contains(message, "redis.engine_version"):
		return "redis_engine_version"
	case strings.Contains(message, "redis.node_type"):
		return "redis_node_type"
	case strings.Contains(message, "redis.node_count"):
		return "redis_node_count"
	default:
		return ""
	}
}

func handleDeploy(w http.ResponseWriter, r *http.Request, d Deps, bp store.Blueprint) {
	name := r.FormValue("env_name")
	stack := slug(name) + "-" + uuid.NewString()[:8]
	envID, err := d.Store.CreateEnvironment(r.Context(), store.Environment{
		BlueprintID: bp.ID, CloudAccountID: bp.CloudAccountID, Name: name,
		PulumiStack: stack, Region: bp.Params.Region, Snapshot: bp.Params,
	})
	if err != nil {
		blueprintInternalError(w, "create environment from blueprint", "无法创建环境", err)
		return
	}
	if _, err := d.Orchestrator.Enqueue(r.Context(), envID, store.ActionPreview); err != nil {
		redirectAfterInitialPreviewFailure(w, r, d, envID, err)
		return
	}
	http.Redirect(w, r, "/environments/"+strconv.FormatInt(envID, 10), http.StatusSeeOther)
}

func blueprintInternalError(w http.ResponseWriter, operation, publicMessage string, err error) {
	log.Printf("%s: %v", operation, err)
	http.Error(w, publicMessage, http.StatusInternalServerError)
}

func redirectAfterInitialPreviewFailure(w http.ResponseWriter, r *http.Request, d Deps, envID int64, enqueueErr error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	cleanupErr := d.Store.DeletePendingEnvironment(cleanupCtx, envID)
	if cleanupErr == nil {
		cancel()
		redirectLifecycleResult(w, r, "/blueprints", enqueueErr)
		return
	}

	log.Printf("delete environment %d after preview enqueue failure: %v", envID, cleanupErr)
	_, lookupErr := d.Store.GetEnvironment(cleanupCtx, envID)
	cancel()
	if errors.Is(lookupErr, sql.ErrNoRows) {
		redirectLifecycleResult(w, r, "/blueprints", enqueueErr)
		return
	}
	if lookupErr != nil {
		log.Printf("verify environment %d after cleanup failure: %v", envID, lookupErr)
	}

	message := "任务启动失败，环境未能自动清理，请在此处继续处理"
	if errors.Is(cleanupErr, store.ErrStaleTransition) {
		message = "环境状态已变化，请在详情中确认后续操作"
	}
	redirectErrorMessage(w, r, environmentPath(envID), message)
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
	if len(out) > maxPulumiStackBaseLength {
		out = strings.TrimRight(out[:maxPulumiStackBaseLength], "-")
	}
	if out == "" {
		out = "env"
	}
	return out
}

func splitCIDRs(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}
