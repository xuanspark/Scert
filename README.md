# Scert - SSL 证书全生命周期自动化管理系统

<p align="center">
  <img src="frontend/logo/Scert.png" alt="Scert Logo" width="120" />
</p>

<p align="center">
  <strong>现代化、安全可靠的企业级 SSL/TLS 证书自动化申请、续期、监控、分发与部署系统</strong>
</p>

---

## 🌟 项目简介

**Scert** 是一款面向现代化运维环境的高性能 SSL/TLS 证书全生命周期管理系统。支持 ACME 协议与主流云厂商 DNS 自动化联动，提供多域名/泛域名申请、到期监控巡检、多主机/多集群自动推送部署、安全回滚以及基于角色的多项目访问控制。

系统采用 **Go (Gin)** 高性能服务端与 **Vue 3** 响应式管理前端，支持 **Linux** 与 **Windows** 原生服务化部署，支持 SQLite、MySQL、PostgreSQL 三种企业级数据库存储引擎。

---

## 🚀 核心特性

- **ACME 自动化签发与续期**
  - 支持 Let's Encrypt 等合规 ACME CA 机构。
  - 支持单域名、多域名、泛域名（支持多级子域名与主泛合并）申请。
  - 自动化 DNS-01 验证（原生支持阿里云 DNS、腾讯云 DNSPod、华为云 DNS、AWS Route53 等主流 DNS 提供商）及 HTTP-01 验证。
- **证书智能分发与自动部署**
  - 支持一键或定时将证书推送到远端服务器（通过安全 SSH/SFTP 协议）。
  - 支持目标服务器 Nginx / Web 服务自动重载与 HTTPS 状态存活复核校验。
  - 具备原子部署事务保护，当部署失败或校验未通过时自动触发安全回滚，保障现有生产业务不受中断。
- **多数据库引擎支持**
  - 原生支持 **SQLite**（轻量自包含）、**MySQL** (5.7/8.0+)、**PostgreSQL** (12+)。
  - 初始化向导提供数据库连接测试、自动表结构迁移与数据一致性检查。
- **多租户与多项目权限管理**
  - 支持细粒度角色权限（超级管理员、管理员、普通用户）。
  - 支持多项目隔离管理，证书、域名、服务器资产按项目权限范围安全受控。
- **全方位告警与系统通知**
  - 支持内置 SMTP 邮件推送与邮件验证码安全验证。
  - 支持证书临期告警（支持自定义告警阈值，如 30 天/15 天/7 天）、签发失败告警及部署异常告警。
- **生产级安全体系**
  - 敏感凭据（DNS API Key/Secret、SSH 私钥等）基于主密钥进行高强度加密存储（AES-GCM / 保护密钥隔离）。
  - 生产运维边界严格受控，仅允许受信任的反向代理提供标准 HTTPS 访问，内部服务端口默认仅绑定回环地址（127.0.0.1）。

---

## 🏗️ 架构与技术栈

### 技术栈选型

- **后端架构 (Backend)**：
  - 核心语言：**Go 1.26+**
  - Web 框架：**Gin**
  - ACME 库：**lego/v5**
  - 数据库引擎：`modernc.org/sqlite`、`go-sql-driver/mysql`、`jackc/pgx/v5`
  - 命令行体系：**Cobra**
- **前端架构 (Frontend)**：
  - 框架：**Vue 3** (Composition API)
  - 构建工具：**Vite**
  - 样式设计：原生模块化 CSS，遵循现代规范的组件化与卡片式视觉风格
- **CI/CD 与运维 (DevOps)**：
  - CI/CD：统一的 GitHub Actions / Gitea Actions 工作流，支持全架构矩阵编译与自动化打包发布。
  - 系统服务：Linux Systemd / Windows Service 原生守护进程。

---

## 📁 目录结构

```text
Scert/
├── .github/
│   └── workflows/
│       └── release.yml        # CI/CD 自动化多平台打包发布工作流
├── backend/                   # Go 后端工程
│   ├── cmd/
│   │   ├── scert/             # 核心服务入口
│   │   ├── scert-admin/       # 超级管理员与运维重置入口
│   │   ├── scert-nginx/       # Nginx 辅助管理程序
│   │   ├── scert-release/     # 离线归档辅助工具
│   │   └── scert-storage/     # 证书安全存储与挂载辅助
│   ├── internal/              # 业务核心逻辑（ACME、DNS、部署、存储、通知）
│   └── go.mod
├── frontend/                  # Vue 3 前端工程
│   ├── src/
│   │   ├── views/             # 页面视图组件（Dashboard, UserManagement, SetupWizard 等）
│   │   ├── components/        # 公共基础组件
│   │   └── App.vue
│   └── package.json
├── deploy/                    # 部署脚本与配置文件模板
│   ├── install-linux.sh       # Linux 一键安装与升级脚本
│   ├── install-windows.ps1    # Windows PowerShell 一键安装脚本
│   ├── package.mjs            # 规范发布包打包脚本
│   ├── nginx/                 # Nginx 反向代理配置样例
│   └── systemd/               # Systemd 服务与 Socket 定义
├── doc/                       # 项目技术规范、接口说明与运维执行清单
└── README.md                  # 项目主文档
```

---

## 🛠️ 本地开发与构建

### 1. 环境准备

- **Go**：>= 1.26
- **Node.js**：>= 24
- **npm**：>= 11

### 2. 前端开发与构建

```bash
cd frontend

# 安装依赖
npm ci

# 启动本地开发热更新服务器
npm run dev

# 运行前端全量单元测试
npm test

# 编译前端生产静态资源
npm run build
```

### 3. 后端编译与启动

```bash
cd backend

# 编译服务端可执行文件
go build -trimpath -o ../bin/scert ./cmd/scert
go build -trimpath -o ../bin/scert-admin ./cmd/scert-admin

# 启动服务端（默认读取配置并初始化）
../bin/scert serve
```

---

## 📦 发布包制作与 CI/CD

项目配备统一的打包脚本 `deploy/package.mjs`，产物严格遵循 **`项目名+版本+架构`** 的命名规范：

```bash
# 打包 Linux amd64 归档包 (生成 Scert_v1.52.2_linux_amd64.tar.gz)
node deploy/package.mjs --target=linux_amd64 --version=v1.52.2

# 打包 Windows amd64 压缩包 (生成 Scert_v1.52.2_windows_amd64.zip)
node deploy/package.mjs --target=windows_amd64 --version=v1.52.2
```

发布包内包含：
- `scert` / `scert-admin`（Linux 下含 `scert-nginx` 与 `scert-storage`）
- `web/` 编译好的前端生产静态资源
- `install-linux.sh` / `install-windows.ps1`
- `systemd/` 与 `nginx/` 配置示例模板
- `SHA256SUMS` 完整性校验文件

推送版本标签（例如 `git tag v1.52.2 && git push Scert v1.52.2`）后，GitHub 与 Gitea 会自动触发 Actions 矩阵编译，生成各架构的发布包并自动挂载至 Release 资产清单中。

---

## 💻 生产环境部署

### Linux (Ubuntu / Debian / CentOS / Rocky)

1. 从 Release 页面下载对应架构的发布包并解压：
   ```bash
   tar -zxvf Scert_v1.52.2_linux_amd64.tar.gz
   cd Scert_v1.52.2_linux_amd64
   ```

2. 执行安装脚本：
   ```bash
   chmod +x install-linux.sh scert scert-admin
   sudo ./install-linux.sh --source-dir .
   ```

3. 启动并配置服务：
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable Scert
   sudo systemctl start Scert
   ```

4. 配置 Nginx 反向代理将标准 HTTPS 流量转发至 `127.0.0.1:8443`（参考 `nginx/Scert.conf.example`）。

### Windows

1. 解压 `Scert_v1.52.2_windows_amd64.zip`。
2. 以管理员权限启动 PowerShell 并执行：
   ```powershell
   powershell -ExecutionPolicy Bypass -File .\install-windows.ps1 -SourceDirectory .
   ```
3. 服务将自动注册为 Windows 守护进程运行。

---

## 📄 开源与许可证

本项目所有权属于原开发团队。遵循安全合规及企业授权协议分发使用。