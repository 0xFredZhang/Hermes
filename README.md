# Hermes

Hermes 是一个轻量的自托管 AWS 资源编排控制台。当前 MVP 支持加密保存 AWS
账号、创建项目与蓝图、预览 Pulumi plan、创建带安全组和弹性 IP 的 EC2
实例、实时查看 Job 日志，并在完成后销毁环境。

## 运行要求

- Go 1.25+
- Node.js / npm（仅用于重建 Tailwind 控制台样式）
- SQLite（通过纯 Go 的 `modernc.org/sqlite` 驱动使用）
- `PATH` 中可用的 Pulumi CLI
- Pulumi AWS / Random provider plugins
- 用于真实 provisioning 验证的 AWS 测试账号

安装 Pulumi provider plugins：

```bash
make setup-pulumi
```

## 配置

创建本地 `.env` 文件：

```bash
make env
```

然后编辑 `.env`：

- `HERMES_MASTER_KEY`：base64 编码的 32 字节主密钥，`make env` 会自动填充。
- `HERMES_LOGIN_PASSWORD`：本地控制台登录密码。
- `HERMES_ADDR`：监听地址，默认 `127.0.0.1:8080`，只接受本机连接。
- `HERMES_DB_PATH`：SQLite 数据库路径，默认 `hermes.db`。
- `HERMES_PULUMI_BACKEND`：Pulumi state backend，默认
  `file://<repo>/data/pulumi-state`（实际值是 hostless absolute URI，例如
  `file:///absolute/path/to/repo/data/pulumi-state`）。自定义本地 backend 也必须使用
  `file:///absolute/path`；如需共享 state，可先创建 S3 bucket，
  再设置为 `s3://<bucket>/<optional-prefix>`。Hermes 启动时会校验该值，
  当前只接受 `file:///absolute/path` 或 `s3://<bucket>[/prefix]`。
- `HERMES_PULUMI_PROJECT`：Pulumi project 名称，默认 `hermes`。
- `HERMES_WORKERS`：provisioning worker 数量，默认 `2`。

## 开发

首次安装前端样式构建依赖：

```bash
npm install
```

控制台样式使用 Tailwind CSS 编译。修改 `internal/web/assets/app.css` 或模板 class
后，可以手动重建：

```bash
make css
```

开发样式时也可以开启 watch：

```bash
npm run css:watch
```

运行本地检查：

```bash
make check
```

`make check` 会重建并校验提交的 CSS、运行 JavaScript 测试、检查 gofmt、运行不带
integration build tag 的 Go 单元测试、`go vet` 和 `go build`。它不会调用 AWS，也不会
执行真实 provisioning 集成测试。

启动服务：

```bash
make run
```

然后打开 `http://127.0.0.1:8080` 访问控制台。

## 本地诊断与重置

诊断本地 Pulumi CLI 和 AWS/Random resource plugins：

```bash
make doctor
```

`doctor` 只执行本机的 `pulumi plugin ls --json`，不需要
`HERMES_MASTER_KEY`/`HERMES_LOGIN_PASSWORD`，也不会调用 AWS。

重置 SQLite 前必须先停止 Hermes，避免仍在运行的进程继续写数据库或重新创建 sidecar：

```bash
make reset-local CONFIRM=reset
```

该命令只删除仓库内配置的 SQLite 文件，以及名称完全匹配的 `-wal`、`-shm`、
`-journal` sidecar；它会保留备份、无关文件和 Pulumi state。仓库外路径、目录、SQLite
URI、内存数据库和 symlink escape 都会被拒绝。

删除本地 Pulumi state 是独立且风险更高的操作，同样必须先停止 Hermes：

```bash
make reset-local-state CONFIRM=reset-state
# 只有确认本地 stack 文件也应丢弃时：
make reset-local-state CONFIRM=reset-state FORCE=1
```

该命令只接受仓库内以 `data/pulumi-state` 结尾的 `file:///absolute/path`
backend；S3、仓库外路径、其他 source/backup 目录和 symlink escape 都会被拒绝。检测到
`.pulumi/stacks` 下任何层级的 stack
文件或备份时，必须额外提供 `FORCE=1`。

**删除 Pulumi state 永远不会删除 AWS 资源。** 如果 state 对应的云资源仍然存在，
它们可能继续计费并成为 orphaned resources，且之后可能很难由 Hermes/Pulumi 管理或
安全销毁。只有在确认对应 AWS 资源已被正确销毁或准备承担手工清理责任时，才执行该命令。

## AWS 编排流程

1. 登录。
2. 添加 AWS 账号。Hermes 会通过 STS 校验凭证，并加密保存 secret key。
3. 创建项目。
4. 创建蓝图。Region、实例规格、AMI 选项会在可用时从 AWS 元数据自动填充。
5. 基于蓝图部署环境。
6. 查看 preview job。
7. 确认执行 provisioning。
8. 查看实时日志，直到环境进入 `up` 状态。
9. 可按需执行漂移检测，让 Pulumi refresh 采纳云端真实状态。
10. 使用完毕后先执行销毁预演，确认待删除资源后再销毁环境。

当前蓝图会创建：

- 蓝图表单中配置的安全组入站规则。
- 账号默认 VPC / 子网中的一台或多台 EC2 实例；也可以选择由 Hermes 创建一个
  开发用 VPC、Internet Gateway、两个公网子网、路由表和路由表关联。
- 每台实例一个弹性 IP。
- 可选的 RDS MySQL 实例（私网访问，只允许 EC2 安全组访问）。
- 可选的 ElastiCache Redis replication group（私网访问，只允许 EC2 安全组访问）。
- 包含 instance ID、公网 IP、公网 DNS、可选 VPC/subnet ID、RDS endpoint、Redis
  endpoint 的 outputs。

RDS/Redis 默认关闭。启用后使用低成本开发默认值：MySQL 8.0、
`db.t3.micro`、20GB 存储；Redis 7.2、`cache.t3.micro`、1 节点。RDS
master 密码由 Hermes 使用强随机数生成，和用户名一起加密保存在本地 SQLite
`environment_secrets` 表中；Pulumi 创建 RDS 时只拿到运行时 secret 输入。密码不会写入
Hermes 环境 outputs，也不会出现在常规状态轮询里；环境进入 `up` 后，可以在环境详情页按需
点击“显示凭据”查看。

Redis 默认仍不启用 auth token，访问边界依赖 VPC 与安全组。如果在蓝图中勾选 Redis
AUTH，Hermes 会生成一个 ElastiCache auth token，并和默认用户名 `default` 一起加密保存到
本地 SQLite；Pulumi 创建 Redis 时会把 token 作为 secret 输入，同时启用 in-transit
encryption。Redis token 不会写入 Hermes outputs，也不会出现在常规状态轮询里；环境进入
`up` 后，可以在环境详情页按需点击“显示凭据”查看。

Hermes 暂不接入 AWS Secrets Manager：本轮只解决开发/自用场景下的 MySQL/Redis 凭据保存与查看。
如果后续需要自动轮换、跨服务读取、审计或托管密钥策略，再引入 Secrets Manager。
如果使用的是 M3a 期间已创建的 RDS 环境，旧密码仍只存在于当时的 Pulumi state；
建议先销毁重建，或后续单独做一次凭据导入/重置迁移。

Hermes-managed VPC 默认关闭。启用后默认使用 `10.0.0.0/16`，创建
`10.0.1.0/24` 与 `10.0.2.0/24` 两个公网子网。M3 仍面向开发/自用，不包含 NAT
Gateway、私有子网或生产级高可用拓扑。

环境进入 `up` 后，Hermes 会要求先运行 destroy preview，再显示“确认销毁”按钮。
如果预演后决定保留资源，可以取消销毁预演并回到 `up` 状态。
环境详情页也提供“检测漂移”动作；它会调用 Pulumi refresh，对比 stack state 与云端
实际资源，并把 refresh 后的资源变更摘要展示在页面上。

## AWS 权限提示

除 EC2 实例、安全组、EIP 与只读目录查询权限外，启用自建 VPC 或可选资源还需要
对应账号具备相关创建/删除权限，例如：

- `ec2:CreateVpc`、`ec2:DeleteVpc`、`ec2:CreateSubnet`、`ec2:DeleteSubnet`、
  `ec2:CreateInternetGateway`、`ec2:AttachInternetGateway`、
  `ec2:DetachInternetGateway`、`ec2:DeleteInternetGateway`、
  `ec2:CreateRouteTable`、`ec2:DeleteRouteTable`、`ec2:AssociateRouteTable`、
  `ec2:DisassociateRouteTable`、`ec2:DescribeAvailabilityZones`。
- `rds:CreateDBInstance`、`rds:DeleteDBInstance`、`rds:CreateDBSubnetGroup`、
  `rds:DeleteDBSubnetGroup`、`rds:DescribeDBInstances`。
- `elasticache:CreateReplicationGroup`、`elasticache:DeleteReplicationGroup`、
  `elasticache:CreateCacheSubnetGroup`、`elasticache:DeleteCacheSubnetGroup`、
  `elasticache:DescribeReplicationGroups`。

销毁预演会调用 Pulumi 的 destroy preview，同样需要读取当前 stack state 和描述目标
资源的权限。
漂移检测会调用 Pulumi refresh，需要读取当前 stack state、描述云端资源，并把刷新后
的 state 写回当前 Pulumi backend。

如果 `HERMES_PULUMI_BACKEND` 使用 `s3://...`，运行 Hermes 的身份还需要访问该
bucket 的权限，例如 `s3:GetObject`、`s3:PutObject`、`s3:DeleteObject`、
`s3:ListBucket`。Hermes 不会自动创建 state bucket。

## 真实 AWS 验证

集成测试需要真实 AWS 凭证，并会创建可能产生费用的 AWS 资源。建议使用一次性测试账号或权限收敛的测试凭证。

```bash
AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... AWS_REGION=ap-southeast-1 make test-integration
```

默认集成测试只创建 EC2、安全组与弹性 IP。要同时验证自建 VPC、可选数据库或缓存
资源，可显式打开：

```bash
HERMES_IT_NETWORK=1 HERMES_IT_RDS=1 HERMES_IT_REDIS=1 HERMES_IT_REDIS_AUTH=1 \
AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... AWS_REGION=ap-southeast-1 \
make test-integration
```

Hermes-managed VPC 本身不产生 NAT Gateway 费用；EC2/EIP、RDS 与 ElastiCache
仍可能产生费用。RDS 与 ElastiCache 的创建和销毁耗时明显更长。`HERMES_IT_REDIS_AUTH=1`
会自动启用 Redis，并额外验证 Redis auth token 不会作为 output 导出。

M2 已用真实 AWS 手动验证：EC2、安全组、弹性 IP 可以成功创建。确认 destroy
后 EC2、安全组、弹性 IP 都已清理时，可以把资源清理记录补到里程碑计划里。
