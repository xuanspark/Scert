# Scert 服务端部署

## 正式范围

正式安装包仅包含 Web 管理界面、Windows/Linux 服务端及其运维辅助程序，不含 Wails 桌面端，不要求 WebView2 或 GTK。桌面源码仅作为历史参考保留。

CLI 用于初始化、服务启动、运维诊断、数据库迁移、备份恢复、服务安装和卸载；日常业务管理使用 Web。Windows 使用包内 `install-windows.ps1`，Linux 使用 `install-linux.sh`；发布入口及双平台包结构见仓库 `build/README.md`。下文是 Linux 部署步骤。

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

Linux 发布包还包含 `scert-nginx`、`systemd/scert-nginx.socket`、`systemd/scert-nginx.service` 和 `nginx/` 下的策略模板与中文说明。它们是独立、显式安装的管理证书激活组件，普通安装器不安装、不启用、不修改共享 Nginx。接线流程见 [Nginx 辅助服务部署说明](nginx/Nginx辅助服务部署说明.md)，需要单独完成实机验证。

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

1. 检查 Linux、root 权限、`systemctl`、Linux amd64 ELF 安装产物、Web 特殊文件和符号链接。
2. 创建 `Scert` 系统用户和同名系统用户组，禁止交互式登录。
3. 创建 `/scert`、`/scert/conf`、`/scert/data`、`/scert/log`、`/scert/backups`、`/scert/docs`、`/scert/web` 和证书目录。
4. 安装 `scert`、`scert-admin`、Web 静态资源以及两个 systemd unit。
5. 设置安装目录、服务目录和配置目录权限，防止 `Scert` 运行用户替换 root 所有的程序文件。
6. 为证书根目录生成 systemd drop-in，并执行一次 `systemctl daemon-reload`。
7. 升级时读取并保留既有 `installation.json` 中的证书根目录；只有显式传入 `--certificate-dir` 才会变更。
8. 在复制程序前确认 `Scert.service` 和 `Scert-admin.service` 都已停止，并通过同文件系统临时文件替换核心二进制。

安装器不会：

- 执行 `systemctl enable Scert-admin`。
- 启动或重启 `Scert-admin`。
- 创建公网防火墙规则。
- 删除已有配置、数据库、证书和备份。

如果服务仍在运行，安装器会直接拒绝安装或升级。生产升级必须先创建完整备份，再停止服务：

```bash
sudo systemctl stop Scert
sudo systemctl stop Scert-admin
sudo ./install-linux.sh --source-dir /tmp/scert-linux-amd64
sudo systemctl start Scert
```

## 安装后流程

安装完成后先启动主服务，再通过服务器 IP 访问独立 HTTPS 初始化页面，无需 Linux 图形界面：

```bash
sudo systemctl enable Scert
sudo systemctl start Scert
sudo systemctl status Scert --no-pager -l
sudo cat /run/scert/bootstrap-url
```

默认临时监听端口是 `8080`，须由管理员预先在主机防火墙和云安全组中对受信任的管理来源放行。云 NAT 环境应在启动前通过 systemd 的 `SCERT_BOOTSTRAP_PUBLIC_IP` 设置可访问的公网 IP；它只影响临时地址和证书，不会创建网络规则。

先打开裸 IP 地址查看浏览器证书详情，通过可信 SSH 或控制台读取最新 `journalctl -u Scert -b -n 30` 中的临时证书 SHA-256 指纹。与浏览器完全一致后，再打开受保护文件内的完整 HTTPS 链接。链接片段是短期一次性令牌，不要存入普通文档、日志或截图。指纹不一致必须停止，不得输入 RAM 凭据。临时证书为自签名，不能作为公共 CA 信任或生产 TLS 通过的证据；服务重启会生成新的临时证书，必须重新核对。

向导提供 SQLite、MySQL、PostgreSQL 三种存储选项，外部数据库强制 TLS 证书及主机名校验。数据库和阿里云 DNS 验证通过后可提交管理员、通知及正式域名/IP 配置；完成后临时服务关闭，自动加载正式配置并跳转正式 HTTPS 地址。IP 管理证书目前仍为自签名。

固定及动态生成的 Linux systemd unit 显式设置 `SCERT_BOOTSTRAP_FIREWALL_MODE=external`，初始化不查询或修改主机防火墙及云安全组，而在日志、页面提示和完成响应中说明管理员责任。这不是端口已放行或网络已验证的证明：首次及正式管理端口都必须预先按最小来源范围配置；反向代理场景不能将内部端口开放到公网。

命令行直接运行仍默认 `auto`，其防火墙检查或写入失败会中止初始化，不会悄悄降级。管理员已管理网络规则时可指定 `scert serve --bootstrap-firewall-mode external`；该参数及环境变量仅影响首次 Web 初始化，不能由远程请求切换策略。不能为通过初始化而把服务改成 root。非特权 Linux 实机验收尚未完成；反向代理的回环监听配置应在 IP 初始化结束后再应用，具体顺序见 `doc/生产部署与运维说明.md` 第 4.1 节。

`Scert-admin` 默认保持停止状态。确有密码重置需求时，仅使用：

```bash
systemctl start Scert-admin
systemctl stop Scert-admin
```

`Scert-admin` 不监听任何 TCP/UDP 端口。启动后，它只维护 Scert 主服务 `/reset` 路径的短时激活租约，并将一次性校验码写入 `/run/scert-admin/reset-code` 受保护临时文件，不写入标准输出或 systemd 日志；停止后入口立即失效，异常退出后入口会在租约过期时失效。管理员可使用 `sudo cat /run/scert-admin/reset-code` 读取校验码，校验码在成功消费、服务停止或租约失效后自动销毁。云安全组只需由管理员根据初始化提示放行主服务端口。

### 存活检查与生产就绪检查

Scert 提供两个用途不同的无认证检查接口：

- `GET /healthz`：只表示进程和 HTTP 服务仍可响应，不能证明 DNS 凭据、TLS 文件和数据库可用。
- `GET /readyz`：生产就绪检查，只有初始化完成、已配置的敏感凭据可解密、TLS 证书与私钥为普通文件且数据库可访问时才返回 HTTP `200`。

安装、升级、恢复和配置变更后必须同时检查两个接口，并验证证书名称和可信链。以下示例在部署服务器上执行；域名、上游端口和 CA 文件必须与实际配置一致，不能直接使用示例域名：

```bash
management_domain=scert.example.com
upstream_port=9004
ca_bundle=/etc/ssl/certs/ca-certificates.crt
curl --noproxy '*' --fail --silent --show-error --max-time 15 \
  --cacert "$ca_bundle" --resolve "${management_domain}:${upstream_port}:127.0.0.1" \
  "https://${management_domain}:${upstream_port}/healthz"
curl --noproxy '*' --fail --silent --show-error --max-time 15 \
  --cacert "$ca_bundle" --resolve "${management_domain}:${upstream_port}:127.0.0.1" \
  "https://${management_domain}:${upstream_port}/readyz"
```

`--resolve` 仅将连接定向到回环地址，仍按 URL 中的配置域名发送 SNI 并验证 TLS。私有 CA 场景应将 `ca_bundle` 改为已通过可信渠道核验、仅管理员可修改的 CA 文件，不得使用 `-k` 或 `--insecure`。这里检查的是上游，不代表 Nginx 或公网 DNS 已通过验收。

反向代理模式还需在独立客户端按正常 DNS 检查公开入口，不使用指定解析或隧道。以下公开端口必须与 `server.proxy_https_port` 一致；直接模式使用实际对外 HTTPS 端口：

```bash
management_domain=scert.example.com
public_port=443
curl --noproxy '*' --fail --silent --show-error --max-time 15 \
  "https://${management_domain}:${public_port}/healthz"
curl --noproxy '*' --fail --silent --show-error --max-time 15 \
  "https://${management_domain}:${public_port}/readyz"
```

公开入口默认使用客户端系统信任库；私有 CA 环境须显式指定经过核验的 CA 文件。HTTP `200` 之外还应核对 `/readyz` JSON 的 `status` 为 `ready`；TLS 失败、错误响应或域名解析不符都不能算通过。

`/readyz` 失败时返回 HTTP `503` 和不包含敏感引用的原因码：

- `setup_incomplete`：初始化尚未完成。
- `dns_credential_missing`：DNS 凭据引用为空。
- `secret_store_unavailable`：敏感信息存储未初始化。
- `credential_unavailable`：DNS、HTTP 通知或 SMTP 通知凭据引用不存在或无法解密，必须通过管理页面重新在线测试并保存真实凭据。
- `tls_material_unavailable`：内部 TLS 证书或私钥缺失、不是普通文件或是符号链接。
- `database_unavailable`：数据库不可访问。

禁止写入假 Token 或伪造敏感信息引用来绕过就绪检查。Nginx、监控系统和升级验收应使用 `/readyz` 判断是否可接收生产流量，`/healthz` 仅用于判断进程是否存活。

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
- `--certificate-dir`：证书根目录参数，默认 `/ssl/scert`，不接管共用的 `/ssl`。安装器会创建专用目录，写入 `/scert/conf/installation.json`，并生成对应的 `Scert.service.d/10-certificate-path.conf`。首次初始化会读取该记录作为证书目录默认值；既有安装仍沿用安装记录，不自动迁移证书。已存在且不属于 Scert 的目录会被拒绝，不修改其所有者或内容，请选择尚不存在的专用子目录。

Linux 安装目录当前固定为 `/scert`。自定义证书根目录必须是规范化的安全绝对路径，只允许字母、数字、点、下划线、连字符和斜杠，不允许空格、`..` 路径片段、符号链接、受保护系统目录或 `/scert` 下的路径；`/var` 下仅允许 `/var/lib/scert` 专用目录。

安装器是文件部署阶段，不负责替代首次初始化向导，也不代替真实 ACME 申请和远程部署验收。

## 生产反向代理要求

生产环境必须让 Scert 只监听回环地址，例如 `127.0.0.1:9004`，由 Nginx 对外监听 `443`。Nginx 到 Scert 的 HTTPS 上游连接必须校验证书，不得使用 `proxy_ssl_verify off`：

```nginx
location / {
    proxy_pass https://127.0.0.1:9004;
    proxy_ssl_server_name on;
    proxy_ssl_name scert.example.com;
    proxy_ssl_verify on;
    proxy_ssl_trusted_certificate /etc/ssl/certs/ca-certificates.crt;
    proxy_ssl_verify_depth 3;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto https;
}
```

如果内部证书由私有 CA 签发，应将对应 CA 证书保存到仅 root 可修改的独立文件，并配置到 `proxy_ssl_trusted_certificate`。不要通过关闭上游验证规避证书名称或信任链问题。

## 开发阶段反向代理部署

### 自定义公开端口

管理域名由部署配置决定，不固定为示例域名。使用非标准 HTTPS 端口时，必须设置 `server.proxy_https_port`，初始化向导和管理设置均提供对应字段。省略该字段或设为 `0` 时，代理公开端口仍为 `443`；直接模式忽略该字段并使用 `server.port`。

例如自定义端口方案可以使用 `web.apmmk.cn`，HTTP `8003` 跳转至 HTTPS `8004`，核心服务仅监听 `127.0.0.1:9004`。以下仅为配置示例，不代表当前测试环境或生产环境的实际域名、端口：

```json
{
  "server": {
    "host": "127.0.0.1",
    "port": 9004,
    "https_mode": "proxy",
    "proxy_https_port": 8004
  },
  "web": {
    "hostname": "web.apmmk.cn"
  }
}
```

```nginx
server {
    listen 8003;
    server_name web.apmmk.cn;
    return 308 https://web.apmmk.cn:8004$request_uri;
}

server {
    listen 8004 ssl;
    server_name web.apmmk.cn;
    ssl_certificate /实际证书目录/fullchain.pem;
    ssl_certificate_key /实际证书目录/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;

    location / {
        proxy_pass https://127.0.0.1:9004;
        proxy_ssl_server_name on;
        proxy_ssl_name web.apmmk.cn;
        proxy_ssl_verify on;
        proxy_ssl_trusted_certificate /etc/ssl/certs/ca-certificates.crt;
        proxy_ssl_verify_depth 3;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

`Host` 必须保留外部端口，否则同源安全检查可能拒绝请求。跳转目标使用明确配置的域名，不直接信任来访 `Host`。证书路径须替换为实际签发路径；证书更新后需要执行配置检查并平滑重载 Nginx。安全组仅开放所需公开端口，不开放内部 `9004`，不改动其他应用占用的 `80/443`。先验证真实证书、DNS、登录、会话和重置链接，再判定部署通过。

### 标准公开端口示例

使用标准公开端口时，让 Scert 只监听本机端口 `9004`，由 Nginx 在共享 `443` 上按域名接收访问并转发。下面的 `scert.example.com` 必须替换为实际配置域名：

```text
公开访问 HTTPS 443
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
    "https_mode": "proxy",
    "proxy_https_port": 443
  },
  "web": {
    "hostname": "scert.example.com"
  }
}
```

Nginx 示例：

```nginx
server {
    listen 443 ssl http2;
    server_name scert.example.com;

    ssl_certificate /实际证书目录/fullchain.pem;
    ssl_certificate_key /实际证书目录/privkey.pem;

    location / {
        proxy_pass https://127.0.0.1:9004;
        proxy_ssl_server_name on;
        proxy_ssl_name scert.example.com;
        proxy_ssl_verify on;
        proxy_ssl_trusted_certificate /etc/ssl/certs/ca-certificates.crt;
        proxy_ssl_verify_depth 3;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }
}
```

如果需要 HTTP 自动跳转，再为实际管理域名配置 `80` 到 `443` 的跳转。共享 Nginx 时仅增加该域名的命名虚拟主机，不修改其他站点或增加冲突的默认站点。Scert 内部端口 `9004` 不应加入公网防火墙规则；修改 `server.port` 后需要同步调整 Nginx 上游并重启 Scert。

示例域名与配置不能作为生产环境证书、DNS、ACME 或最终 HTTPS 验收依据。

开发环境使用自签名证书时，应优先将测试 CA 加入独立信任文件。只有在隔离的临时开发环境中，才允许短时使用 `proxy_ssl_verify off`；该设置禁止复制到生产配置。

## 备份与恢复

完整备份包含配置、主密钥、加密敏感配置、SQLite 一致性快照、MySQL 导出或 PostgreSQL 自定义格式归档、证书目录以及带 SHA-256 的 `manifest.json`。备份目录必须由 `Scert` 用户控制并保持 `0700`，不得放入 Web 根目录或公开共享目录。

PostgreSQL 的 `backup`、`restore` 已接入官方 `pg_dump`、`pg_restore`。两个工具必须具有相同主版本且至少为 16，`pg_dump` 主版本不得低于数据库服务端主版本；连接必须保持 `sslmode=verify-full`。工具缺失、版本不兼容或 TLS 校验失败都会中止，不会发布遗漏数据库的备份。

PostgreSQL 自动恢复仅支持原目标数据库、对象清单及所有者一致的场景，不支持空库灾难恢复、跨数据库或跨结构迁移。集群角色、角色密码、表空间定义、服务端配置及外置数据库 CA/客户端证书须另行保护；本机维护锁也不能阻止其他主机写入，恢复前必须停止所有相关实例和外部结构变更任务。归档可能包含可执行 SQL，只能恢复可信来源的备份。已有隔离测试不替代当前部署环境的恢复演练，不能据此声明生产验收通过。

备份和恢复都会获取跨进程维护锁；如果主服务仍在运行会直接拒绝。运维时仍必须显式停服，避免外部工具或尚未纳入锁协议的操作修改证书目录：

```bash
sudo systemctl stop Scert
sudo -u Scert /scert/scert --config /scert/conf/scert.json backup
sudo systemctl start Scert
```

恢复命令：

```bash
sudo systemctl stop Scert
sudo -u Scert /scert/scert --config /scert/conf/scert.json restore --input /scert/backups/scert-YYYYMMDD-HHMMSS
sudo systemctl start Scert
```

恢复前会校验清单、权限、大小和 SHA-256，并保留 `*.before-restore-YYYYMMDD-HHMMSS` 副本；恢复失败会尝试回滚配置、数据库、主密钥、敏感配置和证书。备份清单不是数字签名，不能抵抗有权限同时篡改文件并重新生成清单的攻击者，因此备份目录权限和离线副本仍是生产安全边界。

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

安装器会检查 Windows、管理员权限、`sc.exe`、`icacls.exe`、Windows amd64 PE 程序产物、路径重解析点和服务停止状态；创建配置、数据、日志、备份、文档、运行时和证书目录；设置 Windows ACL；使用 `LocalService` 身份注册 `Scert` 与 `Scert-admin` 服务；并写入不含凭据的安装记录。

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

## 标准端口反向代理部署约束（2026-10-01）

本轮 Linux 部署使用以下端口边界；前面的自定义公开端口示例不用于本次生产部署。

| 组件 | 监听地址 | 用途 |
| --- | --- | --- |
| Nginx | 公网 `80` | 跳转到管理域名的 HTTPS 入口 |
| Nginx | 公网 `443` | 管理页面及接口的唯一 HTTPS 入口 |
| Scert | `127.0.0.1:8443` | 仅供本机 Nginx 访问，不在安全组或防火墙额外放行 |

静态安装模板与命令行生成的 Linux systemd unit 均设置 `Environment=SCERT_SERVER_HOST=127.0.0.1`。内部端口由 Scert 配置决定，本次使用 `8443`，该环境变量只约束监听地址。部署时必须核对已有 drop-in 或环境文件没有将监听地址覆盖为公网地址。

使用 `deploy/nginx/Scert.conf.example` 时，将 HTTP、HTTPS、上游端口分别填写为 `80`、`443`、`8443`，管理域名和证书路径使用实际配置。保留 `proxy_ssl_verify on`、管理域名 SNI 和可信 CA；不得通过关闭 TLS 校验解决上游证书错误。共享 Nginx 仅调整 Scert 对应站点，不覆盖邮件或其他站点。

首次初始化也不能临时开放公网业务端口。尚无可信管理证书时，通过密码 SSH 本地转发访问回环初始化入口，并按照既有初始化说明核验临时证书指纹；完成正式证书配置后再启用严格校验的公开反代。

复核至少包括：实际监听地址、`nginx -t`、HTTP 跳转、管理域名 HTTPS 就绪检查、回环上游严格 TLS 检查以及独立外部连接无法访问内部端口。检查通过不代替首次初始化、证书签发、部署恢复及数据库生产矩阵验收。
