# Hermes 设计文档:AWS 基础设施一键编排平台(MVP · Phase 1)

- **日期**:2026-07-07
- **状态**:已通过 brainstorming 评审,待编写实现计划
- **范围**:AWS 单云 · Phase 1(provisioning)· 自用/小团队轻量工具

---

## 1. 背景与问题

当前痛点:每次部署新项目,都要在 AWS 控制台**手动**开一整套资源 —— EC2、RDS(MySQL)、ElastiCache(Redis),再配置安全组和弹性 IP。操作重复、繁琐、易错,跨多个 AWS 账号时尤其混乱。

理想状态:在一个云端平台**配置一次规则**,即可**一键自动化创建整套环境资源**;后续还能在创建好的服务器上执行脚本(装 Docker/Nginx、部署/更新 Docker 服务)。

## 2. 产品愿景(两阶段)与 MVP 边界

整体分两阶段:

- **Phase 1 · Provisioning(本 MVP)**:一键创建整套云资源。声明式 IaC 领域。
- **Phase 2 · 配置与部署(后续)**:SSH 到已建服务器装服务、发应用。配置管理领域。

**Hermes 的本质是「编排器」(orchestrator)**:它不取代 Pulumi/Terraform/Ansible,而是站在其上,统一管理【多云凭证 + 环境定义 + 执行流程 + 状态历史】,把手动重复操作变成"配置一次、一键复用"。

关于 Ansible vs Terraform 的结论:二者是**分工互补**而非二选一 —— Phase 1(建资源)属于 Terraform/Pulumi 的 IaC 领域,Phase 2(配置/部署)属于 Ansible 领域。**本 MVP 只涉及 Phase 1。**

## 3. 关键决策记录

| 决策 | 选择 | 理由 |
|---|---|---|
| 平台定位 | 综合平台,分阶段落地 | 先要 provisioning,再要部署;分步走,避免一次摊子铺太大 |
| 使用规模 | 自用 / 小团队 | 不做多租户/复杂 RBAC/审批流,砍掉过度设计(YAGNI) |
| Phase 1 执行引擎 | **Pulumi Automation API(Go)** | Go 原生、类型安全、为"内嵌 IaC 平台"而生;白嫖 state/plan/漂移检测;引擎抽象成 interface,可替换 |
| state backend | **单个 S3 bucket** | 一个 backend 存所有账号所有环境的 state;自主可控、生产可靠。**被管账号数量与 backend 数量无关** |
| 前端 | templ/html-template + htmx + SSE | 单二进制、无前端构建;表单/列表/实时日志足够 |
| 元数据存储 | SQLite 起步 | 自用够用、单文件;预留 Postgres 迁移接口 |
| Web 框架 | 标准库 `net/http`(必要时加 `chi`) | 自用工具,不上重框架 |

## 4. 架构

### 4.1 分层架构(核心思想:编排层稳定,执行引擎可替换)

```
┌───────────────────────────────────────────────┐
│  Web UI  ── 表单(配环境)· 列表 · 实时日志      │
├───────────────────────────────────────────────┤
│  HTTP API 层(Go)                              │
├───────────────────────────────────────────────┤
│  编排层 Orchestrator                            │
│   · 云账号/凭证管理  · 环境定义  · Job 调度+日志 │
├────────────────┬──────────────────────────────┤
│  Provisioner interface  ← 可替换引擎的抽象      │
│      └─ PulumiProvisioner(MVP 唯一实现)       │
├────────────────┴──────────────────────────────┤
│  Pulumi Automation API  ──────────────►  AWS    │
└───────────────────────────────────────────────┘
      ▲ 元数据:SQLite          ▲ Pulumi state:S3
```

### 4.2 引擎抽象接口

`Provisioner` 是核心解耦点,保证以后换引擎(OpenTofu/SDK)、扩 GCP 时上层不动:

```go
type Provisioner interface {
    Preview(ctx context.Context, env *Environment) (PreviewResult, error)
    Up(ctx context.Context, env *Environment, logs io.Writer) (UpResult, error)
    Destroy(ctx context.Context, env *Environment, logs io.Writer) error
    Refresh(ctx context.Context, env *Environment) (RefreshResult, error) // 漂移检测
}
```

- `env` 携带:blueprint 快照、目标 CloudAccount(解密后凭证)、Pulumi stack 标识。
- `logs` 用于流式输出(接到 SSE)。
- MVP 只实现 `PulumiProvisioner`。

## 5. 数据模型(SQLite,5 个核心实体)

**1. CloudAccount(云账号)** — 每个连接的 AWS 账号
- `name` 别名、`provider=aws`、`default_region`
- 凭证:`access_key_id` + `secret_access_key`(**AES-GCM 加密存**),或 `assume_role_arn`(后续增强)

**2. Project(项目)** — 逻辑分组,一个项目下可挂多套环境
- `name`、`description`

**3. Blueprint(环境蓝图)** — "一套环境要哪些资源"的定义,归属某 Project
- 绑定 `cloud_account_id` + `region`
- 资源参数(**结构化表单**,存 JSON):
  - EC2:规格、数量、AMI、磁盘
  - RDS:引擎(MySQL)、版本、规格、存储、账号密码
  - Redis:规格、版本、节点数
  - 安全组:入站规则(端口/来源)
  - 弹性 IP:是否分配、绑定目标

**4. Environment(环境实例)** — 蓝图的一次实际部署 = **一个 Pulumi stack**
- `pulumi_stack_name`(如 `project-a/prod`)、`cloud_account_id`、`region`
- `blueprint_snapshot`(部署当时的参数快照,保证可追溯)
- `status`:`pending → provisioning → up / failed / destroyed`
- **`outputs`**:建成后的资源信息(EC2 公网 IP、RDS endpoint、Redis endpoint……)—— **Phase 1 → Phase 2 的桥梁**

**5. Job(执行任务)** — 一次 `up/preview/destroy/refresh` 操作,即执行历史/审计
- `action`、`status`(`queued → running → succeeded/failed`)、`logs`、起止时间、`summary`

**关系**

```
CloudAccount ─┐
              ├─► Environment(=Pulumi stack) ─► Job(执行历史)
Project ─► Blueprint ─┘
```

**关键点**
- Environment 存**蓝图快照**:蓝图后续修改不影响已部署环境的记录准确性。
- 凭证**只在 CloudAccount 加密存一份**,Environment 引用它,不重复存。
- Blueprint 采用**结构化表单**(平台预设支持 EC2/RDS/Redis/SG/EIP),**不做**自由 DSL 或网页写 Pulumi 代码。

## 6. 执行流程 / 数据流

### 6.1 核心机制:Pulumi inline program

Automation API 的 inline program 不需要磁盘上的 Pulumi 项目文件,直接传一个 Go 函数,按 Blueprint 快照动态声明资源。表单参数在此变成真实资源:

```go
program := func(ctx *pulumi.Context) error {
    sg := ec2.NewSecurityGroup(ctx, ...)        // 按 blueprint.SG 入站规则
    for i := 0; i < bp.EC2.Count; i++ {
        ec2.NewInstance(ctx, ...)               // 按 blueprint.EC2 规格/数量
    }
    db := rds.NewInstance(ctx, ...)             // 按 blueprint.RDS
    rc := elasticache.NewCluster(ctx, ...)      // 按 blueprint.Redis
    ctx.Export("rds_endpoint", db.Endpoint)     // ← 进 Environment.outputs
    ctx.Export("redis_endpoint", rc.Endpoint)
    return nil
}
stack, _ := auto.UpsertStackInlineSource(ctx, stackName, projectName, program)
```

### 6.2 完整链路(含 preview 预演)

```
[网页] 填 Blueprint 参数 + 选云账号 → 点"创建环境"
   │
   ▼
[API] 建 Environment(pending) + 入队 Job(queued)
   │
   ▼
[Worker goroutine] 取 Job → running
   │  1. 解密该 CloudAccount 凭证(per-job 注入)
   │  2. 组 inline program(按 blueprint 快照)
   │  3. 选/建 Pulumi stack(S3 backend + secrets 加密)
   │  4. stack.Preview() ──► 网页展示"将创建 N 个资源" ──► 用户确认
   │  5. stack.Up(ProgressStreams) ──stream──► [SSE] ──► 网页实时日志
   │
   ├─ 成功 → 抓 outputs(IP/endpoint)存进 Environment → status=up
   └─ 失败 → status=failed,日志留错(资源可能部分创建 → 见 §7)
```

### 6.3 三个要点

1. **凭证 per-job 隔离**(多账号正确性关键):不同 stack 用不同 AWS 账号,凭证**不能用全局环境变量**。每个 Job 执行时,把解密后的凭证通过 Automation API 的 workspace `EnvVars`(`AWS_ACCESS_KEY_ID` 等)注入,只对该次执行生效。
2. **异步 Job + 实时日志**:后端 in-process worker pool(几个 goroutine)从 Job 队列取活;Pulumi 输出经 `ProgressStreams` 写入 per-job 广播 channel;网页开 `GET /jobs/{id}/logs/stream`(SSE)实时展示。**自用规模不需要 Redis/MQ**。
3. **preview 先行**:点"创建"后先跑 `Preview`(= `terraform plan`),展示将创建/变更内容,用户确认再 `Up`;`Destroy` 前强制走此步。防误操作。

## 7. 错误处理与回滚

### 7.1 依托 Pulumi state,不自造回滚

apply 中途失败时,已建资源已记入 state。Pulumi 不自动回滚,而是保留并报告失败点。Hermes 只提供两个操作:

1. **修复后重试**:改掉出错参数再 `Up`,Pulumi 跳过已建的、只补失败的(幂等)。最常用。
2. **销毁**:放弃则 `Destroy`,按 state 删干净整套环境。

因此**无需**自己实现补偿/回滚逻辑。

### 7.2 状态机

```
pending → provisioning → up
              ↘ failed ⇄ 重试(→provisioning) / 销毁(→destroying→destroyed)
up → updating   → up / failed
up → destroying → destroyed / failed
```

### 7.3 健壮性

- **部分失败可见性**:把 Pulumi 报告的"成功/失败资源"存进 `Job.summary`,网页明确展示"N 个成功、X 个失败及原因",提供「重试 / 销毁」按钮。
- **并发锁**:同一 Environment(stack)同时只允许一个 Job 运行(Pulumi stack 本身有锁,Hermes 层面也挡住重复提交)。

## 8. 安全

### 8.1 三层加密 + 一个主密钥

| 敏感数据 | 处理 |
|---|---|
| 云账号凭证(AWS key) | 落库前 AES-256-GCM 加密;主密钥从**环境变量注入**(不进代码/库/git) |
| Pulumi state 里的密码 | 开 Pulumi secrets 加密(passphrase 走主密钥体系),S3 中 state 即使泄露也是密文 |
| 资源密码(RDS master 等) | 存库加密 + 传 Pulumi 时作 secret;**推荐平台自动生成**强密码,建成后在 outputs 展示 |

### 8.2 凭证方式(MVP 取舍)

- MVP 先支持 **AWS access key**(简单直接)。
- **IAM assume role**(Hermes 持基础凭证去 assume 各账号 role,不存各账号长期 key)作为后续增强。

### 8.3 访问控制

- MVP 需有**基础登录**(单用户/口令,不裸奔)。
- 建议 HTTPS / 内网部署。
- `Job` 表天然是**操作审计**(谁、何时、对哪个环境、做了什么)。

## 9. 测试策略

难点:核心逻辑涉及真实 AWS(慢 + 花钱),不能每次真建资源。分层:

| 层 | 方式 |
|---|---|
| 纯逻辑单测 | Blueprint→program 映射、凭证 AES 加解密、Job 状态机、蓝图快照 —— Go 表驱动测试 |
| 编排层 | 用 **fake Provisioner**(不真调 Pulumi)测 Job 调度/状态/日志广播 |
| Pulumi 层 | 用官方 `pulumi.WithMocks` —— 不碰真实 AWS 验证 program 声明的资源与 outputs |
| 集成测试 | 少量,`//go:build integration` 标记,打专用测试账号跑 up→verify→destroy,默认不跑 |
| API 层 | `httptest` 测表单提交、SSE 日志流 |

实现按 **TDD**(先写测试再实现)推进。

## 10. 目录结构(Go 惯例,体现分层)

```
hermes/
├─ cmd/hermes/main.go       # 入口
├─ internal/
│  ├─ config/               # 配置、主密钥加载
│  ├─ crypto/               # AES-GCM 加解密
│  ├─ store/                # SQLite:5 个实体的 models + queries
│  ├─ provisioner/          # Provisioner interface(可替换引擎)
│  │  ├─ provisioner.go     #   Preview/Up/Destroy/Refresh
│  │  └─ pulumi/            #   inline program 构建 + Automation API 封装
│  ├─ orchestrator/         # Job 队列、worker pool、状态机、日志广播
│  ├─ cloud/                # 云账号凭证管理 + per-job 注入
│  ├─ api/                  # HTTP handlers + 路由 + SSE
│  └─ web/                  # templ 模板 + htmx 资源
├─ migrations/              # SQLite schema
└─ go.mod
```

## 11. MVP 范围

**✅ 做**

- 多 AWS 账号管理(access key 加密存)
- Project + Blueprint 表单化配置(EC2/RDS/Redis/SG/EIP)
- 一键 provision:preview → up,异步 Job + SSE 实时日志
- Environment 管理:状态、outputs(IP/endpoint)、重试、销毁
- 执行历史(Job 列表 + 日志回看)
- 基础登录
- state 存单个 S3 bucket

**❌ 不做(留给后续阶段)**

- GCP(第二阶段)
- Phase 2:SSH 装 Docker/Nginx、部署应用(第二阶段)
- 多租户 / 复杂 RBAC / 审批流
- 自由 DSL / 网页写 Pulumi 代码
- assume role、KMS/Vault(增强项)
- 计费 / 成本分析 / 监控告警

## 12. 后续路线图

1. **Phase 1 增强**:GCP provider(复用 Provisioner interface + 编排层)、assume role、KMS/Vault 密钥托管、Postgres。
2. **Phase 2 · 配置与部署**:基于 Environment.outputs,SSH 到 EC2 装 Docker/Nginx、部署/更新 Docker 服务。执行引擎候选:Ansible(`ansible-runner`)或自研 SSH 执行器 —— 届时另开 spec 评审。
3. **平台化增强**:视需要再引入多用户/RBAC、成本与监控。
