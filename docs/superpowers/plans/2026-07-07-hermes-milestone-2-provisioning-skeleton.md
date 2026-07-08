# Hermes M2:Provisioning 走通骨架(SG + EC2 端到端)Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 M1 地基上,让用户能用「安全组 + EC2」蓝图一键 preview→确认→up、看实时日志、展示公网 IP、并能 destroy 清掉 —— 端到端走通整条 provisioning 链路。

**Architecture:** 分层新增 `provisioner`(引擎抽象 + Pulumi 实现)、`orchestrator`(内存 Job 队列 + worker pool + 状态机 + SSE 日志 broker),store 加 4 个实体(Project/Blueprint/Environment/Job),api/web 加对应页面与流程。执行引擎经 `Provisioner` 接口解耦;Pulumi 用 Automation API 的 inline program;state 存本地 `file://` 后端;凭证 per-job 经 workspace EnvVars 注入。

**Tech Stack:** Go 1.25、`net/http`、`html/template`、htmx、SSE、`modernc.org/sqlite`、Pulumi Automation API(`github.com/pulumi/pulumi/sdk/v3`)、Pulumi AWS(`github.com/pulumi/pulumi-aws/sdk/v6`)、`github.com/google/uuid`。

**Spec:** [../specs/2026-07-07-hermes-m2-provisioning-skeleton-design.md](../specs/2026-07-07-hermes-m2-provisioning-skeleton-design.md)

## Global Constraints

- module path:`github.com/0xFredZhang/Hermes`
- Go 版本:1.25(承接现有 `go.mod`)
- SQLite 驱动:`modernc.org/sqlite`(纯 Go);`PRAGMA foreign_keys=ON` 已在 `store.Open` 设置,外键约束生效
- 主密钥:`HERMES_MASTER_KEY`(base64 32 字节);M2 新增第三个 HKDF 子密钥域 `"hermes:pulumi-passphrase:v1"` 作 Pulumi secrets passphrase
- state 后端:本地 `file://`,经 `HERMES_PULUMI_BACKEND` 配置(默认 `file://<cwd>/data/pulumi-state`)
- 凭证隔离:解密后的 AWS 凭证只经 Automation API workspace `EnvVars` 注入,**绝不写全局环境变量**
- 运行时外部依赖:`pulumi` CLI 需在 PATH + AWS provider 插件(`make setup-pulumi`);单测/mock 测不依赖它,仅 `//go:build integration` 的真跑依赖
- 测试:标准 `testing` + 表驱动;真实 AWS 测试用 `//go:build integration`,默认不跑
- 提交:conventional commits,英文 message
- 状态常量(全程统一):
  - Environment.status:`pending` `previewing` `preview_ready` `provisioning` `up` `failed` `destroying` `destroyed`
  - Job.status:`queued` `running` `succeeded` `failed`;Job.action:`preview` `up` `destroy`

---

### Task 1:Provisioner 接口与蓝图参数类型(`internal/provisioner`)

**Files:**
- Create: `internal/provisioner/provisioner.go`
- Test: `internal/provisioner/provisioner_test.go`

**Interfaces:**
- Consumes: 无(纯类型 + 校验)
- Produces:
  - `type Ingress struct { Port int; Protocol, CIDR, Desc string }`(JSON:`port`/`protocol`/`cidr`/`desc`)
  - `type SecurityGroup struct { Ingress []Ingress }`(JSON:`ingress`)
  - `type EC2 struct { InstanceType string; Count int; AMI string; RootVolumeGB int; KeyName string }`(JSON:`instance_type`/`count`/`ami`/`root_volume_gb`/`key_name`)
  - `type BlueprintParams struct { Region string; SecurityGroup SecurityGroup; EC2 EC2 }`(JSON:`region`/`security_group`/`ec2`);方法 `Validate() error`
  - `type AWSCreds struct { AccessKeyID, SecretAccessKey string }`
  - `type Spec struct { StackName, Region string; Params BlueprintParams; Creds AWSCreds }`
  - `type PreviewResult struct { Creates, Updates, Deletes, Sames int; Summary string }`
  - `type UpResult struct { Outputs map[string]any; Summary string }`
  - `type Provisioner interface { Preview(ctx, Spec, io.Writer) (PreviewResult, error); Up(ctx, Spec, io.Writer) (UpResult, error); Destroy(ctx, Spec, io.Writer) error }`

- [ ] **Step 1: Write the failing test**

Create `internal/provisioner/provisioner_test.go`:
```go
package provisioner

import "testing"

func validParams() BlueprintParams {
	return BlueprintParams{
		Region: "ap-southeast-1",
		SecurityGroup: SecurityGroup{Ingress: []Ingress{
			{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
		}},
		EC2: EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*BlueprintParams)
		wantErr bool
	}{
		{"valid", func(*BlueprintParams) {}, false},
		{"empty region", func(p *BlueprintParams) { p.Region = "" }, true},
		{"empty instance type", func(p *BlueprintParams) { p.EC2.InstanceType = "" }, true},
		{"count zero", func(p *BlueprintParams) { p.EC2.Count = 0 }, true},
		{"count over max", func(p *BlueprintParams) { p.EC2.Count = 11 }, true},
		{"disk too small", func(p *BlueprintParams) { p.EC2.RootVolumeGB = 4 }, true},
		{"bad port", func(p *BlueprintParams) { p.SecurityGroup.Ingress[0].Port = 0 }, true},
		{"bad protocol", func(p *BlueprintParams) { p.SecurityGroup.Ingress[0].Protocol = "icmp" }, true},
		{"bad cidr", func(p *BlueprintParams) { p.SecurityGroup.Ingress[0].CIDR = "not-a-cidr" }, true},
		{"empty ami is allowed", func(p *BlueprintParams) { p.EC2.AMI = "" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validParams()
			tt.mutate(&p)
			err := p.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provisioner/`
Expected: FAIL(`undefined: BlueprintParams`)

- [ ] **Step 3: Write minimal implementation**

Create `internal/provisioner/provisioner.go`:
```go
// Package provisioner defines the engine abstraction that turns a blueprint
// into real cloud resources, plus the structured blueprint parameter types.
package provisioner

import (
	"context"
	"fmt"
	"io"
	"net"
)

type Ingress struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	CIDR     string `json:"cidr"`
	Desc     string `json:"desc"`
}

type SecurityGroup struct {
	Ingress []Ingress `json:"ingress"`
}

type EC2 struct {
	InstanceType string `json:"instance_type"`
	Count        int    `json:"count"`
	AMI          string `json:"ami"` // empty = auto-resolve latest AL2023
	RootVolumeGB int    `json:"root_volume_gb"`
	KeyName      string `json:"key_name"`
}

type BlueprintParams struct {
	Region        string        `json:"region"`
	SecurityGroup SecurityGroup `json:"security_group"`
	EC2           EC2           `json:"ec2"`
}

// Validate enforces the M2 minimal-blueprint rules.
func (p BlueprintParams) Validate() error {
	if p.Region == "" {
		return fmt.Errorf("region is required")
	}
	if p.EC2.InstanceType == "" {
		return fmt.Errorf("ec2.instance_type is required")
	}
	if p.EC2.Count < 1 || p.EC2.Count > 10 {
		return fmt.Errorf("ec2.count must be between 1 and 10, got %d", p.EC2.Count)
	}
	if p.EC2.RootVolumeGB < 8 {
		return fmt.Errorf("ec2.root_volume_gb must be >= 8, got %d", p.EC2.RootVolumeGB)
	}
	for i, in := range p.SecurityGroup.Ingress {
		if in.Port < 1 || in.Port > 65535 {
			return fmt.Errorf("ingress[%d]: port out of range: %d", i, in.Port)
		}
		if in.Protocol != "tcp" && in.Protocol != "udp" {
			return fmt.Errorf("ingress[%d]: protocol must be tcp or udp, got %q", i, in.Protocol)
		}
		if _, _, err := net.ParseCIDR(in.CIDR); err != nil {
			return fmt.Errorf("ingress[%d]: invalid cidr %q: %w", i, in.CIDR, err)
		}
	}
	return nil
}

type AWSCreds struct {
	AccessKeyID     string
	SecretAccessKey string
}

// Spec is the per-execution input to a Provisioner. Process-level config
// (backend URL, passphrase, pulumi project) lives on the implementation, not here.
type Spec struct {
	StackName string
	Region    string
	Params    BlueprintParams
	Creds     AWSCreds
}

type PreviewResult struct {
	Creates, Updates, Deletes, Sames int
	Summary                          string
}

type UpResult struct {
	Outputs map[string]any
	Summary string
}

// Provisioner is the decoupling point between the orchestrator and the engine.
// logs receives streaming output (wired to the SSE broker).
type Provisioner interface {
	Preview(ctx context.Context, spec Spec, logs io.Writer) (PreviewResult, error)
	Up(ctx context.Context, spec Spec, logs io.Writer) (UpResult, error)
	Destroy(ctx context.Context, spec Spec, logs io.Writer) error
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/provisioner/ -v`
Expected: PASS(所有子测试)

- [ ] **Step 5: Commit**

```bash
git add internal/provisioner/provisioner.go internal/provisioner/provisioner_test.go
git commit -m "feat: add provisioner interface and blueprint params with validation"
```

---

### Task 2:migration 0003 + Project store(`internal/store`)

**Files:**
- Create: `internal/store/migrations/0003_provisioning.sql`
- Create: `internal/store/project.go`
- Test: `internal/store/project_test.go`

**Interfaces:**
- Consumes: `Store`(M1)
- Produces:
  - `type Project struct { ID int64; Name, Description string; CreatedAt time.Time }`
  - `func (s *Store) CreateProject(ctx, Project) (int64, error)`
  - `func (s *Store) GetProject(ctx, id int64) (Project, error)`
  - `func (s *Store) ListProjects(ctx) ([]Project, error)`
  - `func (s *Store) DeleteProject(ctx, id int64) error`

- [ ] **Step 1: Write the migration(建全部 4 张表)**

Create `internal/store/migrations/0003_provisioning.sql`:
```sql
CREATE TABLE projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE blueprints (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id       INTEGER NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    name             TEXT NOT NULL,
    cloud_account_id INTEGER NOT NULL REFERENCES cloud_accounts(id) ON DELETE RESTRICT,
    params_json      TEXT NOT NULL,
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE environments (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    blueprint_id            INTEGER NOT NULL REFERENCES blueprints(id) ON DELETE RESTRICT,
    cloud_account_id        INTEGER NOT NULL REFERENCES cloud_accounts(id) ON DELETE RESTRICT,
    name                    TEXT NOT NULL,
    pulumi_stack            TEXT NOT NULL,
    region                  TEXT NOT NULL,
    blueprint_snapshot_json TEXT NOT NULL,
    status                  TEXT NOT NULL DEFAULT 'pending',
    outputs_json            TEXT NOT NULL DEFAULT '',
    created_at              TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE jobs (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    environment_id INTEGER NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    action         TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'queued',
    logs           TEXT NOT NULL DEFAULT '',
    summary_json   TEXT NOT NULL DEFAULT '',
    error          TEXT NOT NULL DEFAULT '',
    started_at     TIMESTAMP,
    finished_at    TIMESTAMP,
    created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

- [ ] **Step 2: Write the failing test**

Create `internal/store/project_test.go`:
```go
package store

import (
	"context"
	"testing"
)

func TestProjectCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.CreateProject(ctx, Project{Name: "web", Description: "web stack"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetProject(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "web" || got.Description != "web stack" {
		t.Fatalf("unexpected project: %+v", got)
	}

	list, err := s.ListProjects(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v len=%d", err, len(list))
	}

	if err := s.DeleteProject(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetProject(ctx, id); err == nil {
		t.Fatal("expected error getting deleted project")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestProjectCRUD`
Expected: FAIL(`undefined: Project`)

- [ ] **Step 4: Write minimal implementation**

Create `internal/store/project.go`:
```go
package store

import (
	"context"
	"time"
)

type Project struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
}

func (s *Store) CreateProject(ctx context.Context, p Project) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (name, description) VALUES (?, ?)`,
		p.Name, p.Description)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetProject(ctx context.Context, id int64) (Project, error) {
	var p Project
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, description, created_at FROM projects WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt)
	return p, err
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, description, created_at FROM projects ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeleteProject(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	return err
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestProjectCRUD -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations/0003_provisioning.sql internal/store/project.go internal/store/project_test.go
git commit -m "feat: add provisioning schema migration and project store"
```

---

### Task 3:Blueprint store(`internal/store`)

**Files:**
- Create: `internal/store/blueprint.go`
- Test: `internal/store/blueprint_test.go`

**Interfaces:**
- Consumes: `Store`(M1)、`provisioner.BlueprintParams`(Task 1)、`Project`(Task 2)、`CloudAccount`(M1)
- Produces:
  - `type Blueprint struct { ID, ProjectID, CloudAccountID int64; Name string; Params provisioner.BlueprintParams; CreatedAt time.Time }`
  - `func (s *Store) CreateBlueprint(ctx, Blueprint) (int64, error)`(内部 `json.Marshal(Params)` 存 `params_json`)
  - `func (s *Store) GetBlueprint(ctx, id int64) (Blueprint, error)`(内部 unmarshal)
  - `func (s *Store) ListBlueprints(ctx) ([]Blueprint, error)`
  - `func (s *Store) DeleteBlueprint(ctx, id int64) error`

- [ ] **Step 1: Write the failing test**

Create `internal/store/blueprint_test.go`:
```go
package store

import (
	"context"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

// seedProjectAndAccount creates a project + cloud account and returns their ids,
// so blueprint FKs resolve.
func seedProjectAndAccount(t *testing.T, s *Store) (projectID, accountID int64) {
	t.Helper()
	ctx := context.Background()
	pid, err := s.CreateProject(ctx, Project{Name: "p"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	aid, err := s.CreateCloudAccount(ctx, sampleAccount())
	if err != nil {
		t.Fatalf("CreateCloudAccount: %v", err)
	}
	return pid, aid
}

func sampleBlueprint(projectID, accountID int64) Blueprint {
	return Blueprint{
		ProjectID:      projectID,
		CloudAccountID: accountID,
		Name:           "web-bp",
		Params: provisioner.BlueprintParams{
			Region: "ap-southeast-1",
			SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
				{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
			}},
			EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 2, RootVolumeGB: 8},
		},
	}
}

func TestBlueprintCRUD_RoundTripsParams(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	pid, aid := seedProjectAndAccount(t, s)

	id, err := s.CreateBlueprint(ctx, sampleBlueprint(pid, aid))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetBlueprint(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "web-bp" || got.Params.EC2.Count != 2 || got.Params.Region != "ap-southeast-1" {
		t.Fatalf("params did not round-trip: %+v", got)
	}
	if len(got.Params.SecurityGroup.Ingress) != 1 || got.Params.SecurityGroup.Ingress[0].Port != 22 {
		t.Fatalf("ingress did not round-trip: %+v", got.Params.SecurityGroup)
	}

	list, err := s.ListBlueprints(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v len=%d", err, len(list))
	}

	if err := s.DeleteBlueprint(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
```

Note: `sampleAccount()` 已存在于 `internal/store/cloud_account_test.go`(M1),同包可直接调用。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestBlueprintCRUD`
Expected: FAIL(`undefined: Blueprint`)

- [ ] **Step 3: Write minimal implementation**

Create `internal/store/blueprint.go`:
```go
package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

type Blueprint struct {
	ID             int64
	ProjectID      int64
	CloudAccountID int64
	Name           string
	Params         provisioner.BlueprintParams
	CreatedAt      time.Time
}

func (s *Store) CreateBlueprint(ctx context.Context, b Blueprint) (int64, error) {
	raw, err := json.Marshal(b.Params)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO blueprints (project_id, name, cloud_account_id, params_json)
		 VALUES (?, ?, ?, ?)`,
		b.ProjectID, b.Name, b.CloudAccountID, string(raw))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetBlueprint(ctx context.Context, id int64) (Blueprint, error) {
	var b Blueprint
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, name, cloud_account_id, params_json, created_at
		 FROM blueprints WHERE id = ?`, id,
	).Scan(&b.ID, &b.ProjectID, &b.Name, &b.CloudAccountID, &raw, &b.CreatedAt)
	if err != nil {
		return Blueprint{}, err
	}
	if err := json.Unmarshal([]byte(raw), &b.Params); err != nil {
		return Blueprint{}, err
	}
	return b, nil
}

func (s *Store) ListBlueprints(ctx context.Context) ([]Blueprint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, name, cloud_account_id, params_json, created_at
		 FROM blueprints ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Blueprint
	for rows.Next() {
		var b Blueprint
		var raw string
		if err := rows.Scan(&b.ID, &b.ProjectID, &b.Name, &b.CloudAccountID, &raw, &b.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &b.Params); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) DeleteBlueprint(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM blueprints WHERE id = ?`, id)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestBlueprintCRUD -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/blueprint.go internal/store/blueprint_test.go
git commit -m "feat: add blueprint store with json params round-trip"
```

---

### Task 4:Environment store(`internal/store`)

**Files:**
- Create: `internal/store/environment.go`
- Test: `internal/store/environment_test.go`

**Interfaces:**
- Consumes: `Store`、`provisioner.BlueprintParams`、`Blueprint`(Task 3)
- Produces:
  - Env 状态常量:`EnvPending`、`EnvPreviewing`、`EnvPreviewReady`、`EnvProvisioning`、`EnvUp`、`EnvFailed`、`EnvDestroying`、`EnvDestroyed`
  - `type Environment struct { ID, BlueprintID, CloudAccountID int64; Name, PulumiStack, Region string; Snapshot provisioner.BlueprintParams; Status string; Outputs map[string]any; CreatedAt, UpdatedAt time.Time }`
  - `func (s *Store) CreateEnvironment(ctx, Environment) (int64, error)`(marshal Snapshot;status 默认 `pending`)
  - `func (s *Store) GetEnvironment(ctx, id int64) (Environment, error)`
  - `func (s *Store) ListEnvironments(ctx) ([]Environment, error)`
  - `func (s *Store) UpdateEnvironmentStatus(ctx, id int64, status string) error`(同时刷新 `updated_at`)
  - `func (s *Store) SetEnvironmentOutputs(ctx, id int64, outputs map[string]any) error`

- [ ] **Step 1: Write the failing test**

Create `internal/store/environment_test.go`:
```go
package store

import (
	"context"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

func seedBlueprint(t *testing.T, s *Store) (blueprintID, accountID int64) {
	t.Helper()
	pid, aid := seedProjectAndAccount(t, s)
	id, err := s.CreateBlueprint(context.Background(), sampleBlueprint(pid, aid))
	if err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}
	return id, aid
}

func TestEnvironmentLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	bpID, aid := seedBlueprint(t, s)

	id, err := s.CreateEnvironment(ctx, Environment{
		BlueprintID:    bpID,
		CloudAccountID: aid,
		Name:           "prod",
		PulumiStack:    "prod-abc123",
		Region:         "ap-southeast-1",
		Snapshot:       provisioner.BlueprintParams{Region: "ap-southeast-1", EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetEnvironment(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != EnvPending {
		t.Fatalf("status = %q, want pending", got.Status)
	}
	if got.Snapshot.EC2.InstanceType != "t3.micro" {
		t.Fatalf("snapshot did not round-trip: %+v", got.Snapshot)
	}

	if err := s.UpdateEnvironmentStatus(ctx, id, EnvUp); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if err := s.SetEnvironmentOutputs(ctx, id, map[string]any{"public_ips": []any{"1.2.3.4"}}); err != nil {
		t.Fatalf("SetOutputs: %v", err)
	}
	got, _ = s.GetEnvironment(ctx, id)
	if got.Status != EnvUp {
		t.Fatalf("status = %q, want up", got.Status)
	}
	if got.Outputs["public_ips"] == nil {
		t.Fatalf("outputs did not persist: %+v", got.Outputs)
	}

	list, err := s.ListEnvironments(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v len=%d", err, len(list))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestEnvironmentLifecycle`
Expected: FAIL(`undefined: Environment`)

- [ ] **Step 3: Write minimal implementation**

Create `internal/store/environment.go`:
```go
package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

const (
	EnvPending      = "pending"
	EnvPreviewing   = "previewing"
	EnvPreviewReady = "preview_ready"
	EnvProvisioning = "provisioning"
	EnvUp           = "up"
	EnvFailed       = "failed"
	EnvDestroying   = "destroying"
	EnvDestroyed    = "destroyed"
)

type Environment struct {
	ID             int64
	BlueprintID    int64
	CloudAccountID int64
	Name           string
	PulumiStack    string
	Region         string
	Snapshot       provisioner.BlueprintParams
	Status         string
	Outputs        map[string]any
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (s *Store) CreateEnvironment(ctx context.Context, e Environment) (int64, error) {
	snap, err := json.Marshal(e.Snapshot)
	if err != nil {
		return 0, err
	}
	if e.Status == "" {
		e.Status = EnvPending
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO environments
		 (blueprint_id, cloud_account_id, name, pulumi_stack, region, blueprint_snapshot_json, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.BlueprintID, e.CloudAccountID, e.Name, e.PulumiStack, e.Region, string(snap), e.Status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetEnvironment(ctx context.Context, id int64) (Environment, error) {
	var e Environment
	var snap, outputs string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, blueprint_id, cloud_account_id, name, pulumi_stack, region,
		        blueprint_snapshot_json, status, outputs_json, created_at, updated_at
		 FROM environments WHERE id = ?`, id,
	).Scan(&e.ID, &e.BlueprintID, &e.CloudAccountID, &e.Name, &e.PulumiStack, &e.Region,
		&snap, &e.Status, &outputs, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return Environment{}, err
	}
	if err := json.Unmarshal([]byte(snap), &e.Snapshot); err != nil {
		return Environment{}, err
	}
	if outputs != "" {
		if err := json.Unmarshal([]byte(outputs), &e.Outputs); err != nil {
			return Environment{}, err
		}
	}
	return e, nil
}

func (s *Store) ListEnvironments(ctx context.Context) ([]Environment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, blueprint_id, cloud_account_id, name, pulumi_stack, region,
		        blueprint_snapshot_json, status, outputs_json, created_at, updated_at
		 FROM environments ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Environment
	for rows.Next() {
		var e Environment
		var snap, outputs string
		if err := rows.Scan(&e.ID, &e.BlueprintID, &e.CloudAccountID, &e.Name, &e.PulumiStack,
			&e.Region, &snap, &e.Status, &outputs, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(snap), &e.Snapshot)
		if outputs != "" {
			_ = json.Unmarshal([]byte(outputs), &e.Outputs)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) UpdateEnvironmentStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE environments SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, id)
	return err
}

func (s *Store) SetEnvironmentOutputs(ctx context.Context, id int64, outputs map[string]any) error {
	raw, err := json.Marshal(outputs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE environments SET outputs_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		string(raw), id)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestEnvironmentLifecycle -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/environment.go internal/store/environment_test.go
git commit -m "feat: add environment store with snapshot, status and outputs"
```

---

### Task 5:Job store(`internal/store`)

**Files:**
- Create: `internal/store/job.go`
- Test: `internal/store/job_test.go`

**Interfaces:**
- Consumes: `Store`、`Environment`(Task 4)
- Produces:
  - Job 常量:`JobQueued`、`JobRunning`、`JobSucceeded`、`JobFailed`;`ActionPreview`、`ActionUp`、`ActionDestroy`
  - `type Job struct { ID, EnvironmentID int64; Action, Status, Logs, Error string; Summary map[string]any; StartedAt, FinishedAt sql.NullTime; CreatedAt time.Time }`
  - `func (s *Store) CreateJob(ctx, Job) (int64, error)`(status 默认 queued)
  - `func (s *Store) GetJob(ctx, id int64) (Job, error)`
  - `func (s *Store) ListJobsByEnvironment(ctx, envID int64) ([]Job, error)`
  - `func (s *Store) UpdateJobStatus(ctx, id int64, status string) error`(running→写 started_at;succeeded/failed→写 finished_at)
  - `func (s *Store) SetJobLogs(ctx, id int64, logs string) error`
  - `func (s *Store) SetJobSummary(ctx, id int64, summary map[string]any) error`
  - `func (s *Store) SetJobError(ctx, id int64, msg string) error`
  - `func (s *Store) HasActiveJob(ctx, envID int64) (bool, error)`(存在 queued/running)
  - `func (s *Store) ListOrphanJobs(ctx) ([]Job, error)`(全部 queued/running,供崩溃恢复)

- [ ] **Step 1: Write the failing test**

Create `internal/store/job_test.go`:
```go
package store

import (
	"context"
	"testing"
)

func seedEnvironment(t *testing.T, s *Store) int64 {
	t.Helper()
	bpID, aid := seedBlueprint(t, s)
	id, err := s.CreateEnvironment(context.Background(), Environment{
		BlueprintID: bpID, CloudAccountID: aid, Name: "e", PulumiStack: "e-1", Region: "ap-southeast-1",
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	return id
}

func TestJobLifecycleAndActiveGuard(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)

	id, err := s.CreateJob(ctx, Job{EnvironmentID: envID, Action: ActionPreview})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, _ := s.GetJob(ctx, id)
	if got.Status != JobQueued || got.Action != ActionPreview {
		t.Fatalf("unexpected job: %+v", got)
	}

	active, err := s.HasActiveJob(ctx, envID)
	if err != nil || !active {
		t.Fatalf("HasActiveJob = %v, %v; want true", active, err)
	}

	if err := s.UpdateJobStatus(ctx, id, JobRunning); err != nil {
		t.Fatalf("UpdateStatus running: %v", err)
	}
	_ = s.SetJobLogs(ctx, id, "line1\nline2")
	_ = s.SetJobSummary(ctx, id, map[string]any{"creates": 3})
	if err := s.UpdateJobStatus(ctx, id, JobSucceeded); err != nil {
		t.Fatalf("UpdateStatus succeeded: %v", err)
	}

	got, _ = s.GetJob(ctx, id)
	if got.Status != JobSucceeded || !got.StartedAt.Valid || !got.FinishedAt.Valid {
		t.Fatalf("timestamps/status not set: %+v", got)
	}
	if got.Logs != "line1\nline2" || got.Summary["creates"] == nil {
		t.Fatalf("logs/summary not persisted: %+v", got)
	}

	active, _ = s.HasActiveJob(ctx, envID)
	if active {
		t.Fatal("HasActiveJob should be false after job succeeded")
	}

	byEnv, err := s.ListJobsByEnvironment(ctx, envID)
	if err != nil || len(byEnv) != 1 {
		t.Fatalf("ListJobsByEnvironment: %v len=%d", err, len(byEnv))
	}
}

func TestListOrphanJobs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)

	q, _ := s.CreateJob(ctx, Job{EnvironmentID: envID, Action: ActionUp})
	r, _ := s.CreateJob(ctx, Job{EnvironmentID: envID, Action: ActionUp})
	_ = s.UpdateJobStatus(ctx, r, JobRunning)
	done, _ := s.CreateJob(ctx, Job{EnvironmentID: envID, Action: ActionUp})
	_ = s.UpdateJobStatus(ctx, done, JobSucceeded)

	orphans, err := s.ListOrphanJobs(ctx)
	if err != nil {
		t.Fatalf("ListOrphanJobs: %v", err)
	}
	if len(orphans) != 2 {
		t.Fatalf("orphans = %d, want 2 (queued %d + running %d)", len(orphans), q, r)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestJobLifecycle|TestListOrphan'`
Expected: FAIL(`undefined: Job`)

- [ ] **Step 3: Write minimal implementation**

Create `internal/store/job.go`:
```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"

	ActionPreview = "preview"
	ActionUp      = "up"
	ActionDestroy = "destroy"
)

type Job struct {
	ID            int64
	EnvironmentID int64
	Action        string
	Status        string
	Logs          string
	Error         string
	Summary       map[string]any
	StartedAt     sql.NullTime
	FinishedAt    sql.NullTime
	CreatedAt     time.Time
}

func (s *Store) CreateJob(ctx context.Context, j Job) (int64, error) {
	if j.Status == "" {
		j.Status = JobQueued
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (environment_id, action, status) VALUES (?, ?, ?)`,
		j.EnvironmentID, j.Action, j.Status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func scanJob(sc interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var summary string
	if err := sc.Scan(&j.ID, &j.EnvironmentID, &j.Action, &j.Status, &j.Logs, &summary,
		&j.Error, &j.StartedAt, &j.FinishedAt, &j.CreatedAt); err != nil {
		return Job{}, err
	}
	if summary != "" {
		if err := json.Unmarshal([]byte(summary), &j.Summary); err != nil {
			return Job{}, err
		}
	}
	return j, nil
}

const jobCols = `id, environment_id, action, status, logs, summary_json, error, started_at, finished_at, created_at`

func (s *Store) GetJob(ctx context.Context, id int64) (Job, error) {
	return scanJob(s.db.QueryRowContext(ctx,
		`SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
}

func (s *Store) ListJobsByEnvironment(ctx context.Context, envID int64) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+jobCols+` FROM jobs WHERE environment_id = ? ORDER BY id DESC`, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) UpdateJobStatus(ctx context.Context, id int64, status string) error {
	var q string
	switch status {
	case JobRunning:
		q = `UPDATE jobs SET status = ?, started_at = CURRENT_TIMESTAMP WHERE id = ?`
	case JobSucceeded, JobFailed:
		q = `UPDATE jobs SET status = ?, finished_at = CURRENT_TIMESTAMP WHERE id = ?`
	default:
		q = `UPDATE jobs SET status = ? WHERE id = ?`
	}
	_, err := s.db.ExecContext(ctx, q, status, id)
	return err
}

func (s *Store) SetJobLogs(ctx context.Context, id int64, logs string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET logs = ? WHERE id = ?`, logs, id)
	return err
}

func (s *Store) SetJobSummary(ctx context.Context, id int64, summary map[string]any) error {
	raw, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE jobs SET summary_json = ? WHERE id = ?`, string(raw), id)
	return err
}

func (s *Store) SetJobError(ctx context.Context, id int64, msg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET error = ? WHERE id = ?`, msg, id)
	return err
}

func (s *Store) HasActiveJob(ctx context.Context, envID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE environment_id = ? AND status IN ('queued','running')`,
		envID).Scan(&n)
	return n > 0, err
}

func (s *Store) ListOrphanJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+jobCols+` FROM jobs WHERE status IN ('queued','running') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestJobLifecycle|TestListOrphan' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/job.go internal/store/job_test.go
git commit -m "feat: add job store with status timestamps and orphan query"
```

---

### Task 6:SSE 日志 broker(`internal/orchestrator`)

**Files:**
- Create: `internal/orchestrator/broker.go`
- Test: `internal/orchestrator/broker_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `type Broker struct { ... }`;`func NewBroker() *Broker`
  - `func (b *Broker) Writer(jobID int64) io.Writer` —— 按行切分,追加历史 + 扇出订阅者
  - `func (b *Broker) Subscribe(jobID int64) (history []string, ch chan string, done bool, cancel func())`
  - `func (b *Broker) Close(jobID int64)` —— flush 残行 + 标记 done + 关闭订阅者 channel
  - `func (b *Broker) Snapshot(jobID int64) string` —— 全量日志文本(落库用)

- [ ] **Step 1: Write the failing test**

Create `internal/orchestrator/broker_test.go`:
```go
package orchestrator

import (
	"fmt"
	"testing"
	"time"
)

func TestBrokerWriteSubscribeClose(t *testing.T) {
	b := NewBroker()
	w := b.Writer(1)

	fmt.Fprint(w, "hello\nwor") // "hello" complete; "wor" pending

	history, ch, done, cancel := b.Subscribe(1)
	defer cancel()
	if done {
		t.Fatal("topic should not be done yet")
	}
	if len(history) != 1 || history[0] != "hello" {
		t.Fatalf("history = %v, want [hello]", history)
	}

	fmt.Fprint(w, "ld\nbye\n") // completes "world", then "bye"

	var live []string
	for i := 0; i < 2; i++ {
		select {
		case l := <-ch:
			live = append(live, l)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for live line")
		}
	}
	if live[0] != "world" || live[1] != "bye" {
		t.Fatalf("live = %v, want [world bye]", live)
	}

	b.Close(1)
	if _, ok := <-ch; ok {
		t.Fatal("subscriber channel should be closed after Close")
	}

	hist2, ch2, done2, _ := b.Subscribe(1)
	if !done2 || ch2 != nil {
		t.Fatal("resubscribe after close should report done with nil channel")
	}
	if len(hist2) != 3 {
		t.Fatalf("history after close = %v, want 3 lines", hist2)
	}
	if got := b.Snapshot(1); got != "hello\nworld\nbye" {
		t.Fatalf("Snapshot = %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/orchestrator/ -run TestBroker`
Expected: FAIL(`undefined: NewBroker`)

- [ ] **Step 3: Write minimal implementation**

Create `internal/orchestrator/broker.go`:
```go
package orchestrator

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// Broker fans streaming log lines out to SSE subscribers per job, while keeping
// the full backlog so late subscribers can replay from the start.
type Broker struct {
	mu     sync.Mutex
	topics map[int64]*topic
}

type topic struct {
	mu      sync.Mutex
	lines   []string
	pending []byte // partial line not yet terminated by '\n'
	subs    map[chan string]struct{}
	closed  bool
}

func NewBroker() *Broker { return &Broker{topics: map[int64]*topic{}} }

func (b *Broker) get(jobID int64) *topic {
	b.mu.Lock()
	defer b.mu.Unlock()
	tp := b.topics[jobID]
	if tp == nil {
		tp = &topic{subs: map[chan string]struct{}{}}
		b.topics[jobID] = tp
	}
	return tp
}

func (b *Broker) Writer(jobID int64) io.Writer { return &topicWriter{tp: b.get(jobID)} }

type topicWriter struct{ tp *topic }

func (w *topicWriter) Write(p []byte) (int, error) {
	w.tp.mu.Lock()
	defer w.tp.mu.Unlock()
	w.tp.pending = append(w.tp.pending, p...)
	for {
		i := bytes.IndexByte(w.tp.pending, '\n')
		if i < 0 {
			break
		}
		line := string(w.tp.pending[:i])
		w.tp.pending = w.tp.pending[i+1:]
		w.tp.emit(line)
	}
	return len(p), nil
}

// emit must be called with tp.mu held.
func (tp *topic) emit(line string) {
	tp.lines = append(tp.lines, line)
	for ch := range tp.subs {
		select {
		case ch <- line:
		default: // slow subscriber: drop live line; full backlog stays in tp.lines
		}
	}
}

func (b *Broker) Subscribe(jobID int64) (history []string, ch chan string, done bool, cancel func()) {
	tp := b.get(jobID)
	tp.mu.Lock()
	defer tp.mu.Unlock()
	history = append([]string(nil), tp.lines...)
	if tp.closed {
		return history, nil, true, func() {}
	}
	ch = make(chan string, 256)
	tp.subs[ch] = struct{}{}
	cancel = func() {
		tp.mu.Lock()
		defer tp.mu.Unlock()
		if _, ok := tp.subs[ch]; ok {
			delete(tp.subs, ch)
			close(ch)
		}
	}
	return history, ch, false, cancel
}

func (b *Broker) Close(jobID int64) {
	tp := b.get(jobID)
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.closed {
		return
	}
	if len(tp.pending) > 0 {
		tp.emit(string(tp.pending))
		tp.pending = nil
	}
	tp.closed = true
	for ch := range tp.subs {
		delete(tp.subs, ch)
		close(ch)
	}
}

func (b *Broker) Snapshot(jobID int64) string {
	tp := b.get(jobID)
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return strings.Join(tp.lines, "\n")
}
```

> **Note:** topics 留在内存不清理(自用规模、job 数少,可接受)。后续里程碑可加 job 完成后的定时清理。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/orchestrator/ -run TestBroker -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/broker.go internal/orchestrator/broker_test.go
git commit -m "feat: add per-job SSE log broker with replay"
```

---

### Task 7:编排器 —— 队列 + worker + 状态机 + 崩溃恢复(`internal/orchestrator`)

**Files:**
- Create: `internal/orchestrator/orchestrator.go`
- Test: `internal/orchestrator/orchestrator_test.go`

**Interfaces:**
- Consumes: `*store.Store`(Tasks 2-5)、`provisioner.Provisioner`/`provisioner.Spec`(Task 1)、`*Broker`(Task 6)
- Produces:
  - `var ErrEnvironmentBusy error`
  - `type Orchestrator struct { ... }`;`func New(*store.Store, provisioner.Provisioner, *Broker, workers int) *Orchestrator`
  - `func (o *Orchestrator) Start(ctx context.Context)`(先 recoverOrphans,再起 worker 池)
  - `func (o *Orchestrator) Stop()`(cancel + 等待在途 job)
  - `func (o *Orchestrator) Enqueue(ctx, envID int64, action string) (jobID int64, err error)`

- [ ] **Step 1: Write the failing test**

Create `internal/orchestrator/orchestrator_test.go`:
```go
package orchestrator

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/crypto"
	"github.com/0xFredZhang/Hermes/internal/provisioner"
	"github.com/0xFredZhang/Hermes/internal/store"
)

type fakeProvisioner struct {
	upErr   error
	outputs map[string]any
	logLine string
}

func (f *fakeProvisioner) Preview(_ context.Context, _ provisioner.Spec, logs io.Writer) (provisioner.PreviewResult, error) {
	if f.logLine != "" {
		fmt.Fprintln(logs, f.logLine)
	}
	return provisioner.PreviewResult{Creates: 3}, nil
}

func (f *fakeProvisioner) Up(_ context.Context, _ provisioner.Spec, logs io.Writer) (provisioner.UpResult, error) {
	if f.logLine != "" {
		fmt.Fprintln(logs, f.logLine)
	}
	if f.upErr != nil {
		return provisioner.UpResult{}, f.upErr
	}
	return provisioner.UpResult{Outputs: f.outputs}, nil
}

func (f *fakeProvisioner) Destroy(_ context.Context, _ provisioner.Spec, logs io.Writer) error {
	if f.logLine != "" {
		fmt.Fprintln(logs, f.logLine)
	}
	return nil
}

func newSeededStore(t *testing.T) (*store.Store, int64) {
	t.Helper()
	c, err := crypto.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	st, err := store.Open(":memory:", c)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	pid, _ := st.CreateProject(ctx, store.Project{Name: "p"})
	aid, err := st.CreateCloudAccount(ctx, store.CloudAccount{
		Name: "a", DefaultRegion: "ap-southeast-1", AccessKeyID: "AKIA",
		SecretAccessKey: "sec", AWSAccountID: "111111111111", ARN: "arn:aws:iam::111111111111:user/x",
	})
	if err != nil {
		t.Fatalf("CreateCloudAccount: %v", err)
	}
	bpID, _ := st.CreateBlueprint(ctx, store.Blueprint{
		ProjectID: pid, CloudAccountID: aid, Name: "bp",
		Params: provisioner.BlueprintParams{Region: "ap-southeast-1",
			EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8}},
	})
	envID, _ := st.CreateEnvironment(ctx, store.Environment{
		BlueprintID: bpID, CloudAccountID: aid, Name: "e", PulumiStack: "e-1", Region: "ap-southeast-1",
	})
	return st, envID
}

func TestRunPreviewSucceeds(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	o := New(st, &fakeProvisioner{logLine: "previewing"}, NewBroker(), 1)

	jobID, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionPreview})
	o.run(ctx, jobID)

	job, _ := st.GetJob(ctx, jobID)
	if job.Status != store.JobSucceeded {
		t.Fatalf("job status = %q, want succeeded", job.Status)
	}
	if !strings.Contains(job.Logs, "previewing") {
		t.Fatalf("logs not persisted: %q", job.Logs)
	}
	env, _ := st.GetEnvironment(ctx, envID)
	if env.Status != store.EnvPreviewReady {
		t.Fatalf("env status = %q, want preview_ready", env.Status)
	}
}

func TestRunUpStoresOutputs(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	o := New(st, &fakeProvisioner{outputs: map[string]any{"public_ips": []any{"1.2.3.4"}}}, NewBroker(), 1)

	jobID, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionUp})
	o.run(ctx, jobID)

	env, _ := st.GetEnvironment(ctx, envID)
	if env.Status != store.EnvUp {
		t.Fatalf("env status = %q, want up", env.Status)
	}
	if env.Outputs["public_ips"] == nil {
		t.Fatalf("outputs not stored: %+v", env.Outputs)
	}
}

func TestRunUpFailureMarksFailed(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	o := New(st, &fakeProvisioner{upErr: fmt.Errorf("boom")}, NewBroker(), 1)

	jobID, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionUp})
	o.run(ctx, jobID)

	job, _ := st.GetJob(ctx, jobID)
	if job.Status != store.JobFailed || job.Error == "" {
		t.Fatalf("job = %+v, want failed with error", job)
	}
	env, _ := st.GetEnvironment(ctx, envID)
	if env.Status != store.EnvFailed {
		t.Fatalf("env status = %q, want failed", env.Status)
	}
}

func TestEnqueueRejectsBusyEnvironment(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	o := New(st, &fakeProvisioner{}, NewBroker(), 1)

	if _, err := o.Enqueue(ctx, envID, store.ActionPreview); err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	if _, err := o.Enqueue(ctx, envID, store.ActionUp); err != ErrEnvironmentBusy {
		t.Fatalf("second Enqueue err = %v, want ErrEnvironmentBusy", err)
	}
}

func TestRecoverOrphans(t *testing.T) {
	st, envID := newSeededStore(t)
	ctx := context.Background()
	_ = st.UpdateEnvironmentStatus(ctx, envID, store.EnvProvisioning)
	orphan, _ := st.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: store.ActionUp})
	_ = st.UpdateJobStatus(ctx, orphan, store.JobRunning)

	o := New(st, &fakeProvisioner{}, NewBroker(), 1)
	o.recoverOrphans(ctx)

	job, _ := st.GetJob(ctx, orphan)
	if job.Status != store.JobFailed {
		t.Fatalf("orphan job status = %q, want failed", job.Status)
	}
	env, _ := st.GetEnvironment(ctx, envID)
	if env.Status != store.EnvFailed {
		t.Fatalf("env status = %q, want failed", env.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/orchestrator/ -run 'TestRun|TestEnqueue|TestRecover'`
Expected: FAIL(`undefined: New`)

- [ ] **Step 3: Write minimal implementation**

Create `internal/orchestrator/orchestrator.go`:
```go
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
	"github.com/0xFredZhang/Hermes/internal/store"
)

var ErrEnvironmentBusy = errors.New("environment already has an active job")

type Orchestrator struct {
	store   *store.Store
	prov    provisioner.Provisioner
	broker  *Broker
	queue   chan int64
	workers int

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(st *store.Store, prov provisioner.Provisioner, broker *Broker, workers int) *Orchestrator {
	if workers < 1 {
		workers = 1
	}
	return &Orchestrator{
		store: st, prov: prov, broker: broker,
		queue: make(chan int64, 128), workers: workers,
	}
}

// Start recovers orphaned jobs from a prior run, then launches the worker pool.
func (o *Orchestrator) Start(ctx context.Context) {
	ctx, o.cancel = context.WithCancel(ctx)
	o.recoverOrphans(ctx)
	for i := 0; i < o.workers; i++ {
		o.wg.Add(1)
		go func() {
			defer o.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case jobID := <-o.queue:
					o.run(ctx, jobID)
				}
			}
		}()
	}
}

// Stop cancels workers and waits for the in-flight jobs to return.
func (o *Orchestrator) Stop() {
	if o.cancel != nil {
		o.cancel()
	}
	o.wg.Wait()
}

// Enqueue guards one active job per environment, creates a queued Job, and
// hands it to the worker pool.
func (o *Orchestrator) Enqueue(ctx context.Context, envID int64, action string) (int64, error) {
	active, err := o.store.HasActiveJob(ctx, envID)
	if err != nil {
		return 0, err
	}
	if active {
		return 0, ErrEnvironmentBusy
	}
	jobID, err := o.store.CreateJob(ctx, store.Job{EnvironmentID: envID, Action: action})
	if err != nil {
		return 0, err
	}
	o.queue <- jobID
	return jobID, nil
}

func (o *Orchestrator) run(ctx context.Context, jobID int64) {
	logs := o.broker.Writer(jobID)
	defer o.broker.Close(jobID)

	job, err := o.store.GetJob(ctx, jobID)
	if err != nil {
		return
	}
	env, err := o.store.GetEnvironment(ctx, job.EnvironmentID)
	if err != nil {
		o.fail(ctx, jobID, 0, logs, err)
		return
	}
	acct, err := o.store.GetCloudAccount(ctx, env.CloudAccountID) // decrypts secret
	if err != nil {
		o.fail(ctx, jobID, env.ID, logs, err)
		return
	}

	_ = o.store.UpdateJobStatus(ctx, jobID, store.JobRunning)
	_ = o.store.UpdateEnvironmentStatus(ctx, env.ID, transientStatus(job.Action))

	spec := provisioner.Spec{
		StackName: env.PulumiStack,
		Region:    env.Region,
		Params:    env.Snapshot,
		Creds:     provisioner.AWSCreds{AccessKeyID: acct.AccessKeyID, SecretAccessKey: acct.SecretAccessKey},
	}

	switch job.Action {
	case store.ActionPreview:
		res, err := o.prov.Preview(ctx, spec, logs)
		if err != nil {
			o.fail(ctx, jobID, env.ID, logs, err)
			return
		}
		_ = o.store.SetJobSummary(ctx, jobID, map[string]any{
			"creates": res.Creates, "updates": res.Updates,
			"deletes": res.Deletes, "sames": res.Sames,
		})
		_ = o.store.UpdateEnvironmentStatus(ctx, env.ID, store.EnvPreviewReady)
	case store.ActionUp:
		res, err := o.prov.Up(ctx, spec, logs)
		if err != nil {
			o.fail(ctx, jobID, env.ID, logs, err)
			return
		}
		_ = o.store.SetEnvironmentOutputs(ctx, env.ID, res.Outputs)
		_ = o.store.UpdateEnvironmentStatus(ctx, env.ID, store.EnvUp)
	case store.ActionDestroy:
		if err := o.prov.Destroy(ctx, spec, logs); err != nil {
			o.fail(ctx, jobID, env.ID, logs, err)
			return
		}
		_ = o.store.UpdateEnvironmentStatus(ctx, env.ID, store.EnvDestroyed)
	}

	o.persistLogs(ctx, jobID)
	_ = o.store.UpdateJobStatus(ctx, jobID, store.JobSucceeded)
}

func transientStatus(action string) string {
	switch action {
	case store.ActionPreview:
		return store.EnvPreviewing
	case store.ActionDestroy:
		return store.EnvDestroying
	default:
		return store.EnvProvisioning
	}
}

func (o *Orchestrator) fail(ctx context.Context, jobID, envID int64, logs io.Writer, cause error) {
	fmt.Fprintf(logs, "ERROR: %v\n", cause)
	o.persistLogs(ctx, jobID)
	_ = o.store.SetJobError(ctx, jobID, cause.Error())
	_ = o.store.UpdateJobStatus(ctx, jobID, store.JobFailed)
	if envID != 0 {
		_ = o.store.UpdateEnvironmentStatus(ctx, envID, store.EnvFailed)
	}
}

func (o *Orchestrator) persistLogs(ctx context.Context, jobID int64) {
	_ = o.store.SetJobLogs(ctx, jobID, o.broker.Snapshot(jobID))
}

func (o *Orchestrator) recoverOrphans(ctx context.Context) {
	orphans, err := o.store.ListOrphanJobs(ctx)
	if err != nil {
		return
	}
	for _, j := range orphans {
		_ = o.store.SetJobError(ctx, j.ID, "interrupted by restart")
		_ = o.store.UpdateJobStatus(ctx, j.ID, store.JobFailed)
		env, err := o.store.GetEnvironment(ctx, j.EnvironmentID)
		if err != nil {
			continue
		}
		switch env.Status {
		case store.EnvPreviewing, store.EnvProvisioning, store.EnvDestroying:
			_ = o.store.UpdateEnvironmentStatus(ctx, env.ID, store.EnvFailed)
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/orchestrator/ -v`
Expected: PASS(broker + orchestrator 全部)

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/orchestrator.go internal/orchestrator/orchestrator_test.go
git commit -m "feat: add orchestrator with worker pool, state machine and crash recovery"
```

---

### Task 8:Pulumi inline program(SG + EC2)+ WithMocks 测试(`internal/provisioner/pulumiengine`)

> **命名注意:** 子包目录与包名用 `pulumiengine`(不用 `pulumi`),避免与 Pulumi SDK 的 `pulumi` 包名冲突。

**Files:**
- Modify: `go.mod`/`go.sum`(加 Pulumi 依赖)
- Create: `internal/provisioner/pulumiengine/program.go`
- Test: `internal/provisioner/pulumiengine/program_test.go`

**Interfaces:**
- Consumes: `provisioner.BlueprintParams`(Task 1)、Pulumi SDK、Pulumi AWS SDK
- Produces:
  - `func buildProgram(p provisioner.BlueprintParams) pulumi.RunFunc` —— 按快照声明 1 个 SG + N 台 EC2,导出 `instance_ids`/`public_ips`/`public_dns`

- [ ] **Step 1: Add Pulumi dependencies**

Run:
```bash
go get github.com/pulumi/pulumi/sdk/v3
go get github.com/pulumi/pulumi-aws/sdk/v6
go mod tidy
```
Expected: `go.mod` 新增 `github.com/pulumi/pulumi/sdk/v3` 与 `github.com/pulumi/pulumi-aws/sdk/v6`

- [ ] **Step 2: Write the inline program**

Create `internal/provisioner/pulumiengine/program.go`:
```go
// Package pulumiengine builds Hermes blueprints into real AWS resources via the
// Pulumi Automation API. Named pulumiengine (not pulumi) to avoid clashing with
// the Pulumi SDK's own "pulumi" package.
package pulumiengine

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ssm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

// al2023SSMParam resolves the latest Amazon Linux 2023 AMI per region.
const al2023SSMParam = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"

// buildProgram returns a Pulumi inline program declaring the blueprint's
// security group and EC2 instances in the account's default VPC.
func buildProgram(p provisioner.BlueprintParams) pulumi.RunFunc {
	return func(ctx *pulumi.Context) error {
		amiID := p.EC2.AMI
		if amiID == "" {
			param, err := ssm.LookupParameter(ctx, &ssm.LookupParameterArgs{Name: al2023SSMParam})
			if err != nil {
				return err
			}
			amiID = param.Value
		}

		vpc, err := ec2.LookupVpc(ctx, &ec2.LookupVpcArgs{Default: pulumi.BoolRef(true)})
		if err != nil {
			return err
		}
		subnets, err := ec2.GetSubnets(ctx, &ec2.GetSubnetsArgs{
			Filters: []ec2.GetSubnetsFilter{{Name: "vpc-id", Values: []string{vpc.Id}}},
		})
		if err != nil {
			return err
		}
		if len(subnets.Ids) == 0 {
			return fmt.Errorf("no subnets found in default vpc %s", vpc.Id)
		}
		subnetID := subnets.Ids[0]

		ingress := ec2.SecurityGroupIngressArray{}
		for _, in := range p.SecurityGroup.Ingress {
			ingress = append(ingress, ec2.SecurityGroupIngressArgs{
				Protocol:    pulumi.String(in.Protocol),
				FromPort:    pulumi.Int(in.Port),
				ToPort:      pulumi.Int(in.Port),
				CidrBlocks:  pulumi.StringArray{pulumi.String(in.CIDR)},
				Description: pulumi.String(in.Desc),
			})
		}
		sg, err := ec2.NewSecurityGroup(ctx, "hermes-sg", &ec2.SecurityGroupArgs{
			VpcId:   pulumi.String(vpc.Id),
			Ingress: ingress,
			Egress: ec2.SecurityGroupEgressArray{
				ec2.SecurityGroupEgressArgs{
					Protocol:   pulumi.String("-1"),
					FromPort:   pulumi.Int(0),
					ToPort:     pulumi.Int(0),
					CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
				},
			},
		})
		if err != nil {
			return err
		}

		var ids, ips, dns pulumi.StringArray
		for i := 0; i < p.EC2.Count; i++ {
			args := &ec2.InstanceArgs{
				Ami:                 pulumi.String(amiID),
				InstanceType:        pulumi.String(p.EC2.InstanceType),
				SubnetId:            pulumi.String(subnetID),
				VpcSecurityGroupIds: pulumi.StringArray{sg.ID()},
				RootBlockDevice: &ec2.InstanceRootBlockDeviceArgs{
					VolumeSize: pulumi.Int(p.EC2.RootVolumeGB),
				},
			}
			if p.EC2.KeyName != "" {
				args.KeyName = pulumi.String(p.EC2.KeyName)
			}
			inst, err := ec2.NewInstance(ctx, fmt.Sprintf("hermes-ec2-%d", i), args)
			if err != nil {
				return err
			}
			ids = append(ids, inst.ID().ToStringOutput())
			ips = append(ips, inst.PublicIp)
			dns = append(dns, inst.PublicDns)
		}

		ctx.Export("instance_ids", ids)
		ctx.Export("public_ips", ips)
		ctx.Export("public_dns", dns)
		return nil
	}
}
```

> **实现注意:** pulumi-aws v6 的具体字段/函数名(`ec2.GetSubnets`、`LookupVpc`、`InstanceRootBlockDeviceArgs.VolumeSize` 等)以实际 SDK 为准;若编译报字段名不符,按编译器提示微调(语义不变)。

- [ ] **Step 3: Write the failing test**

Create `internal/provisioner/pulumiengine/program_test.go`:
```go
package pulumiengine

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

type recordMocks struct {
	mu    sync.Mutex
	types []string
}

func (m *recordMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	m.types = append(m.types, args.TypeToken)
	m.mu.Unlock()
	outputs := args.Inputs.Mappable()
	if args.TypeToken == "aws:ec2/instance:Instance" {
		outputs["publicIp"] = "1.2.3.4"
		outputs["publicDns"] = "ec2-1-2-3-4.compute.amazonaws.com"
	}
	return args.Name + "_id", resource.NewPropertyMapFromMap(outputs), nil
}

func (m *recordMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	switch args.Token {
	case "aws:ec2/getVpc:getVpc":
		return resource.NewPropertyMapFromMap(map[string]any{"id": "vpc-123"}), nil
	case "aws:ec2/getSubnets:getSubnets":
		return resource.NewPropertyMapFromMap(map[string]any{"ids": []any{"subnet-123"}}), nil
	case "aws:ssm/getParameter:getParameter":
		return resource.NewPropertyMapFromMap(map[string]any{"value": "ami-0abc"}), nil
	}
	return args.Args, nil
}

func (m *recordMocks) MethodCall(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args, nil
}

func TestBuildProgramDeclaresResources(t *testing.T) {
	params := provisioner.BlueprintParams{
		Region: "ap-southeast-1",
		SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
			{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
		}},
		EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 2, RootVolumeGB: 8},
	}
	m := &recordMocks{}
	err := pulumi.RunErr(buildProgram(params), pulumi.WithMocks("hermes", "test", m))
	if err != nil {
		t.Fatalf("RunErr: %v", err)
	}
	count := func(tok string) int {
		n := 0
		for _, x := range m.types {
			if x == tok {
				n++
			}
		}
		return n
	}
	if got := count("aws:ec2/securityGroup:SecurityGroup"); got != 1 {
		t.Fatalf("security groups = %d, want 1", got)
	}
	if got := count("aws:ec2/instance:Instance"); got != 2 {
		t.Fatalf("instances = %d, want 2 (matches EC2.Count)", got)
	}
}
```

- [ ] **Step 4: Run test to verify it fails then passes**

Run: `go test ./internal/provisioner/pulumiengine/ -run TestBuildProgram`
Expected: 先因 `undefined: buildProgram` FAIL;补全后 PASS(可能首跑较慢,Pulumi SDK 编译体积大)

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/provisioner/pulumiengine/program.go internal/provisioner/pulumiengine/program_test.go
git commit -m "feat: add pulumi inline program for security group and ec2"
```

---

### Task 9:PulumiProvisioner(Automation API 封装)(`internal/provisioner/pulumiengine`)

**Files:**
- Create: `internal/provisioner/pulumiengine/pulumiengine.go`
- Test: `internal/provisioner/pulumiengine/pulumiengine_test.go`

**Interfaces:**
- Consumes: `provisioner.Spec`/`provisioner.Provisioner`(Task 1)、`buildProgram`(Task 8)、Automation API
- Produces:
  - `type Provisioner struct { ... }`(实现 `provisioner.Provisioner`)
  - `func New(project, backendURL, passphrase string) *Provisioner`
  - `func (p *Provisioner) envVars(spec provisioner.Spec) map[string]string`(纯函数,可测)
  - `Preview`/`Up`/`Destroy`(经 Automation API,流式 `ProgressStreams`)

- [ ] **Step 1: Write the failing test**

Create `internal/provisioner/pulumiengine/pulumiengine_test.go`:
```go
package pulumiengine

import (
	"testing"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

// Compile-time assertion that Provisioner satisfies the interface.
var _ provisioner.Provisioner = (*Provisioner)(nil)

func TestEnvVarsScopesCredentials(t *testing.T) {
	p := New("hermes", "file:///tmp/state", "pass123")
	ev := p.envVars(provisioner.Spec{
		Region: "ap-southeast-1",
		Creds:  provisioner.AWSCreds{AccessKeyID: "AKIA", SecretAccessKey: "SECRET"},
	})
	if ev["AWS_ACCESS_KEY_ID"] != "AKIA" || ev["AWS_SECRET_ACCESS_KEY"] != "SECRET" {
		t.Fatalf("aws creds not mapped: %v", ev)
	}
	if ev["AWS_REGION"] != "ap-southeast-1" {
		t.Fatalf("region not mapped: %v", ev)
	}
	if ev["PULUMI_BACKEND_URL"] != "file:///tmp/state" || ev["PULUMI_CONFIG_PASSPHRASE"] != "pass123" {
		t.Fatalf("backend/passphrase not mapped: %v", ev)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provisioner/pulumiengine/ -run TestEnvVars`
Expected: FAIL(`undefined: New`)

- [ ] **Step 3: Write minimal implementation**

Create `internal/provisioner/pulumiengine/pulumiengine.go`:
```go
package pulumiengine

import (
	"context"
	"fmt"
	"io"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

// Provisioner drives the Pulumi Automation API against a shared backend.
// Per-job AWS credentials are injected via the workspace environment only.
type Provisioner struct {
	project    string
	backendURL string
	passphrase string
}

func New(project, backendURL, passphrase string) *Provisioner {
	return &Provisioner{project: project, backendURL: backendURL, passphrase: passphrase}
}

// envVars builds the workspace environment for one execution. Credentials are
// scoped to this workspace — never the global process environment.
func (p *Provisioner) envVars(spec provisioner.Spec) map[string]string {
	return map[string]string{
		"AWS_ACCESS_KEY_ID":        spec.Creds.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY":    spec.Creds.SecretAccessKey,
		"AWS_REGION":               spec.Region,
		"PULUMI_CONFIG_PASSPHRASE": p.passphrase,
		"PULUMI_BACKEND_URL":       p.backendURL,
	}
}

func (p *Provisioner) stack(ctx context.Context, spec provisioner.Spec) (auto.Stack, error) {
	return auto.UpsertStackInlineSource(ctx, spec.StackName, p.project,
		buildProgram(spec.Params), auto.EnvVars(p.envVars(spec)))
}

func (p *Provisioner) Preview(ctx context.Context, spec provisioner.Spec, logs io.Writer) (provisioner.PreviewResult, error) {
	st, err := p.stack(ctx, spec)
	if err != nil {
		return provisioner.PreviewResult{}, err
	}
	res, err := st.Preview(ctx, optpreview.ProgressStreams(logs))
	if err != nil {
		return provisioner.PreviewResult{}, err
	}
	cs := res.ChangeSummary
	return provisioner.PreviewResult{
		Creates: cs[apitype.OpCreate],
		Updates: cs[apitype.OpUpdate],
		Deletes: cs[apitype.OpDelete],
		Sames:   cs[apitype.OpSame],
		Summary: fmt.Sprintf("%d to create, %d to update, %d to delete",
			cs[apitype.OpCreate], cs[apitype.OpUpdate], cs[apitype.OpDelete]),
	}, nil
}

func (p *Provisioner) Up(ctx context.Context, spec provisioner.Spec, logs io.Writer) (provisioner.UpResult, error) {
	st, err := p.stack(ctx, spec)
	if err != nil {
		return provisioner.UpResult{}, err
	}
	res, err := st.Up(ctx, optup.ProgressStreams(logs))
	if err != nil {
		return provisioner.UpResult{}, err
	}
	outputs := make(map[string]any, len(res.Outputs))
	for k, v := range res.Outputs {
		outputs[k] = v.Value
	}
	return provisioner.UpResult{Outputs: outputs, Summary: res.Summary.Message}, nil
}

func (p *Provisioner) Destroy(ctx context.Context, spec provisioner.Spec, logs io.Writer) error {
	st, err := p.stack(ctx, spec)
	if err != nil {
		return err
	}
	_, err = st.Destroy(ctx, optdestroy.ProgressStreams(logs))
	return err
}
```

> **实现注意:** `res.Summary.Message`(auto.UpResult 的 UpdateSummary 字段)以实际 SDK 为准;若字段名不符,取任一简短摘要或置空,不影响 M2 功能。真实行为由 Task 15 的集成测试覆盖。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/provisioner/pulumiengine/ -run TestEnvVars -v`
Expected: PASS(且 `var _ provisioner.Provisioner = (*Provisioner)(nil)` 编译通过,证明接口已实现)

- [ ] **Step 5: Commit**

```bash
git add internal/provisioner/pulumiengine/pulumiengine.go internal/provisioner/pulumiengine/pulumiengine_test.go
git commit -m "feat: add pulumi automation api provisioner with per-job cred injection"
```

---

### Task 10:配置新增(`internal/config`)

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`(追加测试函数)

**Interfaces:**
- Produces:`Config` 新增 `PulumiBackend string`、`PulumiProject string`、`Workers int`

- [ ] **Step 1: Write the failing test(追加到 `config_test.go`)**

Append to `internal/config/config_test.go`:
```go
func TestLoadProvisioningDefaults(t *testing.T) {
	t.Setenv("HERMES_MASTER_KEY", validKeyB64())
	t.Setenv("HERMES_LOGIN_PASSWORD", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PulumiProject != "hermes" {
		t.Fatalf("PulumiProject = %q, want hermes", cfg.PulumiProject)
	}
	if cfg.Workers != 2 {
		t.Fatalf("Workers = %d, want 2", cfg.Workers)
	}
	if len(cfg.PulumiBackend) < 7 || cfg.PulumiBackend[:7] != "file://" {
		t.Fatalf("PulumiBackend = %q, want file:// default", cfg.PulumiBackend)
	}
}

func TestLoadWorkersOverride(t *testing.T) {
	t.Setenv("HERMES_MASTER_KEY", validKeyB64())
	t.Setenv("HERMES_LOGIN_PASSWORD", "secret")
	t.Setenv("HERMES_WORKERS", "4")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Workers != 4 {
		t.Fatalf("Workers = %d, want 4", cfg.Workers)
	}
}

func TestLoadRejectsBadWorkers(t *testing.T) {
	t.Setenv("HERMES_MASTER_KEY", validKeyB64())
	t.Setenv("HERMES_LOGIN_PASSWORD", "secret")
	t.Setenv("HERMES_WORKERS", "zero")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for non-numeric HERMES_WORKERS")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadProvisioning`
Expected: FAIL(`cfg.PulumiProject undefined`)

- [ ] **Step 3: Modify `config.go`**

Replace `internal/config/config.go` with:
```go
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Addr          string
	DBPath        string
	MasterKey     []byte
	LoginPassword string
	PulumiBackend string
	PulumiProject string
	Workers       int
}

func Load() (Config, error) {
	cfg := Config{
		Addr:          envOr("HERMES_ADDR", ":8080"),
		DBPath:        envOr("HERMES_DB_PATH", "hermes.db"),
		LoginPassword: os.Getenv("HERMES_LOGIN_PASSWORD"),
		PulumiProject: envOr("HERMES_PULUMI_PROJECT", "hermes"),
	}

	rawKey := os.Getenv("HERMES_MASTER_KEY")
	if rawKey == "" {
		return Config{}, errors.New("HERMES_MASTER_KEY is required")
	}
	key, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil {
		return Config{}, fmt.Errorf("HERMES_MASTER_KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return Config{}, fmt.Errorf("HERMES_MASTER_KEY must decode to 32 bytes, got %d", len(key))
	}
	cfg.MasterKey = key

	if cfg.LoginPassword == "" {
		return Config{}, errors.New("HERMES_LOGIN_PASSWORD is required")
	}

	cfg.PulumiBackend = os.Getenv("HERMES_PULUMI_BACKEND")
	if cfg.PulumiBackend == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Config{}, err
		}
		cfg.PulumiBackend = "file://" + filepath.Join(cwd, "data", "pulumi-state")
	}

	cfg.Workers = 2
	if w := os.Getenv("HERMES_WORKERS"); w != "" {
		n, err := strconv.Atoi(w)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("HERMES_WORKERS must be a positive integer, got %q", w)
		}
		cfg.Workers = n
	}

	return cfg, nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS(含 M1 原有 `TestLoad`)

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add pulumi backend, project and worker count config"
```

---

### Task 11:Web 渲染器与模板(nav + provisioning 页面)(`internal/web`)

**Files:**
- Modify: `internal/web/templates/layout.html`(加导航栏)
- Modify: `internal/web/templates/accounts.html`(移除自带退出按钮,已在 nav)
- Create: `internal/web/templates/projects.html`
- Create: `internal/web/templates/blueprints.html`
- Create: `internal/web/templates/environments.html`
- Create: `internal/web/templates/environment_detail.html`
- Create: `internal/web/templates/_fragments.html`
- Modify: `internal/web/web.go`(NewRenderer 解析新页面 + `RenderPartial`)
- Test: `internal/web/web_test.go`

**Interfaces:**
- Produces:
  - `Renderer.Render(w, name, data)`(不变,新增页面 login/accounts/projects/blueprints/environments/environment_detail)
  - `func (r *Renderer) RenderPartial(w io.Writer, name string, data any) error`(渲染命名 fragment)
  - `RenderRows`(保留,委托到 partials 的 `rows`)
  - fragment 名:`rows`(账号,原有)、`project_rows`、`blueprint_rows`、`env_status`

- [ ] **Step 1: Modify `layout.html`(加 nav 与样式)**

Replace `internal/web/templates/layout.html`:
```html
{{define "layout"}}<!doctype html>
<html lang="zh">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Hermes</title>
  <script src="/static/htmx.min.js"></script>
  <style>
    body{font-family:system-ui,sans-serif;max-width:960px;margin:2rem auto;padding:0 1rem}
    nav a{margin-right:1rem}
    table{border-collapse:collapse;width:100%}th,td{border:1px solid #ddd;padding:.5rem;text-align:left}
    input,select,button{padding:.4rem;margin:.2rem 0}
    .err{color:#b00}.ok{color:#070}.muted{color:#777}
    pre{background:#111;color:#eee;padding:.75rem;border-radius:6px;max-height:360px;overflow:auto}
    fieldset label{display:block;margin:.3rem 0}
  </style>
</head>
<body>
  <h1>Hermes</h1>
  <nav>
    <a href="/accounts">账号</a>
    <a href="/projects">项目</a>
    <a href="/blueprints">蓝图</a>
    <a href="/environments">环境</a>
    <form method="post" action="/logout" style="display:inline;float:right"><button>退出</button></form>
  </nav>
  <hr>
  {{template "content" .}}
</body>
</html>{{end}}
```

- [ ] **Step 2: Modify `accounts.html`(去掉自带退出按钮)**

In `internal/web/templates/accounts.html`, delete the line:
```html
<form method="post" action="/logout" style="float:right"><button>退出</button></form>
```
(其余不变;退出已在 nav。)

- [ ] **Step 3: Create the new page + fragment templates**

Create `internal/web/templates/projects.html`:
```html
{{define "content"}}
<h2>项目</h2>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<form hx-post="/projects" hx-target="#project-rows" hx-swap="innerHTML">
  <input name="name" placeholder="项目名" required>
  <input name="description" placeholder="描述(可选)">
  <button type="submit">新建项目</button>
</form>
<table>
  <thead><tr><th>名称</th><th>描述</th><th></th></tr></thead>
  <tbody id="project-rows">{{template "project_rows" .Projects}}</tbody>
</table>
{{end}}
```

Create `internal/web/templates/blueprints.html`:
```html
{{define "content"}}
<h2>蓝图</h2>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<form method="post" action="/blueprints">
  <fieldset>
    <legend>新建蓝图(安全组 + EC2)</legend>
    <label>名称 <input name="name" required></label>
    <label>项目 <select name="project_id" required>{{range .Projects}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></label>
    <label>云账号 <select name="cloud_account_id" required>{{range .Accounts}}<option value="{{.ID}}">{{.Name}} ({{.AWSAccountID}})</option>{{end}}</select></label>
    <label>Region <input name="region" value="ap-southeast-1" required></label>
    <label>实例规格 <input name="instance_type" value="t3.micro" required></label>
    <label>数量 <input name="count" type="number" value="1" min="1" max="10" required></label>
    <label>AMI(留空=最新 AL2023) <input name="ami" placeholder="可选"></label>
    <label>系统盘 GB <input name="root_volume_gb" type="number" value="8" min="8" required></label>
    <label>Key Pair(可选) <input name="key_name" placeholder="可选"></label>
    <label>入站端口 <input name="ingress_port" type="number" value="22" required></label>
    <label>协议 <select name="ingress_protocol"><option value="tcp">tcp</option><option value="udp">udp</option></select></label>
    <label>来源 CIDR <input name="ingress_cidr" value="0.0.0.0/0" required></label>
  </fieldset>
  <button type="submit">保存蓝图</button>
</form>
<table>
  <thead><tr><th>名称</th><th>Region</th><th>规格×数量</th><th></th></tr></thead>
  <tbody id="blueprint-rows">{{template "blueprint_rows" .Blueprints}}</tbody>
</table>
{{end}}
```

> **M2 取舍:** 蓝图表单只支持**一条**入站规则(端口/协议/CIDR)。数据模型是列表、已支持多条;多条规则的动态增行 UI 留后续。

Create `internal/web/templates/environments.html`:
```html
{{define "content"}}
<h2>环境</h2>
<table>
  <thead><tr><th>ID</th><th>名称</th><th>Stack</th><th>Region</th><th>状态</th><th></th></tr></thead>
  <tbody>
  {{range .Environments}}
    <tr><td>{{.ID}}</td><td>{{.Name}}</td><td>{{.PulumiStack}}</td><td>{{.Region}}</td><td>{{.Status}}</td>
        <td><a href="/environments/{{.ID}}">详情</a></td></tr>
  {{else}}
    <tr><td colspan="6" class="muted">暂无环境,去蓝图页部署一个</td></tr>
  {{end}}
  </tbody>
</table>
{{end}}
```

Create `internal/web/templates/environment_detail.html`:
```html
{{define "content"}}
<p><a href="/environments">← 返回环境列表</a></p>
<h2>环境:{{.Env.Name}} <span class="muted">({{.Env.PulumiStack}})</span></h2>
{{template "env_status" .}}
<h3>实时日志</h3>
<pre id="log">{{.CurrentLogs}}</pre>
{{if .CurrentJobID}}
<script>
(function(){
  var es = new EventSource("/jobs/{{.CurrentJobID}}/logs/stream");
  var log = document.getElementById("log");
  es.onmessage = function(e){ log.textContent += e.data + "\n"; log.scrollTop = log.scrollHeight; };
  es.addEventListener("done", function(){
    es.close();
    if (window.htmx) htmx.ajax("GET", "/environments/{{.Env.ID}}/status", "#status");
  });
})();
</script>
{{end}}
{{end}}
```

Create `internal/web/templates/_fragments.html`:
```html
{{define "project_rows"}}
{{range .}}
<tr>
  <td>{{.Name}}</td><td>{{.Description}}</td>
  <td><button hx-delete="/projects/{{.ID}}" hx-target="#project-rows" hx-swap="innerHTML" hx-confirm="删除该项目?">删除</button></td>
</tr>
{{else}}
<tr><td colspan="3" class="muted">暂无项目</td></tr>
{{end}}
{{end}}

{{define "blueprint_rows"}}
{{range .}}
<tr>
  <td>{{.Name}}</td><td>{{.Params.Region}}</td><td>{{.Params.EC2.InstanceType}} × {{.Params.EC2.Count}}</td>
  <td>
    <form method="post" action="/blueprints/{{.ID}}/deploy" style="display:inline">
      <input name="env_name" placeholder="环境名" required style="width:7rem">
      <button type="submit">部署</button>
    </form>
    <button hx-delete="/blueprints/{{.ID}}" hx-target="#blueprint-rows" hx-swap="innerHTML" hx-confirm="删除该蓝图?">删除</button>
  </td>
</tr>
{{else}}
<tr><td colspan="4" class="muted">暂无蓝图</td></tr>
{{end}}
{{end}}

{{define "env_status"}}
<div id="status" hx-get="/environments/{{.Env.ID}}/status" hx-trigger="every 2s" hx-swap="outerHTML">
  <p>状态:<strong>{{.Env.Status}}</strong></p>
  {{if eq .Env.Status "preview_ready"}}
    <p>预演:{{.Plan}}</p>
    <form method="post" action="/environments/{{.Env.ID}}/up" style="display:inline"><button>确认创建</button></form>
    <form method="post" action="/environments/{{.Env.ID}}/destroy" style="display:inline"><button>销毁</button></form>
  {{else if eq .Env.Status "up"}}
    <p class="ok">已就绪</p>
    {{if .PublicIPs}}<p>公网 IP:{{.PublicIPs}}</p>{{end}}
    <form method="post" action="/environments/{{.Env.ID}}/destroy" style="display:inline"><button>销毁</button></form>
  {{else if eq .Env.Status "failed"}}
    <p class="err">失败</p>
    <form method="post" action="/environments/{{.Env.ID}}/retry" style="display:inline"><button>重试</button></form>
    <form method="post" action="/environments/{{.Env.ID}}/destroy" style="display:inline"><button>销毁</button></form>
  {{else if eq .Env.Status "destroyed"}}
    <p class="muted">已销毁</p>
  {{end}}
</div>
{{end}}
```

- [ ] **Step 4: Write the failing test**

Create `internal/web/web_test.go`:
```go
package web

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewRendererParsesAllPages(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	for _, name := range []string{"login", "accounts", "projects", "blueprints", "environments", "environment_detail"} {
		if r.pages[name] == nil {
			t.Fatalf("page %q not parsed", name)
		}
	}
}

func TestRenderPartialEnvStatusUp(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	var b bytes.Buffer
	data := map[string]any{
		"Env":       map[string]any{"ID": int64(1), "Status": "up", "Outputs": map[string]any{"public_ips": []any{"1.2.3.4"}}},
		"PublicIPs": "1.2.3.4",
	}
	if err := r.RenderPartial(&b, "env_status", data); err != nil {
		t.Fatalf("RenderPartial: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "销毁") || !strings.Contains(out, "1.2.3.4") {
		t.Fatalf("env_status(up) missing destroy/ip: %s", out)
	}
}
```

- [ ] **Step 5: Run test to verify it fails**

Run: `go test ./internal/web/ -run 'TestNewRenderer|TestRenderPartial'`
Expected: FAIL(`RenderPartial undefined` 或 pages 未含新页面)

- [ ] **Step 6: Rewrite `web.go`**

Replace `internal/web/web.go`:
```go
package web

import (
	"embed"
	"html/template"
	"io"
	"net/http"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var StaticFS embed.FS

type Renderer struct {
	pages    map[string]*template.Template
	partials *template.Template
}

// NewRenderer parses each page together with the layout and all fragment files,
// plus a standalone partial set for htmx swaps.
func NewRenderer() (*Renderer, error) {
	shared := []string{
		"templates/layout.html",
		"templates/_account_rows.html",
		"templates/_fragments.html",
	}
	pageFiles := map[string]string{
		"login":              "templates/login.html",
		"accounts":           "templates/accounts.html",
		"projects":           "templates/projects.html",
		"blueprints":         "templates/blueprints.html",
		"environments":       "templates/environments.html",
		"environment_detail": "templates/environment_detail.html",
	}
	r := &Renderer{pages: map[string]*template.Template{}}
	for name, file := range pageFiles {
		files := append([]string{file}, shared...)
		t, err := template.ParseFS(templatesFS, files...)
		if err != nil {
			return nil, err
		}
		r.pages[name] = t
	}
	partials, err := template.ParseFS(templatesFS,
		"templates/_account_rows.html", "templates/_fragments.html")
	if err != nil {
		return nil, err
	}
	r.partials = partials
	return r, nil
}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := r.pages[name].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// RenderPartial writes a single named fragment (for htmx swaps).
func (r *Renderer) RenderPartial(w io.Writer, name string, data any) error {
	return r.partials.ExecuteTemplate(w, name, data)
}

// RenderRows writes the cloud-account rows fragment (kept for the M1 accounts flow).
func (r *Renderer) RenderRows(w io.Writer, accounts any) error {
	return r.partials.ExecuteTemplate(w, "rows", accounts)
}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/web/ ./internal/api/ -v`
Expected: PASS(web 新测 + M1 的 `internal/api` accounts 测试仍绿,证明 `RenderRows`/账号流程未破)

- [ ] **Step 8: Commit**

```bash
git add internal/web/
git commit -m "feat: add nav and provisioning page templates with fragment renderer"
```

---

### Task 12:API —— 项目 handlers + Deps 扩展(`internal/api`)

**Files:**
- Modify: `internal/api/server.go`(Deps 加 `Orchestrator`/`Broker` 字段;NewRouter 调 `addProjectRoutes`)
- Create: `internal/api/projects.go`
- Test: `internal/api/projects_test.go`

**Interfaces:**
- Consumes: `store`、`web.Renderer`、`orchestrator`(类型)
- Produces:`Deps` 新增 `Orchestrator *orchestrator.Orchestrator`、`Broker *orchestrator.Broker`;`func addProjectRoutes(mux *http.ServeMux, d Deps)`

- [ ] **Step 1: Extend `Deps` and wire the projects routes in `server.go`**

In `internal/api/server.go`:

(a) add import `"github.com/0xFredZhang/Hermes/internal/orchestrator"`.

(b) replace the `Deps` struct with:
```go
type Deps struct {
	Store        *store.Store
	Validator    *cloud.Validator
	Auth         *auth.Authenticator
	Renderer     *web.Renderer
	Orchestrator *orchestrator.Orchestrator
	Broker       *orchestrator.Broker
}
```

(c) in `NewRouter`, immediately before `return d.Auth.Middleware(mux)`, add:
```go
	addProjectRoutes(mux, d)
```

- [ ] **Step 2: Write the failing test**

Create `internal/api/projects_test.go`:
```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func authedPost(t *testing.T, deps Deps, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(deps.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, req)
	return rec
}

func TestCreateProjectPersistsAndRendersRows(t *testing.T) {
	deps := testDeps(t)
	rec := authedPost(t, deps, "/projects", url.Values{"name": {"web"}, "description": {"d"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "web") {
		t.Fatalf("rows should show new project: %s", rec.Body.String())
	}
	list, _ := deps.Store.ListProjects(context.Background())
	if len(list) != 1 {
		t.Fatalf("project not persisted: %d", len(list))
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestCreateProject`
Expected: FAIL(`undefined: addProjectRoutes`)

- [ ] **Step 4: Write `projects.go`**

Create `internal/api/projects.go`:
```go
package api

import (
	"net/http"
	"strconv"

	"github.com/0xFredZhang/Hermes/internal/store"
)

func addProjectRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /projects", func(w http.ResponseWriter, r *http.Request) {
		list, err := d.Store.ListProjects(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.Renderer.Render(w, "projects", map[string]any{"Projects": list})
	})
	mux.HandleFunc("POST /projects", func(w http.ResponseWriter, r *http.Request) {
		name := r.FormValue("name")
		if name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		_, err := d.Store.CreateProject(r.Context(),
			store.Project{Name: name, Description: r.FormValue("description")})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeProjectRows(w, r, d)
	})
	mux.HandleFunc("DELETE /projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err := d.Store.DeleteProject(r.Context(), id); err != nil {
			// FK RESTRICT (project still has blueprints) → inline error row
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<tr><td colspan="3" class="err">无法删除:该项目下还有蓝图</td></tr>`))
			return
		}
		writeProjectRows(w, r, d)
	})
}

func writeProjectRows(w http.ResponseWriter, r *http.Request, d Deps) {
	list, err := d.Store.ListProjects(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := d.Renderer.RenderPartial(w, "project_rows", list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestCreateProject -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/server.go internal/api/projects.go internal/api/projects_test.go
git commit -m "feat: add project management routes and handlers"
```

---

### Task 13:API —— 蓝图 handlers + 部署(`internal/api`)

**Files:**
- Modify: `internal/api/server.go`(NewRouter 调 `addBlueprintRoutes`)
- Create: `internal/api/blueprints.go`
- Test: `internal/api/blueprints_test.go`

**Interfaces:**
- Consumes: `store`、`provisioner`、`orchestrator`(Enqueue)、`github.com/google/uuid`
- Produces:`func addBlueprintRoutes(mux *http.ServeMux, d Deps)`;测试辅助 `stubProvisioner`、`testDepsWithOrchestrator`、`seedEnv`(供 Tasks 13/14 共用)

- [ ] **Step 1: Wire the routes in `server.go`**

In `NewRouter`, before `return d.Auth.Middleware(mux)`, add:
```go
	addBlueprintRoutes(mux, d)
```

- [ ] **Step 2: Write the failing test(含共用辅助)**

Create `internal/api/blueprints_test.go`:
```go
package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
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

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
```

Note: `strconv` 需在测试文件 import(`itoa` 用到)。

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/api/ -run 'TestCreateBlueprint|TestDeploy'`
Expected: FAIL(`undefined: addBlueprintRoutes`)

- [ ] **Step 4: Write `blueprints.go`**

Create `internal/api/blueprints.go`:
```go
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
	params := provisioner.BlueprintParams{
		Region: r.FormValue("region"),
		SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
			{Port: port, Protocol: r.FormValue("ingress_protocol"), CIDR: r.FormValue("ingress_cidr"), Desc: "ingress"},
		}},
		EC2: provisioner.EC2{
			InstanceType: r.FormValue("instance_type"), Count: count,
			AMI: r.FormValue("ami"), RootVolumeGB: disk, KeyName: r.FormValue("key_name"),
		},
	}
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
```

- [ ] **Step 5: Add the uuid dependency, run tests**

Run:
```bash
go get github.com/google/uuid
go test ./internal/api/ -run 'TestCreateBlueprint|TestDeploy' -v
```
Expected: PASS(3 个测试)

- [ ] **Step 6: Commit**

```bash
git add internal/api/server.go internal/api/blueprints.go internal/api/blueprints_test.go go.mod go.sum
git commit -m "feat: add blueprint management and deploy-to-environment routes"
```

---

### Task 14:API —— 环境 handlers(状态/up/destroy/retry)+ jobs SSE(`internal/api`)

**Files:**
- Modify: `internal/api/server.go`(NewRouter 调 `addEnvironmentRoutes`、`addJobRoutes`)
- Create: `internal/api/environments.go`
- Create: `internal/api/jobs.go`
- Test: `internal/api/environments_test.go`
- Test: `internal/api/jobs_test.go`

**Interfaces:**
- Consumes: `store`、`orchestrator`(Enqueue、Broker.Subscribe)
- Produces:`func addEnvironmentRoutes(mux, d)`、`func addJobRoutes(mux, d)`、`func envViewData(store.Environment, []store.Job) map[string]any`

- [ ] **Step 1: Wire the routes in `server.go`**

In `NewRouter`, before `return d.Auth.Middleware(mux)`, add:
```go
	addEnvironmentRoutes(mux, d)
	addJobRoutes(mux, d)
```

- [ ] **Step 2: Write the failing tests**

Create `internal/api/environments_test.go`:
```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/store"
)

func seedEnv(t *testing.T, d Deps) int64 {
	t.Helper()
	pid, aid := seedProjectAccount(t, d)
	bpID, _ := d.Store.CreateBlueprint(context.Background(), store.Blueprint{
		ProjectID: pid, CloudAccountID: aid, Name: "bp", Params: validBPParams(),
	})
	envID, _ := d.Store.CreateEnvironment(context.Background(), store.Environment{
		BlueprintID: bpID, CloudAccountID: aid, Name: "e", PulumiStack: "e-1", Region: "ap-southeast-1",
	})
	return envID
}

func TestEnvironmentUpEnqueuesJob(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)

	rec := authedPost(t, d, "/environments/"+itoa(envID)+"/up", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	jobs, _ := d.Store.ListJobsByEnvironment(context.Background(), envID)
	if len(jobs) != 1 || jobs[0].Action != store.ActionUp {
		t.Fatalf("up job not enqueued: %+v", jobs)
	}
}

func TestEnvironmentStatusFragmentShowsConfirmButton(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	_ = d.Store.UpdateEnvironmentStatus(context.Background(), envID, store.EnvPreviewReady)

	req := httptest.NewRequest(http.MethodGet, "/environments/"+itoa(envID)+"/status", nil)
	req.AddCookie(d.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	NewRouter(d).ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "确认创建") {
		t.Fatalf("preview_ready status should show confirm button: %s", rec.Body.String())
	}
}
```

Create `internal/api/jobs_test.go`:
```go
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/store"
)

func TestJobLogStreamReplaysAndSignalsDone(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	envID := seedEnv(t, d)
	jobID, _ := d.Store.CreateJob(context.Background(), store.Job{EnvironmentID: envID, Action: store.ActionPreview})

	w := d.Broker.Writer(jobID)
	fmt.Fprintln(w, "hello")
	fmt.Fprintln(w, "world")
	d.Broker.Close(jobID) // closed before request → handler replays history + done immediately

	req := httptest.NewRequest(http.MethodGet, "/jobs/"+itoa(jobID)+"/logs/stream", nil)
	req.AddCookie(d.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	NewRouter(d).ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "data: hello") || !strings.Contains(body, "data: world") {
		t.Fatalf("missing replayed lines: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("missing done event: %s", body)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/api/ -run 'TestEnvironment|TestJobLog'`
Expected: FAIL(`undefined: addEnvironmentRoutes`)

- [ ] **Step 4: Write `environments.go`**

Create `internal/api/environments.go`:
```go
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
	mux.HandleFunc("POST /environments/{id}/retry", enqueueHandler(d, store.ActionUp))
	mux.HandleFunc("POST /environments/{id}/destroy", enqueueHandler(d, store.ActionDestroy))
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
```

- [ ] **Step 5: Write `jobs.go`(SSE)**

Create `internal/api/jobs.go`:
```go
package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func addJobRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /jobs/{id}/logs/stream", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		rc := http.NewResponseController(w)
		// SSE is long-lived: clear the server's WriteTimeout for this connection.
		// (ResponseRecorder in tests returns ErrNotSupported here; safe to ignore.)
		_ = rc.SetWriteDeadline(time.Time{})

		history, ch, done, cancel := d.Broker.Subscribe(id)
		defer cancel()

		for _, line := range history {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
		_ = rc.Flush()
		if done {
			fmt.Fprint(w, "event: done\ndata: end\n\n")
			_ = rc.Flush()
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case line, ok := <-ch:
				if !ok {
					fmt.Fprint(w, "event: done\ndata: end\n\n")
					_ = rc.Flush()
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
				_ = rc.Flush()
			}
		}
	})
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/api/ -v`
Expected: PASS(全部 api 测试:M1 accounts + projects + blueprints + deploy + environments + jobs SSE)

- [ ] **Step 7: Commit**

```bash
git add internal/api/server.go internal/api/environments.go internal/api/jobs.go internal/api/environments_test.go internal/api/jobs_test.go
git commit -m "feat: add environment actions and SSE job log streaming"
```

---

### Task 15:装配 main.go + 集成测试 + Makefile/env(端到端接线)

**Files:**
- Modify: `cmd/hermes/main.go`
- Modify: `.env.example`
- Modify: `Makefile`
- Create: `internal/provisioner/pulumiengine/integration_test.go`

**Interfaces:**
- Consumes: 全部前序 Task
- Produces: 可运行的完整 M2 应用;`//go:build integration` 的真跑测试

- [ ] **Step 1: Rewrite `cmd/hermes/main.go`**

Replace `cmd/hermes/main.go`:
```go
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/0xFredZhang/Hermes/internal/api"
	"github.com/0xFredZhang/Hermes/internal/auth"
	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/config"
	"github.com/0xFredZhang/Hermes/internal/crypto"
	"github.com/0xFredZhang/Hermes/internal/orchestrator"
	"github.com/0xFredZhang/Hermes/internal/provisioner/pulumiengine"
	"github.com/0xFredZhang/Hermes/internal/store"
	"github.com/0xFredZhang/Hermes/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	aesKey, err := crypto.DeriveKey(cfg.MasterKey, "hermes:aes-gcm:v1")
	if err != nil {
		log.Fatalf("derive aes key: %v", err)
	}
	sessionKey, err := crypto.DeriveKey(cfg.MasterKey, "hermes:session-hmac:v1")
	if err != nil {
		log.Fatalf("derive session key: %v", err)
	}
	ppKey, err := crypto.DeriveKey(cfg.MasterKey, "hermes:pulumi-passphrase:v1")
	if err != nil {
		log.Fatalf("derive pulumi passphrase: %v", err)
	}
	passphrase := base64.StdEncoding.EncodeToString(ppKey)

	cipher, err := crypto.NewCipher(aesKey)
	if err != nil {
		log.Fatalf("cipher: %v", err)
	}
	st, err := store.Open(cfg.DBPath, cipher)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()
	renderer, err := web.NewRenderer()
	if err != nil {
		log.Fatalf("renderer: %v", err)
	}

	// Ensure the local Pulumi state directory exists for the file:// backend.
	if dir, ok := strings.CutPrefix(cfg.PulumiBackend, "file://"); ok {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("pulumi state dir: %v", err)
		}
	}

	broker := orchestrator.NewBroker()
	prov := pulumiengine.New(cfg.PulumiProject, cfg.PulumiBackend, passphrase)
	orch := orchestrator.New(st, prov, broker, cfg.Workers)
	orch.Start(context.Background())

	deps := api.Deps{
		Store:        st,
		Validator:    cloud.NewValidator(),
		Auth:         auth.New(cfg.LoginPassword, sessionKey),
		Renderer:     renderer,
		Orchestrator: orch,
		Broker:       broker,
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.NewRouter(deps),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		// No global WriteTimeout: SSE streams are long-lived. Per-request
		// deadlines are managed where needed (login/forms are quick anyway).
		IdleTimeout: 60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("hermes listening on %s (workers=%d, backend=%s)", cfg.Addr, cfg.Workers, cfg.PulumiBackend)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Print("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	orch.Stop() // wait for in-flight provisioning jobs to return
	st.Close()
}
```

> **注意:** 去掉了 M1 的全局 `WriteTimeout`(SSE 长连接会被它掐断)。登录/表单等短请求不受影响;若担心慢请求,可在具体 handler 用 `http.ResponseController` 设置 per-request 截止时间。

- [ ] **Step 2: Update `.env.example`**

Append to `.env.example`:
```bash
# Pulumi state backend (default file://<cwd>/data/pulumi-state). Set to s3://<bucket> in M3+.
HERMES_PULUMI_BACKEND=

# Pulumi project name (default hermes)
HERMES_PULUMI_PROJECT=hermes

# Number of concurrent provisioning workers (default 2)
HERMES_WORKERS=2
```

- [ ] **Step 3: Add Makefile targets**

In `Makefile`, update the `.PHONY` line to include the new targets and append two targets:
```makefile
.PHONY: run build test vet fmt tidy gen-key env clean help setup-pulumi test-integration

setup-pulumi: ## Install the Pulumi AWS provider plugin (requires pulumi CLI on PATH)
	@command -v pulumi >/dev/null || { echo "pulumi CLI not found — install: https://www.pulumi.com/docs/install/"; exit 1; }
	pulumi plugin install resource aws

test-integration: ## Run the real-AWS integration test (needs pulumi + AWS creds in env)
	go test -tags integration ./internal/provisioner/pulumiengine/ -run TestIntegration -v
```

- [ ] **Step 4: Write the integration test(真跑,默认不跑)**

Create `internal/provisioner/pulumiengine/integration_test.go`:
```go
//go:build integration

package pulumiengine

import (
	"context"
	"os"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

// TestIntegrationUpDestroy runs a real up→destroy against AWS.
// Requires: pulumi CLI + AWS plugin installed, and AWS creds in the environment.
// Run: make test-integration   (or)
//   go test -tags integration ./internal/provisioner/pulumiengine/ -run TestIntegration -v
func TestIntegrationUpDestroy(t *testing.T) {
	ak, sk := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY")
	if ak == "" || sk == "" {
		t.Skip("set AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY to run the integration test")
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "ap-southeast-1"
	}

	p := New("hermes-it", "file://"+t.TempDir(), "integration-passphrase")
	spec := provisioner.Spec{
		StackName: "it-stack",
		Region:    region,
		Params: provisioner.BlueprintParams{
			Region: region,
			SecurityGroup: provisioner.SecurityGroup{Ingress: []provisioner.Ingress{
				{Port: 22, Protocol: "tcp", CIDR: "0.0.0.0/0", Desc: "SSH"},
			}},
			EC2: provisioner.EC2{InstanceType: "t3.micro", Count: 1, RootVolumeGB: 8},
		},
		Creds: provisioner.AWSCreds{AccessKeyID: ak, SecretAccessKey: sk},
	}
	ctx := context.Background()

	// Always attempt to clean up, even if assertions fail.
	t.Cleanup(func() {
		if err := p.Destroy(ctx, spec, os.Stderr); err != nil {
			t.Errorf("Destroy (cleanup): %v", err)
		}
	})

	res, err := p.Up(ctx, spec, os.Stderr)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if res.Outputs["public_ips"] == nil {
		t.Fatalf("expected public_ips output, got %+v", res.Outputs)
	}
}
```

- [ ] **Step 5: Full build + vet + unit tests**

Run:
```bash
go mod tidy
go build ./...
go vet ./...
go test ./...
```
Expected: 全部通过(集成测试因 build tag 默认跳过)

- [ ] **Step 6: Manual smoke test(不含真实 AWS)**

Run:
```bash
make env            # 若还没有 .env
make gen-key        # 如需手动填 key
go run ./cmd/hermes &
sleep 1
curl -s localhost:8080/healthz                         # -> ok
curl -s -i localhost:8080/environments | head -1       # -> 303 跳 /login
kill %1
```
Expected:`/healthz` 返回 `ok`;未登录访问 `/environments` 303 跳 `/login`。
浏览器手动核对:登录 → 账号 → 项目 → 蓝图(建 SG+EC2)→ 部署 → 环境详情出现、状态轮询、日志窗建立 SSE。

- [ ] **Step 7: Commit**

```bash
git add cmd/hermes/main.go .env.example Makefile internal/provisioner/pulumiengine/integration_test.go go.mod go.sum
git commit -m "feat: wire orchestrator into main and add pulumi integration test"
```

- [ ] **Step 8: 真跑一次(M2 验收,需 pulumi + 测试 AWS 账号)**

Run:
```bash
# 一次性:装 pulumi CLI(如 brew install pulumi),然后:
make setup-pulumi
# 用测试账号真跑 up→destroy:
AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... AWS_REGION=ap-southeast-1 make test-integration
```
Expected: 创建 EC2/SG → 断言 outputs 含 `public_ips` → destroy 清干净,测试 PASS。
(亦可通过浏览器完整走一遍 UI 流程验收:蓝图 → 部署 → preview → 确认 → 实时日志 → up 显示公网 IP → 销毁。)

---

## 完成标准(M2 Done)

- `go build ./...` 通过,`go vet ./...` 干净,`go test ./...`(mock/单测)全绿。
- 端到端(UI):登录 → 连账号 → 建项目 → 建 SG+EC2 蓝图 → 部署 → 看到 preview 计划 → 确认 → 实时日志 → 环境到 `up` 且展示公网 IP → 销毁 → `destroyed`。
- **真跑一次**:装好 `pulumi` CLI,`make test-integration` 对测试 AWS 账号跑通 up→日志→destroy,资源清干净。
- per-job 凭证只经 workspace `EnvVars` 注入(`pulumiengine` 的 `envVars` 有断言);CloudAccount secret 落库仍密文;Pulumi state secrets 经 passphrase 加密。
- **下一里程碑(M3)**:扩全资源(RDS/Redis/EIP、自建 VPC)、S3 state 后端、outputs 富展示;详见 spec §1 里程碑路线。

## 备注:M1 遗留清理(可选,不阻塞 M2)

spec/progress 记录的 M1 minor backlog(如 auth cookie 未设 `Secure/MaxAge`、`server.go` logout 硬编码 cookie 名等)不属于 M2 范围;如在改到相邻代码时顺手,可一并清理,否则留独立 cleanup pass。

