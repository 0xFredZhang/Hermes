# Hermes 蓝图创建增强设计:AWS 目录联动 + 弹性 IP

**日期**:2026-07-07
**状态**:已批准,待实现计划
**关联**:延续 M2(provisioning walking skeleton),吃掉 M3 backlog 中的 EIP 与 M7(架构自适应 AMI)。

## 1. 背景与目标

M2 的蓝图创建表单里,Region、实例规格、AMI 都是手输文本框,网络用默认动态公网 IP。手输易错(要记 region 名、规格名、AMI id),动态 IP 在实例重启后会变。

本次目标:
1. Region、实例规格改为**从 AWS 实时拉取的可搜索下拉**。
2. AMI 改为**精选 OS 目录的下拉选择**,默认 **Ubuntu 26.04 LTS**,并按所选实例规格的 CPU 架构自动匹配。
3. 每台实例分配**弹性 IP(EIP)**,重启后公网 IP 不变。

非目标(本期不做):多 AZ / 自建 VPC、RDS/Redis、目录结果缓存、跨字段架构强校验。

## 2. 关键决策(brainstorming 结论)

| 决策点 | 选择 |
| --- | --- |
| AMI 下拉内容 | **精选 OS 目录**:实时解析每 region/架构下的最新镜像,初定 Ubuntu 26.04 LTS(默认)、Ubuntu 24.04 LTS、Amazon Linux 2023 |
| 架构支持 | **x86_64 + ARM(Graviton)自适应**,AMI 按所选规格架构匹配;顺带修 M7(旧逻辑写死 x86_64) |
| 可搜索下拉实现 | **原生 `<datalist>`**(Region、实例规格),零依赖;AMI 因清单短用普通 `<select>`(友好名 + 提交 ami-id) |
| 弹性 IP | **总是为每台实例分配 EIP**(AWS 对所有公网 IPv4 一样计费,EIP 相比动态 IP 无额外成本) |

## 3. 用户流程

蓝图表单内的联动链(htmx,均为局部片段替换):

```
选云账号 ──change/load──▶ GET /blueprints/regions          ──▶ 填充 Region datalist
Region  ──change──▶      GET /blueprints/instance-types    ──▶ 填充 实例规格 datalist
实例规格 ──change──▶      GET /blueprints/amis(推导架构)   ──▶ 填充 AMI <select>(默认选中 Ubuntu 26.04)
提交    ──▶ POST /blueprints(处理器不变,取值来自更丰富的控件)
部署    ──▶ provision:架构自适应 Ubuntu 26.04 默认 + 每实例 EIP → public_ips = EIP 稳定 IP
```

云账号 select 带 `hx-trigger="change, load"`,页面打开即为默认账号拉取 region。Region/实例规格用 `<input list>`,拉取失败或用户确知取值时可**直接手输兜底**。

## 4. 架构与组件

### 4.1 `internal/cloud/catalog.go` — AWS 元数据目录

与现有 `Validator` 并排,复用「可注入 client 工厂」范式以便测试。内部用静态凭证构造 `aws-sdk-go-v2/service/ec2` 客户端。

```go
type EC2API interface {
    DescribeRegions(ctx, *ec2.DescribeRegionsInput, ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error)
    DescribeInstanceTypeOfferings(ctx, *ec2.DescribeInstanceTypeOfferingsInput, ...) (*ec2.DescribeInstanceTypeOfferingsOutput, error)
    DescribeInstanceTypes(ctx, *ec2.DescribeInstanceTypesInput, ...) (*ec2.DescribeInstanceTypesOutput, error)
    DescribeImages(ctx, *ec2.DescribeImagesInput, ...) (*ec2.DescribeImagesOutput, error)
}

type Catalog struct {
    NewClient func(creds provisioner.AWSCreds, region string) EC2API // overridable in tests
}

type Image struct {
    ID          string // ami-...
    Name        string // 友好名,如 "Ubuntu 26.04 LTS"
    Default     bool   // Ubuntu 26.04 = true
}

func (c *Catalog) Regions(ctx, creds) ([]string, error)
func (c *Catalog) InstanceTypes(ctx, creds, region string) ([]string, error)
func (c *Catalog) Architecture(ctx, creds, region, instanceType string) (string, error) // "x86_64" | "arm64"
func (c *Catalog) Images(ctx, creds, region, arch string) ([]Image, error)
```

- `Regions`:`DescribeRegions`(默认只返回已启用 region),取 `RegionName`,排序。
- `InstanceTypes`:`DescribeInstanceTypeOfferings`(`LocationType=region`),分页取 `InstanceType`,排序去重。
- `Architecture`:`DescribeInstanceTypes(InstanceTypes=[t])` 读 `ProcessorInfo.SupportedArchitectures`;含 `arm64` 且不含 `x86_64` → `arm64`,否则 `x86_64`。
- `Images`:遍历**硬编码目录**(见 4.5),对每条按 (region, arch) 调 `DescribeImages`(owner + name 过滤),按 `CreationDate` 取最新一枚;解析不到的条目跳过。

### 4.2 元数据接口 — `internal/api/metadata.go`(新文件,`addBlueprintRoutes` 注册)

三个只读 GET,鉴权走现有 mux 中间件,返回 HTML `<option>` 片段:

- `GET /blueprints/regions?account_id=` → `<option>` 列表填 Region datalist。
- `GET /blueprints/instance-types?account_id=&region=` → 填实例规格 datalist。
- `GET /blueprints/amis?account_id=&region=&instance_type=` → 先 `Architecture()` 推架构,再 `Images()`,渲染 `<select>` 选项片段。片段结构固定为:**首项**永远是兜底 `<option value="">自动:最新 Ubuntu 26.04 LTS</option>`(不带 `selected`);其后是目录解析出的各镜像 `<option value="ami-...">友好名</option>`,其中 `Default==true`(Ubuntu 26.04)那项带 `selected`,从而覆盖首项成为默认选中。若目录解析为空(如权限不足),片段只剩兜底首项,它作为唯一项自然被选中——空 AMI 在部署时仍自动解析 Ubuntu 26.04。

凭证:`Store.GetCloudAccount(id)`(解密)→ `provisioner.AWSCreds`,仅进程内构造 SDK client,不落日志。`Catalog` 注入 `api.Deps`,在 `cmd/hermes/main.go` 构造。

### 4.3 表单 — `internal/web/templates/blueprints.html`

- 云账号 select:`hx-get="/blueprints/regions" hx-trigger="change, load" hx-target="#region-list" hx-swap="innerHTML" hx-include="[name='cloud_account_id']"`。
- Region:`<input name="region" list="region-opts">` + `<datalist id="region-opts">`;自身 `hx-get="/blueprints/instance-types" hx-trigger="change" hx-target="#itype-opts" hx-include="[name='cloud_account_id'],[name='region']"`。
- 实例规格:`<input name="instance_type" list="itype-opts">` + `<datalist id="itype-opts">`;自身 `hx-get="/blueprints/amis" hx-trigger="change" hx-target="#ami-select" hx-include="[name='cloud_account_id'],[name='region'],[name='instance_type']"`。
- AMI:`<select name="ami" id="ami-select">`,初始含兜底首项;由上面 htmx 调用填充。

datalist 的 target 是 `<datalist>` 的 id,`hx-swap="innerHTML"` 替换其中的 `<option>`。

### 4.4 Provisioner — `internal/provisioner/pulumiengine/program.go`

两处改动:

1. **默认 AMI 改为架构自适应 Ubuntu 26.04 LTS**。`p.EC2.AMI == ""` 时:
   - `ec2.GetInstanceType(ctx, &ec2.GetInstanceTypeArgs{InstanceType: p.EC2.InstanceType})` 读 `SupportedArchitectures`,推 `arch`(同 4.1 规则)。
   - `ec2.LookupAmi`(Canonical owner `099720109477`,name 过滤见 4.5,arch 对应)。
   - 替换现有写死 `al2023NameFilter` 的 x86_64 逻辑(**修 M7**)。`p.EC2.AMI != ""` 时按给定 id 用,语义不变。
2. **每台实例分配 EIP**。实例创建后 `ec2.NewEip(ctx, fmt.Sprintf("hermes-eip-%d", i), &ec2.EipArgs{Instance: inst.ID(), Domain: pulumi.String("vpc")})`;`public_ips` 输出改为 `eip.PublicIp`(稳定 IP)。`instance_ids`、`public_dns` 保留。

### 4.5 OS 目录定义(硬编码常量)

| 友好名 | owner | name 过滤模板(`{arch}` 见下) | 默认 |
| --- | --- | --- | --- |
| Ubuntu 26.04 LTS | `099720109477` | `ubuntu/images/hvm-ssd*/ubuntu-*-26.04-{ubuntuArch}-server-*` | ✅ |
| Ubuntu 24.04 LTS | `099720109477` | `ubuntu/images/hvm-ssd*/ubuntu-*-24.04-{ubuntuArch}-server-*` | |
| Amazon Linux 2023 | `amazon` | `al2023-ami-2023.*-{alArch}` | |

架构占位映射:`x86_64` → `ubuntuArch=amd64`、`alArch=x86_64`;`arm64` → `ubuntuArch=arm64`、`alArch=arm64`。全部附加 `state=available` 过滤,按 `CreationDate` 降序取最新。

> 实现注:Ubuntu 26.04 的 codename 由 `ubuntu-*-26.04` 通配覆盖;`hvm-ssd*` 同时匹配 `hvm-ssd` 与 `hvm-ssd-gp3`。首个架构自适应 AMI 任务用一次真实 `DescribeImages` 确认过滤命中(TDD 覆盖)。

## 5. 数据模型与校验

**不变**。EIP 总是分配(无需字段);AMI 空=自动解析、非空=按选定用(语义不变);架构在部署时推导。`provisioner.BlueprintParams`、`EC2`、`Validate()` 均不改。表单 AMI `<select>` 提交显式 ami-id 或空串,两条路径都已被现有程序逻辑支持。

## 6. AWS 调用与权限

元数据接口需账号具备 `ec2:DescribeRegions`、`ec2:DescribeInstanceTypeOfferings`、`ec2:DescribeInstanceTypes`、`ec2:DescribeImages`(均为只读)。Provisioner 额外需 `ec2:AllocateAddress`、`ec2:AssociateAddress`、`ec2:ReleaseAddress`、`ec2:DisassociateAddress`(EIP)与 `ec2:DescribeInstanceTypes`(架构查询)。

新增依赖:`github.com/aws/aws-sdk-go-v2/service/ec2`(`go get` + `go mod tidy`)。

## 7. 错误处理与降级

- 元数据接口:账号不存在 → 返回带提示的空片段;AWS 报错(如权限不足)→ 返回内联提示 `<option>` 且不阻断,用户可在 datalist 中手输、AMI 用兜底首项。
- AMI 兜底首项 value 为空,部署时程序自动解析 Ubuntu 26.04,故即使实时列表全挂,「默认 Ubuntu 26.04」仍成立。
- 手输了与实例架构不匹配的 AMI:交给 AWS 在 preview 时报错,本期不加跨字段校验(YAGNI)。

## 8. 安全

延续 M2 约束:账号 secret 加密静置,元数据接口经 `GetCloudAccount` 解密后**仅进程内**构造只读 SDK client,绝不写日志、不注入全局进程 env。这些是只读 Describe 调用,不改任何资源。

## 9. 测试策略

- `internal/cloud/catalog_test.go`:假 `EC2API`。验证 Regions/InstanceTypes 解析+排序;`Architecture` 的 x86_64/arm64 判定;`Images` 的架构→name 映射(x86_64→amd64/x86_64、arm64→arm64)与「取最新」;Ubuntu 26.04 标 `Default`。
- `internal/api/blueprints_test.go`(扩展):三个接口注入假 `Catalog`,断言渲染 `<option>` 片段;账号不存在 / AWS 报错时的降级片段。
- `internal/provisioner/pulumiengine/program_test.go`(扩展):mock 断言每实例声明一个 `aws:ec2/eip:Eip`;AMI 经 Canonical `getAmi` 解析(替代 AL2023);架构推导正确;保留「不经 ssm getParameter」断言。
- 集成测试(`//go:build integration`,扩展):`public_ips` 来自 EIP 且非空;up→destroy 干净(含 EIP 释放)。

## 10. 范围外 / 未来

- 多 AZ / 子网选择(仍用 `subnet[0]`)—— 归 M3 M7 剩余项。
- 目录结果 TTL 缓存(实例规格列表较大)—— 后续优化。
- 更多发行版(Debian、Amazon Linux 2 等)—— 往 4.5 目录加条目即可。

## 11. 文件清单

- 新增:`internal/cloud/catalog.go`、`internal/cloud/catalog_test.go`、`internal/api/metadata.go`
- 修改:`internal/web/templates/blueprints.html`、`internal/provisioner/pulumiengine/program.go`、`internal/api/server.go`(Deps 加 `Catalog`)、`cmd/hermes/main.go`(构造 Catalog)、`go.mod`/`go.sum`
- 扩展测试:`internal/api/blueprints_test.go`、`internal/provisioner/pulumiengine/program_test.go`、`internal/provisioner/pulumiengine/integration_test.go`
