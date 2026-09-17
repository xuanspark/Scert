# Linux 生产流程测试记录

## 范围与任务状态

2026-09-17 在管理机 `47.108.78.12` 执行当前版本的受控测试。`8.137.23.95` 仅为推送测试机，本记录不包含对它的操作。不运行固定安装器，不切换正式服务，不清空既有数据库，不修改共享 Nginx 或业务证书。

- [✓] 密码 SSH 登录并通过已有主机密钥校验。
- [✓] 核对管理机服务、工具、空间和健康接口。
- [✓] 独立干净克隆构建 Linux 包，本地与远程归档、测试程序 SHA-256 一致。
- [✓] PostgreSQL 18 第一轮超时后完成资源故障取证、残留集群停止及受限七场景重测。
- [✓] Linux Bootstrap 测试包及服务配置测试通过；修复地址族白名单后，候选程序加固启动、HTTPS 协议及重启失效检查通过。
- [!] 真实浏览器、授权 DNS 签发、初始化完成、正式切流和业务推送未验收。

## 发布来源

候选源码提交：`8990710ee145809eb3a937d3b4eb578adbdeb3fe`。发布清单 `source_dirty=false`，两个程序均内嵌相同提交及 `vcs.modified=false`。构建工具链为 Go `1.26.6`，目标 `linux/amd64`，`CGO_ENABLED=0`。

本地发布目录：`build/linux-production-20260917-1040/`。远程独立目录：`/scert/acceptance-20260917-1044/`。前端 39 项自动化测试通过，依赖安装审计为零漏洞；这些不是浏览器验收。

| 产物 | SHA-256 |
| --- | --- |
| `release.tar.gz` | `3eee5a62403d6dadcc1f36e105de8cfa1684f65b688af7a1e898c7269e475344` |
| `cli.test` | `7bf731c5dade9ceb24f385dd0bb4c54d1809e3e6a9400c710ecfe871c9de1d1f` |

本地发布清单 14 项与包内校验清单 12 项通过。远程两项上传文件哈希及解压后的 12 项校验通过，安装器 `bash -n` 通过，候选程序返回 `Scert 0.1.0`。归档成员清单只含相对路径下的普通文件及目录，可执行程序和安装器为 `0755`。

## 实机初查

10:43 至 10:44，通过可信 SSH 通道获得：

- Linux `7.0.0-29-generic`，`x86_64`，根分区剩余约 28 GiB。
- `Scert.service` 为 `active/running`，用户 `Scert`，PID `530794`，监听 `127.0.0.1:9004`。
- 已有 PostgreSQL `18.6` 官方工具，既有数据库端口 `5432` 不作为测试目标。
- `/healthz` 为 HTTP `200`；`/readyz` 为 HTTP `503`，原因 `setup_incomplete`。
- 正式 ACME 目录正常 TLS 校验下为 HTTP `200`，TCP 约 `0.206` 秒、TLS 约 `0.484` 秒。此前网络超时本次未复现，但目录可达不代表签发通过。
- 在用 `/scert/scert` SHA-256：`ce2438fdddb1e471184d42ec98374abe1e96f74d3687ec60ad493eb659d5eb43`。

## 测试执行与异常

远程测试命令先在新目录解压，执行包内校验、安装器语法检查、版本检查，然后以 `Scert` 用户运行 `TestPostgresRealBackupRestore`。测试使用 `/usr/lib/postgresql/18/bin`，产物根目录为 `/scert/acceptance-20260917-1044/postgres-artifacts`，测试超时为五分钟。

该测试自行创建回环随机端口集群、每场景全新数据库、SCRAM 密码和严格 TLS；不接受业务 DSN，完成时停止自己的集群并保留产物。

10:50 左右尚未收到测试命令结果，另建 SSH 连接在协议 banner 阶段超时。约 10:53 原连接收到测试退出码 `2`：成功恢复与证书失败回滚场景打印检查通过；第三个 `cancel` 场景尚在初始备份清单检查时触发总超时，栈位于 `validateBackupManifest` 的 `Lstat`，还未进入取消注入阶段。其余四场景未执行。不能将该结果归因为取消回滚逻辑故障，也不能报告整轮通过。

测试产物：`/scert/acceptance-20260917-1044/postgres-artifacts/scert-postgres-integration-2406225956/`。总超时导致测试清理回调不保证执行，随后只读检查请求也出现 `Timeout opening channel`。已请求仅停止该目录中的隔离集群，停止结果仍待确认。未向既有 PostgreSQL 服务或正式 Scert 发送停止命令。

恢复连接后必须先核对本轮测试进程、`postmaster.pid`、端口、系统负载及原服务状态，再决定是否重试。保留所有产物，不重跑解压命令覆盖已有目录，不在连接不稳定时继续启动额外测试负载。

## 资源故障恢复与受限重测

以下为 11:04 之后的续测结果，补充并更新上节当时尚未确认的状态。

- 主机约 1607 MiB 内存、无 Swap。内核在 11:00:20 记录全局 OOM，MySQL 被杀死；同时存在 journald、logind watchdog 超时。该证据确认系统发生内存故障，但不能仅凭受害进程或单次堆栈认定某个测试是唯一来源。
- MySQL 自动恢复为 PID `549584`，重启计数为 1；正式 Scert PID `530794` 未变。未重启这些服务，未调整 Swap、MySQL 配置或其他项目的资源设置。
- 核对首轮隔离集群目录、PID `549227` 及端口 `44387` 后，使用该集群的 `pg_ctl -m fast -w -t 30 stop` 正常停止；数据库与测试产物保留。既有 PostgreSQL 端口 `5432` 未操作。
- 测试辅助配置降低至 `shared_buffers=16MB`、`work_mem=1MB`、`maintenance_work_mem=16MB`、`max_connections=32`，禁用并行 worker。该变更只作用于新建隔离集群，不调整业务 PostgreSQL。
- 使用独立 systemd 临时单元串行运行：`MemoryMax=180M`、`MemorySwapMax=0`、`CPUQuota=50%`、`TasksMax=64`、`RuntimeMaxSec=300`、`KillMode=control-group`、`OOMPolicy=stop`，并设置 `GOMEMLIMIT=32MiB`、`GOMAXPROCS=1`。这些是本轮小数据演练参数，不是生产容量建议。
- 11:08:00 至 11:08:07，七个真实场景全部通过，用时 7.23 秒，单元退出码为 0。产物目录为 `postgres-bounded/scert-postgres-integration-710949348`；测试程序 SHA-256 为 `634666af045474b7dcec8f4b6c9eb325ef3fd95a28b628a2c388bbd304472f2e`。
- 此后内核另有其他项目测试单元的 `CONSTRAINT_MEMCG` OOM，不能记为本轮 Scert 测试或新的全局 OOM。共享机器仍需协调测试负载，不能因本轮通过就认为容量问题已消失。

## 加固启动缺陷与新候选

旧候选在保留地址族白名单的真实 systemd 环境中启动失败：`route ip+net: netlinkrib: address family not supported by protocol`。原因是初始化临时证书读取网卡地址需要 `AF_NETLINK`，原 unit 只允许 `AF_UNIX AF_INET AF_INET6`。此前单元测试只校验原有文本，未发现实际系统调用受限。

修复提交 `5573c64b209eae391c77f2a476ccc6784fd5b2e2` 同步更新生成式 unit 和 `deploy/systemd/Scert.service`，仅增加 `AF_NETLINK`；新增发布 unit 的安全属性断言。主服务仍为 `Scert` 用户，capability 集为空，保留 `NoNewPrivileges` 和其他加固；超级管理员恢复服务仍限制为本地 IPC，未增加网络地址族。

新候选从独立干净克隆构建：

- 本地：`build/linux-netlink-20260917-1119/Scert-20260917-V0.1.0-linux-amd64.tar.gz`。
- 远程：`/scert/acceptance-20260917-1044/release-netlink/`。
- 归档 SHA-256：`d592bf5753e88326de9b7ab7986338f07e92028743e75f4c1a9e9e2eb2f8a544`。
- 清单 `source_dirty=false`，程序内嵌 `vcs.modified=false` 和上述提交；本地清单、远程上传哈希及包内 12 项校验通过，安装器语法检查通过。
- 本地 CLI 与 servicehost 回归通过；Linux 原 Bootstrap 测试包通过，峰值内存约 16 MiB。新增 servicehost Linux 测试（含发布 unit 断言）也全部通过。Bootstrap 测试内部的 DNS/签发替身不代表真实外部签发。
- 11:22:21 至 11:22:27，使用新候选 CLI 再跑七个 PostgreSQL 真实场景，全部通过，用时 6.91 秒，退出码 0；新产物为 `postgres-bounded/scert-postgres-integration-1008171600`。取消场景仍采用测试程序进程内确定性注入，其他 CLI 场景使用新包中的可执行文件。
- 本地追加执行 Bootstrap、servicehost、管理员恢复和归档模块回归，全部通过；`git diff --check` 无格式错误。

## 候选程序实际协议检查

使用新候选程序、全新隔离配置和 SQLite，监听 `127.0.0.1:19017`，保持非 root 与系统加固。由于 Linux 初始化地址固定写入 `/run/scert/bootstrap-url`，测试单元使用私有 `/run` 及 `BindPaths` 将独立目录映射到该路径，不写正式运行目录。不运行固定安装器、不替换 `/scert/scert`。

通过项目：

1. 从可信 SSH 日志取得当前启动证书指纹，与实际 TLS 证书匹配；之后所有业务请求严格验证证书及 IP。探测证书时不发送令牌、Cookie 或业务请求。
2. 初始化页面和构建 JS/CSS 返回有效内容；这只是协议与资源检查，不是浏览器渲染验收。
3. 未登录的设置接口返回 401；跨域令牌兑换返回 403；正确令牌兑换成功后重复兑换返回 401。
4. Cookie 具有 `Secure`、`HttpOnly`、`SameSite=Strict`，路径限定为 `/api/v1/bootstrap`；令牌文件为 `0600`，URL 查询参数不携带令牌。
5. SQLite 写权限验证通过；缺少 DNS 令牌返回 400，随机无效 DNS 令牌返回 422；配置字节不变，不伪造初始化完成。
6. 重启本轮候选单元后，临时证书和一次性令牌均轮换，旧令牌及旧 Cookie 返回 401，新令牌可兑换。
7. 停止本轮候选单元后，端口不再监听，程序正常清理自己的初始化地址文件。隔离配置、密钥及测试产物保留在受限目录，禁止提交 Git。

协议检查脚本保留在远程 `bootstrap_protocol_netlink_v2.py`，不含固定密码或 Token；测试使用的临时令牌和随机管理员密码仅在内存中处理。第一次脚本把缺少 DNS 参数的预期码误写为 422，修正为 400 并单独增加无效令牌 422 检查后整轮通过，未因此修改应用校验规则。

11:24 收尾确认：没有本轮 PostgreSQL 进程残留，`19017` 无监听；正式 Scert 仍为 PID `530794`、重启次数 0，MySQL 仍为 PID `549584`、重启次数 1；正式程序 SHA-256 与初查一致。正式就绪接口仍报告 `setup_incomplete`。当时 `MemAvailable` 约 215 MiB，主机容量风险仍需持续监控。

## 尚未满足的生产准入条件

真实浏览器、合法授权域名与 RAM 的 DNS 写入清理、ACME 签发、初始化完成到正式入口的切换、目标机证书推送及最终业务访问仍未完成。本轮没有操作 `8.137.23.95`。正式服务仍返回 `setup_incomplete` 时不可正式切流；受控隔离测试通过不能替代上述验收。
