package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/orchestrator"
	"github.com/0xFredZhang/Hermes/internal/provisioner"
	"github.com/0xFredZhang/Hermes/internal/store"
)

// stubProvisioner is a no-op Provisioner for handler tests.
type stubProvisioner struct{}

func (stubProvisioner) Preview(_ context.Context, _ provisioner.Spec, _ io.Writer) (provisioner.PreviewResult, error) {
	return provisioner.PreviewResult{Creates: 1}, nil
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
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("blueprint form missing %q", want)
		}
	}
}
