# Scert Linux 部署

## 安装包目录

Linux 安装包应将以下文件放在同一部署目录中：

```text
deploy/
├── install-linux.sh
├── scert
├── scert-admin
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
3. 创建 `/scert`、`/scert/conf`、`/scert/data`、`/scert/log`、`/scert/backups`、`/scert/docs` 和证书目录。
4. 安装 `scert`、`scert-admin` 以及两个 systemd unit。
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

`Scert-admin` 不监听任何 TCP/UDP 端口。启动后，它只维护 Scert 主服务 `/reset` 路径的短时激活租约并输出一次性校验码；停止后入口立即失效，异常退出后入口会在租约过期时失效。云安全组只需由管理员根据初始化提示放行主服务端口。

## 参数说明

- `--source-dir`：包含两个 Linux 可执行文件的目录，默认是安装器所在目录。
- `--certificate-dir`：证书根目录参数，默认 `/ssl`。安装器会创建该目录，写入 `/scert/conf/installation.json`，并生成对应的 `Scert.service.d/10-certificate-path.conf`。首次初始化会读取该记录作为证书目录默认值。

Linux 安装目录当前固定为 `/scert`。自定义证书根目录必须是规范化的安全绝对路径，只允许字母、数字、点、下划线、连字符和斜杠，不允许空格、`..` 路径片段、符号链接、受保护系统目录或 `/scert` 下的路径；`/var` 下仅允许 `/var/lib/scert` 专用目录。

安装器是文件部署阶段，不负责替代首次初始化向导，也不代替真实 ACME 申请和远程部署验收。
