# Scert

Scert 是面向 Windows 和 Linux 的 SSL 证书管理服务。当前代码处于首轮基础骨架阶段，已建立配置、数据库、日志、CLI、HTTP API 和前端占位结构。

## 当前已实现

- Go 后端模块和 `scert` CLI 入口。
- JSON 配置生成、读取、校验和原子保存。
- Windows 与 Linux 默认目录模型。
- SQLite 和 MySQL 数据库连接抽象。
- SQLite/MySQL 首版迁移脚本。
- 域名基础模型与 Repository。
- 健康检查、状态和域名基础 API。
- 中文 CLI：`version`、`help`、`init`、`status`、`serve`、`domain list`、`domain add`。
- Vue 前端状态占位页面。

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

当前 `serve` 是 HTTP 基础骨架。HTTPS 双模式、ACME 证书申请、DNS-01、远程部署等功能按开发任务清单在后续阶段实现。

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

## 当前未实现

ACME/lego 实际申请、DNS 服务商 API、自动续期、SSH/SFTP 部署、Nginx/Caddy/Apache reload、完整 Web 管理界面、Wails 桌面版、Windows Service 和 Linux systemd 均未在首轮基础骨架中宣称完成。

## 首轮复审状态

2026 年 8 月 26 日已完成首轮代码复审。`gofmt`、`go vet`、`go test`、竞态检测、Windows amd64 构建、Linux amd64 交叉构建、CLI 和 HTTP 实际流程均通过。MySQL 5.7/8.0 仅完成代码层实现和客户端版本确认，真实实例连接与迁移仍需明确 DSN、端口和凭据后验证。