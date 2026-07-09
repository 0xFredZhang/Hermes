# Hermes

Hermes 是一个轻量的自托管 AWS 资源编排控制台。当前 MVP 支持加密保存 AWS
账号、创建项目与蓝图、预览 Pulumi plan、创建带安全组和弹性 IP 的 EC2
实例、实时查看 Job 日志，并在完成后销毁环境。

## 运行要求

- Go 1.25+
- SQLite（通过纯 Go 的 `modernc.org/sqlite` 驱动使用）
- `PATH` 中可用的 Pulumi CLI
- Pulumi AWS provider plugin
- 用于真实 provisioning 验证的 AWS 测试账号

安装 Pulumi AWS provider plugin：

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
- `HERMES_DB_PATH`：SQLite 数据库路径，默认 `hermes.db`。
- `HERMES_PULUMI_BACKEND`：Pulumi state backend，默认
  `file://<repo>/data/pulumi-state`.
- `HERMES_PULUMI_PROJECT`：Pulumi project 名称，默认 `hermes`。
- `HERMES_WORKERS`：provisioning worker 数量，默认 `2`。

## 开发

运行本地检查：

```bash
make test
make vet
make build
```

启动服务：

```bash
make run
```

然后打开 `http://localhost:8080` 访问控制台。

## AWS 编排流程

1. 登录。
2. 添加 AWS 账号。Hermes 会通过 STS 校验凭证，并加密保存 secret key。
3. 创建项目。
4. 创建蓝图。Region、实例规格、AMI 选项会在可用时从 AWS 元数据自动填充。
5. 基于蓝图部署环境。
6. 查看 preview job。
7. 确认执行 provisioning。
8. 查看实时日志，直到环境进入 `up` 状态。
9. 使用完毕后销毁环境。

当前蓝图会创建：

- 蓝图表单中配置的安全组入站规则。
- 账号默认 VPC / 子网中的一台或多台 EC2 实例。
- 每台实例一个弹性 IP。
- 包含 instance ID、公网 IP、公网 DNS 的 outputs。

## 真实 AWS 验证

集成测试需要真实 AWS 凭证，并会创建可能产生费用的 AWS 资源。建议使用一次性测试账号或权限收敛的测试凭证。

```bash
AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... AWS_REGION=ap-southeast-1 make test-integration
```

M2 已用真实 AWS 手动验证：EC2、安全组、弹性 IP 可以成功创建。确认 destroy
后 EC2、安全组、弹性 IP 都已清理时，可以把资源清理记录补到里程碑计划里。
