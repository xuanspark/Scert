# Scert 项目

Scert 是一个统一管理 SSL 证书、自动续期并部署到多台服务器的跨平台项目。

## 项目目录

- `backend`：Go 核心服务、CLI、数据库和 HTTP API。
- `frontend`：Vue Web 管理界面占位结构。
- `doc`：需求确认、技术架构和开发任务清单。

## 开发基线

- 第一阶段使用 Let’s Encrypt + `go-acme/lego`。
- 第一阶段包含单域名、泛域名和多域名 SAN。
- 支持 MySQL 5.7、MySQL 8.0 和 SQLite。
- HTTPS 采用默认核心服务终止、高级反向代理终止的双模式设计。
- CLI、Web 和桌面版共享核心服务、配置和数据。

## 开发状态

首轮基础骨架已经完成并等待复审。当前已完成配置、日志、数据库迁移、域名 Repository、基础 HTTP API、CLI 和前端占位页面；完整证书生命周期功能按 `doc/Scert开发任务清单.md` 继续开发。

详细内容请先阅读：

- `doc/SSL服务程序需求确认文档.md`
- `doc/SSL服务程序技术架构设计文档.md`
- `doc/Scert开发任务清单.md`

## 首轮复审状态

2026 年 8 月 26 日已完成首轮代码复审。基础骨架、SQLite、CLI、HTTP、前端生产构建和 Windows/Linux amd64 构建已通过；MySQL 5.7/8.0 真实实例测试因缺少明确连接参数保持阻塞。详细记录以 `doc/Scert开发任务清单.md` 的“二十七、首轮开发复审记录”为准。