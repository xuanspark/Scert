# Linux 生产流程测试记录

## 范围与任务状态

2026-09-17 在管理机 `47.108.78.12` 执行当前版本的受控测试。`8.137.23.95` 仅为推送测试机，本记录不包含对它的操作。不运行固定安装器，不切换正式服务，不清空既有数据库，不修改共享 Nginx 或业务证书。

- [✓] 密码 SSH 登录并通过已有主机密钥校验。
- [✓] 核对管理机服务、工具、空间和健康接口。
- [✓] 独立干净克隆构建 Linux 包，本地与远程归档、测试程序 SHA-256 一致。
- [✓] PostgreSQL 18 第一轮超时后完成资源故障取证、残留集群停止及受限七场景重测。
- [✓] Linux Bootstrap 测试包及服务配置测试通过；修复地址族白名单后，候选程序加固启动、HTTPS 协议及重启失效检查通过。
- [✓] 初始化前三步的桌面浏览器检查、提示修复和 SMTP 输入框裁切复测通过；未完成整个初始化流程。
- [!] 授权 DNS 签发、初始化完成、正式切流和业务推送未验收。

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

## 桌面浏览器修复与最终候选复测

11:36 起继续使用独立本地配置和远端隔离目录验证，未清空业务数据库。浏览器测试连接本机 Windows 候选，Linux 实机测试通过 SSH 执行真实协议请求；二者不能混称为远程浏览器全流程验收。

修复与证据：

1. `4993199`：邮箱每 250 毫秒自动同步会清除全局错误，导致数据库未验证提示一闪即逝。自动同步现在保留错误，手动编辑仍可清除；新增回归先失败后通过。同步修复用户名与邮箱输入框上边缘错位，补齐数据库字段标识。
2. `f0c735a`：SMTP 固定四列在窄桌面裁切密码框，改用自适应网格，并补齐 DNS 明文复选框标识。1024×768 与 1440×900 的控件边界测量、实际截图检查通过。
3. `0bee64c`：真实浏览器发现数据库验证成功后仍显示此前的数据库步骤错误。只在验证成功时清除两类数据库提示，不清除密码等其他错误。新增三项回归后前端共 44 项全部通过；真实浏览器确认验证前错误持续显示、成功后错误消失且成功提示保留。

最终候选源码为 `0bee64ce8607831d025df8ea1ef52e39b57d026f`，从独立干净克隆构建，清单 `source_dirty=false`。本地归档为 `build/linux-db-message-20260917-1220/Scert-20260917-V0.1.0-linux-amd64.tar.gz`，SHA-256 为 `5a0b74167961cae3cbab9f7e47052df61ecf47c2c96584255c7c3842e470cb2a`。新 JS 为 `index-DYAdDhBP.js`，CSS 为 `index-B53m06GR.css`。

最终本地浏览器使用全新配置、数据库路径和 Chrome profile，监听 `https://127.0.0.1:19027`。先核对日志证书 SHA-256，再仅允许匹配 SPKI 的临时证书；没有全局忽略证书错误。页面实际加载新 JS，SQLite 在线写权限验证通过。两种桌面宽度下所有 SMTP 输入和下拉框均在标签与面板内，无整页横向溢出。截图保留于 `.runtime/browser-20260917-1223/smtp-1024.png` 和 `smtp-1440.png`，均已实际查看。此前版本的 SMTP/HTTP 必填提示与 DNS 缺凭据阻断也已检查；没有提交真实 DNS 凭据或发起 ACME 签发。浏览器另记录一条表单字段缺少标识的非阻断提示，未将其宣称为已解决。

最终 Linux 归档上传至 `/scert/acceptance-20260917-1044/release-message-0bee64c.tar.gz`，解压到独立 `message-0bee64c/release`。归档哈希、包内 12 项校验、安装器语法和程序版本检查通过。候选单元 `scert-message-0bee64c.service` 保留发布加固，限定 `MemoryMax=96M`、`MemorySwapMax=0`、`CPUQuota=25%`、`GOMEMLIMIT=48MiB`、`GOMAXPROCS=1`、`RuntimeMaxSec=900`，使用私有 `/run` 映射，不替换正式程序。

首次紧接启动执行协议脚本时未及时读到证书指纹而退出；候选就绪后重跑全部通过，没有为此放宽证书校验。通过项包括当前构建资源、严格 TLS 指纹、未登录 401、跨域兑换 403、一次性令牌不可复用、安全 Cookie、SQLite 写权限、重启证书与令牌轮换、旧会话失效及配置字节不变。脚本分别保留为远端 `linux_message_candidate.py` 和 `linux_message_protocol.py`。

约 12:22 收尾确认：最终 Linux 候选已停止、PID 为 0、19017 无监听；正式 Scert PID 仍为 `530794`、重启次数 0，MySQL PID 仍为 `549584`、重启次数 1；正式程序哈希不变。`MemAvailable` 约 320 MiB。正式 `/readyz` 仍为 `setup_incomplete`，共享机器的历史 OOM 和容量风险尚未解除。

## 尚未满足的生产准入条件

桌面浏览器前三步已部分验收，但合法授权域名与 RAM 的 DNS 写入清理、ACME 签发、初始化完成到正式入口的切换、登录与多用户、升级回退、目标机证书推送及最终业务访问仍未完成。本轮没有操作 `8.137.23.95`，没有改动 `pcux.cn` 及其子域。继续外部签发前需明确可写测试域名与凭据授权范围，SSH 密码不等同于 DNS 授权。正式服务仍返回 `setup_incomplete` 时不可正式切流；受控隔离测试通过不能替代上述验收。

## 账户边界修复与续测

12:30 至 12:50 继续审查用户管理、登录和修改密码，完成以下项目：

- [✓] `60b00f1` 修复用户管理可修改主超级管理员数据库行却不更新配置的问题。新增回归先复现用户名、邮箱、密码修改返回 200，以及新增第二个超级管理员返回 201，修复后均拒绝。
- [✓] 按已确认的单超级管理员需求，禁止通过用户管理新增或提权为超级管理员；禁止通过该接口修改主账号。既有历史账号不自动删除，不修改正式数据库。
- [✓] 主账号登录只接受配置凭据，数据库残留凭据不能取得随后立即失效的会话。保留持当前密码的自助改密；真实 SQLite 回归检查配置与数据库摘要一致、旧会话失效、旧密码拒绝、新密码登录及重建服务后的会话有效。
- [✓] 本地后端 HTTP API、仓库、认证、CLI、归档五个包回归通过；前端共 47 项测试通过。
- [✓] Linux 执行账户相关 9 个顶层测试及 5 个字段子场景全部通过，退出码 0。独立 systemd 单元限制 96 MiB、CPU 25%、无交换、单 Go 线程配额；运行 5.556 秒，峰值内存 17.7 MiB。测试程序 SHA-256 为 `24e779f320228b2c4afe2abcc5c781c2bcc0a490f502a6c49371d2a53a9226dd`。这是实机运行的自动化测试，不是生产账号全流程验收。
- [✓] 真实浏览器发现用户管理依赖父组件的作用域样式，弹窗实际落在普通文档流中且按钮无样式。`a3d0f6c` 在用户管理自己的 CSS 中补齐遮罩定位、表单、按钮和响应式约束。
- [✓] 最终发布资源使用本地只读界面夹具、独立 Chrome profile、端口 19028 验证。1024×768 和 1440×900 下弹窗为 620×439，8 个控件和按钮均在弹窗边界内；主账号没有编辑、禁用和删除按钮，新增角色没有超级管理员选项。两张修复后截图均已查看：`.runtime/users-browser-20260917/create-fixed-1024.png`、`create-fixed-1440.png`。夹具不是正式后端，不能据此宣称浏览器完整登录或写入通过。

最终候选从独立干净克隆构建，源码为 `a3d0f6c`，清单 `source_dirty=false`。本地归档 `build/linux-users-ui-20260917-1245/Scert-20260917-V0.1.0-linux-amd64.tar.gz`，SHA-256 为 `92b5d22b44a1b1a91ef6ba62eea9d48faebace26cc04e892aa230368e32f30ec`；资源为 `index-DsUM7ZTg.js` 和 `index-C7wT0wtw.css`。远程归档及包内 12 项校验通过，独立目录为 `/scert/acceptance-20260917-1044/users-ui-a3d0f6c/release`。

最终候选在发布 systemd 加固基础上使用私有运行目录、96 MiB 内存上限和 CPU 25% 配额，实际启动通过。严格 TLS 指纹、新构建资源、未登录 401、跨域 403、一次性令牌、安全 Cookie、SQLite 写权限、重启证书及令牌轮换、旧会话拒绝、配置字节不变全部通过。未发起 DNS 或 ACME 操作。辅助脚本为远端 `linux_users_ui_candidate.py` 和 `linux_users_ui_protocol.py`。

12:50 收尾确认：本轮两个 Linux 候选单元已停止，19017 无监听；本地夹具及专用 Chrome MCP 已停止，SSH 已关闭。正式 Scert PID `530794`、重启次数 0，MySQL PID `549584`、重启次数 1，正式程序哈希未变；正式 `/readyz` 仍为 `setup_incomplete`，可用内存约 313 MiB。升级回退、真实 DNS 签发、完整初始化与切流、目标机推送及业务验收仍未完成。原有测试产物全部保留。

## 安装失败回退修复与发布包续测

12:54 至 13:11 审查安装升级流程，修复提交为 `8c3d8786808cd142229af4b1c6422e9d638aa0c0`。修改前检查点 `9d3bd50` 和修复提交均已推送。没有在宿主机直接运行固定路径安装器，没有停止或替换正式服务。

### 缺陷与修复

旧包在真实 Linux 文件系统上复现四类问题：既有 Web 符号链接在二进制已经替换后才拒绝；二进制目标为目录时错误返回安装成功；第二个 unit 写入失败与 daemon-reload 失败均留下新旧发布文件混用。旧包四场景测试整体退出码为 1，不是成功验收。

新安装器提前检查文件和目录类型，保留 root 所有、`0700` 的 `/scert/.install-*` 副本；捕获安装失败后恢复发布文件并重新加载 systemd，不启动服务。Web 改为整目录替换，旧文件和失败现场只移动保留、不删除。回退自身失败时明确提示禁止启动服务。业务目录权限规范化、用户创建、断电和 SIGKILL 不在自动回退保证内，安装成功后的数据库迁移也不由此机制恢复，详见运维说明 4.2。

### 隔离方法与结果

使用仓库 `deploy/tests/install-linux-test.py`，在私有挂载命名空间中将新建测试目录映射到固定的 `/scert`、`/etc/systemd/system` 和本机既有 `/ssl-scert-acceptance`。源码以只读挂载保留，执行前核对挂载 inode。systemctl 被限定为测试桩，只允许状态检查和 daemon-reload，不能操作宿主服务。使用已有 Scert 用户，不创建新的宿主账号。

第一版脚本因宿主 `/ssl` 不存在在预检阶段退出，未执行安装器；随后显式指定已有证书挂载点，并把产物放在被覆盖路径以外的 `/var/tmp/scert-installer-20260917-1303`。所有产物保留。受控单元限制 `MemoryMax=96M`、`MemorySwapMax=0`、`CPUQuota=25%`、`TasksMax=32`、`RuntimeMaxSec=180`，与候选服务顺序执行。

最终发布包八场景均通过，整体退出码 0：

| 场景 | 安装器预期退出码 | 最终产物子目录 |
| --- | --- | --- |
| Web 符号链接拒绝 | 1 | `web-link-y4ivfj_n` |
| 二进制目标目录拒绝 | 1 | `binary-directory-o3gvmqpe` |
| unit 写入失败后恢复旧版 | 43 | `unit-failure-4c4c8ou_` |
| reload 失败后恢复旧版 | 42 | `reload-failure-bffej3sp` |
| 回退 reload 再失败，明确禁止启动 | 42 | `rollback-reload-failure-qrwm_v37` |
| 升级成功，旧 Web 资源退出生效目录 | 0 | `success-cgz9m71a` |
| 首次安装失败，发布文件回到缺失状态 | 43 | `fresh-failure-y_2axidy` |
| 首次安装成功 | 0 | `fresh-success-e7foazs_` |

每场景检查业务文件内容与证书文件不变、旧发布文件恢复、服务文件恢复或缺失状态、升级副本权限及服务操作白名单；各目录包含 `report.json` 和 `output.txt`。这是隔离文件安装与故障注入回归，不能等同于真实 systemd 升级、正式数据库迁移或完整业务回退验收。

### 新包与通用检查

- 独立干净克隆：`C:/Users/ikun/AppData/Local/Temp/scert-installer-20260917-1306`，源码 `8c3d878`，清单 `source_dirty=false`。
- 发布包：`build/linux-installer-20260917-1306/Scert-20260917-V0.1.0-linux-amd64.tar.gz`。
- SHA-256：`9c89140467264ee620bcd1ab4ba4fcea6a43430a4ea87fa6c7f91fa3be97c6ea`。
- 远程：`/scert/acceptance-20260917-1044/installer-8c3d878/release`；归档哈希、包内 12 项校验、安装器语法及版本检查通过。
- Windows 本地执行 `go test ./...`、`go test -race ./...`、`go vet ./...`、`go mod verify` 全部通过；前端 47 项测试通过。不能将这些表述为 Linux 全量测试。
- `govulncheck ./...` 及详细扫描退出码 0：调用符号和导入包无已知漏洞，依赖模块包含 `GO-2026-5932`，涉及未维护的 `golang.org/x/crypto/openpgp`，当前代码没有导入或调用该漏洞包，公告没有可用修复版本。不能写成依赖模块零漏洞。
- 新候选 `scert-installer-8c3d878.service` 使用发布加固、私有运行目录和 96 MiB 限制启动；严格证书指纹、当前资源、未登录 401、跨域兑换 403、令牌不可复用、安全 Cookie、SQLite 验证、重启证书与令牌轮换、旧会话失效和配置字节不变均通过。没有 DNS 或 ACME 操作。本轮不涉及页面修改，未重复浏览器渲染测试。

13:09 收尾检查：候选服务 inactive、PID 0、19017 无监听；正式 Scert PID `530794`、重启次数 0，MySQL PID `549584`、重启次数 1，正式程序 SHA-256 未变。可用内存约 314 MiB。正式回环端口 9004 使用 HTTP，先用 HTTPS 探测返回协议错误，改用实际 HTTP 后 `/readyz` 明确返回 `setup_incomplete`；这不是正式 TLS 验收。SSH 已关闭，所有测试进程结束。

- [✓] 安装器缺陷复现、修复、新发布包八场景复测及受限候选协议检查。
- [✓] 本地全量测试、race、vet、模块完整性及漏洞调用路径扫描。
- [!] 真实 systemd 升级与业务回退、DNS 签发、完整初始化、正式切流和目标机推送仍未完成。继续外部签发需要用户明确授权测试域名及 DNS 凭据范围，当前不可宣称具备正式切流条件。

## 安装前停服状态核验补充（2026-09-17）

### 现场恢复与需求

- [✓] 原工作目录 `D:/Desktop/SSL` 已不存在；当前项目在 `D:/Desktop/Scert`，起始提交仍为 `cf1fe15`，已跟踪文件无改动。未移动、删除或覆盖既有未跟踪产物。
- [✓] 修改前创建并推送中文检查点 `f04f873`。
- [✓] 管理机只读复核：正式 Scert PID `530794`、重启次数零；MySQL PID `549584`、重启次数一；候选服务停止；正式程序摘要与前轮一致。
- [!] 正式 `/readyz` 仍为 HTTP 503、`setup_incomplete`。管理机 443 由 Smail 占用，最终入口方案需要协调，不可直接占用或停止该服务。

### 缺陷与复现

旧安装器将 `systemctl is-active` 的所有非零退出都当成已停服；不能区分过渡状态和查询故障。使用旧包 `8c3d878` 在私有挂载命名空间注入启动中、停止中、主服务查询失败和管理服务查询失败，四个场景都错误返回零并替换隔离目录中的发布文件。

首次测试因产物目录不存在在预检阶段退出，没有执行安装器；创建全新产物目录后完成上述四场景复现。证据目录 `/var/tmp/scert-install-state-before-20260917-1530`，测试整体退出码一。路径中的时间后缀仅为标识，不代表实际执行时刻。

### 修复与验证

安装器改为显式检查六个 systemd 属性，未知状态和查询失败均拒绝。回归覆盖原八场景以及 active、activating、deactivating、reloading、failed 但仍有进程、排队任务、masked、首次不存在、failed 且已停服、查询失败、空输出、缺失字段和管理服务拒绝分支，共 22 场景。

- [✓] 本地 `go test -count=1 ./internal/admin` 通过，`git diff --check` 通过。
- [✓] 使用旧发布文件的独立副本加上新安装器，Linux 22 场景全部通过；退出码零，运行约 33.6 秒。
- [✓] 拒绝路径对整个安装目录快照（内容、类型、权限、所有者）不产生变化，unit 和证书保持不变，未执行 daemon-reload。
- [✓] 其余场景继续验证首次安装、旧资源退出生效目录、失败发布文件恢复、回退失败提示以及业务文件保护。

产物 `/var/tmp/scert-install-state-after-20260917-1535`。测试通过 systemd 临时单元限制内存 96 MiB、无 Swap、CPU 25%、32 任务、240 秒；systemctl 在安装器命名空间内仍为状态/故障注入桩。宿主 systemd 仅管理测试资源，正式 Scert、MySQL 和 Smail 不停止、不重启。

这证明安装器的状态分支和文件行为，不证明完整真实 systemd 升级、业务回退或最终发布包已验收；最终包验证需单独补记。未使用 DNS、ACME 或业务数据库。
