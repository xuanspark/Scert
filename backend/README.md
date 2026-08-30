# Scert

Scert 是面向 Windows 和 Linux 的 SSL 证书管理服务。当前代码已覆盖核心证书生命周期、DNS-01、自动续期、SSH 部署、通知、认证和 Web 管理流程。

## 当前已实现

- Go 后端模块和 `scert` CLI 入口。
- JSON 配置生成、读取、校验和原子保存。
- Windows 与 Linux 默认目录模型。
- SQLite 和 MySQL 数据库连接抽象。
- SQLite/MySQL 首版迁移脚本。
- 域名基础模型与 Repository。
- 健康检查、状态和域名基础 API。
- 中文 CLI：`version`、`help`、`init`、`status`、`serve`、`domain list`、`domain add`。
- Vue 前端管理页面。

## 首轮运行

在 `backend` 目录执行：

```powershell
go run ./cmd/scert init --base-dir C:\ScertData
go run ./cmd/scert status --base-dir C:\ScertData
go run ./cmd/scert domain add --base-dir C:\ScertData --name example.com --type single
go run ./cmd/scert domain list --base-dir C:\ScertData
go run ./cmd/scert serve --base-dir C:\ScertData
```

服务启动后可访问：

- `GET /healthz`
- `GET /api/v1/status`
- `GET /api/v1/domains`
- `POST /api/v1/domains`
- `DELETE /api/v1/domains/:id`

当前 `serve` 已接入配置加载、数据库迁移、认证、DNS 在线测试、ACME 证书申请、自动续期、证书导入、SSH 部署和通知流程。HTTPS 生产切换、真实 DNS/ACME 申请、远程服务器及平台安装仍需授权环境验收。

### 开发阶段使用 9004 与 9003 端口

反向代理开发测试时，Scert 使用 `127.0.0.1:9004` 提供本机内部 HTTPS，Nginx 使用 `9003` 接收测试访问并转发到 Scert。配置中的 `https_mode` 设为 `proxy`。公网 HTTPS 仍由 Nginx 终止；端口可通过配置文件 `server.port` 或初始化参数 `--server-port` 修改，修改后需要重启服务。

SMTP 通知是 Scert 的出站连接，不需要为 Scert 开放 SMTP 入站端口。公网防火墙通常只需放行 Nginx 使用的 `443`，若配置 HTTP 跳转再放行 `80`；内部端口 `9004` 和 Nginx 测试端口 `9003` 都不应对公网开放。

### systemd 环境变量覆盖

服务启动时可以使用 `SCERT_*` 环境变量覆盖当前进程的配置文件值，适合在 systemd 的 `EnvironmentFile` 中配置端口和部署环境。覆盖只在本次进程生效，不会修改 `scert.json`，敏感凭据仍应使用加密凭据引用，不要直接写入日志。

常用变量如下：

```text
SCERT_SERVER_HOST=127.0.0.1
SCERT_SERVER_PORT=9004
SCERT_SERVER_HTTPS_MODE=proxy
SCERT_WEB_ENABLED=true
SCERT_WEB_HOSTNAME=web.example.com
SCERT_WEB_PUBLIC_IP=192.168.2.3
SCERT_DATABASE_DRIVER=sqlite
SCERT_DATABASE_DSN=/scert/data/scert.db
SCERT_ACME_ENVIRONMENT=staging
SCERT_ACME_ACCOUNT_EMAIL=admin@example.com
SCERT_ACME_RENEWAL_THRESHOLD_DAYS=6
SCERT_ACME_PROPAGATION_TIMEOUT_SECS=900
SCERT_ACME_PROPAGATION_POLLING_INTERVAL_SECS=10
SCERT_DNS_PROVIDER=cloudflare
SCERT_DNS_CREDENTIAL_REF=secret://dns/cloudflare
SCERT_NOTIFICATION_HTTP_CREDENTIAL_REF=secret://notification/http
SCERT_NOTIFICATION_SMTP_CREDENTIAL_REF=secret://notification/smtp
```

例如 Scert 内部服务使用 9004、Nginx 使用 9003 时，Scert 的环境文件可以写成：

```text
SCERT_SERVER_HOST=127.0.0.1
SCERT_SERVER_PORT=9004
SCERT_SERVER_HTTPS_MODE=proxy
```

修改环境文件后必须重启 Scert 服务；Nginx 仍按现有配置监听 9003 并代理到 `https://127.0.0.1:9004`。

## 测试和构建

```powershell
go fmt ./...
go test ./...
go build -o dist/scert.exe ./cmd/scert
```

Linux amd64 交叉编译：

```powershell
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
go build -o dist/scert-linux-amd64 ./cmd/scert
Remove-Item Env:GOOS
Remove-Item Env:GOARCH
```

## 数据库说明

- SQLite 用于单机、桌面和开发测试模式。
- MySQL 5.7 是基础开发和最低兼容版本。
- MySQL 8.0 用于兼容性测试。
- 数据库密码不得写入 Git、脚本参数或日志。
- MySQL 客户端和服务实例必须使用明确路径、端口和 DSN，不能通过 PATH 中的单个 `mysql` 命令判断版本。

## 当前生产验收边界

代码层已实现 ACME/lego 申请、第一批 DNS Provider、CNAME 委派、自动续期、证书导入、SSH/SFTP 部署、Nginx/Caddy/Apache reload、通知和 Web 管理接口。以下项目不能仅凭本地自动化测试宣称生产完成：真实 DNS 权限与公网传播、Let’s Encrypt Production 签发、真实远程服务器部署、Windows Service、Linux systemd 实机安装、Wails 桌面封装、Windows ACL、MySQL 真实实例和最终 HTTPS 到达性验收。

## 首轮复审状态

2026 年 8 月 26 日已完成首轮代码复审。`gofmt`、`go vet`、`go test`、竞态检测、Windows amd64 构建、Linux amd64 交叉构建、CLI 和 HTTP 实际流程均通过。MySQL 5.7/8.0 仅完成代码层实现和客户端版本确认，真实实例连接与迁移仍需明确 DSN、端口和凭据后验证。
