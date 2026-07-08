# Hermes M2 设计文档:Provisioning 走通骨架(SG + EC2 端到端)

- **日期**:2026-07-07
- **状态**:已通过 brainstorming 评审,待编写实现计划
- **上游设计**:[2026-07-07-hermes-aws-provisioning-design.md](./2026-07-07-hermes-aws-provisioning-design.md)(整份 MVP)
- **前置里程碑**:M1(地基 + 云账号管理)已完成 —— 见 [../plans/2026-07-07-hermes-milestone-1-foundation.md](../plans/2026-07-07-hermes-milestone-1-foundation.md)

---

## 1. 背景与本里程碑定位

整份 MVP 设计已评审通过(见上游设计)。M1 交付了可运行地基:HTTP 服务器、config/主密钥(HKDF 派生 AES + session 双子密钥)、AES-256-GCM 加密、SQLite store、CloudAccount CRUD(secret 加密落库、按 AWS Account ID 去重)、STS 凭证验证、签名 cookie 登录、htmx Web UI。

**「M2 = 剩下全部 provisioning」对单个实现计划太大**(4 实体 + Provisioner 接口 + Pulumi 引擎 + S3 backend + Job 队列/worker/状态机 + SSE + preview→up + 全套 Web 页)。因此把剩余 provisioning 拆成子里程碑,本 spec 只覆盖 **M2**。

**切法决策:薄纵切 / 走通骨架。** 用最小资源集把整条集成链路端到端打通(点一下 → 真在 AWS 建出资源 → 网页看实时日志 → 销毁清掉)。最大技术风险恰在这条链路(Pulumi Automation API + 后端 + per-job 凭证注入 + SSE 流式),越早跑通越好。后续里程碑纯增量。

**里程碑路线(本 spec 只做 M2)**
- **M2(本 spec)**:SG + EC2 端到端走通骨架。
- **M3**:扩全资源(RDS/MySQL、ElastiCache/Redis、EIP、自建 VPC 可选)、S3 state 后端、outputs 富展示。
- **M4**:健壮性 —— 漂移检测(Refresh)、destroy 前 preview 门、部分失败精细可见性、并发/锁打磨。

---

## 2. 关键决策记录(M2)

| 决策 | 选择 | 理由 |
|---|---|---|
| M2 范围 | 薄纵切 / 走通骨架 | 端到端跑通链路,最早排掉集成风险;后续增量 |
| 最小资源集 | 安全组 + N×EC2,用账号**默认 VPC + 默认子网** | 不自建 VPC,砍掉网络细节;聚焦链路 |
| AMI | 留空则按 region 解析最新 Amazon Linux 2023(SSM 公共参数) | 免手填 AMI;允许覆盖 |
| Pulumi state 后端 | **本地 `file://` 起步,后端 URL 可配置** | 骨架最快跑起来,省掉 S3 桶/凭证/region 配置;后端被 Provisioner 接口挡住,M3 切 S3 几乎无返工 |
| preview→确认→up 建模 | **两个独立 Job**(preview job → 用户确认 → up job) | 每个 Job 原子、状态/日志清晰;worker 不被占住等待;job 历史即审计 |
| Job 队列 | 内存 buffered channel + worker pool | 自用规模够,设计明确不上 Redis/MQ |
| 实时日志 | SSE(日志窗)+ htmx 轮询(状态/按钮片段) | 控制流稳、日志流畅;不引额外前端依赖 |
| Pulumi passphrase | `crypto.DeriveKey(master, "hermes:pulumi-passphrase:v1")` | 复用主密钥体系,不加新环境变量 |
| M2 完成标准 | 全构建 + mock/单测 + **真跑一次** | 走通骨架的价值在于真的走一遍 |
| Project/Blueprint 编辑 | M2 只做建/删,不做改(改 = 删了重建) | 收敛范围;编辑器留后续 |

**运行时外部依赖(重要):** Pulumi Go Automation API 需要 `pulumi` CLI 在 PATH,且需安装 AWS provider 插件。这**稍微打破了上游设计「单二进制无外部依赖」的理想** —— Pulumi 是 Hermes 的运行时依赖。M2 通过 README + `make setup-pulumi`(`pulumi plugin install resource aws`)显式记录并提供安装入口。

---

## 3. M2 范围

**✅ 做**
- 4 个新实体(Project / Blueprint / Environment / Job)+ migration `0003` + CRUD
- `Provisioner` 接口 + `PulumiProvisioner`(仅 SG + EC2 的 inline program)
- 本地 `file://` state 后端 + Pulumi secrets passphrase(HKDF 派生)
- per-job 凭证注入(解密账号凭证 → workspace EnvVars,绝不进全局环境)
- 编排层:内存 Job 队列 + worker pool + 状态机 + 同环境并发锁 + 崩溃恢复
- SSE 实时日志(持久化 + 重连回放 + done 事件)
- Web:Projects/Blueprints/Environments/Jobs 页面 + preview→确认→up→destroy 流程

**❌ 不做(留后续里程碑)**
- RDS / Redis / EIP / 自建 VPC(→ M3)
- S3 state 后端(→ M3)
- 漂移检测 Refresh、destroy 前的 preview 门、部分失败精细化(→ M4)
- Project/Blueprint 的编辑(M2 只建/删)
- 多租户 / RBAC / 审批流 / 成本监控(整个 MVP 之外)

---

## 4. 数据模型(migration `0003_provisioning.sql`,4 张表)

```
projects(id, name, description, created_at)
   └─< blueprints(id, project_id→, name, cloud_account_id→, params_json, created_at)
          └─(部署一次)─< environments(
                 id, blueprint_id→, cloud_account_id→, name,
                 pulumi_stack,                -- stack 名,如 "proj-a-prod"
                 region,
                 blueprint_snapshot_json,     -- 部署当时的 params 快照(可追溯)
                 status,                       -- 见 §5 状态机
                 outputs_json,                 -- 建成后的 IP/endpoint
                 created_at, updated_at)
                    └─< jobs(
                           id, environment_id→, action,   -- preview|up|destroy
                           status,                          -- queued|running|succeeded|failed
                           logs, summary_json, error,
                           started_at, finished_at, created_at)
```

**关键点**
- **凭证不重复存**:Environment 引用 `cloud_account_id`,执行时才解密(承接 M1 加密存储)。
- **蓝图快照**:Environment 存部署当时的 params 快照,蓝图之后删改不影响已部署环境记录的准确性。
- **外键 `ON DELETE`**:blueprint 若被 environment 引用则拦住删除;environment 需先 `destroyed` 才能删。不做资源级联删除。
- store 层每个实体一个文件(`project.go` / `blueprint.go` / `environment.go` / `job.go`),models + CRUD,复用 M1 的 `Store` 与 `crypto.Cipher`。

### 4.1 最小蓝图形状(`params_json` 结构)

```jsonc
{
  "region": "ap-southeast-1",
  "security_group": {
    "ingress": [
      { "port": 22, "protocol": "tcp", "cidr": "0.0.0.0/0", "desc": "SSH" }
    ]
  },
  "ec2": {
    "instance_type": "t3.micro",
    "count": 1,
    "ami": "",             // 留空 = 按 region 自动解析最新 AL2023(SSM 公共参数)
    "root_volume_gb": 8,
    "key_name": ""         // 可选:已有 EC2 key pair 名,便于后续 SSH
  }
}
```

对应 Go 类型放在 `internal/provisioner`(编排层与引擎共用),带 `Validate()`:
- `region` 必填;`ec2.instance_type` 必填;`ec2.count` ∈ [1, 10](自用上限,防误填);`root_volume_gb` ≥ 8。
- ingress 每条:`port` ∈ [1,65535],`protocol` ∈ {tcp,udp},`cidr` 合法 CIDR。
- `ami` 空 = 自动解析;非空则按用户填的 AMI ID。

---

## 5. 状态机

```
Environment:
  pending → previewing → preview_ready → provisioning → up
                                              ↘ failed ⇄ 重试(→provisioning)
  up|failed → destroying → destroyed | failed

Job(每次操作一行):
  queued → running → succeeded | failed
```

- Job action ∈ {`preview`, `up`, `destroy`}(`refresh`/漂移 → M4)。
- 环境瞬时态(`previewing`/`provisioning`/`destroying`)只在 worker 执行期间存在;崩溃恢复见 §7.4。

---

## 6. 执行核心

### 6.1 Provisioner 接口(M2 版)

`internal/provisioner/provisioner.go` —— 编排层与引擎的解耦点。M2 砍掉 `Refresh`:

```go
type Provisioner interface {
    Preview(ctx context.Context, spec Spec, logs io.Writer) (PreviewResult, error)
    Up(ctx context.Context, spec Spec, logs io.Writer) (UpResult, error)
    Destroy(ctx context.Context, spec Spec, logs io.Writer) error
}

type Spec struct {
    StackName string          // 如 "proj-a-prod"
    Region    string
    Params    BlueprintParams // 解码后的 SG+EC2 参数(蓝图快照)
    Creds     AWSCreds        // 解密后的 access key / secret(仅内存)
}

type AWSCreds struct{ AccessKeyID, SecretAccessKey string }

type PreviewResult struct{ Creates, Updates, Deletes, Sames int; Summary string }
type UpResult      struct{ Outputs map[string]any; Summary string } // public_ips 等
```

- `logs io.Writer` 接到 SSE broker,流式输出。
- **编排层只依赖此接口**;测试用 `fakeProvisioner`,不碰 Pulumi。
- backend URL / passphrase / pulumi project 是进程级配置,构造 `PulumiProvisioner` 时注入,不进 `Spec`(保持接口干净)。

### 6.2 PulumiProvisioner(仅 SG + EC2)

`internal/provisioner/pulumi/`。核心 = inline program + Automation API + **per-job 凭证注入**:

```go
type PulumiProvisioner struct {
    project    string // pulumi project 名,如 "hermes"
    backendURL string // file://...(M2)
    passphrase string // HKDF 派生子密钥
}

func (p *PulumiProvisioner) stack(ctx context.Context, spec Spec) (auto.Stack, error) {
    program := buildProgram(spec.Params) // 见 program.go
    return auto.UpsertStackInlineSource(ctx, spec.StackName, p.project, program,
        auto.EnvVars(map[string]string{                     // ← per-job 隔离的关键
            "AWS_ACCESS_KEY_ID":        spec.Creds.AccessKeyID,
            "AWS_SECRET_ACCESS_KEY":    spec.Creds.SecretAccessKey,
            "AWS_REGION":               spec.Region,
            "PULUMI_CONFIG_PASSPHRASE": p.passphrase,
            "PULUMI_BACKEND_URL":       p.backendURL, // file://...(M2);后端切换只改此值
        }),
    )
}
// 注:后端经 PULUMI_BACKEND_URL 传入(与凭证/passphrase 一致的注入方式),
// 而非某个 auto.Backend() option —— 实现时以 Automation API 实际存在的
// LocalWorkspaceOption 为准(EnvVars / SecretsProvider / Project 等)。
```

inline program(`program.go`)按蓝图快照声明资源:
1. **AMI**:`params.EC2.AMI` 为空 → `ssm.LookupParameter` 取 `/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64`(region 来自 provider 的 `AWS_REGION`)。
2. **默认 VPC + 子网**:`ec2.LookupVpc{Default: true}` → `ec2.GetSubnets`(按 vpc-id 过滤,取一个)。
3. **安全组**:`ec2.NewSecurityGroup`,按 `params.SecurityGroup.Ingress` 动态建入站规则。
4. **实例**:`for i < params.EC2.Count { ec2.NewInstance(...) }`,挂 SG / 子网 / `key_name`(如填)/ root 磁盘。
5. **导出**:`ctx.Export("public_ips", ...)` / `"instance_ids"` / `"public_dns"` → 进 `Environment.outputs`。

- `Up`/`Preview`/`Destroy` 分别用 `optup.ProgressStreams(logs)` / `optpreview.ProgressStreams(logs)` / `optdestroy.ProgressStreams(logs)` 流式。
- **凭证只对该次执行的 workspace 生效**,绝不写全局环境变量 —— 多账号正确性的关键。
- 新依赖:`github.com/pulumi/pulumi/sdk/v3`(Automation API + `pulumi.WithMocks`)、`github.com/pulumi/pulumi-aws/sdk/v6`。

### 6.3 编排层(`internal/orchestrator/`)

- **队列**(`queue.go`):内存 buffered channel,承载 jobID。
- **worker pool**(`worker.go`):N 个 goroutine(`HERMES_WORKERS`,默认 2),`for job := range queue { run(job) }`。
- **同环境并发锁**:入队时校验该 Environment 无 `queued`/`running` job(挡重复提交);运行时再加内存锁兜底(Pulumi stack 自身也有锁)。
- **`run(jobID)` 流程**:
  1. 加载 job + environment + blueprint 快照 → 解密 CloudAccount 凭证。
  2. 置 job `running` + `started_at`;置环境瞬时态(previewing/provisioning/destroying)。
  3. 取 broker 的 logs writer(§6.4)。
  4. 调 `Preview` / `Up` / `Destroy`。
  5. **成功**:preview → 存 plan 到 `job.summary`,环境 `preview_ready`;up → 抓 outputs 存 `environment.outputs`,环境 `up`;destroy → 环境 `destroyed`。job `succeeded` + `finished_at`。
  6. **失败**:job `failed` + 存 `error`;up/destroy 失败 → 环境 `failed`;preview 失败 → 环境 `failed`(可重新 deploy)。
  7. 关闭 broker topic(通知 SSE EOF)。
- **崩溃恢复**(启动时):把上次遗留的 `queued`/`running` job 一律标 `failed`(「被重启中断」),对应环境瞬时态回落 `failed`,由用户重试。Pulumi op 中途不可安全续跑,标失败最稳。

### 6.4 SSE 实时日志(broker,`internal/orchestrator/broker.go`)

- **Broker**:`map[jobID]*topic`;topic 持有 `buffer`(已产出日志)+ `subscribers`(chan 集合)+ `closed`,互斥保护。
- **Writer**:`broker.Writer(jobID)` 返回 `io.Writer`,`Write` 时**追加 buffer + 扇出给订阅者**;job 结束把完整日志落库 `jobs.logs`(刷新页面仍可回看)。
- **SSE handler** `GET /jobs/{id}/logs/stream`:
  - 头 `Content-Type: text/event-stream`、`Cache-Control: no-cache`;
  - **用 `http.NewResponseController(w).SetWriteDeadline(time.Time{})` 清掉 15s 写超时**(M1 的 `http.Server{WriteTimeout:15s}` 会掐断长连接);
  - 先**回放 buffer 历史**,再流式推新行,每行后 `Flush()`;
  - job 结束发 `event: done`,前端据此重载状态片段(显示 outputs / 下一步按钮)。

---

## 7. 错误处理与健壮性

- **依托 Pulumi state,不自造回滚**:apply 中途失败,已建资源记入 state;Hermes 提供「修复后重试(`up` 幂等补建)」或「销毁(按 state 删净)」两个操作(承接上游设计 §7)。
- **preview 门**:动手前先 `preview`,展示将创建内容,用户确认再 `up`。
- **同环境并发锁**:见 §6.3。
- **崩溃恢复**:见 §6.3。
- **优雅关闭**:收到 SIGINT/SIGTERM → 停止队列入队 → 有界等待在途 job(超时后 cancel worker context,Pulumi op 随之中止,受影响 job 由下次启动的崩溃恢复标失败)。

---

## 8. 安全

- **凭证 per-job 隔离**:解密后经 Automation API workspace `EnvVars` 注入,只对该次执行生效(§6.2)。
- **Pulumi state secrets 加密**:`PULUMI_CONFIG_PASSPHRASE` 由主密钥 HKDF 派生(`"hermes:pulumi-passphrase:v1"`),state 内敏感值即使泄露也是密文。
- **落库加密不变**:CloudAccount secret 仍 AES-256-GCM 加密存(M1)。
- 访问控制沿用 M1 登录;`jobs` 表天然是操作审计。

---

## 9. 配置新增(`internal/config`)

```go
type Config struct {
    // M1 原有:Addr, DBPath, MasterKey, LoginPassword
    PulumiBackend string // HERMES_PULUMI_BACKEND,默认 file://<cwd>/data/pulumi-state;M3 设 s3://bucket
    PulumiProject string // HERMES_PULUMI_PROJECT,默认 "hermes"
    Workers       int    // HERMES_WORKERS,默认 2
}
```

- **passphrase 不加新环境变量**:`crypto.DeriveKey(master, "hermes:pulumi-passphrase:v1")`(第三个域分离子密钥)。
- `.env.example` 加上述变量注释;Makefile 加 `make setup-pulumi`(装 AWS provider 插件)。
- `cmd/hermes/main.go`:派生 passphrase → 构造 `PulumiProvisioner` + `Broker` → 启动 orchestrator worker 池 → 进 `Deps`;优雅关闭时停止入队、有界等待在途、再 cancel。

---

## 10. 页面与路由

顶部导航加:账号 / 项目 / 蓝图 / 环境(承接现有 `/accounts`)。

| 路由 | 作用 |
|---|---|
| `GET /projects` · `POST /projects` · `DELETE /projects/{id}` | 项目最小 CRUD(建/删) |
| `GET /blueprints` · `POST /blueprints` · `DELETE /blueprints/{id}` | 蓝图列表 + 建/删;表单:名称、项目、云账号、region、SG 入站规则(可增行)、EC2(规格/数量/AMI选填/磁盘/keyName选填) |
| `POST /blueprints/{id}/deploy` | 填环境名 → 建 Environment(pending)+ 入队 `preview` job → 跳环境详情 |
| `GET /environments` · `GET /environments/{id}` | 环境列表 / 详情(蓝图快照、状态、outputs、job 历史、实时日志窗) |
| `GET /environments/{id}/status` | **htmx 轮询片段**(每 2s):按状态出按钮 |
| `POST /environments/{id}/up` | 确认 → 入队 `up` job |
| `POST /environments/{id}/destroy` | 入队 `destroy` job |
| `POST /environments/{id}/retry` | `failed` 后重跑 `up` job |
| `GET /jobs/{id}/logs/stream` | SSE 日志流(清写超时 + 回放 + `done`) |

**详情页状态 → 按钮**
- `previewing` / `provisioning` / `destroying` → 只看实时日志;
- `preview_ready` → 「将建 N 个资源」+ [确认创建] [销毁];
- `up` → 展示公网 IP 等 outputs + [销毁];
- `failed` → 错误 + [重试] [销毁];
- `destroyed` → 终态提示。

**前端分工**:日志窗用 ~10 行原生 `EventSource`(不引额外 JS 依赖);状态/按钮片段用 htmx `hx-get` 轮询。控制流走 htmx、日志走 SSE,各司其职。

---

## 11. 测试策略

难点:核心逻辑涉及真实 AWS(慢 + 花钱),不能每次真建。分层:

| 层 | 方式 |
|---|---|
| 纯逻辑单测 | BlueprintParams JSON 编解码 + `Validate()`、蓝图快照、Job/环境状态机、broker(写→buffer→扇出→回放→关闭) —— 表驱动 |
| 编排层 | `fakeProvisioner` 测 job 调度 / 状态流转 / 同环境并发锁 / 崩溃恢复,不碰 Pulumi |
| Pulumi 程序 | `pulumi.WithMocks` 断言 program 声明 1 个 SG + N 台 EC2 且 exports 齐全(mock 掉 SSM/VPC/子网查询),不碰真实 AWS |
| API 层 | `httptest` 测蓝图建、deploy 建环境+job、状态片段、SSE 回放 + done |
| 集成(真跑一次) | `//go:build integration`:装 pulumi + 测试账号,up→断言 outputs 有 `public_ips`→destroy 清干净。即 M2 验收的「真跑」 |

全程 **TDD**(先写测试再实现)。

---

## 12. 完成标准(M2 Done)

- `go build ./...` 通过,`go test ./...`(mock/单测)全绿。
- 端到端:登录 → 连账号 → 建项目 → 建 SG+EC2 蓝图 → 部署 → 看到 preview 计划 → 确认 → 实时日志 → 环境到 `up` 且展示公网 IP → 销毁 → `destroyed`。
- **真跑一次**:装好 `pulumi` CLI,对测试 AWS 账号跑通 up→日志→destroy,资源清干净。
- per-job 凭证不泄漏到全局环境(有断言);CloudAccount secret 落库仍密文;Pulumi state secrets 经 passphrase 加密。

---

## 13. 目录结构增量(Go 惯例)

```
internal/
├─ provisioner/
│  ├─ provisioner.go     # Provisioner 接口 + Spec/Result + BlueprintParams(+Validate)
│  └─ pulumi/
│     ├─ pulumi.go       # workspace / 凭证 / backend / passphrase 封装
│     ├─ program.go      # inline program:SG+EC2 + AMI/VPC 查询 + exports
│     └─ program_test.go # pulumi.WithMocks
├─ orchestrator/
│  ├─ queue.go           # channel 队列
│  ├─ worker.go          # worker 池 + run(job) + 状态流转 + 崩溃恢复
│  ├─ broker.go          # SSE 日志 broker(buffer / 扇出 / 回放 / 关闭)
│  └─ *_test.go          # fakeProvisioner 驱动
├─ store/
│  ├─ project.go · blueprint.go · environment.go · job.go   # models + CRUD
│  └─ migrations/0003_provisioning.sql
├─ web/templates/        # projects / blueprints / environments / environment_detail + 片段 + nav
└─ api/                  # projects.go · blueprints.go · environments.go · jobs.go(handlers)
```

`cmd/hermes/main.go` 装配增量见 §9。

---

## 14. 交接到实现

本 spec 通过用户复审后,用 superpowers:writing-plans 生成任务化实现计划(TDD、conventional commits),按 M1 的方式逐 Task 推进。
