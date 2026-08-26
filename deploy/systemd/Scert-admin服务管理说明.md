# Scert-admin 服务管理说明

## 服务边界

`Scert-admin` 是超级管理员密码重置服务，仅允许在本机回环地址监听。它不应被配置为公网服务，也不应为其创建本机防火墙入站规则。

Linux 安装程序必须同时安装 `Scert` 和 `Scert-admin` 的 systemd 单元，但不得执行 `systemctl enable Scert-admin`，不得为 `Scert-admin` 创建开机启动软链接，也不得配置 `Restart=always` 或其他自动拉起策略。

## 支持的生命周期控制

日常运行时，`Scert-admin` 只支持以下两个控制命令：

```bash
systemctl start Scert-admin
systemctl stop Scert-admin
```

- `start`：按配置启动本机密码重置服务。
- `stop`：停止本机密码重置服务。
- `restart`、`reload`、`try-restart` 等操作不属于 Scert-admin 的支持范围；单元不提供 `ExecReload`，且不配置自动重启。
- `enable` 不应使用：该单元没有 `[Install]` 段，因此不会注册开机启动目标。
- `daemon-reload` 仅允许由安装、升级程序在替换 unit 文件后执行，不属于日常服务控制命令。

## 安全限制说明

systemd 无法阻止 root 或具备等效系统权限的操作者调用 `status`、`kill`、`restart`、`mask` 等管理命令。因此，本项目的可落地安全边界是：

1. 单元不提供 `[Install]` 段，默认不会开机自启动。
2. 单元使用 `Restart=no`，异常退出不会被 systemd 自动拉起。
3. 进程使用 `Scert` 用户运行，并由程序层拒绝非回环监听地址。
4. 初始化程序不开放 `Scert-admin` 端口，不创建其防火墙规则。
5. 安装器、桌面端和运维文档只提供 `start`、`stop` 两个日常控制入口。
6. 任何需要重新加载 unit 的行为仅限安装或升级阶段，不能通过 Web 管理界面触发。

这组限制可以避免服务因安装配置被纳入开机启动、避免服务自动反复拉起，并避免密码重置端口暴露给公网；不能把拥有 root 权限的恶意操作者从 systemd 管理能力中排除。

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

安装完成后只提示管理员在确有需要时手动执行 `systemctl start Scert-admin`；不需要密码重置时保持服务停止。