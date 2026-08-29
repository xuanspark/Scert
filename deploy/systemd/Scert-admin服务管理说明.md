# Scert-admin 服务管理说明

## 服务边界

`Scert-admin` 是超级管理员密码重置入口激活器，本身不监听任何 TCP/UDP 端口。Scert 核心服务固定提供 `/reset` 路由，但只有 `Scert-admin` 正在运行且短时租约有效时才显示重置页面，其余时间统一返回 `404`。

Linux 安装程序必须同时安装 `Scert` 和 `Scert-admin` 的 systemd 单元，但不得执行 `systemctl enable Scert-admin`，不得为 `Scert-admin` 创建开机启动软链接，也不得配置 `Restart=always` 或其他自动拉起策略。

## 一次性校验码文件

Linux systemd 安装默认将校验码写入 `/run/scert-admin/reset-code`，该目录由 systemd 创建并设置为 `0700`，文件由 Scert-admin 创建并设置为 `0600`。直接运行程序时可以通过 `--code-output` 指定受保护的临时文件路径；不要将该路径放入公开目录、日志目录或备份目录。

校验码文件只用于人工读取，不属于配置数据。读取后不得复制到工单、聊天记录或日志中；密码重置成功后文件会立即销毁。

## 支持的生命周期控制

日常运行时，`Scert-admin` 只支持以下两个控制命令：

```bash
systemctl start Scert-admin
systemctl stop Scert-admin
```

- `start`：生成一次性校验码，激活由 Web 管理域名或公网 IP 自动生成的 `/reset` 地址，并开始刷新短时租约。
- `stop`：立即使 `/reset` 入口失效；异常退出时，入口会在当前租约到期后自动失效。
- `restart`、`reload`、`try-restart` 等操作不属于 Scert-admin 的支持范围；单元不提供 `ExecReload`，且不配置自动重启。
- `enable` 不应使用：该单元没有 `[Install]` 段，因此不会注册开机启动目标。
- `daemon-reload` 仅允许由安装、升级程序在替换 unit 文件后执行，不属于日常服务控制命令。

## 安全限制说明

systemd 无法阻止 root 或具备等效系统权限的操作者调用 `status`、`kill`、`restart`、`mask` 等管理命令。因此，本项目的可落地安全边界是：

1. 单元不提供 `[Install]` 段，默认不会开机自启动。
2. 单元使用 `Restart=no`，异常退出不会被 systemd 自动拉起。
3. 进程使用 `Scert` 用户运行，systemd 通过 `RestrictAddressFamilies=AF_UNIX` 禁止其建立 IPv4 或 IPv6 网络监听。
4. `Scert-admin` 不配置独立端口，初始化程序只放行 Scert 主服务端口。
5. 安装器、桌面端和运维文档只提供 `start`、`stop` 两个日常控制入口。
6. 任何需要重新加载 unit 的行为仅限安装或升级阶段，不能通过 Web 管理界面触发。
7. 激活租约有效期为 90 秒，每 30 秒刷新；systemd 使用 `RuntimeMaxSec=30min` 将单次激活最长时间限制为 30 分钟。
8. 启动时生成的一次性校验码不会写入标准输出或 systemd 日志。校验码只写入受保护的临时文件；systemd 单元使用 `RuntimeDirectory=scert-admin` 和 `RuntimeDirectoryMode=0700`，文件权限为 `0600`，仅允许 `Scert` 用户和 root 读取。

这组限制可以避免服务因安装配置被纳入开机启动、避免服务自动反复拉起，并彻底取消独立密码重置监听端口。需要注意：`/reset` 与 Scert 主服务共享 HTTPS 端口，激活期间仍可能被外部访问，因此完整密码重置实现还必须校验一次性校验码、限制请求频率、单次消费校验码并记录审计日志。不能把拥有 root 权限的恶意操作者从 systemd 管理能力中排除。

## 安装阶段约定

安装器完成 unit 文件复制后，可以执行一次：

```bash
systemctl daemon-reload
```

安装器不得执行以下命令：

```bash
systemctl enable Scert-admin
systemctl start Scert-admin
systemctl restart Scert-admin
```

安装完成后只提示管理员在确有需要时手动执行 `systemctl start Scert-admin`；不需要密码重置时保持服务停止，完成重置后立即执行 `systemctl stop Scert-admin`。

当前实现已经接入一次性校验码、单次消费、密码复杂度校验、CSRF、同源校验、失败限流、配置原子保存和审计日志。密码重置成功后 `/reset` 立即关闭，Scert-admin 后续刷新旧租约也会自动终止。启动后请按终端提示，使用 root 或 `Scert` 用户读取受保护的临时校验码文件：`sudo cat /run/scert-admin/reset-code`。校验码在密码重置成功、服务停止或租约失效后自动销毁。
