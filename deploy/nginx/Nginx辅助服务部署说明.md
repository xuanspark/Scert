# Nginx 辅助服务部署说明

本流程面向 Linux 的 root 维护人员，使用独立 Unix socket 完成管理证书双层切换。普通安装器不会安装或启用辅助服务。本模板尚待目标主机 systemd 与实际 Nginx 配置验证，不代表生产验收已通过。

## 部署前核对

- 先确认 SSH 主机身份、备份及维护窗口。检查目标站点、已有服务、端口、Nginx 主进程用户、二进制、主配置、PID、日志和缓存目录，不修改其他站点。
- 管理域名和公开端口由部署环境指定。策略只接受具体域名与两个不同的回环地址。Scert 应用的域名、代理公开端口、HTTPS 上游端口必须与策略一致。
- 代理和上游必须已提供同一张受系统 CA 信任、覆盖管理域名且未过期的证书，两个 `/readyz` 均成功。初始化不签发证书，不接受自签名证书，不绕过验证。
- 程序、策略、Nginx 主配置及全部 include、模块、PID 和可执行依赖须由 root 维护。不要让应用用户修改这些路径或任何父目录；不能把任意应用用户可写配置交给 root 的 `nginx -t` 或重载命令。
- 使用与核心程序配套的辅助程序版本，先核验发布包 `SHA256SUMS`。辅助程序必须安装在 `/usr/local/libexec/scert-nginx`，不能直接以 root 运行应用可写目录中的程序。
- 停止并禁止管理证书变更任务；首次接线期间两个服务仍须能响应就绪探测。辅助服务尚未接通时，应用的代理续期保护会拒绝新的签发。

## 首次文件安装

先核对以下路径均无符号链接、硬链接、非 root 写权限或既有安装。本节命令仅用于首次安装，不作为升级覆盖命令。目标已存在时停止，检查来源和内容，不删除旧状态。

```sh
install -d -o root -g root -m 0755 /usr/local/libexec
install -d -o root -g root -m 0700 /etc/scert-nginx
install -d -o root -g root -m 0700 /var/lib/scert-nginx
install -d -o root -g root -m 0755 /run/scert-nginx
test ! -e /usr/local/libexec/scert-nginx && test ! -L /usr/local/libexec/scert-nginx &&
  install -o root -g root -m 0755 ./scert-nginx /usr/local/libexec/scert-nginx
test ! -e /etc/scert-nginx/policy.json && test ! -L /etc/scert-nginx/policy.json &&
  install -o root -g root -m 0600 ./nginx/policy.json.example /etc/scert-nginx/policy.json
id -u Scert
```

使用编辑器填写 `policy.json` 的 `allowed_uid` 为实际非 root 数字 UID，填写域名与现场地址，并核实 Nginx 路径。默认 `allowed_uid=null` 和域名占位符故意不可用，不能原样加载。Nginx 前缀使用规范绝对目录，不带结尾斜杠。应用 socket 固定为 `/run/scert-nginx/activation.sock`，不得改为 TCP。

独立复制当前证书链和私钥到 root 控制的暂存目录，例如 `/etc/scert-nginx/initial/`，目录 0700、文件 0600。先核对实际来源是普通文件、内容属于当前管理证书，再复制；初始化器不直接读取应用用户可写路径或符号链接。不要把密钥内容放入命令行、日志或文档。

```sh
/usr/local/libexec/scert-nginx \
  --policy /etc/scert-nginx/policy.json \
  --initialize \
  --certificate /etc/scert-nginx/initial/fullchain.pem \
  --private-key /etc/scert-nginx/initial/key.pem
```

初始化会核对输入权限、可信证书链、代理与上游实际叶证书、就绪响应及当前 `nginx -t`。通过后，在专用目录内生成随机版本子目录及 `active.conf`，私钥和引用为 0600。它不修改站点、不重载 Nginx、不创建轮换事务。目标目录只允许已有锁文件；任何旧版本、事务或失败残留均拒绝覆盖，失败后先检查，不盲目重试或清空目录。

## 站点接线

保留原站点备份，只把当前管理站点的 `ssl_certificate` 和 `ssl_certificate_key` 两行替换为下面的引用，不在同一层保留重复指令：

```nginx
include /var/lib/scert-nginx/active.conf;
```

继续保留 `Scert.conf.example` 的域名、回环 HTTPS 上游、SNI 和 `proxy_ssl_verify on`。root Nginx 主进程可以读取 0700 专用目录，不给工作进程或应用用户开放私钥权限。非 root 主进程不适用本模板，不能靠放宽权限解决。

先执行策略指定的 Nginx 二进制、前缀和主配置的 `-t`，成功后由维护人员单次平滑重载，再严格验证代理和上游证书及 `/readyz`。对公网域名另行验证正常 DNS 与未强制 IP 的浏览器访问；回环探测不代表公网通过。接线失败时按备份恢复原站点并核验，保留专用目录供排查。

## 安装并启用通道

确认 `/etc/systemd/system/scert-nginx.socket`、`.service` 不存在，且 `/run/scert-nginx` 为 root 所有且不可由其他用户写入。安装发布包中的模板，不覆盖既有服务：

```sh
test ! -e /etc/systemd/system/scert-nginx.socket && test ! -L /etc/systemd/system/scert-nginx.socket &&
  install -o root -g root -m 0644 ./systemd/scert-nginx.socket /etc/systemd/system/scert-nginx.socket
test ! -e /etc/systemd/system/scert-nginx.service && test ! -L /etc/systemd/system/scert-nginx.service &&
  install -o root -g root -m 0644 ./systemd/scert-nginx.service /etc/systemd/system/scert-nginx.service
systemd-analyze verify /etc/systemd/system/scert-nginx.socket /etc/systemd/system/scert-nginx.service
systemctl daemon-reload
systemctl enable --now scert-nginx.socket
systemctl status scert-nginx.socket --no-pager
```

服务由 socket 激活，不设置应用的 sudo、root 用户或额外能力。socket 为 root 所有、Scert 组、0660；服务还以内核对端 UID 核对策略中的唯一调用者。运行目录由 socket 创建，服务停止不移除它，不借用应用所有的 `RuntimeDirectory`。

模板对文件系统只读，仅开放专用状态目录及常见 Nginx 日志、缓存和已有 PID 文件。`CAP_DAC_OVERRIDE` 用于读取可信配置引用的受限材料以及打开工作用户所有的 Nginx 日志，`CAP_CHOWN` 用于 Nginx 配置检查中的路径所有者检查；写入仍受只读挂载及上述有限可写路径约束。辅助服务依赖 `nginx.service`，随其停止或重启，以便重建 PID 文件的隔离挂载；现场 Nginx 单元名不同时须同步调整 `Requires`、`After` 和 `PartOf`。非标准日志、缓存或 PID 路径须按现场最小范围评审 drop-in，不能直接放开 `/etc`、`/run` 或整个文件系统。不要使用私有网络命名空间，否则无法探测正式回环上游。

先完成隔离 systemd 测试，再验证真实调用 UID、非授权 UID 拒绝、辅助服务重启、主机重启、证书轮换、故障恢复和其他站点不受影响。仅有 socket 活跃、`nginx -t` 或核心 `/readyz` 成功都不等于双层协调验收。当前应用尚无独立管理协调就绪命令，完整流程仍须通过实际续期任务和两层证书证据核对。

## 升级与恢复

升级前等待应用协调与辅助事务均结束，停止相关变更任务，再停止 socket 和辅助服务。检查它们均已停止后才替换 root 所有的辅助程序，保留旧程序及校验摘要；不重复初始化、不清空版本或请求记录，重新启动 socket 后复验。

辅助程序状态与应用协调记录必须成套备份。现有应用完整备份不包含 `/etc/scert-nginx`、`/var/lib/scert-nginx` 和 Nginx 站点配置，不能声称只恢复应用备份就完成双层灾难恢复。停止变更并核对终态后，另行备份这些 root 文件、服务模板与 include 依赖；恢复时核对 UID、策略、应用证书路径、两层实际证书和事务记录一致。任何未决或损坏记录均保持保护，交由维护人员检查，不通过删除记录解除保护。

## 标准低端口部署的能力要求

当现有 Nginx 监听 80 或 443 等特权端口时，配置检查也可能执行端口绑定检查。独立激活服务的能力白名单因此包含 `CAP_NET_BIND_SERVICE`，同时保留 `CAP_DAC_OVERRIDE` 与 `CAP_CHOWN`。不要把该能力添加到 Scert 主服务，也不要取消 `NoNewPrivileges`、只读文件系统或本机 Unix socket 身份校验。

更新已部署的辅助服务模板后，先执行 `systemd-analyze verify`，再执行 `systemctl daemon-reload` 和 `systemctl restart scert-nginx.service`。须通过页面触发实际续期并核对结果，不能把 root 终端中单独执行 `nginx -t` 的成功当作受限服务运行成功。
