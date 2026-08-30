# Scert Linux 部署

## 安装包目录

Linux 安装包应将以下文件放在同一部署目录中：

```text
deploy/
├── install-linux.sh
├── scert
├── scert-admin
├── web/
│   ├── index.html
│   └── assets/
└── systemd/
    ├── Scert.service
    ├── Scert-admin.service
    ├── Scert服务管理说明.md
    └── Scert-admin服务管理说明.md
```

二进制文件应针对目标平台构建为 Linux amd64，并且具有可执行权限。

## 安装命令

```bash
chmod +x install-linux.sh scert scert-admin
sudo ./install-linux.sh --source-dir .
```

也可以显式指定产物目录：

```bash
sudo ./install-linux.sh --source-dir /tmp/scert-linux-amd64
```

## 安装器行为

安装器会：

1. 检查 Linux、root 权限、`systemctl` 和安装产物。
2. 创建 `Scert` 系统用户和同名系统用户组，禁止交互式登录。
3. 创建 `/scert`、`/scert/conf`、`/scert/data`、`/scert/log`、`/scert/backups`、`/scert/docs`、`/scert/web` 和证书目录。
4. 安装 `scert`、`scert-admin`、Web 静态资源以及两个 systemd unit。
5. 设置安装目录、服务目录和配置目录权限，防止 `Scert` 运行用户替换 root 所有的程序文件。
6. 为证书根目录生成 systemd drop-in，并执行一次 `systemctl daemon-reload`。

安装器不会：

- 执行 `systemctl enable Scert-admin`。
- 启动或重启 `Scert-admin`。
- 创建公网防火墙规则。
- 删除已有配置、数据库、证书和备份。

## 安装后流程

安装完成后先运行初始化流程，填写超级管理员、邮件、DNS API、端口和证书路径配置。初始化流程负责 DNS API 验证、Web 证书申请、部署校验和主服务端口防火墙处理。

`Scert-admin` 默认保持停止状态。确有密码重置需求时，仅使用：

```bash
systemctl start Scert-admin
systemctl stop Scert-admin
```

`Scert-admin` 不监听任何 TCP/UDP 端口。启动后，它只维护 Scert 主服务 `/reset` 路径的短时激活租约，并将一次性校验码写入 `/run/scert-admin/reset-code` 受保护临时文件，不写入标准输出或 systemd 日志；停止后入口立即失效，异常退出后入口会在租约过期时失效。管理员可使用 `sudo cat /run/scert-admin/reset-code` 读取校验码，校验码在成功消费、服务停止或租约失效后自动销毁。云安全组只需由管理员根据初始化提示放行主服务端口。

### 环境变量覆盖

`Scert` 启动时支持使用 `SCERT_*` 环境变量覆盖当前进程配置，适合写入 systemd 的 `EnvironmentFile`。例如：

```text
SCERT_SERVER_HOST=127.0.0.1
SCERT_SERVER_PORT=9004
SCERT_SERVER_HTTPS_MODE=proxy
SCERT_WEB_ENABLED=true
```

`SCERT_WEB_ENABLED=false` 只关闭 Scert 提供的 Web 静态页面，不会关闭核心 API 或桌面端本地管理能力。环境变量不会回写 `scert.json`；修改后需要重启 `Scert` 服务。

## 参数说明

- `--source-dir`：包含两个 Linux 可执行文件和 `web/index.html` 的目录，默认是安装器所在目录。
- `--certificate-dir`：证书根目录参数，默认 `/ssl`。安装器会创建该目录，写入 `/scert/conf/installation.json`，并生成对应的 `Scert.service.d/10-certificate-path.conf`。首次初始化会读取该记录作为证书目录默认值。

Linux 安装目录当前固定为 `/scert`。自定义证书根目录必须是规范化的安全绝对路径，只允许字母、数字、点、下划线、连字符和斜杠，不允许空格、`..` 路径片段、符号链接、受保护系统目录或 `/scert` 下的路径；`/var` 下仅允许 `/var/lib/scert` 专用目录。

安装器是文件部署阶段，不负责替代首次初始化向导，也不代替真实 ACME 申请和远程部署验收。

## 开发阶段反向代理部署

开发阶段可以使用测试域名 `web.xuans.com` 验证 Web 页面和反向代理链路。推荐让 Scert 只监听本机端口 `9004`，由 Nginx 监听 `9003` 接收测试访问并转发：

```text
测试访问 HTTPS 9003
    ↓
Nginx
    ↓ https://127.0.0.1:9004
Scert
```

对应配置为：

```json
{
  "server": {
    "host": "127.0.0.1",
    "port": 9004,
    "https_mode": "proxy"
  }
}
```

Nginx 示例：

```nginx
server {
    listen 443 ssl http2;
    server_name web.xuans.com;

    ssl_certificate /etc/letsencrypt/live/web.xuans.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/web.xuans.com/privkey.pem;

    location / {
        proxy_pass https://127.0.0.1:9004;
        proxy_ssl_server_name on;
        proxy_ssl_name web.xuans.com;
        proxy_ssl_verify off;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }
}
```

如果需要 HTTP 自动跳转，再配置并开放 `80`；正式公网访问通常使用 `443`，开发测试访问使用 Nginx 的 `9003`。Scert 内部端口 `9004` 和 Nginx 测试端口 `9003` 都不应加入公网防火墙规则。端口可以在配置文件的 `server.port` 修改，修改后需要重启 Scert；如果改为直接对外监听，则还需要同步调整防火墙和安全组规则。

`web.xuans.com` 仅用于开发阶段测试，不能作为生产环境证书、DNS、ACME 或最终 HTTPS 验收依据。

## Windows 部署

Windows 发布目录应包含：

```text
scert.exe
scert-admin.exe
install-windows.ps1
```

使用管理员 PowerShell 执行：

```powershell
powershell -ExecutionPolicy Bypass -File .\install-windows.ps1 -SourceDirectory .
```

默认目录：

- 程序目录：`C:\Program Files\Scert`
- 配置和运行数据目录：`C:\ProgramData\Scert`
- 证书目录：`C:\ProgramData\Scert\ssl`

安装器会检查 Windows、管理员权限、`sc.exe`、`icacls.exe` 和两个程序产物；创建配置、数据、日志、备份、文档、运行时和证书目录；设置 Windows ACL；使用 `LocalService` 身份注册 `Scert` 与 `Scert-admin` 服务；并写入不含凭据的安装记录。

安装器不会删除或覆盖已有配置、数据库、证书和备份，不会启动或重启服务，也不会创建公网防火墙规则。`Scert` 注册为自动启动，`Scert-admin` 注册为手动启动；只有确需密码重置时才应由管理员启动 `Scert-admin`，完成后立即停止。

自定义目录示例：

```powershell
powershell -ExecutionPolicy Bypass -File .\install-windows.ps1 `
  -SourceDirectory D:\release\Scert\windows-amd64 `
  -InstallDirectory D:\Apps\Scert `
  -DataDirectory D:\ScertData `
  -CertificateDirectory D:\ScertCertificates
```

Windows Service 实机安装、开机启动、停止、升级和 ACL 仍需在授权 Windows 主机上验收。
