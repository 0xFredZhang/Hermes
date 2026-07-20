package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0xFredZhang/Hermes/internal/orchestrator"
	"github.com/0xFredZhang/Hermes/internal/provisioner"
	"github.com/0xFredZhang/Hermes/internal/store"
)

// stubProvisioner is a no-op Provisioner for handler tests.
type stubProvisioner struct{}

func (stubProvisioner) Preview(_ context.Context, _ provisioner.Spec, _ io.Writer) (provisioner.PreviewResult, error) {
	return provisioner.PreviewResult{Creates: 1}, nil
}
func (stubProvisioner) PreviewDestroy(_ context.Context, _ provisioner.Spec, _ io.Writer) (provisioner.PreviewResult, error) {
	return provisioner.PreviewResult{Deletes: 1}, nil
}
func (stubProvisioner) Refresh(_ context.Context, _ provisioner.Spec, _ io.Writer) (provisioner.PreviewResult, error) {
	return provisioner.PreviewResult{Updates: 1}, nil
}
func (stubProvisioner) Up(_ context.Context, _ provisioner.Spec, _ io.Writer) (provisioner.UpResult, error) {
	return provisioner.UpResult{Outputs: map[string]any{"public_ips": []any{"1.2.3.4"}}}, nil
}
func (stubProvisioner) Destroy(_ context.Context, _ provisioner.Spec, _ io.Writer) error { return nil }

// testDepsWithOrchestrator adds a Broker + Orchestrator (NOT started) so Enqueue
// creates and buffers jobs; tests assert the resulting DB state.
func testDepsWithOrchestrator(t *testing.T) Deps {
	t.Helper()
	d := testDeps(t)
	b := orchestrator.NewBroker()
	d.Broker = b
	d.Orchestrator = orchestrator.New(d.Store, stubProvisioner{}, b, 1)
	return d
}

func validBPParams() provisioner.BlueprintParams {
	return provisioner.BlueprintParams{
		Region: "ap-southeast-1",
		SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
			{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
		}},
		EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8},
	}
}

func seedProjectAccount(t *testing.T, d Deps) (projectID, accountID int64) {
	t.Helper()
	ctx := context.Background()
	pid, _ := d.Store.CreateProject(ctx, store.Project{Name: "p"})
	aid, err := d.Store.CreateCloudAccount(ctx, store.CloudAccount{
		Name: "a", DefaultRegion: "ap-southeast-1", AccessKeyID: "AK",
		SecretAccessKey: "sk", AWSAccountID: "111111111111", ARN: "arn:aws:iam::111111111111:user/x",
	})
	if err != nil {
		t.Fatalf("CreateCloudAccount: %v", err)
	}
	return pid, aid
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestCreateBlueprintValidatesAndPersists(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)

	form := url.Values{
		"name": {"web"}, "project_id": {itoa(pid)}, "cloud_account_id": {itoa(aid)},
		"region": {"ap-southeast-1"}, "instance_type": {"t3.micro"}, "count": {"2"},
		"root_volume_gb": {"8"}, "ingress_port": {"22"}, "ingress_protocol": {"tcp"},
		"ingress_cidr": {"0.0.0.0/0"},
	}
	rec := authedPost(t, d, "/blueprints", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 redirect; body=%s", rec.Code, rec.Body.String())
	}
	list, _ := d.Store.ListBlueprints(context.Background())
	if len(list) != 1 || list[0].Params.EC2.Count != 2 {
		t.Fatalf("blueprint not persisted correctly: %+v", list)
	}
}

func TestCreateBlueprintPersistsOptionalResources(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)

	form := url.Values{
		"name": {"full"}, "project_id": {itoa(pid)}, "cloud_account_id": {itoa(aid)},
		"region": {"ap-southeast-1"}, "instance_type": {"t3.micro"}, "count": {"1"},
		"root_volume_gb": {"8"}, "ingress_port": {"22"}, "ingress_protocol": {"tcp"},
		"ingress_cidr": {"0.0.0.0/0"},
		"rds_enabled":  {"on"}, "rds_engine_version": {"8.0"}, "rds_instance_class": {"db.t3.micro"},
		"rds_allocated_storage_gb": {"20"}, "rds_db_name": {"app"}, "rds_username": {"admin"},
		"redis_enabled": {"on"}, "redis_engine_version": {"7.2"}, "redis_node_type": {"cache.t3.micro"},
		"redis_node_count": {"1"}, "redis_auth_enabled": {"on"},
	}
	rec := authedPost(t, d, "/blueprints", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 redirect; body=%s", rec.Code, rec.Body.String())
	}
	list, _ := d.Store.ListBlueprints(context.Background())
	if len(list) != 1 {
		t.Fatalf("blueprint not persisted: %+v", list)
	}
	got := list[0].Params
	if !got.RDS.Enabled || got.RDS.Engine != "mysql" || got.RDS.EngineVersion != "8.0" {
		t.Fatalf("RDS config not persisted with defaults: %+v", got.RDS)
	}
	if got.RDS.InstanceClass != "db.t3.micro" || got.RDS.AllocatedStorageGB != 20 || got.RDS.DBName != "app" || got.RDS.Username != "admin" || got.RDS.Port != 3306 {
		t.Fatalf("RDS fields not persisted correctly: %+v", got.RDS)
	}
	if !got.Redis.Enabled || got.Redis.Engine != "redis" || got.Redis.EngineVersion != "7.2" {
		t.Fatalf("Redis config not persisted with defaults: %+v", got.Redis)
	}
	if got.Redis.NodeType != "cache.t3.micro" || got.Redis.NodeCount != 1 || got.Redis.Port != 6379 || !got.Redis.AuthEnabled {
		t.Fatalf("Redis fields not persisted correctly: %+v", got.Redis)
	}
}

func TestCreateBlueprintClearsRedisAuthWhenRedisDisabled(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	form := blueprintFormValues(pid, aid, "crafted-create")
	form.Del("redis_enabled")
	form.Set("redis_auth_enabled", "on")

	rec := authedPost(t, d, "/blueprints", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
	bps, err := d.Store.ListBlueprints(context.Background())
	if err != nil || len(bps) != 1 {
		t.Fatalf("ListBlueprints = %+v, err=%v", bps, err)
	}
	if bps[0].Params.Redis.Enabled || bps[0].Params.Redis.AuthEnabled {
		t.Fatalf("disabled Redis persisted AUTH state: %+v", bps[0].Params.Redis)
	}
}

func TestCreateBlueprintRejectsMissingOwnership(t *testing.T) {
	for _, tc := range []struct {
		name       string
		staleField string
		errorID    string
	}{
		{name: "project", staleField: "project_id", errorID: "error-project_id"},
		{name: "account", staleField: "cloud_account_id", errorID: "error-cloud_account_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDepsWithOrchestrator(t)
			pid, aid := seedProjectAccount(t, d)
			form := blueprintFormValues(pid, aid, "stale-owner")
			form.Set(tc.staleField, "999")
			rec := authedPost(t, d, "/blueprints", form)
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `id="`+tc.errorID+`"`) {
				t.Fatalf("status/body = %d %s, want ownership 422", rec.Code, rec.Body.String())
			}
			if strings.Contains(strings.ToLower(rec.Body.String()), "foreign key") || strings.Contains(rec.Body.String(), "constraint failed") {
				t.Fatalf("ownership validation leaked SQL details: %s", rec.Body.String())
			}
			bps, _ := d.Store.ListBlueprints(context.Background())
			if len(bps) != 0 {
				t.Fatalf("stale ownership created blueprints: %+v", bps)
			}
		})
	}
}

func TestUpdateBlueprintRejectsMissingOwnershipWithoutMutation(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	id, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: validBPParams()})
	form := blueprintFormValues(pid, aid, "mutated")
	form.Set("project_id", "999")
	rec := authedPost(t, d, "/blueprints/"+itoa(id), form)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `id="error-project_id"`) {
		t.Fatalf("status/body = %d %s, want ownership 422", rec.Code, rec.Body.String())
	}
	got, _ := d.Store.GetBlueprint(context.Background(), id)
	if got.Name != "source" || got.ProjectID != pid {
		t.Fatalf("invalid update mutated blueprint: %+v", got)
	}
}

func TestBlueprintOwnershipLookupFailureReturnsSafe500(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	if err := d.Store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	rec := authedPost(t, d, "/blueprints", blueprintFormValues(pid, aid, "lookup-failure"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := strings.ToLower(rec.Body.String())
	if strings.Contains(body, "database is closed") || strings.Contains(body, "sql:") {
		t.Fatalf("lookup failure leaked database details: %s", rec.Body.String())
	}
}

func TestUpdateBlueprintClearsRedisAuthWhenRedisDisabled(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	params := validBPParams()
	params.Redis.Enabled = true
	params.Redis.AuthEnabled = true
	id, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: params})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	form := blueprintFormValues(pid, aid, "updated")
	form.Del("redis_enabled")
	form.Set("redis_auth_enabled", "on")

	rec := authedPost(t, d, "/blueprints/"+itoa(id), form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
	got, err := d.Store.GetBlueprint(context.Background(), id)
	if err != nil {
		t.Fatalf("GetBlueprint: %v", err)
	}
	if got.Params.Redis.Enabled || got.Params.Redis.AuthEnabled {
		t.Fatalf("disabled Redis persisted AUTH state: %+v", got.Params.Redis)
	}
}

func TestCreateBlueprintPersistsManagedNetwork(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)

	form := url.Values{
		"name": {"net"}, "project_id": {itoa(pid)}, "cloud_account_id": {itoa(aid)},
		"region": {"ap-southeast-1"}, "instance_type": {"t3.micro"}, "count": {"1"},
		"root_volume_gb": {"8"}, "ingress_port": {"22"}, "ingress_protocol": {"tcp"},
		"ingress_cidr":                 {"0.0.0.0/0"},
		"network_enabled":              {"on"},
		"network_vpc_cidr":             {"10.42.0.0/16"},
		"network_public_subnet_cidrs":  {"10.42.1.0/24, 10.42.2.0/24"},
		"network_map_public_ip_launch": {"on"},
	}
	rec := authedPost(t, d, "/blueprints", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 redirect; body=%s", rec.Code, rec.Body.String())
	}
	list, _ := d.Store.ListBlueprints(context.Background())
	if len(list) != 1 {
		t.Fatalf("blueprint not persisted: %+v", list)
	}
	got := list[0].Params.Network
	if !got.Enabled || got.VPCCIDR != "10.42.0.0/16" {
		t.Fatalf("managed network config not persisted: %+v", got)
	}
	if len(got.PublicSubnetCIDRs) != 2 || got.PublicSubnetCIDRs[0] != "10.42.1.0/24" || got.PublicSubnetCIDRs[1] != "10.42.2.0/24" {
		t.Fatalf("managed public subnet CIDRs not parsed: %+v", got)
	}
	if !got.MapPublicIPOnLaunch {
		t.Fatalf("map public IP setting not persisted: %+v", got)
	}
}

func TestCreateBlueprintRejectsInvalidParams(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	form := url.Values{
		"name": {"bad"}, "project_id": {itoa(pid)}, "cloud_account_id": {itoa(aid)},
		"region": {"ap-southeast-1"}, "instance_type": {"t3.micro"}, "count": {"99"}, // > 10
		"root_volume_gb": {"8"}, "ingress_port": {"22"}, "ingress_protocol": {"tcp"}, "ingress_cidr": {"0.0.0.0/0"},
	}
	rec := authedPost(t, d, "/blueprints", form)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 re-render with error", rec.Code)
	}
	list, _ := d.Store.ListBlueprints(context.Background())
	if len(list) != 0 {
		t.Fatal("invalid blueprint should not be persisted")
	}
}

func TestDeployCreatesEnvironmentAndPreviewJob(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	bpID, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{
		ProjectID: pid, CloudAccountID: aid, Name: "bp", Params: validBPParams(),
	})

	rec := authedPost(t, d, "/blueprints/"+itoa(bpID)+"/deploy", url.Values{"env_name": {"prod"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
	envs, _ := d.Store.ListEnvironments(context.Background())
	if len(envs) != 1 || envs[0].Name != "prod" {
		t.Fatalf("environment not created: %+v", envs)
	}
	jobs, _ := d.Store.ListJobsByEnvironment(context.Background(), envs[0].ID)
	if len(jobs) != 1 || jobs[0].Action != store.ActionPreview || jobs[0].Status != store.JobQueued {
		t.Fatalf("preview job not enqueued: %+v", jobs)
	}
}

func TestDeployPreviewEnqueueFailureLeavesNoPendingEnvironment(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	bpID, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{
		ProjectID: pid, CloudAccountID: aid, Name: "bp", Params: validBPParams(),
	})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	if _, err := d.Store.DB().ExecContext(context.Background(), `
		CREATE TRIGGER reject_preview_job
		BEFORE INSERT ON jobs
		BEGIN
			SELECT RAISE(ABORT, 'sensitive forced enqueue failure');
		END;
	`); err != nil {
		t.Fatalf("create enqueue failure trigger: %v", err)
	}

	rec := authedPost(t, d, "/blueprints/"+itoa(bpID)+"/deploy", url.Values{"env_name": {"prod"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect Location: %v", err)
	}
	if location.Path != "/blueprints" {
		t.Fatalf("redirect path = %q, want /blueprints", location.Path)
	}
	if got := location.Query().Get("error"); got != "任务启动失败，请稍后重试" {
		t.Fatalf("redirect error = %q, want safe retry guidance", got)
	}
	if strings.Contains(rec.Body.String(), "sensitive forced enqueue failure") || strings.Contains(rec.Header().Get("Location"), "sensitive") {
		t.Fatalf("response leaked internal enqueue error: headers=%v body=%s", rec.Header(), rec.Body.String())
	}
	page := authedGet(t, d, rec.Header().Get("Location"))
	if !strings.Contains(page.Body.String(), "任务启动失败，请稍后重试") {
		t.Fatalf("redirected blueprint page did not show safe recovery message: %s", page.Body.String())
	}
	envs, err := d.Store.ListEnvironments(context.Background())
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	if len(envs) != 0 {
		t.Fatalf("failed initial preview left environments behind: %+v", envs)
	}
}

func TestDeployPreviewCleanupFailureRedirectsToEnvironment(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	bpID, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{
		ProjectID: pid, CloudAccountID: aid, Name: "bp", Params: validBPParams(),
	})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	if _, err := d.Store.DB().ExecContext(context.Background(), `
		CREATE TRIGGER reject_preview_job
		BEFORE INSERT ON jobs
		BEGIN
			SELECT RAISE(ABORT, 'sensitive forced enqueue failure');
		END;
		CREATE TRIGGER reject_environment_cleanup
		BEFORE DELETE ON environments
		BEGIN
			SELECT RAISE(ABORT, 'sensitive forced cleanup failure');
		END;
	`); err != nil {
		t.Fatalf("create failure triggers: %v", err)
	}

	rec := authedPost(t, d, "/blueprints/"+itoa(bpID)+"/deploy", url.Values{"env_name": {"prod"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
	envs, err := d.Store.ListEnvironments(context.Background())
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	if len(envs) != 1 || envs[0].Status != store.EnvPending {
		t.Fatalf("cleanup failure environment = %+v, want one visible pending environment", envs)
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect Location: %v", err)
	}
	if want := "/environments/" + itoa(envs[0].ID); location.Path != want {
		t.Fatalf("redirect path = %q, want %q", location.Path, want)
	}
	wantMessage := "任务启动失败，环境未能自动清理，请在此处继续处理"
	if got := location.Query().Get("error"); got != wantMessage {
		t.Fatalf("redirect error = %q, want %q", got, wantMessage)
	}
	response := rec.Header().Get("Location") + rec.Body.String()
	if strings.Contains(response, "sensitive forced enqueue failure") || strings.Contains(response, "sensitive forced cleanup failure") {
		t.Fatalf("response leaked internal failure: headers=%v body=%s", rec.Header(), rec.Body.String())
	}
	page := authedGet(t, d, rec.Header().Get("Location")).Body.String()
	if !strings.Contains(page, wantMessage) || !strings.Contains(page, `/environments/`+itoa(envs[0].ID)+`/preview`) {
		t.Fatalf("retained environment page lacks recovery guidance/action: %s", page)
	}
}

func TestDeployPreviewStaleCleanupRedirectsToEnvironment(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	bpID, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{
		ProjectID: pid, CloudAccountID: aid, Name: "bp", Params: validBPParams(),
	})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	if _, err := d.Store.DB().ExecContext(context.Background(), `
		CREATE TRIGGER reject_preview_job
		BEFORE INSERT ON jobs
		BEGIN
			SELECT RAISE(ABORT, 'sensitive forced enqueue failure');
		END;
		CREATE TRIGGER ignore_environment_cleanup
		BEFORE DELETE ON environments
		BEGIN
			SELECT RAISE(IGNORE);
		END;
	`); err != nil {
		t.Fatalf("create stale cleanup triggers: %v", err)
	}

	rec := authedPost(t, d, "/blueprints/"+itoa(bpID)+"/deploy", url.Values{"env_name": {"prod"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
	envs, err := d.Store.ListEnvironments(context.Background())
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("stale cleanup environments = %+v, want retained environment", envs)
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect Location: %v", err)
	}
	if want := "/environments/" + itoa(envs[0].ID); location.Path != want {
		t.Fatalf("redirect path = %q, want %q", location.Path, want)
	}
	if got, want := location.Query().Get("error"), "环境状态已变化，请在详情中确认后续操作"; got != want {
		t.Fatalf("redirect error = %q, want %q", got, want)
	}
}

func TestDeployPreviewCanceledEnqueueLeavesNoPendingEnvironment(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	bpID, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{
		ProjectID: pid, CloudAccountID: aid, Name: "bp", Params: validBPParams(),
	})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}

	// An unstarted orchestrator has 128 admission slots. Filling them makes the
	// deploy request block after its Environment insert and before its Job insert.
	for i := 0; i < 128; i++ {
		envID, err := d.Store.CreateEnvironment(context.Background(), store.Environment{
			BlueprintID: bpID, CloudAccountID: aid,
			Name: "queued-" + strconv.Itoa(i), PulumiStack: "queued-" + strconv.Itoa(i),
			Region: "ap-southeast-1", Snapshot: validBPParams(),
		})
		if err != nil {
			t.Fatalf("CreateEnvironment %d: %v", i, err)
		}
		if _, err := d.Orchestrator.Enqueue(context.Background(), envID, store.ActionPreview); err != nil {
			t.Fatalf("fill orchestrator queue %d: %v", i, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	form := url.Values{"env_name": {"cancelled-deploy"}}
	req := httptest.NewRequest(http.MethodPost, "/blueprints/"+itoa(bpID)+"/deploy", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(d.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		NewRouter(d).ServeHTTP(rec, req)
		close(done)
	}()

	var createdID int64
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for createdID == 0 {
		envs, listErr := d.Store.ListEnvironments(context.Background())
		if listErr != nil {
			t.Fatalf("ListEnvironments: %v", listErr)
		}
		for _, env := range envs {
			if env.Name == "cancelled-deploy" {
				createdID = env.ID
				break
			}
		}
		if createdID != 0 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("deploy did not reach enqueue after creating its environment")
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled deploy handler did not return")
	}

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
	if _, err := d.Store.GetEnvironment(context.Background(), createdID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("canceled enqueue left pending environment %d: %v", createdID, err)
	}
}

func TestBlueprintFormHasLiveControls(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	seedProjectAccount(t, d)
	body := authedGet(t, d, "/blueprints/new").Body.String()
	for _, want := range []string{
		`hx-get="/blueprints/regions"`,
		`hx-trigger="change, load"`,
		`data-filter-select="#region-select"`,
		`<select name="region" id="region-select" data-metadata-source="region" required`,
		`hx-get="/blueprints/instance-types"`,
		`hx-target="#instance-type-select"`,
		`data-filter-select="#instance-type-select"`,
		`<select name="instance_type" id="instance-type-select" data-metadata-source="instanceType" required`,
		`t3.micro · 2C1G`,
		`hx-get="/blueprints/amis"`,
		`<select name="ami" id="ami-select">`,
		`自动:最新 Ubuntu 26.04 LTS`,
		`name="rds_enabled"`,
		`name="rds_instance_class" value="db.t3.micro"`,
		`name="rds_allocated_storage_gb" type="number" value="20"`,
		`name="rds_db_name" value="app"`,
		`name="rds_username" value="admin"`,
		`name="redis_enabled"`,
		`name="redis_auth_enabled"`,
		`name="redis_node_type" value="cache.t3.micro"`,
		`name="redis_node_count" type="number" value="1"`,
		`name="network_enabled"`,
		`name="network_vpc_cidr" value="10.0.0.0/16"`,
		`name="network_public_subnet_cidrs"`,
		`10.0.1.0/24,10.0.2.0/24`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("blueprint form missing %q", want)
		}
	}
	if strings.Contains(body, `name="network_map_public_ip_launch"`) {
		t.Fatalf("blueprint form should keep map_public_ip_on_launch as an internal default")
	}
	if strings.Contains(body, `hx-on::after-swap`) {
		t.Fatalf("blueprint form should use the centralized metadata cascade without inline after-swap handlers")
	}
}

func blueprintFormValues(projectID, accountID int64, name string) url.Values {
	return url.Values{
		"name": {name}, "project_id": {itoa(projectID)}, "cloud_account_id": {itoa(accountID)},
		"region": {"ap-east-1"}, "instance_type": {"c7g.large"}, "count": {"3"},
		"ami": {"ami-legacy"}, "root_volume_gb": {"16"}, "key_name": {"ops-key"},
		"ingress_port": {"443"}, "ingress_protocol": {"tcp"}, "ingress_cidr": {"10.0.0.0/8"},
		"network_enabled": {"on"}, "network_vpc_cidr": {"10.42.0.0/16"},
		"network_public_subnet_cidrs": {"10.42.1.0/24, 10.42.2.0/24"},
		"rds_enabled":                 {"on"}, "rds_engine_version": {"8.4"}, "rds_instance_class": {"db.t4g.micro"},
		"rds_allocated_storage_gb": {"30"}, "rds_db_name": {"hermes"}, "rds_username": {"operator"},
		"redis_enabled": {"on"}, "redis_auth_enabled": {"on"}, "redis_engine_version": {"7.2"},
		"redis_node_type": {"cache.t4g.micro"}, "redis_node_count": {"2"},
	}
}

func blueprintFormValuesForParams(projectID, accountID int64, name string, params provisioner.BlueprintParams) url.Values {
	params.ApplyDefaults()
	values := url.Values{
		"name": {name}, "project_id": {itoa(projectID)}, "cloud_account_id": {itoa(accountID)},
		"region": {params.Region}, "instance_type": {params.EC2.InstanceType}, "count": {strconv.Itoa(params.EC2.Count)},
		"ami": {params.EC2.AMI}, "root_volume_gb": {strconv.Itoa(params.EC2.RootVolumeGB)}, "key_name": {params.EC2.KeyName},
		"network_vpc_cidr": {params.Network.VPCCIDR}, "network_public_subnet_cidrs": {strings.Join(params.Network.PublicSubnetCIDRs, ",")},
		"rds_engine_version": {params.RDS.EngineVersion}, "rds_instance_class": {params.RDS.InstanceClass},
		"rds_allocated_storage_gb": {strconv.Itoa(params.RDS.AllocatedStorageGB)}, "rds_db_name": {params.RDS.DBName}, "rds_username": {params.RDS.Username},
		"redis_engine_version": {params.Redis.EngineVersion}, "redis_node_type": {params.Redis.NodeType}, "redis_node_count": {strconv.Itoa(params.Redis.NodeCount)},
	}
	if len(params.SecurityGroup.Ingress) > 0 {
		values.Set("ingress_mode", "rule")
		values.Set("ingress_port", strconv.Itoa(params.SecurityGroup.Ingress[0].Port))
		values.Set("ingress_protocol", params.SecurityGroup.Ingress[0].Protocol)
		values.Set("ingress_cidr", params.SecurityGroup.Ingress[0].CIDR)
	} else {
		values.Set("ingress_mode", "none")
	}
	if params.Network.Enabled {
		values.Set("network_enabled", "on")
	}
	if params.RDS.Enabled {
		values.Set("rds_enabled", "on")
	}
	if params.Redis.Enabled {
		values.Set("redis_enabled", "on")
	}
	if params.Redis.AuthEnabled {
		values.Set("redis_auth_enabled", "on")
	}
	return values
}

func TestBlueprintListContainsNoCreateOrDeployForm(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	_, _ = d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "listed", Params: validBPParams()})

	body := authedGet(t, d, "/blueprints").Body.String()
	if !strings.Contains(body, `href="/blueprints/new"`) {
		t.Fatalf("list is missing dedicated create link: %s", body)
	}
	for _, forbidden := range []string{`action="/blueprints"`, `name="env_name"`, `action="/blueprints/1/deploy"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("list contains inline create/deploy control %q: %s", forbidden, body)
		}
	}
}

func TestNewBlueprintPageRendersDefaults(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	seedProjectAccount(t, d)
	rec := authedGet(t, d, "/blueprints/new")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`value="1"`, `value="8"`, `value="22"`, `value="0.0.0.0/0"`, `value="10.0.0.0/16"`, `value="20"`, `value="cache.t3.micro"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("new blueprint defaults missing %q: %s", want, body)
		}
	}
}

func TestNewBlueprintPrerequisiteLoadFailureReturnsSafe500(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	if err := d.Store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	rec := authedGet(t, d, "/blueprints/new")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `href="/accounts/new"`) || strings.Contains(rec.Body.String(), `href="/projects/new"`) {
		t.Fatalf("DB failure rendered misleading empty prerequisites: %s", rec.Body.String())
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "database is closed") {
		t.Fatalf("DB failure leaked details: %s", rec.Body.String())
	}
}

func TestEditBlueprintPagePrefillsAllFields(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	form := blueprintFormValues(pid, aid, "source")
	if rec := authedPost(t, d, "/blueprints", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("seed status = %d: %s", rec.Code, rec.Body.String())
	}
	bps, _ := d.Store.ListBlueprints(context.Background())
	rec := authedGet(t, d, "/blueprints/"+itoa(bps[0].ID)+"/edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"source", "ap-east-1", "c7g.large", "ami-legacy", "ops-key", "10.42.0.0/16", "8.4", "db.t4g.micro", "hermes", "operator", "cache.t4g.micro"} {
		if !strings.Contains(body, want) {
			t.Fatalf("edit page missing saved value %q: %s", want, body)
		}
	}
	if !strings.Contains(body, "现有环境快照不会更改") {
		t.Fatalf("edit page missing immutable snapshot notice: %s", body)
	}
}

func TestUpdateBlueprintPersistsValidatedFields(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	id, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "before", Params: validBPParams()})
	form := blueprintFormValues(pid, aid, "  更新后  ")
	rec := authedPost(t, d, "/blueprints/"+itoa(id), form)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/blueprints/"+itoa(id) {
		t.Fatalf("status/location = %d %q, want 303 detail; body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	got, err := d.Store.GetBlueprint(context.Background(), id)
	if err != nil {
		t.Fatalf("GetBlueprint: %v", err)
	}
	if got.Name != "更新后" || got.Params.EC2.Count != 3 || got.Params.RDS.DBName != "hermes" || !got.Params.Redis.AuthEnabled {
		t.Fatalf("validated fields not persisted: %+v", got)
	}
}

func TestBlueprintEditPreservesUnexposedParameters(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	params := validBPParams()
	params.SecurityGroup.Ingress = []provisioner.Ingress{
		{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "ssh access"},
		{Port: 443, Protocol: "tcp", CIDR: "10.0.0.0/8", Desc: "private https"},
	}
	params.RDS = provisioner.RDS{Enabled: true, Engine: "mysql", EngineVersion: "8.0", InstanceClass: "db.t3.micro", AllocatedStorageGB: 20, DBName: "app", Username: "admin", Port: 15432}
	params.Redis = provisioner.Redis{Enabled: true, Engine: "redis", EngineVersion: "7.2", NodeType: "cache.t3.micro", NodeCount: 1, Port: 6380, AuthEnabled: true}
	id, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: params})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}

	form := blueprintFormValuesForParams(pid, aid, "renamed only", params)
	rec := authedPost(t, d, "/blueprints/"+itoa(id), form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}

	got, err := d.Store.GetBlueprint(context.Background(), id)
	if err != nil {
		t.Fatalf("GetBlueprint: %v", err)
	}
	if len(got.Params.SecurityGroup.Ingress) != 2 || got.Params.SecurityGroup.Ingress[0].Desc != "ssh access" || got.Params.SecurityGroup.Ingress[1].Desc != "private https" {
		t.Fatalf("edit lost ingress rules/descriptions: %+v", got.Params.SecurityGroup.Ingress)
	}
	if got.Params.RDS.Port != 15432 || got.Params.Redis.Port != 6380 {
		t.Fatalf("edit reset custom service ports: rds=%d redis=%d", got.Params.RDS.Port, got.Params.Redis.Port)
	}
}

func TestDuplicateBlueprintPageDoesNotWriteAndPrefillsCopy(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	if rec := authedPost(t, d, "/blueprints", blueprintFormValues(pid, aid, "source")); rec.Code != http.StatusSeeOther {
		t.Fatalf("seed status = %d: %s", rec.Code, rec.Body.String())
	}
	seeded, _ := d.Store.ListBlueprints(context.Background())
	id := seeded[0].ID
	rec := authedGet(t, d, "/blueprints/"+itoa(id)+"/duplicate")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	bps, _ := d.Store.ListBlueprints(context.Background())
	if len(bps) != 1 {
		t.Fatalf("duplicate GET wrote records: %+v", bps)
	}
	body := rec.Body.String()
	for _, want := range []string{"source 副本", "ap-east-1", "c7g.large", "ami-legacy", "ops-key", "10.42.0.0/16", "8.4", "db.t4g.micro", "hermes", "operator", "cache.t4g.micro"} {
		if !strings.Contains(body, want) {
			t.Fatalf("duplicate page missing source value %q: %s", want, body)
		}
	}
}

func TestBlueprintFormUsesModeSpecificActionAndSubmitLabel(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	id, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: validBPParams()})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}

	for _, tc := range []struct {
		name   string
		path   string
		action string
		label  string
	}{
		{name: "create", path: "/blueprints/new", action: "/blueprints", label: "创建蓝图"},
		{name: "edit", path: "/blueprints/" + itoa(id) + "/edit", action: "/blueprints/" + itoa(id), label: "保存更改"},
		{name: "duplicate", path: "/blueprints/" + itoa(id) + "/duplicate", action: "/blueprints", label: "创建副本"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := authedGet(t, d, tc.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, `<form method="post" action="`+tc.action+`" novalidate>`) {
				t.Fatalf("form action = %q, want %q; body=%s", htmlTagContaining(t, body, `<form method="post"`), tc.action, body)
			}
			if !strings.Contains(body, `type="submit">`+tc.label+`</button>`) {
				t.Fatalf("submit label missing %q: %s", tc.label, body)
			}
		})
	}
}

func TestDuplicateBlueprintSourceIdentityIsSubmittedInsideForm(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	id, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: validBPParams()})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	body := authedGet(t, d, "/blueprints/"+itoa(id)+"/duplicate").Body.String()
	start := strings.Index(body, `<form method="post" action="/blueprints" novalidate>`)
	if start < 0 {
		t.Fatalf("duplicate form start not found: %s", body)
	}
	end := strings.Index(body[start:], `</form>`)
	if end < 0 {
		t.Fatalf("duplicate form end not found: %s", body)
	}
	form := body[start : start+end]
	for _, want := range []string{`name="blueprint_mode" value="duplicate"`, `name="source_blueprint_id" value="` + itoa(id) + `"`} {
		if !strings.Contains(form, want) {
			t.Fatalf("duplicate source identity %q is outside the submitted form: %s", want, body)
		}
	}
}

func TestDuplicateBlueprintFormNormalizesDisabledLegacyRedisAuth(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	params := validBPParams()
	params.Redis.Enabled = false
	params.Redis.AuthEnabled = true
	id, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "legacy", Params: params})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}

	rec := authedGet(t, d, "/blueprints/"+itoa(id)+"/duplicate")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	tag := htmlTagContaining(t, rec.Body.String(), "data-redis-auth")
	if strings.Contains(tag, " disabled") || strings.Contains(tag, " checked") {
		t.Fatalf("legacy disabled Redis AUTH control = %s, want server-functional and unchecked", tag)
	}
	bps, _ := d.Store.ListBlueprints(context.Background())
	if len(bps) != 1 {
		t.Fatalf("duplicate GET wrote records: %+v", bps)
	}
}

func TestZeroIngressBlueprintEditAndDuplicateRender(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	params := validBPParams()
	params.SecurityGroup.Ingress = nil
	id, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "private", Params: params})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	for _, path := range []string{"/blueprints/" + itoa(id) + "/edit", "/blueprints/" + itoa(id) + "/duplicate"} {
		rec := authedGet(t, d, path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200; body=%s", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "template:") {
			t.Errorf("GET %s returned template execution error: %s", path, rec.Body.String())
		}
	}
}

func TestZeroIngressBlueprintUpdateAndDuplicateSavePreserveNoRule(t *testing.T) {
	for _, mode := range []string{"update", "duplicate-save"} {
		t.Run(mode, func(t *testing.T) {
			d := testDepsWithOrchestrator(t)
			pid, aid := seedProjectAccount(t, d)
			params := validBPParams()
			params.SecurityGroup.Ingress = nil
			id, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "private", Params: params})
			if err != nil {
				t.Fatalf("CreateBlueprint: %v", err)
			}
			form := blueprintFormValues(pid, aid, "private-copy")
			form.Del("ingress_port")
			form.Del("ingress_protocol")
			form.Del("ingress_cidr")
			path := "/blueprints"
			if mode == "update" {
				path += "/" + itoa(id)
			}
			rec := authedPost(t, d, path, form)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
			}
			bps, _ := d.Store.ListBlueprints(context.Background())
			for _, bp := range bps {
				if bp.Name == "private-copy" && len(bp.Params.SecurityGroup.Ingress) != 0 {
					t.Fatalf("saved blueprint added ingress: %+v", bp.Params.SecurityGroup.Ingress)
				}
			}
		})
	}
}

func TestPartialIngressInputRerendersSafely(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	form := blueprintFormValues(pid, aid, "partial-ingress")
	form.Set("ingress_mode", "rule")
	form.Set("ingress_port", "")
	form.Set("ingress_protocol", "tcp")
	form.Set("ingress_cidr", "")
	rec := authedPost(t, d, "/blueprints", form)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "template:") {
		t.Fatalf("partial ingress caused template error: %s", body)
	}
	port := htmlTagContaining(t, body, `name="ingress_port"`)
	if !strings.Contains(port, `value=""`) || strings.Contains(port, `value="22"`) {
		t.Fatalf("partial ingress port was not preserved: %s", port)
	}
}

func TestDuplicateBlueprintSaveCreatesSecondRecord(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	sourceID, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: validBPParams()})
	rec := authedPost(t, d, "/blueprints", blueprintFormValues(pid, aid, "source 副本"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	bps, _ := d.Store.ListBlueprints(context.Background())
	if len(bps) != 2 {
		t.Fatalf("duplicate save records = %+v, want two", bps)
	}
	source, _ := d.Store.GetBlueprint(context.Background(), sourceID)
	if source.Name != "source" || source.Params.Region != "ap-southeast-1" {
		t.Fatalf("duplicate save mutated source: %+v", source)
	}
}

func TestBlueprintDuplicatePreservesUnexposedParametersFromSource(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	params := validBPParams()
	params.SecurityGroup.Ingress = []provisioner.Ingress{
		{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "ssh access"},
		{Port: 8443, Protocol: "tcp", CIDR: "10.0.0.0/8", Desc: "internal app"},
	}
	params.RDS = provisioner.RDS{Enabled: true, Engine: "mysql", EngineVersion: "8.0", InstanceClass: "db.t3.micro", AllocatedStorageGB: 20, DBName: "app", Username: "admin", Port: 15432}
	params.Redis = provisioner.Redis{Enabled: true, Engine: "redis", EngineVersion: "7.2", NodeType: "cache.t3.micro", NodeCount: 1, Port: 6380, AuthEnabled: true}
	sourceID, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: params})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}

	form := blueprintFormValuesForParams(pid, aid, "source copy", params)
	form.Set("blueprint_mode", "duplicate")
	form.Set("source_blueprint_id", itoa(sourceID))
	rec := authedPost(t, d, "/blueprints", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
	bps, err := d.Store.ListBlueprints(context.Background())
	if err != nil || len(bps) != 2 {
		t.Fatalf("ListBlueprints = %+v, err=%v", bps, err)
	}
	var copy store.Blueprint
	for _, bp := range bps {
		if bp.Name == "source copy" {
			copy = bp
		}
	}
	if copy.ID == 0 {
		t.Fatalf("duplicate source identity was not honored: %+v", bps)
	}
	if len(copy.Params.SecurityGroup.Ingress) != 2 || copy.Params.SecurityGroup.Ingress[0].Desc != "ssh access" || copy.Params.SecurityGroup.Ingress[1].Desc != "internal app" {
		t.Fatalf("duplicate lost ingress rules/descriptions: %+v", copy.Params.SecurityGroup.Ingress)
	}
	if copy.Params.RDS.Port != 15432 || copy.Params.Redis.Port != 6380 {
		t.Fatalf("duplicate reset custom service ports: rds=%d redis=%d", copy.Params.RDS.Port, copy.Params.Redis.Port)
	}
	source, err := d.Store.GetBlueprint(context.Background(), sourceID)
	if err != nil || source.Name != "source" {
		t.Fatalf("duplicate mutated source identity: %+v, err=%v", source, err)
	}
}

func TestBlueprintValidationRetainsEditAndDuplicateWorkflowIdentity(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	sourceID, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: validBPParams()})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}

	editForm := blueprintFormValues(pid, aid, "")
	edit := authedPost(t, d, "/blueprints/"+itoa(sourceID), editForm)
	if edit.Code != http.StatusUnprocessableEntity {
		t.Fatalf("edit status = %d, want 422; body=%s", edit.Code, edit.Body.String())
	}
	for _, want := range []string{"编辑蓝图", `action="/blueprints/` + itoa(sourceID) + `"`, "保存更改"} {
		if !strings.Contains(edit.Body.String(), want) {
			t.Fatalf("edit validation lost workflow marker %q: %s", want, edit.Body.String())
		}
	}

	duplicateForm := blueprintFormValues(pid, aid, "")
	duplicateForm.Set("blueprint_mode", "duplicate")
	duplicateForm.Set("source_blueprint_id", itoa(sourceID))
	duplicate := authedPost(t, d, "/blueprints", duplicateForm)
	if duplicate.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate status = %d, want 422; body=%s", duplicate.Code, duplicate.Body.String())
	}
	for _, want := range []string{"复制蓝图", `action="/blueprints"`, "创建副本", `name="source_blueprint_id" value="` + itoa(sourceID) + `"`} {
		if !strings.Contains(duplicate.Body.String(), want) {
			t.Fatalf("duplicate validation lost workflow marker %q: %s", want, duplicate.Body.String())
		}
	}
}

func TestBlueprintDuplicateRejectsInvalidSourceIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sourceID string
		want     int
	}{
		{name: "malformed", sourceID: "not-a-number", want: http.StatusBadRequest},
		{name: "missing", sourceID: "999", want: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDepsWithOrchestrator(t)
			pid, aid := seedProjectAccount(t, d)
			form := blueprintFormValues(pid, aid, "copy")
			form.Set("blueprint_mode", "duplicate")
			form.Set("source_blueprint_id", tc.sourceID)
			rec := authedPost(t, d, "/blueprints", form)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
			bps, err := d.Store.ListBlueprints(context.Background())
			if err != nil || len(bps) != 0 {
				t.Fatalf("invalid source created blueprints: %+v, err=%v", bps, err)
			}
		})
	}
}

func TestBlueprintDeployPageRequiresEnvironmentName(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	id, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: validBPParams()})
	get := authedGet(t, d, "/blueprints/"+itoa(id)+"/deploy")
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "source") || !strings.Contains(get.Body.String(), "t3.micro") {
		t.Fatalf("deploy page status/body = %d %s", get.Code, get.Body.String())
	}
	post := authedPost(t, d, "/blueprints/"+itoa(id)+"/deploy", url.Values{"env_name": {"   "}})
	if post.Code != http.StatusUnprocessableEntity || !strings.Contains(post.Body.String(), "请输入环境名") {
		t.Fatalf("blank deploy status/body = %d %s", post.Code, post.Body.String())
	}
	envs, _ := d.Store.ListEnvironments(context.Background())
	if len(envs) != 0 {
		t.Fatalf("blank environment name created records: %+v", envs)
	}
}

func TestDeleteBlueprintRejectsMalformedAndMissingIDs(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/blueprints/not-a-number", want: http.StatusBadRequest},
		{path: "/blueprints/999", want: http.StatusNotFound},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, tc.path, nil)
			req.AddCookie(d.Auth.IssueCookie())
			rec := httptest.NewRecorder()
			NewRouter(d).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "暂无蓝图") {
				t.Fatalf("delete failure returned misleading success fragment: %s", rec.Body.String())
			}
		})
	}
}

func TestDeleteBlueprintReturnsConflictOnlyForForeignKeyReference(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	bpID, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "used", Params: validBPParams()})
	if _, err := d.Store.CreateEnvironment(context.Background(), store.Environment{BlueprintID: bpID, CloudAccountID: aid, Name: "env", PulumiStack: "env-1", Region: "ap-southeast-1"}); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	rec := authedDelete(t, d, "/blueprints/"+itoa(bpID))
	if rec.Code != http.StatusConflict || strings.Contains(rec.Body.String(), "constraint") {
		t.Fatalf("FK delete status/body = %d %s, want safe 409", rec.Code, rec.Body.String())
	}
}

func TestDeleteBlueprintOperationalFailuresReturn500WithoutLeak(t *testing.T) {
	t.Run("delete failure", func(t *testing.T) {
		d := testDepsWithOrchestrator(t)
		pid, aid := seedProjectAccount(t, d)
		bpID, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "target", Params: validBPParams()})
		if _, err := d.Store.DB().ExecContext(context.Background(), `CREATE TRIGGER reject_blueprint_delete BEFORE DELETE ON blueprints BEGIN SELECT RAISE(ABORT, 'sensitive operational failure'); END`); err != nil {
			t.Fatalf("create trigger: %v", err)
		}
		rec := authedDelete(t, d, "/blueprints/"+itoa(bpID))
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "sensitive") {
			t.Fatalf("operational delete status/body = %d %s, want safe 500", rec.Code, rec.Body.String())
		}
	})

	t.Run("list failure after delete", func(t *testing.T) {
		d := testDepsWithOrchestrator(t)
		pid, aid := seedProjectAccount(t, d)
		targetID, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "target", Params: validBPParams()})
		otherID, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "other", Params: validBPParams()})
		if _, err := d.Store.DB().ExecContext(context.Background(), `UPDATE blueprints SET params_json = 'sensitive invalid json' WHERE id = ?`, otherID); err != nil {
			t.Fatalf("corrupt other blueprint: %v", err)
		}
		rec := authedDelete(t, d, "/blueprints/"+itoa(targetID))
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "sensitive") {
			t.Fatalf("post-delete list status/body = %d %s, want safe 500", rec.Code, rec.Body.String())
		}
	})
}

func TestHTMXBlueprintDeleteFailuresExposeSafeFeedbackEvents(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, d Deps, blueprintID, accountID int64)
		want    int
		message string
	}{
		{
			name: "referenced conflict",
			prepare: func(t *testing.T, d Deps, blueprintID, accountID int64) {
				if _, err := d.Store.CreateEnvironment(context.Background(), store.Environment{BlueprintID: blueprintID, CloudAccountID: accountID, Name: "env", PulumiStack: "env-1", Region: "ap-southeast-1"}); err != nil {
					t.Fatalf("CreateEnvironment: %v", err)
				}
			},
			want:    http.StatusConflict,
			message: "该蓝图已有环境引用，无法删除",
		},
		{
			name: "operational failure",
			prepare: func(t *testing.T, d Deps, _, _ int64) {
				if _, err := d.Store.DB().ExecContext(context.Background(), `CREATE TRIGGER reject_blueprint_delete_feedback BEFORE DELETE ON blueprints BEGIN SELECT RAISE(ABORT, 'sensitive delete failure'); END`); err != nil {
					t.Fatalf("create trigger: %v", err)
				}
			},
			want:    http.StatusInternalServerError,
			message: "无法删除蓝图",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDepsWithOrchestrator(t)
			pid, aid := seedProjectAccount(t, d)
			blueprintID, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "target", Params: validBPParams()})
			if err != nil {
				t.Fatalf("CreateBlueprint: %v", err)
			}
			tc.prepare(t, d, blueprintID, aid)

			req := httptest.NewRequest(http.MethodDelete, "/blueprints/"+itoa(blueprintID), nil)
			req.Header.Set("HX-Request", "true")
			req.AddCookie(d.Auth.IssueCookie())
			rec := httptest.NewRecorder()
			NewRouter(d).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
			var events map[string]struct {
				Message string `json:"message"`
			}
			header := rec.Header().Get("HX-Trigger")
			for i := 0; i < len(header); i++ {
				if header[i] > 0x7f {
					t.Fatalf("HX-Trigger must be transport-safe ASCII, got byte %#x in %q", header[i], header)
				}
			}
			if err := json.Unmarshal([]byte(header), &events); err != nil {
				t.Fatalf("HX-Trigger is not valid JSON: %q: %v", header, err)
			}
			if got := events["blueprint-delete-error"].Message; got != tc.message {
				t.Fatalf("feedback message = %q, want %q", got, tc.message)
			}
			if strings.Contains(header, "sensitive") {
				t.Fatalf("feedback leaked operational detail: %s", header)
			}
		})
	}
}

func TestHTMXBlueprintDeleteSuccessAnnouncesAfterSwap(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	blueprintID, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{
		ProjectID: pid, CloudAccountID: aid, Name: "delete-me", Params: validBPParams(),
	})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/blueprints/"+itoa(blueprintID), nil)
	req.Header.Set("HX-Request", "true")
	req.AddCookie(d.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	NewRouter(d).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	assertAfterSwapDeleteSuccess(t, rec, "blueprint-delete-success", "蓝图已删除")
}

func TestBlueprintDeleteListLinksToNoJavaScriptConfirmation(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	bpID, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "delete-me", Params: validBPParams()})
	body := authedGet(t, d, "/blueprints").Body.String()
	deleteHref := `/blueprints/` + itoa(bpID) + `/delete`
	deleteLink := requireHTMLTagClassTokens(t, body, `href="`+deleteHref+`"`, "btn", "btn-outline-danger")
	for _, want := range []string{
		`href="` + deleteHref + `"`,
		`hx-delete="/blueprints/` + itoa(bpID) + `"`,
		`hx-target="#blueprint-rows"`,
		`hx-swap="innerHTML"`,
		`hx-confirm="删除蓝图“delete-me”？"`,
		`data-loading-label="删除中…"`,
	} {
		if !strings.Contains(deleteLink, want) {
			t.Fatalf("delete fallback link missing %q: %s", want, deleteLink)
		}
	}
	if strings.Contains(body, `action="/blueprints/`+itoa(bpID)+`/delete"`) {
		t.Fatalf("list bypasses server confirmation with direct POST action: %s", body)
	}
	if !strings.Contains(body, `id="blueprint-feedback"`) || !strings.Contains(body, `role="alert"`) || !strings.Contains(body, `aria-live="assertive"`) {
		t.Fatalf("blueprint list lacks an accessible HTMX failure target: %s", body)
	}
	if !strings.Contains(body, `id="blueprint-delete-status" class="notice ok" role="status" aria-live="polite" tabindex="-1" hidden`) {
		t.Fatalf("blueprint list lacks an accessible HTMX success target: %s", body)
	}
	if !strings.Contains(body, `src="/static/ui_feedback.js"`) {
		t.Fatalf("blueprint list does not load the safe feedback helper: %s", body)
	}
}

func TestBlueprintDeleteConfirmationGETDoesNotDelete(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	bpID, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "delete-me", Params: validBPParams()})

	rec := authedGet(t, d, "/blueprints/"+itoa(bpID)+"/delete")
	if rec.Code != http.StatusOK {
		t.Fatalf("confirmation status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"delete-me",
		`method="post"`,
		`action="/blueprints/` + itoa(bpID) + `/delete"`,
		`href="/blueprints"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("confirmation page missing %q: %s", want, body)
		}
	}
	if _, err := d.Store.GetBlueprint(context.Background(), bpID); err != nil {
		t.Fatalf("GET confirmation deleted blueprint: %v", err)
	}
}

func TestBlueprintDeleteConfirmationPOSTDeletes(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	bpID, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "delete-me", Params: validBPParams()})

	rec := authedPost(t, d, "/blueprints/"+itoa(bpID)+"/delete", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/blueprints" {
		t.Fatalf("confirmed delete status/location = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if _, err := d.Store.GetBlueprint(context.Background(), bpID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("confirmed POST did not delete blueprint: %v", err)
	}
}

func TestBlueprintDeleteConfirmationRejectsMalformedAndMissingIDs(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodGet, path: "/blueprints/not-a-number/delete", want: http.StatusBadRequest},
		{method: http.MethodGet, path: "/blueprints/999/delete", want: http.StatusNotFound},
		{method: http.MethodPost, path: "/blueprints/not-a-number/delete", want: http.StatusBadRequest},
		{method: http.MethodPost, path: "/blueprints/999/delete", want: http.StatusNotFound},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.AddCookie(d.Auth.IssueCookie())
			rec := httptest.NewRecorder()
			NewRouter(d).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func authedDelete(t *testing.T, d Deps, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	req.AddCookie(d.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	NewRouter(d).ServeHTTP(rec, req)
	return rec
}

func TestBlueprintDeployTrimsEnvironmentName(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	id, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: validBPParams()})
	rec := authedPost(t, d, "/blueprints/"+itoa(id)+"/deploy", url.Values{"env_name": {"  staging  "}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	envs, _ := d.Store.ListEnvironments(context.Background())
	if len(envs) != 1 || envs[0].Name != "staging" {
		t.Fatalf("environment name was not trimmed: %+v", envs)
	}
}

func TestSlugLimitsPulumiStackBaseAndHandlesUnicodeFallback(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "91 byte boundary", input: strings.Repeat("a", 91), want: strings.Repeat("a", 91)},
		{name: "92 byte truncation", input: strings.Repeat("b", 92), want: strings.Repeat("b", 91)},
		{name: "mixed Unicode", input: "生产-" + strings.Repeat("c", 100), want: strings.Repeat("c", 91)},
		{name: "empty ASCII slug fallback", input: "生产环境🚀", want: "env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := slug(tc.input); got != tc.want {
				t.Fatalf("slug(%q) = %q (%d bytes), want %q (%d bytes)", tc.input, got, len(got), tc.want, len(tc.want))
			}
			if got := slug(tc.input) + "-12345678"; len(got) > 100 {
				t.Fatalf("stack name length = %d, want <= 100: %q", len(got), got)
			}
		})
	}
}

func TestBlueprintDeployNeverPersistsOverlongPulumiStack(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	id, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: validBPParams()})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	displayName := strings.Repeat("a", 92)
	rec := authedPost(t, d, "/blueprints/"+itoa(id)+"/deploy", url.Values{"env_name": {displayName}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
	envs, err := d.Store.ListEnvironments(context.Background())
	if err != nil || len(envs) != 1 {
		t.Fatalf("ListEnvironments = %+v, err=%v", envs, err)
	}
	if envs[0].Name != displayName {
		t.Fatalf("display name = %q, want untruncated %q", envs[0].Name, displayName)
	}
	if len(envs[0].PulumiStack) > 100 {
		t.Fatalf("persisted invalid stack length = %d: %q", len(envs[0].PulumiStack), envs[0].PulumiStack)
	}
	jobs, err := d.Store.ListJobsByEnvironment(context.Background(), envs[0].ID)
	if err != nil || len(jobs) != 1 || jobs[0].Status != store.JobQueued {
		t.Fatalf("preview jobs = %+v, err=%v; want one queued job for valid stack", jobs, err)
	}
}

func TestBlueprintPrerequisitesLinkToAccountAndProjectCreation(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	body := authedGet(t, d, "/blueprints/new").Body.String()
	for _, want := range []string{`href="/accounts/new"`, `href="/projects/new"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("prerequisite page missing exact link %q: %s", want, body)
		}
	}
}

func TestBlueprintPagesReturnNotFoundForUnknownID(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	for _, path := range []string{"/blueprints/999", "/blueprints/999/edit", "/blueprints/999/duplicate", "/blueprints/999/deploy"} {
		if rec := authedGet(t, d, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, rec.Code)
		}
	}
	if rec := authedPost(t, d, "/blueprints/999", url.Values{}); rec.Code != http.StatusNotFound {
		t.Errorf("POST unknown status = %d, want 404", rec.Code)
	}
}

func TestBlueprintOperationalRouteErrorsAreSafe(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		path   func(id int64) string
	}{
		{name: "list", method: http.MethodGet, path: func(int64) string { return "/blueprints" }},
		{name: "detail", method: http.MethodGet, path: func(id int64) string { return "/blueprints/" + itoa(id) }},
		{name: "edit", method: http.MethodGet, path: func(id int64) string { return "/blueprints/" + itoa(id) + "/edit" }},
		{name: "duplicate", method: http.MethodGet, path: func(id int64) string { return "/blueprints/" + itoa(id) + "/duplicate" }},
		{name: "deploy", method: http.MethodGet, path: func(id int64) string { return "/blueprints/" + itoa(id) + "/deploy" }},
		{name: "update", method: http.MethodPost, path: func(id int64) string { return "/blueprints/" + itoa(id) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDepsWithOrchestrator(t)
			pid, aid := seedProjectAccount(t, d)
			id, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: validBPParams()})
			if err != nil {
				t.Fatalf("CreateBlueprint: %v", err)
			}
			if err := d.Store.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			var rec *httptest.ResponseRecorder
			if tc.method == http.MethodPost {
				rec = authedPost(t, d, tc.path(id), blueprintFormValues(pid, aid, "updated"))
			} else {
				rec = authedGet(t, d, tc.path(id))
			}
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
			}
			body := strings.ToLower(rec.Body.String())
			for _, leaked := range []string{"database is closed", "sql:", "constraint failed"} {
				if strings.Contains(body, leaked) {
					t.Fatalf("operational response leaked %q: %s", leaked, rec.Body.String())
				}
			}
		})
	}
}

func TestBlueprintDeployEnvironmentInsertFailureIsSafeAndLeavesNoJob(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	id, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: validBPParams()})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	if _, err := d.Store.DB().ExecContext(context.Background(), `CREATE TRIGGER reject_environment_insert BEFORE INSERT ON environments BEGIN SELECT RAISE(ABORT, 'sensitive environment insert'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	rec := authedPost(t, d, "/blueprints/"+itoa(id)+"/deploy", url.Values{"env_name": {"prod"}})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	body := strings.ToLower(rec.Body.String())
	if strings.Contains(body, "sensitive") || strings.Contains(body, "constraint") {
		t.Fatalf("environment insert failure leaked details: %s", rec.Body.String())
	}
	envs, err := d.Store.ListEnvironments(context.Background())
	if err != nil || len(envs) != 0 {
		t.Fatalf("failed deploy environments = %+v, err=%v", envs, err)
	}
	var jobs int
	if err := d.Store.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM jobs`).Scan(&jobs); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if jobs != 0 {
		t.Fatalf("failed deploy left %d jobs", jobs)
	}
}

func TestBlueprintValidationUsesCharacterSafeLengths(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	form := blueprintFormValues(pid, aid, strings.Repeat("蓝", 129))
	rec := authedPost(t, d, "/blueprints", form)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "128") {
		t.Fatalf("long Unicode name status/body = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBlueprintDetailRendersSummaryActionsAndActiveNavigation(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	id, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "detail-source", Params: validBPParams()})
	rec := authedGet(t, d, "/blueprints/"+itoa(id))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"detail-source", "ap-southeast-1", "t3.micro", `/blueprints/` + itoa(id) + `/edit`, `/blueprints/` + itoa(id) + `/duplicate`, `/blueprints/` + itoa(id) + `/deploy`, `href="/blueprints" aria-current="page"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail page missing %q: %s", want, body)
		}
	}
}

func TestBlueprintFormPropagatesSelectedMetadataQueries(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	params := validBPParams()
	params.Region, params.EC2.InstanceType, params.EC2.AMI = "legacy-region-1", "legacy.large", "ami-legacy"
	id, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "legacy", Params: params})
	body := authedGet(t, d, "/blueprints/"+itoa(id)+"/edit").Body.String()
	for _, want := range []string{`name="selected_region" data-selection-hint="region" value="legacy-region-1"`, `name="selected_instance_type" data-selection-hint="instanceType" value="legacy.large"`, `name="selected_ami" data-selection-hint="ami" value="ami-legacy"`, `hx-include="[name='cloud_account_id'],[name='region'],[name='selected_instance_type']"`, `hx-include="[name='cloud_account_id'],[name='region'],[name='instance_type'],[name='selected_ami']"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("edit metadata propagation missing %q: %s", want, body)
		}
	}
}

func TestBlueprintFormMarksMetadataDependenciesForReset(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	id, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "legacy", Params: validBPParams()})
	body := authedGet(t, d, "/blueprints/"+itoa(id)+"/edit").Body.String()
	for _, want := range []string{
		`data-metadata-source="account"`, `data-metadata-source="region"`, `data-metadata-source="instanceType"`,
		`data-selection-hint="region"`, `data-selection-hint="instanceType"`, `data-selection-hint="ami"`,
		`src="/static/blueprint_metadata.js"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metadata reset wiring missing %q: %s", want, body)
		}
	}
}

func TestBlueprintValidationErrorsAreAccessibleAndDoNotLeakSecrets(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	form := blueprintFormValues(pid, aid, "")
	form.Set("sensitive", "do-not-render")
	rec := authedPost(t, d, "/blueprints", form)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`role="alert"`, `aria-invalid="true"`, `id="error-name"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("accessible validation missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "do-not-render") || strings.Contains(body, "SecretAccessKey") {
		t.Fatalf("validation response leaked unrelated secret material: %s", body)
	}
}

func TestBlueprintOwnershipErrorsAssociateAffectedControls(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	form := blueprintFormValues(pid, aid, "valid")
	form.Del("name")
	form.Del("project_id")
	form.Del("cloud_account_id")
	rec := authedPost(t, d, "/blueprints", form)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	for _, tc := range []struct{ marker, errorID string }{
		{marker: `name="name"`, errorID: "error-name"},
		{marker: `name="project_id"`, errorID: "error-project_id"},
		{marker: `name="cloud_account_id"`, errorID: "error-cloud_account_id"},
	} {
		tag := htmlTagContaining(t, body, tc.marker)
		if !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, `aria-describedby="`+tc.errorID+`"`) {
			t.Errorf("control %q lacks error association: %s", tc.marker, tag)
		}
		if !strings.Contains(body, `id="`+tc.errorID+`"`) {
			t.Errorf("response lacks error target %q", tc.errorID)
		}
	}
}

func TestBlueprintParamsErrorAssociatesInvalidControl(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	form := blueprintFormValues(pid, aid, "invalid-count")
	form.Set("count", "99")
	rec := authedPost(t, d, "/blueprints", form)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	tag := htmlTagContaining(t, body, `name="count"`)
	if !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, `aria-describedby="error-params"`) || !strings.Contains(body, `id="error-params"`) {
		t.Fatalf("params error is not associated with count: tag=%s body=%s", tag, body)
	}
}

func TestBlueprintParamsErrorMarksOnlyIdentifiableControl(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	form := blueprintFormValues(pid, aid, "invalid-region")
	form.Set("region", "")
	rec := authedPost(t, d, "/blueprints", form)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	region := htmlTagContaining(t, body, `name="region"`)
	count := htmlTagContaining(t, body, `name="count"`)
	if !strings.Contains(region, `aria-invalid="true"`) || !strings.Contains(region, `aria-describedby="error-params"`) {
		t.Fatalf("region error is not associated: %s", region)
	}
	if strings.Contains(count, `aria-invalid="true"`) {
		t.Fatalf("region error falsely marks count invalid: %s", count)
	}
}

func TestBlueprintOptionalParamErrorAssociatesInvalidControl(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	form := blueprintFormValues(pid, aid, "invalid-rds-storage")
	form.Set("rds_allocated_storage_gb", "10")
	rec := authedPost(t, d, "/blueprints", form)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	tag := htmlTagContaining(t, rec.Body.String(), `name="rds_allocated_storage_gb"`)
	if !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, `aria-describedby="error-params"`) {
		t.Fatalf("RDS storage error is not associated: %s", tag)
	}
}

func TestValidBlueprintFormsDoNotClaimInvalid(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	id, err := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "valid", Params: validBPParams()})
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	for _, path := range []string{"/blueprints/new", "/blueprints/" + itoa(id) + "/edit"} {
		body := authedGet(t, d, path).Body.String()
		if strings.Contains(body, `aria-invalid="true"`) {
			t.Fatalf("valid form claims invalid: %s", body)
		}
	}
}

func TestBlueprintDeployErrorAssociatesEnvironmentName(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	pid, aid := seedProjectAccount(t, d)
	id, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{ProjectID: pid, CloudAccountID: aid, Name: "source", Params: validBPParams()})
	rec := authedPost(t, d, "/blueprints/"+itoa(id)+"/deploy", url.Values{"env_name": {" "}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	tag := htmlTagContaining(t, body, `name="env_name"`)
	if !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, `aria-describedby="env-name-error env-name-hint"`) || !strings.Contains(body, `id="env-name-error"`) {
		t.Fatalf("deploy error is not associated: tag=%s body=%s", tag, body)
	}
	valid := authedGet(t, d, "/blueprints/"+itoa(id)+"/deploy").Body.String()
	if strings.Contains(valid, `aria-invalid="true"`) || strings.Contains(valid, `id="env-name-error"`) {
		t.Fatalf("valid deploy form claims invalid: %s", valid)
	}
}

func htmlTagContaining(t *testing.T, body, marker string) string {
	t.Helper()
	markerAt := strings.Index(body, marker)
	if markerAt < 0 {
		t.Fatalf("response missing marker %q: %s", marker, body)
	}
	start := strings.LastIndex(body[:markerAt], "<")
	endAt := strings.Index(body[markerAt:], ">")
	if start < 0 || endAt < 0 {
		t.Fatalf("cannot locate tag containing %q", marker)
	}
	return body[start : markerAt+endAt+1]
}

func TestBlueprintOptionalSectionsWorkWithoutJavaScript(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	seedProjectAccount(t, d)
	body := authedGet(t, d, "/blueprints/new").Body.String()
	for _, id := range []string{"network-fields", "rds-fields", "redis-fields"} {
		if strings.Contains(body, `id="`+id+`" hidden`) {
			t.Fatalf("optional section %s is unavailable without JavaScript: %s", id, body)
		}
	}
	for _, want := range []string{`data-disclosure`, `aria-controls="network-fields"`, `data-redis-enabled`, `data-redis-auth`} {
		if !strings.Contains(body, want) {
			t.Fatalf("progressive disclosure hook missing %q: %s", want, body)
		}
	}
}

func TestBlueprintRedisAuthUsesOneServerFunctionalControl(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	seedProjectAccount(t, d)
	body := authedGet(t, d, "/blueprints/new").Body.String()
	if got := strings.Count(body, `name="redis_auth_enabled"`); got != 1 {
		t.Fatalf("Redis AUTH control count = %d, want 1: %s", got, body)
	}
	tag := htmlTagContaining(t, body, `name="redis_auth_enabled"`)
	if strings.Contains(tag, " disabled") {
		t.Fatalf("server-rendered Redis AUTH is not functional without JS: %s", tag)
	}
}
