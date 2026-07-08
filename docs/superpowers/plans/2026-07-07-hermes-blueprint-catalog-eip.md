# Blueprint AWS Catalog + Elastic IP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 蓝图创建表单的 Region / 实例规格 / AMI 改为从 AWS 实时拉取的联动下拉(默认 Ubuntu 26.04 LTS,架构自适应),并让每台 EC2 实例分配弹性 IP(重启后公网 IP 不变)。

**Architecture:** 新增 `cloud.Catalog`(与 `cloud.Validator` 并排,可注入 EC2 client 便于测试)封装只读 AWS 元数据调用;三个只读 HTTP 接口用账号解密后的凭证驱动 Catalog,返回 `<option>` 片段;蓝图表单用 htmx 三级联动填充 `<datalist>`(Region/实例规格)与 `<select>`(AMI);Pulumi 程序把默认 AMI 换成架构自适应的 Ubuntu 26.04 并为每台实例分配 EIP。

**Tech Stack:** Go(stdlib `net/http` + `html/template` + htmx)、`aws-sdk-go-v2/service/ec2`、Pulumi Automation API + pulumi-aws v6。

## Global Constraints

- 模块 `github.com/0xFredZhang/Hermes`;沿用 stdlib `net/http` + `html/template` + htmx,不引入前端框架/库。
- 代码标识符、注释、错误信息、日志一律英文;面向用户的模板文案与内联提示用中文(与现有代码一致)。Git commit 用英文 conventional commits。
- 安全:账号 secret 加密静置,只经 `Store.GetCloudAccount`(解密)取出,**仅在进程内**构造只读 SDK client,**绝不写日志、不注入全局进程 env**。
- 元数据 HTTP 接口的账号参数名是 **`cloud_account_id`**(与表单字段同名,htmx 按此名提交);region 参数 `region`;实例规格参数 `instance_type`。
- **不改** `provisioner.BlueprintParams` / `EC2` / `Validate()`。AMI 空串=部署时自动解析、非空=按选定 id 用(语义不变)。
- 默认 AMI = **Ubuntu 26.04 LTS**,架构自适应;Canonical owner = `099720109477`;Amazon Linux 2023 owner = `amazon`。
- EIP **总是**为每台实例分配(无开关字段);`public_ips` 输出改为 EIP 的稳定 IP。
- 可搜索下拉:Region、实例规格用 `<datalist>`;AMI 用 `<select>`(清单短、显示友好名)。
- 架构 token 映射:`x86_64` → Ubuntu `amd64` / AL2023 `x86_64`;`arm64` → Ubuntu `arm64` / AL2023 `arm64`。同时支持两架构的实例规格,优先按 `x86_64` 处理。

**File Structure:**
- `internal/cloud/catalog.go`(新)— `EC2API` 接口、`Catalog`、`NewCatalog`、Regions/InstanceTypes/Architecture/Images、`Image` 类型、OS 目录常量。
- `internal/cloud/catalog_test.go`(新)— 假 `EC2API` + 单元测试。
- `internal/api/metadata.go`(新)— `CatalogAPI` 接口、三个只读 handler、`<option>` 渲染助手。
- `internal/api/metadata_test.go`(新)— `authedGet`、假 `CatalogAPI`、接口测试。
- `internal/api/server.go`(改)— `Deps` 加 `Catalog`,`NewRouter` 注册元数据路由。
- `internal/api/accounts_test.go`(改)— `testDeps` 给 `Catalog` 默认假值。
- `internal/api/blueprints_test.go`(改)— 表单控件冒烟测试。
- `internal/web/templates/blueprints.html`(改)— htmx 联动控件。
- `cmd/hermes/main.go`(改)— 构造并注入 `cloud.NewCatalog()`。
- `internal/provisioner/pulumiengine/program.go`(改)— 架构自适应 Ubuntu 默认 AMI + 每实例 EIP。
- `internal/provisioner/pulumiengine/program_test.go`(改)— arch/filter 纯函数测试 + mock 断言。
- `go.mod` / `go.sum`(改)— 新增 `aws-sdk-go-v2/service/ec2`。

> Task 1–4 是表单/接口链;Task 5–6 是 provisioner,彼此独立,可任意先后。

---

### Task 1: `cloud.Catalog` 骨架 + Regions + InstanceTypes

**Files:**
- Create: `internal/cloud/catalog.go`
- Create: `internal/cloud/catalog_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Produces:
  - `type EC2API interface { DescribeRegions/DescribeInstanceTypeOfferings/DescribeInstanceTypes/DescribeImages(...) }`(方法签名与 `*ec2.Client` 一致)
  - `type Catalog struct { NewClient func(accessKey, secret, region string) EC2API }`
  - `func NewCatalog() *Catalog`
  - `func (c *Catalog) Regions(ctx context.Context, accessKey, secret string) ([]string, error)`
  - `func (c *Catalog) InstanceTypes(ctx context.Context, accessKey, secret, region string) ([]string, error)`

- [ ] **Step 1: 添加 EC2 SDK 依赖**

```bash
go get github.com/aws/aws-sdk-go-v2/service/ec2@latest
go mod tidy
```
Expected: `go.mod` 出现 `github.com/aws/aws-sdk-go-v2/service/ec2`;无编译错误(暂无引用)。

- [ ] **Step 2: 写失败测试**

Create `internal/cloud/catalog_test.go`:
```go
package cloud

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// fakeEC2 implements EC2API; each field, when set, backs one method.
type fakeEC2 struct {
	regions   func(*ec2.DescribeRegionsInput) (*ec2.DescribeRegionsOutput, error)
	offerings func(*ec2.DescribeInstanceTypeOfferingsInput) (*ec2.DescribeInstanceTypeOfferingsOutput, error)
	itypes    func(*ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error)
	images    func(*ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error)
}

func (f *fakeEC2) DescribeRegions(_ context.Context, in *ec2.DescribeRegionsInput, _ ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
	if f.regions != nil {
		return f.regions(in)
	}
	return &ec2.DescribeRegionsOutput{}, nil
}
func (f *fakeEC2) DescribeInstanceTypeOfferings(_ context.Context, in *ec2.DescribeInstanceTypeOfferingsInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error) {
	if f.offerings != nil {
		return f.offerings(in)
	}
	return &ec2.DescribeInstanceTypeOfferingsOutput{}, nil
}
func (f *fakeEC2) DescribeInstanceTypes(_ context.Context, in *ec2.DescribeInstanceTypesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	if f.itypes != nil {
		return f.itypes(in)
	}
	return &ec2.DescribeInstanceTypesOutput{}, nil
}
func (f *fakeEC2) DescribeImages(_ context.Context, in *ec2.DescribeImagesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	if f.images != nil {
		return f.images(in)
	}
	return &ec2.DescribeImagesOutput{}, nil
}

func catalogWith(f *fakeEC2) *Catalog {
	return &Catalog{NewClient: func(_, _, _ string) EC2API { return f }}
}

func TestRegionsSorted(t *testing.T) {
	c := catalogWith(&fakeEC2{
		regions: func(*ec2.DescribeRegionsInput) (*ec2.DescribeRegionsOutput, error) {
			return &ec2.DescribeRegionsOutput{Regions: []types.Region{
				{RegionName: aws.String("us-west-2")},
				{RegionName: aws.String("ap-southeast-1")},
			}}, nil
		},
	})
	got, err := c.Regions(context.Background(), "AK", "sk")
	if err != nil {
		t.Fatalf("Regions: %v", err)
	}
	if len(got) != 2 || got[0] != "ap-southeast-1" || got[1] != "us-west-2" {
		t.Fatalf("Regions = %v, want [ap-southeast-1 us-west-2]", got)
	}
}

func TestInstanceTypesPaginatesAndSorts(t *testing.T) {
	page := 0
	c := catalogWith(&fakeEC2{
		offerings: func(*ec2.DescribeInstanceTypeOfferingsInput) (*ec2.DescribeInstanceTypeOfferingsOutput, error) {
			page++
			if page == 1 {
				return &ec2.DescribeInstanceTypeOfferingsOutput{
					InstanceTypeOfferings: []types.InstanceTypeOffering{{InstanceType: types.InstanceType("t3.micro")}},
					NextToken:             aws.String("more"),
				}, nil
			}
			return &ec2.DescribeInstanceTypeOfferingsOutput{
				InstanceTypeOfferings: []types.InstanceTypeOffering{{InstanceType: types.InstanceType("c7g.large")}},
			}, nil
		},
	})
	got, err := c.InstanceTypes(context.Background(), "AK", "sk", "ap-southeast-1")
	if err != nil {
		t.Fatalf("InstanceTypes: %v", err)
	}
	if len(got) != 2 || got[0] != "c7g.large" || got[1] != "t3.micro" {
		t.Fatalf("InstanceTypes = %v, want [c7g.large t3.micro]", got)
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/cloud/ -run 'TestRegionsSorted|TestInstanceTypesPaginatesAndSorts' -v`
Expected: 编译失败(`undefined: Catalog` / `undefined: EC2API`)。

- [ ] **Step 4: 写实现**

Create `internal/cloud/catalog.go`:
```go
package cloud

import (
	"context"
	"sort"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// discoveryRegion builds the EC2 client for DescribeRegions. Any valid region
// works since the call enumerates all enabled regions.
const discoveryRegion = "us-east-1"

// EC2API is the subset of the EC2 client the Catalog uses. *ec2.Client
// satisfies it; tests inject a fake.
type EC2API interface {
	DescribeRegions(ctx context.Context, in *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error)
	DescribeInstanceTypeOfferings(ctx context.Context, in *ec2.DescribeInstanceTypeOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error)
	DescribeInstanceTypes(ctx context.Context, in *ec2.DescribeInstanceTypesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error)
	DescribeImages(ctx context.Context, in *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
}

// Catalog fetches read-only AWS metadata (regions, instance types, images) for
// the blueprint form. Credentials are per-call and used only to build the SDK
// client — never logged, never placed in the process environment.
type Catalog struct {
	// NewClient builds an EC2 client for static credentials. Overridable in tests.
	NewClient func(accessKey, secret, region string) EC2API
}

func NewCatalog() *Catalog {
	return &Catalog{
		NewClient: func(accessKey, secret, region string) EC2API {
			return ec2.New(ec2.Options{
				Region:      region,
				Credentials: credentials.NewStaticCredentialsProvider(accessKey, secret, ""),
			})
		},
	}
}

// Regions returns the account's enabled regions, sorted.
func (c *Catalog) Regions(ctx context.Context, accessKey, secret string) ([]string, error) {
	out, err := c.NewClient(accessKey, secret, discoveryRegion).
		DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
	if err != nil {
		return nil, err
	}
	var regions []string
	for _, r := range out.Regions {
		if r.RegionName != nil {
			regions = append(regions, *r.RegionName)
		}
	}
	sort.Strings(regions)
	return regions, nil
}

// InstanceTypes returns the instance types offered in region, sorted and deduped.
func (c *Catalog) InstanceTypes(ctx context.Context, accessKey, secret, region string) ([]string, error) {
	client := c.NewClient(accessKey, secret, region)
	seen := map[string]bool{}
	var out []string
	var token *string
	for {
		page, err := client.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{
			LocationType: types.LocationTypeRegion,
			NextToken:    token,
		})
		if err != nil {
			return nil, err
		}
		for _, o := range page.InstanceTypeOfferings {
			s := string(o.InstanceType)
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
		if page.NextToken == nil || *page.NextToken == "" {
			break
		}
		token = page.NextToken
	}
	sort.Strings(out)
	return out, nil
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/cloud/ -run 'TestRegionsSorted|TestInstanceTypesPaginatesAndSorts' -v`
Expected: PASS(2 个)。

- [ ] **Step 6: 提交**

```bash
git add internal/cloud/catalog.go internal/cloud/catalog_test.go go.mod go.sum
git commit -m "feat: add cloud.Catalog with Regions and InstanceTypes lookups"
```

---

### Task 2: Catalog Architecture + Images(精选 OS 目录)

**Files:**
- Modify: `internal/cloud/catalog.go`
- Modify: `internal/cloud/catalog_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Catalog`、`EC2API`、`fakeEC2`、`catalogWith`。
- Produces:
  - `type Image struct { ID string; Name string; Default bool }`
  - `func (c *Catalog) Architecture(ctx context.Context, accessKey, secret, region, instanceType string) (string, error)` → `"x86_64"` / `"arm64"`
  - `func (c *Catalog) Images(ctx context.Context, accessKey, secret, region, arch string) ([]Image, error)`

- [ ] **Step 1: 写失败测试**

Append to `internal/cloud/catalog_test.go`(并在文件顶部 import 块加入 `"strings"`):
```go
func TestArchitectureDetectsArm(t *testing.T) {
	c := catalogWith(&fakeEC2{
		itypes: func(*ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
			return &ec2.DescribeInstanceTypesOutput{InstanceTypes: []types.InstanceTypeInfo{
				{ProcessorInfo: &types.ProcessorInfo{SupportedArchitectures: []types.ArchitectureType{types.ArchitectureTypeArm64}}},
			}}, nil
		},
	})
	arch, err := c.Architecture(context.Background(), "AK", "sk", "ap-southeast-1", "t4g.micro")
	if err != nil {
		t.Fatalf("Architecture: %v", err)
	}
	if arch != "arm64" {
		t.Fatalf("arch = %q, want arm64", arch)
	}
}

func TestArchitectureDefaultsX86(t *testing.T) {
	arch, _ := catalogWith(&fakeEC2{}).Architecture(context.Background(), "AK", "sk", "r", "t3.micro")
	if arch != "x86_64" {
		t.Fatalf("arch = %q, want x86_64 (empty result → default)", arch)
	}
}

func TestImagesResolvesCatalogArchAwareWithDefault(t *testing.T) {
	var nameFilters []string
	c := catalogWith(&fakeEC2{
		images: func(in *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error) {
			for _, fl := range in.Filters {
				if aws.ToString(fl.Name) == "name" {
					nameFilters = append(nameFilters, fl.Values[0])
				}
			}
			return &ec2.DescribeImagesOutput{Images: []types.Image{
				{ImageId: aws.String("ami-old"), CreationDate: aws.String("2024-01-01T00:00:00Z")},
				{ImageId: aws.String("ami-new"), CreationDate: aws.String("2026-05-01T00:00:00Z")},
			}}, nil
		},
	})
	imgs, err := c.Images(context.Background(), "AK", "sk", "ap-southeast-1", "arm64")
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if len(imgs) != 3 {
		t.Fatalf("want 3 catalog images, got %d (%+v)", len(imgs), imgs)
	}
	if imgs[0].Name != "Ubuntu 26.04 LTS" || !imgs[0].Default {
		t.Fatalf("first image must be default Ubuntu 26.04 LTS, got %+v", imgs[0])
	}
	if imgs[0].ID != "ami-new" {
		t.Fatalf("must pick newest by CreationDate, got %q", imgs[0].ID)
	}
	if !strings.Contains(nameFilters[0], "26.04-arm64-server") {
		t.Fatalf("ubuntu 26.04 filter must use arm64 token, got %q", nameFilters[0])
	}
	if !strings.Contains(nameFilters[2], "al2023-ami-2023.*-arm64") {
		t.Fatalf("al2023 filter must use arm64 token, got %q", nameFilters[2])
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/cloud/ -run 'TestArchitecture|TestImages' -v`
Expected: 编译失败(`undefined: Image` / `c.Architecture` / `c.Images`)。

- [ ] **Step 3: 写实现**

在 `internal/cloud/catalog.go` 的 import 块加入 `"fmt"` 与 `"github.com/aws/aws-sdk-go-v2/aws"`,并追加:
```go
// Image is one selectable OS image resolved for a region+architecture.
type Image struct {
	ID      string // ami-...
	Name    string // friendly, e.g. "Ubuntu 26.04 LTS"
	Default bool   // the form pre-selects the default image
}

const canonicalOwner = "099720109477" // Canonical (Ubuntu) AWS account

// osSpec is one entry in the curated OS catalog. pattern has a single %s for the
// architecture token produced by archToken.
type osSpec struct {
	name      string
	owner     string
	pattern   string
	archToken func(arch string) string
	isDefault bool
}

func ubuntuArchToken(arch string) string {
	if arch == "arm64" {
		return "arm64"
	}
	return "amd64"
}

func al2023ArchToken(arch string) string {
	if arch == "arm64" {
		return "arm64"
	}
	return "x86_64"
}

// osCatalog is the curated image list. Add a distro by adding an entry.
// hvm-ssd* matches both the hvm-ssd and hvm-ssd-gp3 Ubuntu storage variants.
var osCatalog = []osSpec{
	{name: "Ubuntu 26.04 LTS", owner: canonicalOwner, pattern: "ubuntu/images/hvm-ssd*/ubuntu-*-26.04-%s-server-*", archToken: ubuntuArchToken, isDefault: true},
	{name: "Ubuntu 24.04 LTS", owner: canonicalOwner, pattern: "ubuntu/images/hvm-ssd*/ubuntu-*-24.04-%s-server-*", archToken: ubuntuArchToken},
	{name: "Amazon Linux 2023", owner: "amazon", pattern: "al2023-ami-2023.*-%s", archToken: al2023ArchToken},
}

// Architecture reports the CPU architecture ("x86_64" or "arm64") of instanceType,
// defaulting to x86_64 when the type reports both or is unknown.
func (c *Catalog) Architecture(ctx context.Context, accessKey, secret, region, instanceType string) (string, error) {
	out, err := c.NewClient(accessKey, secret, region).DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []types.InstanceType{types.InstanceType(instanceType)},
	})
	if err != nil {
		return "", err
	}
	if len(out.InstanceTypes) == 0 || out.InstanceTypes[0].ProcessorInfo == nil {
		return "x86_64", nil
	}
	return archOf(out.InstanceTypes[0].ProcessorInfo.SupportedArchitectures), nil
}

// archOf collapses AWS's SupportedArchitectures to one token, preferring x86_64
// when a type supports both.
func archOf(supported []types.ArchitectureType) string {
	hasArm, hasX86 := false, false
	for _, a := range supported {
		switch a {
		case types.ArchitectureTypeArm64:
			hasArm = true
		case types.ArchitectureTypeX8664:
			hasX86 = true
		}
	}
	if hasArm && !hasX86 {
		return "arm64"
	}
	return "x86_64"
}

// Images resolves the curated OS catalog to the newest AMI per entry for the
// given region and architecture. Entries that resolve to no image are skipped.
func (c *Catalog) Images(ctx context.Context, accessKey, secret, region, arch string) ([]Image, error) {
	client := c.NewClient(accessKey, secret, region)
	var out []Image
	for _, spec := range osCatalog {
		name := fmt.Sprintf(spec.pattern, spec.archToken(arch))
		res, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
			Owners: []string{spec.owner},
			Filters: []types.Filter{
				{Name: aws.String("name"), Values: []string{name}},
				{Name: aws.String("state"), Values: []string{"available"}},
			},
		})
		if err != nil {
			return nil, err
		}
		if newest := newestImage(res.Images); newest != nil {
			out = append(out, Image{ID: aws.ToString(newest.ImageId), Name: spec.name, Default: spec.isDefault})
		}
	}
	return out, nil
}

// newestImage returns the image with the latest CreationDate (ISO-8601 sorts
// lexicographically), or nil for an empty slice.
func newestImage(imgs []types.Image) *types.Image {
	var best *types.Image
	for i := range imgs {
		if best == nil || aws.ToString(imgs[i].CreationDate) > aws.ToString(best.CreationDate) {
			best = &imgs[i]
		}
	}
	return best
}
```

> 实现注:Ubuntu 26.04 codename 由 `ubuntu-*-26.04` 通配覆盖。真实过滤命中在 Task 5 的实机验收中确认(单测用 mock)。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/cloud/ -v`
Expected: PASS(5 个:含 Task 1 的 2 个)。

- [ ] **Step 5: 提交**

```bash
git add internal/cloud/catalog.go internal/cloud/catalog_test.go
git commit -m "feat: add Catalog Architecture and curated OS image resolution"
```

---

### Task 3: 元数据 HTTP 接口 + Deps 接线

**Files:**
- Create: `internal/api/metadata.go`
- Create: `internal/api/metadata_test.go`
- Modify: `internal/api/server.go`(`Deps` 加 `Catalog`;`NewRouter` 注册路由)
- Modify: `internal/api/accounts_test.go`(`testDeps` 给默认 `Catalog`)
- Modify: `cmd/hermes/main.go`(注入 `cloud.NewCatalog()`)

**Interfaces:**
- Consumes: `cloud.Catalog` 的 4 个方法与 `cloud.Image`;`store.GetCloudAccount`。
- Produces:
  - `type CatalogAPI interface { Regions/InstanceTypes/Architecture/Images(...) }`(签名同 `*cloud.Catalog`)
  - `Deps.Catalog CatalogAPI`
  - 路由 `GET /blueprints/regions`、`GET /blueprints/instance-types`、`GET /blueprints/amis`(参数:`cloud_account_id`、`region`、`instance_type`)

- [ ] **Step 1: `Deps` 加 Catalog 字段并注册路由**

在 `internal/api/server.go` 的 `Deps` 结构体加一行(`Broker` 之后):
```go
	Broker       *orchestrator.Broker
	Catalog      CatalogAPI
```
在 `NewRouter` 中 `addBlueprintRoutes(mux, d)` 之后加:
```go
	addBlueprintRoutes(mux, d)
	addMetadataRoutes(mux, d)
```

- [ ] **Step 2: 写失败测试**

Create `internal/api/metadata_test.go`:
```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/cloud"
)

// fakeCatalog implements CatalogAPI; nil func fields return empty results.
type fakeCatalog struct {
	regions func() ([]string, error)
	itypes  func() ([]string, error)
	arch    func() (string, error)
	images  func() ([]cloud.Image, error)
}

func (f fakeCatalog) Regions(context.Context, string, string) ([]string, error) {
	if f.regions != nil {
		return f.regions()
	}
	return nil, nil
}
func (f fakeCatalog) InstanceTypes(context.Context, string, string, string) ([]string, error) {
	if f.itypes != nil {
		return f.itypes()
	}
	return nil, nil
}
func (f fakeCatalog) Architecture(context.Context, string, string, string, string) (string, error) {
	if f.arch != nil {
		return f.arch()
	}
	return "x86_64", nil
}
func (f fakeCatalog) Images(context.Context, string, string, string, string) ([]cloud.Image, error) {
	if f.images != nil {
		return f.images()
	}
	return nil, nil
}

func authedGet(t *testing.T, d Deps, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(d.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	NewRouter(d).ServeHTTP(rec, req)
	return rec
}

func TestRegionsEndpointRendersOptions(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	d.Catalog = fakeCatalog{regions: func() ([]string, error) { return []string{"ap-southeast-1", "us-west-2"}, nil }}
	rec := authedGet(t, d, "/blueprints/regions?cloud_account_id="+itoa(aid))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<option value="ap-southeast-1">`) {
		t.Fatalf("missing region option: %s", rec.Body.String())
	}
}

func TestAMIsEndpointRendersFallbackAndSelectedDefault(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	d.Catalog = fakeCatalog{
		arch:   func() (string, error) { return "x86_64", nil },
		images: func() ([]cloud.Image, error) { return []cloud.Image{{ID: "ami-123", Name: "Ubuntu 26.04 LTS", Default: true}}, nil },
	}
	rec := authedGet(t, d, "/blueprints/amis?cloud_account_id="+itoa(aid)+"&region=ap-southeast-1&instance_type=t3.micro")
	body := rec.Body.String()
	if !strings.Contains(body, `<option value="">自动:最新 Ubuntu 26.04 LTS</option>`) {
		t.Fatalf("missing fallback option: %s", body)
	}
	if !strings.Contains(body, `<option value="ami-123" selected>Ubuntu 26.04 LTS</option>`) {
		t.Fatalf("missing selected default AMI: %s", body)
	}
}

func TestMetadataEndpointUnknownAccountDegrades(t *testing.T) {
	d := testDeps(t)
	rec := authedGet(t, d, "/blueprints/regions?cloud_account_id=999")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 graceful", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "请先选择云账号") {
		t.Fatalf("expected inline hint, got: %s", rec.Body.String())
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/api/ -run 'TestRegionsEndpoint|TestAMIsEndpoint|TestMetadataEndpoint' -v`
Expected: 编译失败(`undefined: CatalogAPI` / `addMetadataRoutes` / `Deps.Catalog`)。

- [ ] **Step 4: 写实现**

Create `internal/api/metadata.go`:
```go
package api

import (
	"context"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/store"
)

// CatalogAPI is the subset of cloud.Catalog the metadata handlers need.
// *cloud.Catalog satisfies it; tests inject a fake.
type CatalogAPI interface {
	Regions(ctx context.Context, accessKey, secret string) ([]string, error)
	InstanceTypes(ctx context.Context, accessKey, secret, region string) ([]string, error)
	Architecture(ctx context.Context, accessKey, secret, region, instanceType string) (string, error)
	Images(ctx context.Context, accessKey, secret, region, arch string) ([]cloud.Image, error)
}

func addMetadataRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /blueprints/regions", func(w http.ResponseWriter, r *http.Request) {
		handleRegions(w, r, d)
	})
	mux.HandleFunc("GET /blueprints/instance-types", func(w http.ResponseWriter, r *http.Request) {
		handleInstanceTypes(w, r, d)
	})
	mux.HandleFunc("GET /blueprints/amis", func(w http.ResponseWriter, r *http.Request) {
		handleAMIs(w, r, d)
	})
}

// resolveAccount reads cloud_account_id and returns its decrypted credentials.
// On failure it writes an inline hint option and returns ok=false.
func resolveAccount(w http.ResponseWriter, r *http.Request, d Deps) (store.CloudAccount, bool) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("cloud_account_id"), 10, 64)
	acc, err := d.Store.GetCloudAccount(r.Context(), id)
	if err != nil {
		writeOptions(w, nil, "请先选择云账号")
		return store.CloudAccount{}, false
	}
	return acc, true
}

func handleRegions(w http.ResponseWriter, r *http.Request, d Deps) {
	acc, ok := resolveAccount(w, r, d)
	if !ok {
		return
	}
	regions, err := d.Catalog.Regions(r.Context(), acc.AccessKeyID, acc.SecretAccessKey)
	if err != nil {
		writeOptions(w, nil, "无法获取 Region:"+err.Error())
		return
	}
	writeOptions(w, regions, "")
}

func handleInstanceTypes(w http.ResponseWriter, r *http.Request, d Deps) {
	acc, ok := resolveAccount(w, r, d)
	if !ok {
		return
	}
	region := r.URL.Query().Get("region")
	itypes, err := d.Catalog.InstanceTypes(r.Context(), acc.AccessKeyID, acc.SecretAccessKey, region)
	if err != nil {
		writeOptions(w, nil, "无法获取实例规格:"+err.Error())
		return
	}
	writeOptions(w, itypes, "")
}

func handleAMIs(w http.ResponseWriter, r *http.Request, d Deps) {
	acc, ok := resolveAccount(w, r, d)
	if !ok {
		return // resolveAccount already wrote the inline hint
	}
	q := r.URL.Query()
	region, itype := q.Get("region"), q.Get("instance_type")
	arch, err := d.Catalog.Architecture(r.Context(), acc.AccessKeyID, acc.SecretAccessKey, region, itype)
	if err != nil {
		writeAMIOptions(w, nil)
		return
	}
	imgs, err := d.Catalog.Images(r.Context(), acc.AccessKeyID, acc.SecretAccessKey, region, arch)
	if err != nil {
		writeAMIOptions(w, nil)
		return
	}
	writeAMIOptions(w, imgs)
}

// writeOptions renders <option> elements for a datalist. A non-empty note is
// rendered first so the user sees why the list is empty.
func writeOptions(w http.ResponseWriter, values []string, note string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	if note != "" {
		b.WriteString(`<option value="">` + html.EscapeString(note) + `</option>`)
	}
	for _, v := range values {
		b.WriteString(`<option value="` + html.EscapeString(v) + `"></option>`)
	}
	_, _ = w.Write([]byte(b.String()))
}

// writeAMIOptions renders <option> elements for the AMI <select>. The first
// option is always the auto/fallback (empty value → the program auto-resolves
// Ubuntu 26.04). The catalog's default image is pre-selected.
func writeAMIOptions(w http.ResponseWriter, imgs []cloud.Image) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	b.WriteString(`<option value="">自动:最新 Ubuntu 26.04 LTS</option>`)
	for _, im := range imgs {
		sel := ""
		if im.Default {
			sel = " selected"
		}
		b.WriteString(`<option value="` + html.EscapeString(im.ID) + `"` + sel + `>` + html.EscapeString(im.Name) + `</option>`)
	}
	_, _ = w.Write([]byte(b.String()))
}
```

- [ ] **Step 5: `testDeps` 加默认 Catalog**

在 `internal/api/accounts_test.go` 的 `testDeps` 返回的 `Deps{...}` 里加一行:
```go
		Auth:      auth.New("pw", []byte("k")),
		Renderer:  r,
		Catalog:   fakeCatalog{},
```

- [ ] **Step 6: main.go 注入真实 Catalog**

在 `cmd/hermes/main.go` 的 `deps := api.Deps{...}`(约 79–86 行)里加一行:
```go
		Orchestrator: orch,
		Broker:       broker,
		Catalog:      cloud.NewCatalog(),
```

- [ ] **Step 7: 运行测试确认通过 + 构建**

Run:
```bash
go test ./internal/api/ -run 'TestRegionsEndpoint|TestAMIsEndpoint|TestMetadataEndpoint' -v
go build ./...
```
Expected: 3 个测试 PASS;`go build ./...` 无错误。

- [ ] **Step 8: 提交**

```bash
git add internal/api/metadata.go internal/api/metadata_test.go internal/api/server.go internal/api/accounts_test.go cmd/hermes/main.go
git commit -m "feat: add blueprint metadata endpoints for regions, instance types, amis"
```

---

### Task 4: 蓝图表单 htmx 联动控件

**Files:**
- Modify: `internal/web/templates/blueprints.html`
- Modify: `internal/api/blueprints_test.go`(表单冒烟测试)

**Interfaces:**
- Consumes: Task 3 的三个接口 URL 与参数名(`cloud_account_id`/`region`/`instance_type`);`authedGet`(Task 3)。

- [ ] **Step 1: 写失败测试**

在 `internal/api/blueprints_test.go` 顶部 import 块加入 `"strings"`,并追加:
```go
func TestBlueprintFormHasLiveControls(t *testing.T) {
	d := testDepsWithOrchestrator(t)
	seedProjectAccount(t, d)
	body := authedGet(t, d, "/blueprints").Body.String()
	for _, want := range []string{
		`hx-get="/blueprints/regions"`,
		`hx-trigger="change, load"`,
		`list="region-opts"`,
		`<datalist id="region-opts">`,
		`hx-get="/blueprints/instance-types"`,
		`list="itype-opts"`,
		`hx-get="/blueprints/amis"`,
		`<select name="ami" id="ami-select">`,
		`自动:最新 Ubuntu 26.04 LTS`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("blueprint form missing %q", want)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/ -run TestBlueprintFormHasLiveControls -v`
Expected: FAIL(当前模板无这些属性/元素)。

- [ ] **Step 3: 改模板**

Replace `internal/web/templates/blueprints.html` 全文为:
```html
{{define "content"}}
<h2>蓝图</h2>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<form method="post" action="/blueprints">
  <fieldset>
    <legend>新建蓝图(安全组 + EC2)</legend>
    <label>名称 <input name="name" required></label>
    <label>项目 <select name="project_id" required>{{range .Projects}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></label>
    <label>云账号
      <select name="cloud_account_id" required
              hx-get="/blueprints/regions" hx-trigger="change, load"
              hx-target="#region-opts" hx-swap="innerHTML">
        {{range .Accounts}}<option value="{{.ID}}">{{.Name}} ({{.AWSAccountID}})</option>{{end}}
      </select>
    </label>
    <label>Region
      <input name="region" list="region-opts" value="ap-southeast-1" required
             hx-get="/blueprints/instance-types" hx-trigger="change"
             hx-target="#itype-opts" hx-swap="innerHTML"
             hx-include="[name='cloud_account_id'],[name='region']">
      <datalist id="region-opts"></datalist>
    </label>
    <label>实例规格
      <input name="instance_type" list="itype-opts" value="t3.micro" required
             hx-get="/blueprints/amis" hx-trigger="change"
             hx-target="#ami-select" hx-swap="innerHTML"
             hx-include="[name='cloud_account_id'],[name='region'],[name='instance_type']">
      <datalist id="itype-opts"></datalist>
    </label>
    <label>数量 <input name="count" type="number" value="1" min="1" max="10" required></label>
    <label>AMI
      <select name="ami" id="ami-select">
        <option value="">自动:最新 Ubuntu 26.04 LTS</option>
      </select>
    </label>
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

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/api/ -run 'TestBlueprintForm|TestCreateBlueprint|TestDeploy' -v`
Expected: 全 PASS(新表单冒烟测试 + 既有创建/部署测试仍绿——字段 name 未变)。

- [ ] **Step 5: 提交**

```bash
git add internal/web/templates/blueprints.html internal/api/blueprints_test.go
git commit -m "feat: wire blueprint form to live AWS catalog via htmx"
```

---

### Task 5: Provisioner —— 架构自适应 Ubuntu 26.04 默认 AMI

**Files:**
- Modify: `internal/provisioner/pulumiengine/program.go`
- Modify: `internal/provisioner/pulumiengine/program_test.go`

**Interfaces:**
- Consumes: `provisioner.BlueprintParams`(不变)。
- Produces: `func resolveArch(supported []string) string`、`func ubuntuNameFilter(arch string) string`、`const ubuntuOwner`(供测试引用)。

- [ ] **Step 1: 写失败的纯函数测试 + mock 断言**

在 `internal/provisioner/pulumiengine/program_test.go` 顶部 import 块加入 `"strings"`,并追加:
```go
func TestResolveArch(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"x86_64"}, "x86_64"},
		{[]string{"arm64"}, "arm64"},
		{[]string{"x86_64", "arm64"}, "x86_64"},
		{nil, "x86_64"},
	}
	for _, c := range cases {
		if got := resolveArch(c.in); got != c.want {
			t.Errorf("resolveArch(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUbuntuNameFilterArchAware(t *testing.T) {
	if got := ubuntuNameFilter("x86_64"); !strings.Contains(got, "26.04-amd64-server") {
		t.Errorf("x86_64 filter = %q, want amd64 token", got)
	}
	if got := ubuntuNameFilter("arm64"); !strings.Contains(got, "26.04-arm64-server") {
		t.Errorf("arm64 filter = %q, want arm64 token", got)
	}
}
```

同时,在 `recordMocks.Call` 的 `switch` 中新增一个 case(在 `getAmi` 附近):
```go
	case "aws:ec2/getInstanceType:getInstanceType":
		return resource.NewPropertyMapFromMap(map[string]any{
			"supportedArchitectures": []any{"x86_64"},
		}), nil
```

并在 `TestBuildProgramDeclaresResources` 末尾(`getAmi` 断言之后)追加:
```go
	if !called("aws:ec2/getInstanceType:getInstanceType") {
		t.Fatalf("expected arch resolution via getInstanceType; calls=%v", m.calls)
	}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/provisioner/pulumiengine/ -run 'TestResolveArch|TestUbuntuNameFilter|TestBuildProgramDeclaresResources' -v`
Expected: 编译失败(`undefined: resolveArch` / `ubuntuNameFilter`)。

- [ ] **Step 3: 写实现**

在 `internal/provisioner/pulumiengine/program.go` 中:

(a) 删除旧的 `al2023NameFilter` 常量及其注释(第 15–19 行那段)。

(b) 在文件末尾(`buildProgram` 之外)追加:
```go
const ubuntuOwner = "099720109477" // Canonical (Ubuntu)

// ubuntuNameFilter matches the latest Ubuntu 26.04 LTS server image for arch.
// hvm-ssd* covers both the hvm-ssd and hvm-ssd-gp3 storage variants.
func ubuntuNameFilter(arch string) string {
	token := "amd64"
	if arch == "arm64" {
		token = "arm64"
	}
	return "ubuntu/images/hvm-ssd*/ubuntu-*-26.04-" + token + "-server-*"
}

// resolveArch collapses an instance type's SupportedArchitectures to one token,
// preferring x86_64 when both are present.
func resolveArch(supported []string) string {
	hasArm, hasX86 := false, false
	for _, a := range supported {
		switch a {
		case "arm64":
			hasArm = true
		case "x86_64":
			hasX86 = true
		}
	}
	if hasArm && !hasX86 {
		return "arm64"
	}
	return "x86_64"
}
```

(c) 把 `buildProgram` 里原来的 AMI 解析块:
```go
		amiID := p.EC2.AMI
		if amiID == "" {
			ami, err := ec2.LookupAmi(ctx, &ec2.LookupAmiArgs{
				MostRecent: pulumi.BoolRef(true),
				Owners:     []string{"amazon"},
				Filters: []ec2.GetAmiFilter{
					{Name: "name", Values: []string{al2023NameFilter}},
					{Name: "state", Values: []string{"available"}},
				},
			})
			if err != nil {
				return err
			}
			amiID = ami.Id
		}
```
替换为:
```go
		amiID := p.EC2.AMI
		if amiID == "" {
			it, err := ec2.GetInstanceType(ctx, &ec2.GetInstanceTypeArgs{InstanceType: p.EC2.InstanceType})
			if err != nil {
				return err
			}
			arch := resolveArch(it.SupportedArchitectures)
			ami, err := ec2.LookupAmi(ctx, &ec2.LookupAmiArgs{
				MostRecent: pulumi.BoolRef(true),
				Owners:     []string{ubuntuOwner},
				Filters: []ec2.GetAmiFilter{
					{Name: "name", Values: []string{ubuntuNameFilter(arch)}},
					{Name: "state", Values: []string{"available"}},
				},
			})
			if err != nil {
				return err
			}
			amiID = ami.Id
		}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/provisioner/pulumiengine/ -v`
Expected: PASS(纯函数测试 + `TestBuildProgramDeclaresResources`,含 getInstanceType 断言与既有 no-ssm 断言)。

- [ ] **Step 5: 提交**

```bash
git add internal/provisioner/pulumiengine/program.go internal/provisioner/pulumiengine/program_test.go
git commit -m "feat: resolve default AMI to arch-aware Ubuntu 26.04 LTS"
```

---

### Task 6: Provisioner —— 每台实例分配弹性 IP

**Files:**
- Modify: `internal/provisioner/pulumiengine/program.go`
- Modify: `internal/provisioner/pulumiengine/program_test.go`

**Interfaces:**
- Consumes: Task 5 后的 `buildProgram`。
- Produces: 每台实例一个 `aws:ec2/eip:Eip`;`public_ips` 输出来自 EIP。

> 实机验收提醒(非代码步骤):运行 `make test-integration` 或真跑前,账号 IAM 需含 `ec2:AllocateAddress`、`ec2:AssociateAddress`、`ec2:DisassociateAddress`、`ec2:ReleaseAddress`。

- [ ] **Step 1: 写失败测试**

在 `recordMocks.NewResource` 里,给 EIP 资源补一个 publicIp 输出(与现有 instance 分支并列):
```go
	if args.TypeToken == "aws:ec2/eip:Eip" {
		outputs["publicIp"] = "52.1.2.3"
	}
```

在 `TestBuildProgramDeclaresResources` 里(`instances = 2` 断言之后)追加:
```go
	if got := count("aws:ec2/eip:Eip"); got != 2 {
		t.Fatalf("eips = %d, want 2 (one per instance)", got)
	}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/provisioner/pulumiengine/ -run TestBuildProgramDeclaresResources -v`
Expected: FAIL(`eips = 0, want 2`)。

- [ ] **Step 3: 写实现**

在 `internal/provisioner/pulumiengine/program.go` 的实例循环中,`inst, err := ec2.NewInstance(...)` 成功之后、`ids = append(...)` 之前插入 EIP,并把 `ips` 改为取 EIP 的 `PublicIp`。整段循环体改为:
```go
			inst, err := ec2.NewInstance(ctx, fmt.Sprintf("hermes-ec2-%d", i), args)
			if err != nil {
				return err
			}
			eip, err := ec2.NewEip(ctx, fmt.Sprintf("hermes-eip-%d", i), &ec2.EipArgs{
				Instance: inst.ID(),
				Domain:   pulumi.String("vpc"),
			})
			if err != nil {
				return err
			}
			ids = append(ids, inst.ID().ToStringOutput())
			ips = append(ips, eip.PublicIp)
			dns = append(dns, inst.PublicDns)
```

- [ ] **Step 4: 更新集成测试注释**

`internal/provisioner/pulumiengine/integration_test.go` 现有断言 `res.Outputs["public_ips"] == nil → fatal` 已覆盖「EIP 输出非空」(destroy 会自动释放 EIP)。把该断言上方注释改为说明来源已变:
```go
	// public_ips are now the instances' Elastic IPs (stable across reboots).
	if res.Outputs["public_ips"] == nil {
		t.Fatalf("expected public_ips output, got %+v", res.Outputs)
	}
```
Run: `go vet -tags integration ./internal/provisioner/pulumiengine/`
Expected: 无告警(集成测试仅在 `-tags integration` + 真实 AWS 下运行)。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/provisioner/pulumiengine/ -v && go vet ./...`
Expected: 全 PASS;`go vet` 无告警。

- [ ] **Step 6: 提交**

```bash
git add internal/provisioner/pulumiengine/program.go internal/provisioner/pulumiengine/program_test.go internal/provisioner/pulumiengine/integration_test.go
git commit -m "feat: allocate an elastic IP per instance for stable public IPs"
```

---

## 收尾验证(全部任务后)

- [ ] `go build ./...` 无错误
- [ ] `go test ./...` 全绿
- [ ] `go vet ./...` 与 `go vet -tags integration ./internal/provisioner/pulumiengine/` 无告警
- [ ] 手动实机验收(真实 AWS,可选但推荐):启动服务 → 新建蓝图时观察 Region/实例规格 datalist 联动、AMI 默认 Ubuntu 26.04 → 部署 → 环境详情页 `public_ips` 为 EIP,实例重启后不变 → 销毁后 EIP 一并释放。
