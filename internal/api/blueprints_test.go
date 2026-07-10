package api

import (
	"context"
	"database/sql"
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
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 re-render with error", rec.Code)
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
	body := authedGet(t, d, "/blueprints").Body.String()
	for _, want := range []string{
		`hx-get="/blueprints/regions"`,
		`hx-trigger="change, load"`,
		`data-filter-select="#region-select"`,
		`<select name="region" id="region-select" required`,
		`hx-get="/blueprints/instance-types"`,
		`hx-target="#instance-type-select"`,
		`data-filter-select="#instance-type-select"`,
		`<select name="instance_type" id="instance-type-select" required`,
		`t3.micro · 2C1G`,
		`hx-get="/blueprints/amis"`,
		`hx-on::after-swap="this.dispatchEvent(new Event('change', {bubbles:true}))"`,
		`function filterSelectOptions(input)`,
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
}
