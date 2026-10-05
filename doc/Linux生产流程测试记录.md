# Linux 生产流程测试记录

## 2026年10月4日 Linux 上线整改接手记录

### 任务状态

- [✓] 已确认本轮范围为 Linux Web 与服务端，不处理 Windows。
- [✓] 已确认检查点 `f5c425f`、候选包路径和归档哈希，并完成第一台部署后的服务恢复核验。
- [✓] 第一台 `8.137.23.95`：`Scert.service` 和 `Scert-storage.socket` active；Scert 仅监听 `127.0.0.1:8443`，MySQL 仅监听回环地址，Nginx 对外提供 `80/443`。
- [!] 第一台 SMTP 配置仍关闭；尚未完成系统邮件、验证码送达、受保护终端密码重置和新密码登录的真实闭环。
- [ ] 第二台 `45.136.13.241`：正式证书推送、Nginx 配置检查、重载、失败回滚和恢复测试尚未执行。
- [ ] 三数据库首次 Web 初始化、重新登录持久化、DNS/ACME 正负向测试、公共 HTTPS 与证书恢复验收尚未完成。
- [!] 第一台历史 `recovery_required` 事务继续按人工处置边界保留，禁止自动接管、删除锁或伪造恢复成功。
- [!] 本记录当前不能作为生产验收通过证明；构建、单元测试或 Smail 页面 HTTP 200 仅属于对应证据层级。

### 候选包证据

- 路径：`release\production-candidate-20261004-f5c425f\Scert-20261004-V0.1.0`。
- 归档 SHA-256：`38fb6986548e3c95295d5ff37b0141e54b2e142798384566a4464018e582f4ef`。
- Manifest：`source_commit=f5c425f4b60fc3c1308ab86d859511868e08e4ff`，`source_dirty=false`，`trimpath=true`，`local_path_check=passed`，目标 `linux-amd64`。
- `web.cdis.cn` 当前返回 Smail 页面，仅证明管理入口可访问，不证明 Scert SMTP 认证、发信或收件箱送达。

## 2026年10月4日11:07 第一台终态复核与本地权限回归

- 第一台 `scert-dns-lease-review-20261004-0757.service` 实查 inactive/dead、Result=success、ExecMainStatus=0，日志结束于 08:04:50。使用源码属主 nobody 读取 Git，提交 `37765ff`、工作区干净；没有关闭 Git 所有权保护。
- 日志 `/var/tmp/scert-dns-lease-review-20261004-0757/review.log`：全量和 vet 已完成；单轮三库租约专项仓储 24.044 秒、数据库 40.830 秒、证书 3.095 秒、HTTP 3.372 秒。MySQL 排序规则故障夹具修复后的七种迁移故障及 PostgreSQL 实际通过。历史 `0743` 失败不改写。
- 最新邮件权限增量目前仅本地回归：HTTP 26.375 秒、通知 0.858 秒、应用缓存通过，三包 vet 通过；前端 350 项零失败零跳过，Vite 构建 103 模块。首轮新增测试因夹具未设 DNS 已验证失败，修正后通过；未改产品验证规则。
- 域名页面保留给用户填写系统通知凭据，仅填非秘密字段，未保存、未发送；TLS SMTP NOOP 探测不等于认证和发信成功。
- 尚未发布最新产物、完成真实 DNS 签发续期或生产总验收。本轮未操作第二台、业务数据库和用户浏览器输入。

## 2026年10月4日07:40 执行租约复审准备

### 07:55 第一台终态与故障夹具修正

- `scert-dns-leases-20261004-0743.service` 于 07:50:38 结束，failed、退出一。固定 `f17ea6c` 的 Linux 全量、vet 通过；三库租约及原子绑定单轮竞态通过：SQLite 3.19 秒、MySQL 16.54 秒、PostgreSQL 2.83 秒，均实际执行；仓储包 23.616 秒。源码工作区仍干净。
- MySQL 迁移专项 38.94 秒失败，唯一失败为 bad-collation：原夹具设置 utf8mb4_unicode_ci，而隔离库本就使用该值，没有制造冲突。其余五个边界通过；证书 2.639 秒、HTTP 3.345 秒专项通过，整轮不计成功。
- 已将故障注入改为与实际规则不同的限定值并验证生效，另加列级排序规则冲突；产品迁移校验逻辑未放宽。检查点 `eab5ba9` 已推送，修正及授权探测增量待第一台新提交复审。

### 07:51 运行状态与授权探测增量

- 第一台干净 `f17ea6c99e3bd4472813853294347bdfad0b8b17` 已于 07:43:33 运行 `scert-dns-leases-20261004-0743.service`；观察 07:49:16 为 active/running，已进入 MySQL 租约专项，仍待最终结果。
- 源码 `/var/tmp/scert-dns-leases-20261004-0743/source`；日志同级 `review.log`。bundle 双端 SHA-256 一致：`3b362fd536022ccefcf1ac808a80fb750e83921e92e29f17e8c2e4ca3b84f2ef`。首次按 main 读取 bundle 失败，核对实际只含 HEAD 后成功导入，不改写验证结果。
- 授权探测增量在本地单独验证：证书全量 6.524 秒、HTTP 26.459 秒、app 11.122 秒及 vet 通过；独立账户十轮专项 8.896 秒通过。后端 761、前端 119 个已跟踪源码换行检查通过。
- 原 `0743` 单元不包含授权探测增量，必须追加固定新提交复审；未因观察等待重启任务。

- 修改前检查点 `7a3b78d` 已提交推送，之后补齐完整配置快照、底层域名租约保护及接口冲突回归。
- 本地 `go test ./internal/repository ./internal/certificate ./internal/database ./internal/httpapi ./internal/app` 和同范围 vet 通过；仓储 8.790 秒、HTTP 26.759 秒、app 11.943 秒，certificate/database 为缓存结果。
- 租约专项十轮通过，原子创建及租约冲突接口专项五轮通过；前端重新执行 339 项，零失败零跳过，构建 103 模块通过。
- 新增 SAN 等价编码用例首轮失败，已纠正夹具保持实际 SAN 后通过；这是测试数据错误，产品未放宽校验。本地 MySQL、PostgreSQL 未启用而跳过，不计三库证据。
- 第一台 SSH 于 07:37 实时验证可用；前轮 `scert-dns-ui-20261004-0630.service` 为 inactive/dead、success、退出零。本轮尚未运行远端测试，不复用前轮结果冒充新代码通过。
- 本批未清理正式数据库、未升级正式服务、未改域名或发信配置、未操作第二台。后续用第一台隔离源码及数据库验证，不创建辅助验收脚本。

## 2026年10月4日06:51 DNS 页面权限干净源码复审

- 第一台 `8.137.23.95`，源码 `/var/tmp/scert-dns-ui-20261004-0630/source`，真实 Git 干净签出 `412c51f5f0cf466f9d1ff01a09e23dd02e116385`；运行后工作区仍干净。
- 最后增量 bundle 本机及第一台 SHA-256 一致：`fc7441a3792813358c12b690ba5eb0a1b0d4cfcfee9ab2c3ed0bb3cb970cd47c`。原等待密码的 SCP 连接关闭，重新密码认证上传成功，未据观察超时重启测试。
- `scert-dns-ui-20261004-0630.service` 从 06:39:22 至 06:45:57，终态 inactive/dead、Result=success、ExecMainStatus=0。使用 nobody、内存 1200M、CPU 150%、本机离线工具链，不创建辅助脚本。
- `go test -p 1 -count=1 -timeout=10m ./...` 及 `go vet -p 1 ./...` 通过。该全量未启用外部数据库，不能据此声称所有三库用例执行。
- 随后开启隔离 MySQL/PostgreSQL，以 `-race -p 1 -count=1 -timeout=10m -v` 执行仓储和 HTTP 的 `Test(ProjectDirectory|ManagedDNSAccount|AuthSessionsSupportOptionalEmail|AdminAPIRejectsAuthenticatedRegularUser)` 专项；仓储 17.076 秒、HTTP 23.331 秒。SQLite 目录 1.26 秒、MySQL 12.88 秒、PostgreSQL 1.90 秒均 PASS，不是跳过或三轮结果。
- 完整日志 `/var/tmp/scert-dns-ui-20261004-0630/review.log`；本轮不清理业务数据库、不升级正式服务、不操作第二台及发信配置。
- 后续域名选择前端增量：本地相关 46 项、全量 336 项及 Vite 构建 103 模块通过；尚无最新页面的正式部署与浏览器证据，不能将本节后端结果套用为新增页面的实机验收。

## 2026年10月4日06:28 磁盘故障终态、绑定复跑和页面开发

### 第一台绑定修复实库结果

- 原 `scert-domain-dns-20261004-0555.service` 已 failed/exit-code、退出一；全量与 vet 完成，三库阶段出现 `no space left on device`。06:08 现场根分区 100%，仅余 22 MB；不能把该任务算作通过。
- 已确认 `0538`、`0555`、`0425` 及相关 `0430/0445/0458` 单元均终止，进程列表仅有生产 mysqld、无隔离服务。只删除 `/var/tmp/scert-domain-dns-20261004-0538/mysql` 和 `/var/tmp/scert-dns-accounts-20261004-0425/mysql-race`、`mysql-review` 下已停止实例的 `data`；先解析绝对路径并匹配固定允许目录，保留日志、源码、工具链及生产数据，清理后可用 3.5 GB。未触碰第二台。
- 新单元 `scert-domain-dns-20261004-0610.service` 复用 `0555/source` 中 `c80530f` 的真实 Git 干净签出；隔离数据库使用新 `0610/mysql`、`0610/postgres` 目录，仅监听回环并严格 TLS。没有创建辅助验收脚本，也没有重复全量。
- 06:17 实查 `ActiveState=inactive`、`SubState=dead`、`Result=success`、`ExecMainStatus=0`。单轮 `go test -race -p 1 -count=1` 专项四包通过：数据库 81.666 秒、仓储 37.304 秒、证书 1.032 秒、HTTP 22.561 秒，包含 MySQL 历史排序规则兼容和拒绝错误列定义。日志 `/var/tmp/scert-domain-dns-20261004-0610/race.log`。不表述为三轮通过。

### 页面增量本地回归

- 修改前检查点 `7f7b084` 已推送。独立 DNS 页面增加创建、改名、启停、轮换、在线验证及项目能力提示，使用已实现 API；凭据无持久化和回填，冲突与网络异常不重放写请求。
- 新增 24 项前端专项；前端全量 310 项及 Vite 生产构建通过。后端 HTTP 全包 27.129 秒、仓储全包 7.944 秒及两包 vet 通过；本地外部库跳过不计实库验证。
- 首次后端回归两项旧测试要求普通用户不可读服务商目录；新契约允许项目用户读取公共字段定义。已更新目录测试，并将原管理员拒绝测试改为实际系统 DNS 在线写操作，保留权限负向覆盖；复跑通过。
- 本批第一台 Linux 干净源码复审及浏览器验证尚待完成。正式服务与邮件配置没有修改；域名表单、执行租约、真实签发续期、旧导入、安全回收及生产验收仍未完成。

## 2026年10月4日05:54 绑定接口及 MySQL 实库失败修复

### 06:00（UTC+8）修复版原任务仍在运行

- `c80530f` 已提交推送；增量 Git bundle 在本地与第一台 SHA-256 一致，为 `bbd73beada9574297b3a19c5a236a10501c96fcf9984e521dfb5b8d071acb9a4`。
- 初次以 root 从 nobody 所属源码仓库克隆被 Git 所有权保护拒绝；未关闭全局保护，改为该源码所属 nobody 用户克隆和检出成功，工作区为空。
- 单元 `scert-domain-dns-20261004-0555.service`，目录 `/var/tmp/scert-domain-dns-20261004-0555/source`，先执行全量和 vet，随后单列隔离 SQLite、MySQL、PostgreSQL 三轮专项竞态，包含新增绑定 API。日志分别为同级 `full.log` 与 `race.log`。
- 最新实查仍 active/running，终态未知；全量日志正在推进，不能使用运行中的 `Result=success` 作为通过结论。下一轮继续查询本单元，不重启或覆盖源码。

### 原任务与本地修复证据

- 第一台 `scert-domain-dns-20261004-0538.service` 使用真实 Git 干净提交 `7a6da6b`；实查 `ActiveState=failed`、`Result=exit-code`、`ExecMainStatus=1`。原目录 `/var/tmp/scert-domain-dns-20261004-0538` 的 `full.log` 记录全量及 vet 通过，`race.log` 记录三库专项失败，不重写日志。
- 三轮数据库、仓储 MySQL 用例均报 3780：新 `domain_id` 与历史 `domains.id` 字符排序规则不兼容；数据库 214.109 秒和仓储 96.024 秒失败，证书 1.036 秒、HTTP 32.665 秒通过。整体不能标为成功。
- 修复读取历史主键字符集和排序规则，应用于新表外键列；严格重入校验支持该列专用规则，不修改父表，不取消约束。新增默认值变更及错误列排序规则两种实库用例，等待复跑。
- 域名绑定 API 已加入读取、修改和解除，覆盖身份、同源、缓存、严格参数、过期版本、账户状态、任务占用、依赖异常和审计失败回滚。本地 HTTP 全量 31.842 秒、数据库全量 27.797 秒及 vet 通过；首次新增夹具编译错误已修正，本地外部库跳过不计实库证据。
- 本批没有升级服务、改 DNS、清业务数据、创建辅助验收脚本或操作第二台。执行租约和签发链未接通，P1-03 及整体生产验收仍未完成。

## 2026年10月4日05:34 域名账户绑定与干净源码回归

- `scert-dns-clean-20261004-0514.service` 使用 `1dc72f2` 的真实 Git 干净签出，源码 `/var/tmp/scert-dns-clean-20261004-0514/source`，日志 `full.log`；已 failed、退出一。仓储并发用例发现 SQLite 锁错误被更新入口包装成凭据重复错误；后续 vet、竞态因前置失败未执行，不计为通过。
- 修复只按 SQLite 驱动码识别忙及锁冲突并返回版本冲突；保留真实结构与凭据错误的失败，不泄露原始驱动文本。补主错误码、扩展码和非锁拒绝单测，原并发成功数量与审计数量断言不变。
- 绑定测试首轮夹具漏填 `tasks.message` 被非空约束拒绝；补齐测试字段，不修改生产约束。随后本地数据库 25.410 秒、仓储 9.620 秒、证书 4.847 秒、HTTP 29.070 秒全包通过；追加状态用例及分类测试后二十轮仓储专项 3.914 秒、四包 vet 通过。
- 新增三库迁移二十八、同项目绑定与引用保护、SQLite 四种外键/递归开关、MySQL 十种中断及结构冲突用例；本地外部数据库跳过不能作为实库验收。待当前代码提交后在第一台运行干净源码专项及全量。
- 本批未升级正式服务、未修改公共解析、未清理业务数据、未操作第二台；旧账户导入、绑定接口、执行租约和真实云端签发仍未验收。

## 2026年10月4日05:04 DNS 多账户数据层及管理接口

本节按运行环境 UTC+8 记录。第一台只执行独立数据库和源码目录测试，未升级正式服务；第二台未操作。没有清理生产数据、新增辅助验收脚本或制作远端备份。

### 首轮及失败记录

- `scert-dns-accounts-20261004-0425.service`：已成功终止，三库迁移、基础仓储、MySQL 八种中断/结构冲突及 vet 通过。源码目录 `/var/tmp/scert-dns-accounts-20261004-0425`，日志 `dns-accounts.log`。
- `scert-dns-accounts-race-20261004-0430.service`：实查 failed、退出一；数据库三轮竞态通过，185.274 秒；仓储 SQLite/PostgreSQL 通过，MySQL 故障触发器创建三轮均报 1419。修复只让隔离库的故障注入使用测试管理连接，仓储仍使用受限业务连接，没有放宽数据库全局信任参数。
- `scert-dns-accounts-race-fixed-20261004-0445.service`：实查 failed、退出一；启动命令误用隔离产物环境变量，测试正确拒绝缺失的绝对目录。修正为 `SCERT_MYSQL_TEST_ROOT` 和 `SCERT_POSTGRES_TEST_ROOT` 后启动下一任务，此次失败不能计为实库通过。
- 新增 HTTP 用户已删除场景首轮被“先撤销项目成员关系”的既有保护拒绝；夹具补充正式撤权后再删除测试用户，不移除保护或放宽断言。

### 当前回归范围

- 本地领域、数据库、仓储、HTTP 四包全量成功，分别 0.047、23.621、8.633、28.412 秒，随后 vet 成功；增加秘密读写失败、依赖缺失和验证取消场景后三轮 HTTP/仓储专项及 vet 通过。本地外部实库跳过不计通过证据。
- 05:09 实查第一台 `scert-dns-accounts-api-20261004-0458.service` 已 inactive、success、退出码零；日志 `dns-accounts-api-race.log`。三库和 HTTP 三轮竞态及 vet 通过，领域 1.023 秒、数据库 185.441 秒、仓储 49.344 秒、HTTP 40.278 秒。覆盖 DNS 账户、SQLite 四种开关、MySQL 迁移恢复和结构校验、迁移目录及 DNS 接口。隔离覆盖树未含最后追加的依赖故障用例及领域标识生成器复用，不是最新干净发行树全量。
- 使用隔离 MySQL 8.0.46、PostgreSQL 14.24；数据库仅回环监听，MySQL 严格 TLS。源码为覆盖测试树，不冒充干净发行树；最后本地追加的依赖故障用例和复用领域标识生成器将在干净提交验证中覆盖。
- HTTP 自动化覆盖真实 SQL 仓储与接口路由，但云验证器为注入实现，未调用真实云 DNS；没有声称独立账户已经用于实际签发、续期或部署。

### 尚未完成

旧系统账户幂等导入、域名账户绑定、运行租约与安全引用回收、签发/续期/重试接入、前端操作、干净源码 Linux 全量、正式升级及公共 DNS/CA 验收仍需继续。P1-03 和总体生产门禁不关闭。

## 2026年10月4日03:55 系统设置越权读取修复及正式部署

- 旧 `9c26db0` 工作树加入 `settings_scope_test.go`，执行 `scert-settings-scope-red-20261004-0333.service`，真实失败，退出码一。普通管理员 GET 原返回二百而非四百零三，响应未隔离缓存；角色降权后的旧会话原已返回四百零一，不误报为原认证失效。
- 修复：GET/PUT 均使用超级管理员实时会话中间件并先设置 `private, no-store` 与 `Vary: Cookie`；读取不访问明文凭据。前端先确认会话，仅超级管理员请求设置，响应解码完成后再次核验加载版本，避免旧请求覆盖新会话。
- 首次前端全量 284 项中一项失败：旧保存契约夹具未模拟超级管理员会话；补充真实会话响应，保留原数据提交断言。新增设置拒绝和旧响应竞争后，全量 286 项通过，Vite 构建通过。
- [✓] 设置专项修复后在第一台通过，耗时 0.364 秒。
- [!] `scert-settings-scope-green-20261004-0340.service` 已失败，退出码一。设置专项通过，HTTP API 全量 49.447 秒后两项失败：旧可选邮箱测试仍要求普通管理员读取全局设置、旧中间件测试仍要求通用管理错误文案。未运行后续 vet 和竞态，不以专项替代全量。
- [x] 调整旧权限契约测试：可选邮箱会话继续验证普通管理员可读服务商目录，同时新增普通用户与普通管理员均不可读系统设置的断言；通用管理拒绝文案使用真正的普通管理路由，系统设置专属拒绝由新范围测试覆盖。没有放宽实际系统设置权限。
- [✓] 复跑单元 `scert-settings-scope-fixed-20261004-0343.service` 已 inactive、Result=success、ExecMainStatus=0；HTTP API 全量 49.496 秒、vet 和设置、DNS 范围、可选邮箱、管理中间件三轮竞态 45.136 秒通过。日志同目录 `settings-scope-fixed.log`。
- [x] 继续核对真实路由发现服务商目录仍要求全局管理员；前端普通用户或降权后的加载不再请求该目录，测试夹具模拟真实四百零三，避免无条件成功响应掩盖页面失败。
- [✓] 干净 `3ff59dd` 第一台后端全量及 vet 已通过，单元 `scert-settings-full-20261004-0346.service` 为 inactive、success、退出码零；日志 `/var/tmp/scert-settings-clean-20261004-0344/full.log`。实际执行 `go test -p 1 -count=1 -timeout=8m ./...` 后执行 `go vet -p 1 ./...`。这不是三库首次 Web 或外部数据库全部矩阵验收。
- [✓] 最终前端增量提交 `c5774f3` 与 `3ff59dd` 的后端 Git 树均为 `8bf6518caee0cbcc979e89672497a7d754a240fc`，没有后端变更；最终前端全量 286 项及官方构建通过。没有重复执行相同后端全量，也未安装旧候选包。
- [✓] 干净 `c5774f3` 官方发布：`source_dirty=false`、`trimpath=true`、`local_path_check=passed`，仅 Linux amd64。归档 SHA-256 `7aa4e0cf6c79975675231688a8e430398c7da26b8d4750dbad9e5aa1e02a4ff9`，第一台归档及包内二十二个文件摘要全部通过。
- [✓] 第一台通过归档内正式安装器升级；执行单元 `scert-settings-install-20261004-0355.service` 已 inactive、success、退出码零，日志 `/var/tmp/scert-release-settings-c5774f3/install.log`。实际开始为 UTC+8 03:54:20，03:54:29 服务 active；名称中的时间仅为任务标识，不作为实际时间。
- [✓] `/scert/scert` SHA-256 `ff6e2ca82ba5f3661689fc760ebe3a6698865b6f860b07455191d84b1b85e068`；新前端 `index-G-gSx-q1.js` 的磁盘及实际 HTTPS 返回摘要均为 `f3145e70fd60b2a3658a3e652ebba5a03375885cc53103251c993db1c7878d99`，与本地正式包一致。
- [✓] Scert、存储 socket、Nginx active；Nginx 配置检查通过，已有 http2 弃用警告保留。回环定向严格 TLS 就绪返回二百、TLS 校验零；匿名设置返回四百零一、`Cache-Control: private, no-store` 和 `Vary: Cookie`；80 未知挑战返回无缓存四百零四。Scert 仅 `127.0.0.1:8443`，MySQL 仅回环，公网仍由 Nginx 80/443 接入。
- [!] 上述 HTTP 检查均为第一台回环定向内部证据，不计作公共 DNS、浏览器多角色或公共 CA 验收。03:35 公共 DNS 仍指第二台，未获确认切换；未清理生产数据库、操作第二台、修改 DNS 或新增辅助脚本及额外备份。安装器自身升级事务保留不等于另做数据备份。

## 2026年10月4日 真实 lego 客户端隔离协议回归

- [✓] 新增 `lego_http_protocol_test.go`、`lego_protocol_fixture_test.go` 正式 Go 用例，调用产品真实 LegoIssuer 和真实 HTTP 挑战服务。仅通过已有 HTTPClient 注入把 ACME 目录限制在回环 TLS CA；信任测试 CA，不跳过 TLS，禁止访问外部 ACME 地址。
- [✓] 隔离 CA 校验一次性 nonce、受保护 URL、ES256 请求签名、账户标识、申请域名、每个挑战的公钥指纹绑定、CSR 签名和 SAN；按实际 CSR 动态签发临时证书，再由产品验证私钥匹配和测试根信任链。
- [✓] 单域名、SAN、CA 拒绝和挑战期间取消四场景通过；失败不提交 CSR 或下载证书。每个场景都在测试主动关闭租约之前核对挑战已返回 404。
- [✓] 第一台 `scert-http01-protocol-20261004-0325.service` 已 inactive、Result=success、ExecMainStatus=0；普通专项 0.686 秒，五轮专项竞态 4.614 秒。日志 `/var/tmp/scert-http01-nginx-20261004/lego-protocol.log`。
- [✓] `scert-http01-protocol-package-20261004-0324.service` 已 inactive、success、退出码 0；证书包全量 6.328 秒，随后 vet 通过。日志同目录 `lego-protocol-package.log`。测试使用 `9c26db0` 工作树加上述两个用例，生产实现未改。
- [!] 这不是公共 CA 签发、所有 ACME 协议异常一致性、真实公网 Nginx 与 CA 联合验收或三库首次 Web。没有操作第二台、线上数据库、DNS 或正式证书。

## 2026年10月4日03:14 HTTP 验证回归与第一台正式升级

时间按运行环境 UTC+8。本轮接续重新读取第一台执行单元终态、下列日志与运行摘要，没有重复执行已成功的测试。第二台未操作；没有新增辅助验收脚本、备份或清空业务数据。

### 回归、修复及复审证据

- 首批 `scert-http01-check-20261004-0240.service` 退出码 1：接口测试把嵌入 HistoryScope 字段直接写到 Task 字面量，SQLite 快照 SELECT 星号又因迁移新增列误报。改为显式嵌入字面量与历史列清单，保留 rowid 和历史数据完整性断言，不跳过测试。
- [✓] `scert-http01-fix-20261004-0251.service`：inactive、Result=success、ExecMainStatus=0；源码 `201a272`。依次执行 `go test -p 1 -count=1 -timeout=8m ./...`、`go vet -p 1 ./...` 和 httpchallenge/certificate/httpapi/database 专项竞态。日志位于 `/var/tmp/scert-http01-20261004/`：`backend-fix-full.log`、`fix-vet.log`、`fix-race.log`。vet 日志零字节，后续竞态四包均通过。
- [✓] 实际签发配置授权覆盖 HTTP 不变、切换 DNS、空值默认 DNS、项目/域名变化、证书/部署撤权及重试权限；拒绝早于副作用。系统续期仍用原系统工作流，不宣称全部权限通道已经审计。
- [✓] `scert-http01-nginx-20261004-0257.service`：inactive、success、退出码 0；源码 `9c26db0`。日志 `/var/tmp/scert-http01-nginx-20261004/nginx-integration.log`：challenge、head、wrong_host、unknown、query、encoded、post、redirect、business、tls_rejected 十项通过，总计 0.133 秒。使用正式 Nginx 模板、独立回环进程、严格 TLS 测试上游，包含凭据剥离和 Host 隔离，不重载线上站点。
- [✓] `scert-http01-db-review.service`：inactive、success、退出码 0；同目录 `database-integration.log`。`TestDomainChallengeMigration` 在 SQLite、MySQL 8.0.46、PostgreSQL 14.24 全部通过，MySQL 中断重入及冲突字段拒绝通过，33.259 秒，无 skip。外部数据库为隔离回环实例、严格 TLS；这是迁移专项，不是三库首次 Web 或 MySQL 5.7 验收。

### 正式包与实际安装

- [✓] 官方 `build/build-release.ps1 -Target linux-amd64` 构建成功；manifest 的 source_commit 为 `9c26db081e0ab4c3e126ed7e3b0122a5110b4dbe`、source_dirty=false、trimpath=true、local_path_check=passed。前端 279/279 通过，Vite 构建通过，24 项产物摘要匹配，包内包含两个 Nginx 模板。
- [✓] 归档 `Scert-20261004-V0.1.0-linux-amd64.tar.gz` 两端 SHA256：`4f41314f48f40c67290f211d0a46859a18d26a86d88a6b13db579155f748f071`；远端 `/var/tmp/scert-http01-9c26db0-linux-amd64.tar.gz` 本轮再次读取得到相同摘要。
- [✓] 解包后 SHA256SUMS 全部通过，第一台官方安装器退出 0。安装记录 `/var/tmp/scert-release-http01-9c26db0/install.log`；只有安装器自身事务机制，没有额外远端备份。
- [✓] 实际 `/scert/scert` SHA256：`36d8769f7164539e831e19bddb5bd6bec759f3bcc05ee150624b93a35e3e2735`，本轮重新读取一致。前批已核对包内、安装及 Nginx 返回的 `index-AwxGoqOG.js` 摘要均为 `e5d5834529b30d6de46da851f5b55c76ae9f8ecd931ffa5c2916729d51280b04`。
- [✓] Scert、Scert-storage.socket、Nginx 本轮重新读取均 active。第一台本机严格 TLS `/readyz` 返回 ready；Nginx 80 未知挑战返回 404、no-store、nosniff。普通路径仍 HTTPS 跳转。reload 后最初旧 worker 的 301 观察保留，后续已得到 404。

### 未关闭的验收边界

上述请求使用第一台回环定向诊断，不是正常公共入口验收。正常 DNS 当前仍指第二台，未获确认前不切换、不在第二台配置发信。完整 lego ACME 协议、公网 HTTP-01 签发、各托管域名站点接入、Smail 系统通知和重置、三库首次 Web、完整 DNS 账户与 CNAME 委派仍待完成。线上迁移版本二十六未在本节直接查表，不用 ready 代替该证据；两条缺可信基线旧事务未篡改。P1-03 与整体上线门禁保持未通过。

## 2026年10月4日02:03 DNS 隔离正式升级与全量环境修正

本节时间为 UTC+8。沿用已确认任务，仅第一台执行主程序回归与升级，第二台不重复测试。发布源码为干净提交 `2513f935bc8c52622d79a3409a15051bc7d14c5c`；未对正式测试断言作任何修改。

- [✓] 正式 Linux 构建完成，发布路径检查通过，清单 source_dirty=false、targets 仅 linux-amd64；本地逐项复核 manifest 摘要通过。
- [✓] 密码 SSH 严格指纹校验上传，归档两端 SHA256 均为 `6a2d71493847ce4669c9a5a07a97e63c85d22b13a093cc35a91bb4d8bb7b5012`；第一台解压后 SHA256SUMS 全部通过。没有提前停服安装。
- [!] 首批全量单元 `scert-dns-scope-full-20261004-0137.service` 已明确结束，Result=exit-code、ExecMainStatus=1。releasearchive 的源码忽略规则与换行测试依赖真实 Git 索引，普通源码归档不含该索引，两个用例失败；后续 vet 与竞态因串联条件未执行。不得将这批记为全量通过。
- [✓] 使用同一提交的真实 Git bundle 修正测试输入，不伪造索引、不跳过测试。两端 bundle 摘要为 `09473e0c66c18b9f945b17adf1223520763fd2c89287c32210e2e9f6e2631b1d`；第一台由 nobody 克隆并明确 detached checkout 至该提交，工作树无修改。
- [✓] 正式重跑单元 `scert-dns-scope-checkout-20261004-0149.service` 已结束，ActiveState=inactive、Result=success、ExecMainStatus=0。目录 `/var/tmp/scert-checkout-2513f93-20261004/backend`；依次执行全量、vet、DNS 项目权限专项竞态全部通过。releasearchive 为 1.272 秒，全量 HTTP API 为 48.209 秒，专项竞态为 15.057 秒；复核测试后工作树仍无修改。
- [✓] 升级前只读核实业务活动任务、运行中恢复处置、恢复占用均为零；用户一条、域名三条、schema25。未清空业务数据库，也不将本次保留数据升级算作首次初始化验收。
- [✓] 全量通过后再次确认活动任务、恢复处置与占用为零，停止 Scert、管理程序和目录服务，通过包内官方安装器升级第一台，再启动主服务和目录 socket；Nginx 未停服，第二台未操作。仅保留安装器自身事务目录 `/scert/.install-xEc9mZqU`，未额外备份或清理文件。
- [✓] 实际运行 PID 为 163546，`/proc/163546/exe` 指向 `/scert/scert`，运行文件及落盘主程序 SHA256 均为 `c5c5ffaeba079a7e784d472f4654df88434ff988d2869bf28bcc4fbd6a78ce42`；管理程序摘要为 `f5140cec45082b69af3b335b64006ea3d6adfaaf5fbadbd01f83cd93019ce866`，与发布清单一致。Scert active/running、NRestarts=0，管理重置服务 inactive。
- [✓] 回环严格 TLS 的 `/readyz` 返回 ready、HTTP 200、TLS 校验零；第一台 Nginx 回环 HTTPS 前端返回 200，首页引用本次新资源，实际获取的 JS 摘要为 `0e4a136a684cc434ddf7e01407d70bd6edfed9532f1068998149803e8a6f11a1`，与发布文件一致。Scert 仅监听 127.0.0.1:8443，MySQL 仅回环，公网项目入口仍仅 Nginx 80/443。
- [✓] 已部署正式 DNS 列表匿名返回 401，包含 private, no-store 和 Vary: Cookie；通过正式密码登录获得 super_admin/complete，列表返回一条 default 项目、已分配归属、可用凭据，伪造 actor_id/project_id 不改变真实范围，未返回凭据引用或密钥字段。正式退出返回 200，会话与密码变量已清除，未写入文件。
- [✓] 升级后只读复核用户一条、域名三条、schema25，活动任务及恢复占用均零。项目角色、跨项目拒绝、实时撤权及数据库失败来自正式 Linux 隔离测试，不冒充生产实例上的全角色操作矩阵；当前实机仅复核匿名及超级管理员正式路由。其他设置元数据通道尚未由本批全面审完。
- [!] `odacm.cn` 已在浏览器正常域名打开登录页，但第一台解析仍指第二台；不在该页配置第一台发信。HTTP-01、多 DNS 账户完整生命周期、委派管理、系统通知授权与三数据库首次 Web 仍未完成。

以上时间按第一台输出的 UTC+8 记录，02:00 对应 HTTP 日期头的 2026-10-03 18:00 GMT。回环固定名称仅为内部升级诊断，不算第一台公共域名或浏览器生产验收；本批保留数据升级也不算清库首次流程。

## 2026年10月4日01:35 DNS 账户项目隔离红绿测试

修改前中文检查点 `e060205` 已推送。旧源码基线的 DNS 账户实现摘要为 `9bcb1dbe1ca60dc0dfcbd8dfecbb1ceead1dfe92d54bc501f5c2432e0bcdf585`，本机与第一台既有隔离源码一致。新测试使用正式 Go 用例，不创建辅助验收脚本、不修改生产配置或数据库，不使用第二台。

- [x] 隔离目录为第一台 `/var/tmp/scert-dns-scope-20261004-0130/backend`，使用原生 Linux Go 工具链、nobody 账户、独立临时 SQLite；CPU、内存及运行时间有限制。
- [x] 单元 `scert-dns-scope-red-20261004-0130.service` 在旧源码运行新用例，终态 failed、退出一：无项目角色的全局管理员仍返回 system-dns，撤销项目 DNS 角色后仍可读取。项目角色反被全局管理员门槛拒绝；缺授权依赖未拒绝读取。该失败为缺陷复现，不隐去。
- [x] 修复后单元 `scert-dns-scope-green-20261004-0134.service` 完成，终态 inactive、Result=success、退出零；DNS 账户、原有 DNS API、项目资源与项目目录专项共用正式 HTTP 路由及隔离 SQL 权限仓储，包耗时 2.910 秒。
- [x] 用例覆盖真实归属、管理员无隐式权限、项目管理员及只读角色、跨项目伪造参数、撤权后旧会话、默认项目、不存在项目、缺依赖、数据库读失败、匿名缓存隔离和凭据不回显；拒绝路径断言凭据 Get 次数为零。
- [x] 前端正式测试共 276 项通过，无失败或跳过；新增项目角色 DNS 入口及退出后导航复位用例。
- [~] 后端全量、竞态及最新正式包升级尚未完成；当前结果不是线上或浏览器验收，也不代表 P0-07 全部完成。

本次读取源码还确认：当前 ACME 入口仅调用 DNS-01 Provider 设置，没有 HTTP-01 接入；DNS 列表仅包含当前 system-dns，没有独立多账户增删管理。完整矩阵必须区分开发未完成与已有能力缺实机证据，不再无限复跑已通过的 DNS-01 或证书回滚来替代这些缺项。

## 2026年10月4日01:10 首次 Web 前提与系统通知授权核查

- [✓] 第一台 Scert、Nginx 正常运行；正确名称的回环严格 TLS 前端首页返回 200，后端就绪已通过。本轮未启动新的隔离 Web 实例、未清空现有业务库。
- [!] 从 Windows 对第一台 `8.137.23.95` 使用 `odacm.cn` 的 Host/SNI 定向诊断，HTTP 返回 403、Server 为 Beaver，HTTPS 握手失败。固定地址仅用于诊断，不作为公共入口验收，也没有关闭 TLS 验证。
- [!] 第一台系统解析器查询 `odacm.cn` 仍返回 `45.136.13.241`。访问该域名不能算第一台首次 Web；没有擅自切换 DNS，也没有将第二台改为主程序测试机。
- [✓] 第一台请求 `https://web.cdis.cn/` 返回 HTTP 200、TLS 校验零、远端 `47.108.78.12`。
- [!] Smail 系统通知状态接口在未认证请求下返回业务码 403、登录失效；这是正常权限保护，不是发信接口故障。阅读 Smail 当前控制器确认需认证并受账号管理权限和近期身份验证约束。
- [!] Scert 正式设置接口的脱敏投影为 smtp_enabled=false、smtp_host 为空、smtp_port=587、smtp_mode=starttls、smtp_credential_configured=false。网站地址不能代替系统通知专用 SMTP 授权，不改用普通邮箱、不猜授权码。
- [ ] 完整首次 Web 前需提供可正常访问并指向第一台的管理域名；真实邮件及验证码重置前需配置 Smail 已启用的系统通知身份和授权。上述外部前提未满足，不继续清库或重复同一失败探测。

浏览器专用工具可连接，当前仅空白页，未开展新的页面测试。第二台本批证书推送已在 01:04 收尾，未再执行主程序、数据库或初始化测试。三数据库首次 Web、DNS/ACME 剩余矩阵及整体生产验收均保持未完成。

## 2026年10月4日01:04 不同版本恢复、故障回滚及正式重试

本批只在第一台运行 Scert 业务测试，第二台只验证证书接收及 HTTPS 生效。沿用正式包 `e00a757`，保留数据库与失败历史，不新增辅助脚本、不额外备份、不重新签发、不检查磁盘。以下时间均为 UTC+8；产品版本号中的日期按其实际返回记录。

### 版本与正常部署

- 测试域名 `acceptance-0928.odacm.cn`，域名编号 `40d6666b-25c8-4921-bda9-a768afc14815`，已有双目标绑定未修改。
- 原当前版本 `20260930-V1.1`，证书指纹 `49AD6418581A0B1C01EB5C0DEDE7E5B0AAEA8D926A8BEBA4E91B2EECF697CA04`；历史版本 `20260930-V1.0`，指纹 `C65F3151FA4239632DB6172620AB0D2382FCD1FF5DA4440D4F5155AA987E487E`。两张均为有效正式证书。
- [✓] 通过正式历史恢复接口提交任务 `c2a3a114-3f78-4467-8c26-b08f3357d5d6`，成功创建当前版本 `20261003-V1.0`，指纹为历史证书。接口明确说明仅恢复托管版本，不代表远端部署完成。
- [✓] 随后正式部署任务 `c7c08514-5884-4ab2-80f0-fc5e15155dc6` 成功，两个目标均 committed，报告 success=2、failed=0、verification_status=success。
- [✓] 第二台证书文件和正常 DNS 的 HTTPS 叶证书指纹均变为历史证书；HTTP 200、TLS 校验零、连接目标 `45.136.13.241`。不是仅看任务成功或文件存在。

### 未生效的中断尝试

- `da74c966-6a72-4cf6-b89b-8d9781236c0a` 正常恢复原指纹；观察命令启动时已经 committed，未发生故障注入，仅计为正常部署。
- `35cc4bf3-6a56-44a6-9793-eca225539920` 再次部署历史指纹时命中观察阶段，但 systemctl 不支持所用参数，中断命令失败；MainPID 未变、任务正常 success，两条事务 committed。终端后续提示不能作为注入成功证据，本次不计为故障回滚。

### 不同版本真实中断与自动回滚

- 通过正式恢复任务 `b2a654a1-ed53-418b-a269-2bfa06939fa2` 将原证书恢复为托管当前版本 `20261003-V1.3`，随后发起部署 `e0cbde62-640a-4e37-a4af-9ddd2fc82d0c`。
- 先启动只读阶段观察，再发起任务；00:59:59 第二目标事务 `20261003-165955.302853441-9c49cc09015d` 为 reload_attempted/pending。证据中的 previous_fingerprint 为 `C65F3151...E487E`，expected_fingerprint 为 `49AD6418...7CA04`，两者不同。
- 核对 MainPID 对应 `/scert/scert` 后发送 SIGKILL，命令成功；MainPID 从 151723 变为 154395，NRestarts 从二变为三。没有网络限流、防火墙规则或手工恢复文件。
- [✓] 01:00:33 第一台自动重启并就绪，两条事务 `20261003-165951.098466625-880b008877bc`、`20261003-165955.302853441-9c49cc09015d` 均为 rolled_back/rolled_back；任务按 service_restart 失败，不伪装成功。
- [✓] 两目标文件恢复为旧指纹，第二台正常 DNS 严格 HTTPS 的实际叶证书也是旧指纹，HTTP 200、TLS 校验零。本域名目录无本次临时文件、事务备份或锁残留。

### 正式重试与最终状态

- 重启后旧登录会话返回 401；通过正式登录重新认证，仅在内存保存会话。01:02:01 使用原任务的正式 retry 接口，不新建伪成功记录。
- [✓] 01:02:24 第二次尝试完成，success=2、failed=0、verification_status=success。第一次尝试仍 failed，第二次 success；原两条事务仍 rolled_back，新事务 `20261003-170202.139622572-14e0670f16be`、`20261003-170207.421146426-f5c6ff9bbf3c` 为 committed。
- [✓] 审计保留原执行、task_recovery 失败、重试开始与重试成功；未通过 SQL 修改任何业务状态。
- [✓] 托管当前版本 `20261003-V1.3`、两目标文件及第二台 HTTPS 指纹均为原指纹 `49AD6418...7CA04`；第二台 HTTP 200、TLS 校验零，目录无本次事务残留。
- [✓] 第一台 Scert、目录 socket、Nginx active；Scert 与 MySQL 仅回环监听，公网项目入口仍为 Nginx 80/443。
- [!] 两条旧无可信基线事务仍为 recovery_required；本批未处理它们，也未复跑系统邮件、三数据库首次 Web、DNS/ACME 全矩阵或第一台公共入口。整体生产验收仍未完成。

## 2026年10月4日00:40 人工回滚成功及人工提交中断连查询

本批继续使用 `e00a757` 正式运行包，没有修改产品源码。时间为 UTC+8。只对已授权的双机测试域名执行，保留原数据库和全部失败历史。

### 人工恢复基线成功

- 正式部署 API 接收任务 `ed4cbe61-2337-4a19-b5c9-6a1ad12ecafa`。第二目标事务 `20261003-163018.496164107-ea4c5b7a0187` 进入 reload_attempted/pending 后，真实 SIGKILL 中断第一台 Scert 进程。
- 临时防火墙规则只限制第一台 Scert 用户 UID 997 到第二台 TCP 22，事先设置九十秒自动撤销；第一目标自动 rolled_back，第二目标 recovery_required，任务 failed/service_restart。恢复服务后立即撤销规则及定时器，root SSH 和站点流量未被规则匹配。
- 正式 `systemctl start Scert-admin` 菜单三先核查所有目标、备份与锁，选择恢复基线并完整确认，生成处置 `c341a3afffa758cc2773ff26ae436e4ef4df3e4d6da08b9ec0cd03e3b53042e3`。
- [✓] 终端返回恢复成功；菜单四独立查询一致。只读 SQL 确认处置 succeeded/rolled_back、原事务 phase/status 均 rolled_back、started/finished 审计完整、recovery_claims 为零。

### 人工提交执行中真实 SSH 断连

- 新任务 `8b055549-dce5-4a11-a4e7-561979688f4d` 同样在第二目标 reload_attempted 阶段中断，生成事务 `20261003-163523.357071304-e4ccd8d9831c` 的 recovery_required；第一目标自动回滚。临时限制仍只匹配 Scert 到第二台 SSH，预设自动撤销并在服务恢复后主动撤销。
- 新建密码 SSH 交互连接，通过正式菜单三选择完成提交，生成处置 `a7b2b4d03b6e6768b793d930acc0b6ff577d77a51a139324edba46711e24ce2a`。
- 00:37:20 独立只读查询确认处置 running、管理终端 activating；随后用 SSH 客户端断开操作连接，工具确认该连接关闭并退出一。不是在动作结束后退出终端。
- 重新建立密码 SSH 连接，仅执行正式菜单四按原处置编号查询，没有重放提交。
- [✓] 查询返回提交成功；只读数据库确认处置 succeeded/committed，原事务 committed，started 为 00:37:10、finished 为 00:38:02，开始/结束审计各一条，占用为零。

### 现场收尾及结论边界

- [✓] 两个临时撤销 timer 均 inactive，OUTPUT 中没有本批 owner/目标地址规则；Scert、目录 socket、Nginx active。
- [✓] 第二台测试域名目录未发现临时文件、备份或锁残留，cert/fullchain 摘要仍为 `7e8b56227b345ed9b66659e428f9bd4421604f11253ab88d65b3ed61737f35cf`；正常 DNS 严格 HTTPS 200，TLS 校验零，连接目标为第二台；Scert、Nginx、MySQL、PMail active。
- [✓] 本批关闭同证书人工提交、人工回滚和执行中 SSH 断连后查询三个实机场景；没有直接修改事务终态或删除占用，没有创建辅助验收脚本。
- [ ] 本批 previous_fingerprint 与 expected_fingerprint 相同，不计为不同版本证书回滚。三数据库首次 Web、系统邮件、DNS/ACME 完整矩阵、第一台公网和无可信基线历史事务仍未全部通过。

## 2026年10月4日00:24 正式升级与独立重新核查成功

本批为保留生产数据的升级和恢复验证，不是清库首次 Web 验收。所有时间为 UTC+8，口令及授权信息不进入记录。

### 源码测试最终结果

- [✓] `RECONCILIATION_LATEST_EXIT=0`、`RECONCILIATION_RACE_EXIT=0`：五包专项及竞态通过。
- [✓] `RECONCILIATION_FULL_EXIT=0`、`RECONCILIATION_VET_EXIT=0`：后端全量及静态检查通过，日志位于 `/var/tmp/scert-local-recovery-20261003-1828/`。随后仅修正测试快照比较和补充包内说明，没有再改产品实现。
- [✓] 首批实库迁移专项通过，但整批退出码为一：测试直接比较内存时间与数据库落盘时间，受 MySQL/PostgreSQL 时间精度影响而误报历史改变。改为先读取持久化快照，再对比前后快照；保留完整历史不变断言。
- [✓] 独立最终目录 `/var/tmp/scert-reconciliation-final-20261004/` 下 `external-rerun.log` 复跑通过，`RECONCILIATION_FINAL_EXTERNAL_RERUN_EXIT=0`；包含两个实库执行器、两方向核查、审计失败、连接池重开和 SQLite 危险场景拒绝。该目录首次执行因 nobody 无法遍历目录失败，调整隔离目录归属后才复跑成功；没有放宽生产权限。
- [✓] `FINAL_REPOSITORY_RACE_EXIT=0`、`FINAL_REPOSITORY_VET_EXIT=0`；前端测试 275 项通过、零失败、零跳过。

### 正式包与升级

- [✓] 发布源 `e00a757d2257569920ec1c56d5da55ba4968023d`，manifest 的 `source_dirty=false`；Linux 构建及发布路径检查退出码零。
- 包：`Scert-20261004-V0.1.0-linux-amd64.tar.gz`；SHA256：`0273e2b8e7b9fabcc026e9fba3fe618afc22152f651595feaf92ef78e1edcfd8`。
- 程序 SHA256：`530f4610ae0f48cbfd1d408f459f4f9b2d747595bbec0352f0da8adaa00765d1`；管理终端 SHA256：`4f1fe048adbb0162dc71eebd6d31fccbb28e55aa18f50108b178c35b98360b3a`。
- [✓] 密码 SCP 上传第一台 `/var/tmp/scert-release-e00a757-20261004/`，包摘要及全部 SHA256SUMS 校验通过；仅停止 Scert 相关服务和目录 socket，使用官方安装器升级，未停止共享 Nginx。
- [✓] 00:21 核实正式程序摘要匹配，schema25，Scert 与目录 socket active，回环严格 TLS `/readyz` 返回 ready；Scert 仍为 `127.0.0.1:8443`，管理公网仍由 Nginx 80/443 接入。此回环验证不算公共 DNS/入口验收。
- 安装器自动保留升级前发布文件以支持失败回退；没有另行制作远端数据备份。

### 正式本机入口及持久化结果

- 原处置：`bc4099fd53c10ce0ae643ae2a89c2597fa48ed7ea6d6661fe4126f7f310b7007`；升级后仍为 uncertain 且占用保留，未被启动过程自动收尾。
- 在密码 SSH 前台执行 `systemctl start Scert-admin`，选择菜单五并完整确认，产生新核查 `798e7235b4491c6ca6fc4e49b2e03f98a68ece86fd40a836df451a0931477d1f`。
- [✓] 正式终端返回成功；重新启动终端使用菜单四按新编号查询仍成功。新动作为 verify_commit，状态 succeeded，结果 committed，关联原处置。
- [✓] 00:23 只读 SQL 核实原事务 `20261003-150034.784155038-30bcd090470c` 的 phase/status 均 committed，recovery_claims 为零；原处置仍 uncertain，原 started/finished 审计保留，新 started/finished 审计完整。
- [✓] 第二台 `https://scert-multi-20260930.apmmk.cn/` 正常 DNS 严格 HTTPS 返回 200；本次核查不重放远端文件操作或服务重载。
- [!] 事务 `20261003-142919.192475924-417b9bcc5b09`、`20261003-142924.300953335-fac63bc92b1f` 仍为 recovery_required，没有可信旧基线，不删除锁、不补造证据。

### 未完成门禁

人工回滚成功、执行中断连查询、不同版本证书回滚、三数据库首次 Web、系统邮件与重置、DNS/ACME 完整矩阵及第一台公网入口仍未全部通过。本批未重新验证公网备案拦截及系统通知授权，二者按既有阻塞保留，不宣称本批已复核。

## 2026年10月3日23:58 重新核查源码验证

第一台隔离源码目录：`/var/tmp/scert-local-recovery-20261003-1828/backend`；使用 nobody 和原生 Go 1.26.6，不替换运行中的正式服务。

- [✓] `reconciliation-latest.log`：五包专项退出码零，筛选包含 Recovery、Disposition、Reconciliation，覆盖新本机会话测试。
- [✓] `reconciliation-race.log`：同五包竞态退出码零。
- [~] `reconciliation-external.log`：显式启用隔离 MySQL 8.0 与 PostgreSQL 14，验证迁移二十五、中断重入、两个收尾方向、审计失败及连接池重开；尚未取得最终结果。
- [~] `reconciliation-full.log`、`reconciliation-vet.log`：最新后端全量和静态检查正在顺序运行。

上批 `STRICT_UMASK_FULL_EXIT=0` 仅覆盖关闭修复及备份测试权限基线；不能替代本批结果。五包竞态结束后只补充了外部实库测试，产品实现未再改变。所有日志位于上述源码目录的父目录。

当前线上仍为 `a5dfd42`、schema24；真实处置 `bc4099fd53c10ce0ae643ae2a89c2597fa48ed7ea6d6661fe4126f7f310b7007` 及占用未手动修改。正式新包、schema25、真实本机菜单五收尾均待验。

## 2026年10月3日23:14 真实中断恢复与关闭误判

### 重载阶段中断与自动恢复

- 第一轮任务 `b795be80-e086-46b4-a2ba-e7b51c8662f0` 因旧 systemd 不支持 `--kill-whom`，注入失败并正常完成；不能计为崩溃测试通过。确认本机帮助后改用 `--kill-who=main`。
- 22:57:25 对正式 Scert 主进程发送 SIGKILL，命中任务 `c68e3956-1b6d-42c7-ba9a-753ef2d1a44b` 第二目标事务的 reload_attempted 阶段。
- systemd 的 MainPID 从 130794 变为 134595，NRestarts=1；22:58:16 已核实服务恢复 active、回环严格 TLS 就绪检查通过。
- 事务 `20261003-145716.121859533-09a2a331489a` 与 `20261003-145720.641188477-c769c8d3e153` 均 rolled_back；任务为 failed/service_restart，没有覆盖为成功。
- 双机 cert/fullchain 摘要保持 `7e8b56227b345ed9b66659e428f9bd4421604f11253ab88d65b3ed61737f35cf`，未发现该域名遗留事务文件。第二台正常 DNS HTTPS 200、peer=45.136.13.241、TLS 校验结果零。

### 真实待恢复事务及人工提交异常

- 任务 `5e89aa6d-3e25-4e3b-bf6c-71bf612aaf32` 在第二台 reload_attempted 阶段再次中断；临时 OUTPUT 拒绝规则仅匹配第一台 Scert 用户 UID 997 到第二台 TCP 22，预设九十秒撤销定时器。
- 第一目标自动回滚；第二事务 `20261003-150034.784155038-30bcd090470c` 因 SSH 被拒进入 recovery_required。确认服务已恢复后立即撤销规则和定时器；未影响 root SSH 或第二台 HTTPS，未创建辅助脚本。
- 正式本机管理菜单三选择完成提交，完整确认后生成处置 `bc4099fd53c10ce0ae643ae2a89c2597fa48ed7ea6d6661fe4126f7f310b7007`，执行时间 23:02:05—23:02:34。
- 目标文件及备份摘要匹配，锁归属匹配；动作后原事务 phase=commit_verified、status=recovery_required，远端备份及锁均已不存在，处置却为 uncertain，持久化占用保留。这是失败，不算人工提交通过。
- 原无可信旧基线的两条事务未改写。新不确定处置不能重放或直接删占用；第二台站点保持严格 HTTPS 200，但后续新部署被占用阻止。

### 修复及验证边界

- 新增真实 SSH/SFTP 协议用例在第一台 Linux 复现健康 Close 返回 use of closed network connection，CLOSE_REPRO_EXIT=1。
- 修复正常主动关闭后的预期错误分类及重复、并发关闭幂等，保留已存在的断连和其他关闭错误。`ssh-close-race.log` 中部署整包竞态通过，CLOSE_RACE_EXIT=0，约 3.261 秒。
- 后端全量及静态检查仍在同一 SSH 会话执行，正式候选包尚未更新。该修复不追溯改写 uncertain 历史记录。
- 本轮为保留数据、同一有效证书重新部署，不是清库首次 Web、不同版本证书回滚、ACME 新签发或完整生产验收。

## 2026年10月3日22:44 多对多、恢复负向与外部门禁

### 正确域名的第二组双机推送

- 核对两台 Nginx 的真实站点均为 `scert-multi-20260930.apmmk.cn`，没有 `.odacm.cn` 的同名前缀站点；原失败记录和锁未修改。
- 经正式 API 创建正确域名 `b6824906-fea9-4f2a-a682-3c2b1ade1ee3` 并绑定原两台服务器，分别返回 201、200；导入已有有效正式证书，未重新签发，也未展示或记录私钥内容。该测试域名关闭自动续期，避免本轮导入引发额外签发。
- 导入任务 `af31d106-9231-4659-bab1-bca2932f8ff4` 于 22:38:41—22:38:56 完成，success=2、failed=0、verification_status=success、certificate_imported=true。
- 两条事务 `20261003-143842.607984571-0bffe1eb5ef9`、`20261003-143848.492916882-246b37ecbcc4` 均 committed；双机证书 SHA-256 为 `7e8b56227b345ed9b66659e428f9bd4421604f11253ab88d65b3ed61737f35cf`。
- 第二台普通 DNS 的 HTTPS 返回 200、peer=45.136.13.241、ssl_verify_result=0；与先前有效域名合计两个域名、两台服务器、四个成功目标。第一台 Scert、存储 socket 和 Nginx active，Scert/MySQL 仍回环；第二台五项既有服务 active。

### 恢复拒绝的正式终端验证

- 原失败两条事务的历史基线 `previous_fingerprint` 均为空；现场没有对应 `.odacm.cn` 站点，不能凭当前文件不存在宣称恢复通过。
- 对第一条失败事务通过菜单三选择恢复基线，完整确认后返回已拒绝、未执行远端变更；处置编号 `278d050dc33d6775d8eb453b74e0a0691f8cff9be2531476678b34b423fc9e85`。
- 数据库只读核对结果为 restore_baseline/rejected/rejected，原事务仍需恢复。该负向不覆盖人工恢复成功或强制断连场景。

### 第一台公网与系统邮件阻塞

- 从第二台以目标域名、Host/SNI 和固定第一台地址诊断：HTTP 返回 403、Server Beaver，页面标题 Non-compliance ICP Filing；测试域名及 `odacm.cn` 的 HTTPS 均 connection reset。Windows 独立探测第一台测试域名也在 TLS 握手失败。未关闭 TLS，固定地址仅用于定位，不能当作正常 DNS 验收。
- 第一台访问 `https://web.cdis.cn/` 严格 TLS 返回 HTTP 200，真实远端为 47.108.78.12；通知状态 API HTTP 为 200，但业务 errorNo=403、Login has expired，不能误判已授权。
- 正式 Scert 设置接口只读投影：smtp_enabled=0、smtp_host 为空、smtp_port=587、smtp_mode=starttls、smtp_credential_configured=0，非系统通知账号。
- 阅读当前 `D:\Desktop\Smail\backend\controllers\system_notification.go`：系统通知状态需登录，返回实际 SMTP 主机、端口、notify: 用户名及系统发件身份；授权/凭据由 Smail 管理范围和近期身份确认控制。网站地址不是可自动推导的 SMTP 授权，未猜测凭据或改用自定义邮箱。

整体生产验收未通过。需继续人工写动作成功与历史恢复、三数据库清库首次 Web、真实系统邮件与重置、DNS/ACME 全矩阵；第一台公网部署入口和 Smail 系统授权是已实测的外部门禁。

## 2026年10月3日22:32 正式包升级与双机真实推送

范围：第一台 `8.137.23.95` 运行新包并发起部署，第二台 `45.136.13.241` 接收证书。保留既有数据库的升级验证，不是清库首次流程；第二台未升级本批管理程序。没有创建辅助验收脚本、额外远端备份或关闭 TLS。

### 候选包与第一台安装

- 源码提交 `a5dfd42b4e27f5e1d10e7111229e883363f50bf1`，`source_dirty=false`；目标仅 `linux-amd64`，trimpath 与本地路径检查通过。
- 归档 `Scert-20261003-V0.1.0-linux-amd64.tar.gz` 的 SHA-256：`ed700422d915184468b43635b9aeca665288114187952c48b9b96fe79bf77526`。
- 第一台正式包目录 `/var/tmp/scert-release-a5dfd42-20261003-2210`；按包内安装器停服升级成功，运行 `/scert/scert` 摘要为 `5b18180831b5601f882eedd43fceef85665a6bd79dfa7bc4e7e2a34d0309b4f3`，SQLite 迁移版本 24。
- Scert、Scert-storage.socket、Nginx active；管理上游回环严格 TLS 就绪成功，不算第一台公共域名入口验收。

### 单域名双目标成功与幂等

- 正式 API 部署 `acceptance-0928.odacm.cn`，任务 `ecfd31f0-ba6d-4cbb-a3e7-485568eabfaf`，22:27:16 开始，22:27:32 完成，success=2、failed=0、verification_status=success。
- 两条事务 `20261003-142717.443369420-acc7ff48a6f2`、`20261003-142723.040819317-2acd624440a8` 均 committed。
- 两台 `/ssl/acceptance-0928.odacm.cn/single/cert.pem` 的 SHA-256 相同：`53d965ff2668f476775f2b29b3da4acd1a6c82c73a395a73ff03db70d82fbeef`。证书 SAN 为该域名，到期时间为 2026年12月29日18:44:39 UTC。
- 第二台普通 DNS 解析目标为 `45.136.13.241`；未使用强制解析或跳过校验的 HTTPS 请求返回 HTTP 200、peer=45.136.13.241、ssl_verify_result=0。Nginx 配置测试通过，五项既有服务 active。
- 使用相同幂等键再次 POST 返回 HTTP 200、`replayed=true` 及原任务编号，没有重复部署。

### 第二域名失败与正式人工记录

- `scert-multi-20260930.odacm.cn` 任务 `8efd495b-5511-4070-a7ab-2e02afac82bc` 在 22:29:18—22:29:36 执行并失败；日志明确第一台 HTTPS 收到 `*.salg.cn, salg.cn` 证书，不匹配目标域名。
- 两条事务 `20261003-142919.192475924-417b9bcc5b09`、`20261003-142924.300953335-fac63bc92b1f` 报告 `recovery_unconfirmed`，保留部署锁；没有直接重试。
- 正式 root 本机终端菜单二对第一条事务采样：目标、临时和备份文件均不存在，三个锁存在且归属匹配；该采样本身不证明站点或服务恢复。
- 菜单三选择仅记录并完整确认，处置 `dd256d2258a1a3a5e28d53593638a8997ed71b3d09420e20b5542a4037dd146d` 返回已记录；重开终端后菜单四查询得到同一结论，原部署事务仍待恢复。
- 待核对实际站点配置；人工提交、回滚、强制断连、历史无证据恢复、首次 Web、系统邮件与完整生产验收尚未完成。

## 2026年10月3日22:03 外部数据库整组终态

- [✓] 21:57 通过原 SSH 监控会话实查 `scert-external-disposition-20261003-2143.service`：MainPID=0、ActiveState=inactive、Result=success。
- [✓] 原日志 `/var/tmp/scert-local-recovery-20261003-1828/external-disposition.log` 中 `EXTERNAL_POSTGRES_EXIT`、`EXTERNAL_MYSQL_EXIT`、`EXTERNAL_SQLITE_REGRESSION_EXIT`、`EXTERNAL_VET_EXIT`、`EXTERNAL_FULL_EXIT` 均为 0。
- [✓] SQLite 执行器竞态约 5.519 秒，后端普通全量中的 HTTP 包约 44.928 秒；本组未清理生产数据库、未重启业务服务。
- [~] 正在修正实际发行运维说明，随后从干净提交构建 Linux 候选包；首次回环反代、系统邮件、真实 SSH/TLS 和完整生产验收尚未完成。


## 2026年10月3日21:48 外部数据库专项

- [✓] 21:41 实查 `scert-local-actions-fixed-20261003-2125.service` 已 inactive、MainPID=0、Result=success，`LOCAL_ACTIONS_FIXED_HTTP_FULL_RACE_EXIT=0`；HTTP 全包竞态约 393.752 秒，五项检查完整通过。
- [x] 新四份测试源码归档 SHA-256 为 `421b52f1916f7be8cf088e0c0bd9623ad71e777f7b1439277a7e58faa4045e24`，本地与第一台一致；旧任务终止后才更新隔离源码。
- [x] PostgreSQL 14.24 工具经 APT 下载校验后仅解压在 `/var/tmp/scert-pg14-disposition-20261003`，未安装系统服务。Git 隔离目录属主检查首次阻止任务启动，改用单条命令的 safe.directory 后启动，没有修改全局 Git 配置或重复启动测试。
- [✓] `scert-external-disposition-20261003-2143.service` 的 `external-disposition.log`：PostgreSQL 迁移 22—24 三场景通过，数据库包约 3.672 秒；执行器十场景通过，仓储包约 7.603 秒；`EXTERNAL_POSTGRES_EXIT=0`。
- [✓] 同日志 MySQL 5.7.44 执行器十场景通过，约 25.574 秒；`EXTERNAL_MYSQL_EXIT=0`。三动作分别覆盖正常、开始审计失败、结束审计失败，另外包含八并发幂等；重开的是实际连接池，远端文件和站点核验是测试实现。
- [x] 21:46 查询无 PostgreSQL 残留进程，测试自动停止独立集群；仅 SQL 存储专项，不替代 PostgreSQL 16 备份工具、正式包或首次 Web 验收。
- [✓] 本地 `npm.cmd test`：275 项通过，零失败、零跳过。
- [~] 最新 SQLite 执行器、完整静态检查及后端普通全量仍在上述同一单元继续执行；不提前标记整组成功。


## 2026年10月3日21:32 本机接入修复版回归

- [x] 首轮任务 `scert-local-actions-20261003-2116.service` 已失败终止，`LOCAL_ACTIONS_RACE_EXIT=1`。分片测试夹具使用默认四 KiB 读取器导致过早失败；已改用协议最大帧大小的读取器，没有放宽生产协议。HTTP 全包竞态五分钟超时，堆栈位于既有 SQLite 迁移，不能记为通过。
- [x] 修复版源码归档 SHA-256 为 `c767ae48b4518f5edeb8ce416d9180a9e12d18a284c6c03977b75be5a06580e7`，已核对本地与第一台一致。
- [✓] `scert-local-actions-fixed-20261003-2125.service` 日志 `local-actions-fixed.log`：`LOCAL_ACTIONS_FIXED_RACE_EXIT=0`、`LOCAL_ACTIONS_FIXED_HTTP_EXIT=0`、`LOCAL_ACTIONS_FIXED_VET_EXIT=0`、`LOCAL_ACTIONS_FIXED_FULL_EXIT=0`。三包竞态通过，HTTP 本机专项约 61.428 秒，普通全量 HTTP 约 46.288 秒。
- [~] 21:31 实查该单元仍为 active、MainPID 为 126858，最后一项 HTTP 全包竞态正在执行，不能以此时的 Result=success 宣布整组通过。
- [x] Unix socket 用例使用实际会话与执行服务，但持久仓储及远端文件为测试实现；终端 socket 菜单为测试服务端，不扩大为实库、SSH 或生产证据。
- [x] 21:28 第二台指纹匹配，根分区可用 26 GB、使用率 11%、inode 3%，五项服务 active，Nginx 配置通过；本轮无删除。21:31 第一台 Scert/Nginx active、可用约 9.4 GB，未重启业务或清理业务数据库。
- [ ] 必要外部实库、正式 Linux 候选包及完整双机生产验收未完成。


## 2026年10月3日21:00 最新执行器终态确认

- [✓] 实查 `scert-executor-final-20261003-2052.service` 为 `inactive`、`MainPID=0`、`Result=success`。
- [✓] 日志 `executor-final.log` 的部署竞态、SQLite 五场景、完整静态检查及后端全量四项退出码均为零；SQLite 竞态耗时 5.802 秒。
- [x] 本批执行器代码已提交并推送 `bf4caeb`。
- [~] 下一批本机协议和终端接入；本次是隔离自动回归，不是正式包、外部实库或第二台真实推送验收。


## 2026年10月3日20:52 一次性确认与执行器回归

范围为第一台隔离源码目录 `/var/tmp/scert-local-recovery-20261003-1828`，不替代正式安装包；远端文件和 HTTPS 回调在本批 Go 测试中使用测试实现，SQLite 为真实临时数据库。线上业务数据库未清理、正式服务未升级。

- [✓] `scert-executor-20261003-2035.service`：部署全包竞态通过，约 3.254 秒，`EXECUTOR_RACE_EXIT=0`。
- [✓] `scert-executor-extra-20261003-2041.service` 的部署竞态通过，约 3.510 秒；但同组 SQLite 五场景因测试客户端缺少证据接口方法而失败，整组失败记录保留于 `executor-extra.log`，不能记整组成功。
- [x] 测试夹具补齐拒绝建锁的方法及编译期证据接口断言，未放宽生产核查边界。
- [✓] `scert-executor-sqlite-20261003-2045.service`：SQLite 五场景竞态复测通过，仓储约 5.303 秒，`EXECUTOR_SQLITE_FIX_EXIT=0`；完整静态检查和后端全量通过，`EXECUTOR_VET_EXIT=0`、`EXECUTOR_FULL_EXIT=0`。服务已退出，`MainPID=0`、`Result=success`。
- [~] 最新追加未知临时文件漏项拦截与脱敏查询，八份源码归档 SHA-256：`101eb370619bac98d4b069ea3c242206b3035fca7cd097006b83fb8f4494a83f`，本地与第一台一致。重新执行任务 `scert-executor-final-20261003-2052.service`，日志 `executor-final.log`，当前未记通过。
- [x] 本轮第二台根分区复核可用 26 GB、使用率 10%，未继续删除文件；第一台 Scert、Nginx 运行，可用约 9.7 GB。
- [ ] 本机协议/终端、PostgreSQL 等必要实库和正式双机生产闭环仍待完成；生产验收未通过。

## 2026年10月3日20:20 后端全量最终结果

- [✓] 第一台 `scert-operation-full-20261003-2012.service` 已终止，`MainPID=0`、`Result=success`、`ActiveState=inactive`；整组约四分五十八秒，`OPERATION_FULL_RUN_EXIT=0`。
- [✓] `operation-full.log` 显示补充竞态退出码零、`go test -p 1 -count=1 -timeout 10m ./...` 退出码零；HTTP 包约 44.344 秒、仓储约 14.511 秒。所有后端包完成，未跳过 HTTP 或修改断言以通过。
- [x] 上述默认全量未启用外部数据库实库；MySQL 六场景实库证据单独见此前修复专项，PostgreSQL 新持久化能力实库尚待验证。
- [x] 代码检查点 `b3f27e8` 已推送；第一台根分区可用约 9.7 GB，Scert 与 Nginx active；第二台本轮此前复核可用 26 GB。
- [ ] 本批不构成正式安装包、三库首次 Web、系统邮件、公共 HTTPS 或最终生产验收。本节覆盖下方全量运行中描述。


## 2026年10月3日20:17 完整操作互斥回归

- [x] `scert-operation-20261003-2011.service` 成功结束；`operation-race-vet.log` 中部署包竞态约 3.189 秒，`OPERATION_RACE_EXIT=0`、`OPERATION_VET_EXIT=0`，整组约 43.506 秒。
- [x] 补充竞态：部署约 1.086 秒、仓储约 2.016 秒，`OPERATION_EXTRA_RACE_EXIT=0`；实际覆盖 SQLite 关库重开后服务启动识别占用及原事务不变。
- [~] `scert-operation-full-20261003-2012.service` 正执行最新后端全量；日志为第一台隔离目录中的 `operation-full.log`。未修改正在运行任务的源码。
- [x] `service.go`、`operation.go`、`operation_test.go`、`recovery_disposition_repository_test.go` 的本地与远端 SHA-256 一致；测试目录 Git 索引仅用于源码边界测试，不是正式部署。
- [ ] 本轮未替换线上正式包，不代表公共入口、首次 Web 流程或生产验收通过。


## 2026年10月3日20:04 MySQL 修复复测与磁盘复核

- [x] 原任务 `scert-disposition-extra-20261003-1945.service` 已失败终止：补充竞态通过，MySQL 迁移十二场景通过；仓储三个审计注入因错误 1419 失败。原日志保留，不记整组通过。
- [x] 修复任务 `scert-disposition-fix-20261003-2000.service` 成功结束；`disposition-mysql-fix.log` 显示三动作及三个审计失败回滚全部通过，`DISPOSITION_MYSQL_FIX_EXIT=0`，仓储约 32.197 秒。
- [x] 故障触发器仅由本次隔离实例管理连接创建，限定测试数据库；仓储继续使用受限业务连接，严格 TLS 与回环监听保持。
- [x] 第二台 `45.136.13.241` 根分区 29 GB，已用 2.9 GB，可用 26 GB，使用率 10%，inode 3%；Scert、scert-nginx、nginx、mysql、pmail 均 active，`nginx -t` 通过。本次只读复核。
- [ ] 上述证据仅证明专项和主机状态，不代表正式安装包、公共 HTTPS 或生产验收通过。


## 2026年10月3日19:43 自动回归及实机边界

本节覆盖先前仍标运行中的结果；这些是第一台隔离目录中的原生 Linux Go 回归，不是正式候选包验收。未清理生产数据库、未手工改历史事务、未创建辅助验收脚本。

- [x] `final-full-http-race.log`：R2 最新后端全量退出码零；当时 HTTP 包四分钟累计超时，不能将当次整组记为通过。
- [x] `review-race-vet.log`：加入 R3 准备绑定后，部署、本机协议、管理员终端三包竞态、完整 `go vet -p 1 ./...`、HTTP 全包竞态分别退出码零；HTTP 约 374.825 秒；整组 `REVIEW_RUN_EXIT=0`。
- [x] `disposition-race-vet.log`：仓储约 104.544 秒、数据库约 133.013 秒、部署约 3.103 秒，三包竞态通过；完整静态检查通过；整组退出码零。测试单元限制内存 900 MiB、CPU 75%，以 nobody 运行。
- [~] 后续新增数据库关闭重开、准备摘要转换及 MySQL 实库专项，尚未取得结果；不能由上一批结果覆盖这些新增测试。
- [x] 第二台实机复核：根分区 29 GB，使用 2.9 GB，可用 26 GB，使用率 10%；五项既有服务 active，Nginx 配置检查通过。本轮没有扩大删除范围。
- [ ] 正式候选包、三数据库首次 Web、系统邮件、真实 DNS/ACME、公共 HTTPS、多对多推送和失败恢复仍需单独验收。

自动回归证据目录为第一台 `/var/tmp/scert-local-recovery-20261003-1828/`；第二台继续只用于证书推送测试。


## 2026年10月3日19:02 本机核查边界回归

第一台 `8.137.23.95` 隔离目录 `/var/tmp/scert-local-recovery-20261003-1828/`；不使用第二台运行本轮回归，不清理业务数据库或恢复现场。

- [x] `full.log`：数据库修复后的后端全量退出码零，耗时四分四十四秒。外部数据库及实机可选测试缺少环境配置时跳过，不属于生产验收证据。
- [!] `race.log`：数据库、部署、本机协议、管理员终端四包通过，HTTP 依赖编译因内存限流达到二十分钟上限；总退出码一、单元 Result=timeout、MainPID=0，未当成测试通过。
- [x] `frames-vet.log`：长路径有界分片版本的本机协议、管理员终端和部署三包竞态通过；SQLite 截止时间两组专项重复二十次通过；后端完整 `go vet -p 1 ./...` 通过；联合命令退出码零，耗时一分四十八秒。
- [~] `final-full-http-race.log`：最新全量与 HTTP 单包竞态串行运行，结果待记录。隔离任务内存硬上限 900 MiB、CPU 75%，未调整生产服务配置或关闭校验。
- [x] 第二台磁盘再次复核：根分区 29 GiB、已用 2.9 GiB、可用 26 GiB，使用率 10%，inode 3%；五项原业务服务 active，Nginx 配置检查通过。本轮没有扩大删除范围。
- [ ] 新代码尚未通过正式安装器部署；三库首次 Web、系统发信、DNS/ACME、公共 HTTPS、证书推送与回滚仍需独立实测。


## 2026年10月3日18:28 最新执行证据

本节优先于下方历史状态；第一台部署测试，第二台仅推送测试。源码隔离测试不替代清库后的正式安装与浏览器业务验收，未清理生产数据库或部署现场。

- [x] 第一批 R2 三包回归及 HTTP 本机核查、密码重置专项通过，证据分别为第一台 `/var/tmp/scert-local-recovery-20261003-1757/unit.log`、`http.log`。
- [!] `/var/tmp/scert-local-recovery-20261003-1811/full.log` 对应单元已终止，退出码一。安装器、发布边界、服务单元检查缺少外围源码；数据库路径 `immediate_transaction` 在约 151 毫秒返回 SQLITE_BUSY 而非截止时间错误。
- [x] 完整基线归档 SHA-256 为 `436ee793b03e980642400992515833d559faefb615c0f4124f2f077a3b4a2b20`，与最新后端源码合并到 `/var/tmp/scert-local-recovery-20261003-1828`；建立无远端凭据的隔离 Git 索引，未改运行中的源码。
- [x] 新增确定性截止时间用例在旧实现复现两项失败，早期锁冲突、成功结果、其他错误和主动取消四项通过；日志 `deadline-before.log`，退出码一是预期的缺陷复现，不是验收通过。
- [~] 修复后回归、R2 竞态与静态检查待运行，尚未构建正式包。
- [x] 第二台于 18:21 清理可重建 APT 索引及缓存，释放约 315 MiB，根分区使用率从 12% 降至 10%，可用约 26 GiB；Scert、证书激活、nginx、mysql、pmail 均 active，nginx 配置检查通过，业务数据保留。

> 本轮最新证据见文末「2026年10月3日17:22 只读恢复核查原生 Linux 回归」，不代表正式部署验收通过。

## 2026年10月2日21:30 手动全局续期后台化回归（未部署）

- [x] 修改前基线 4ab3f93 已提交推送。本批继续只用密码 SSH/SCP 和严格主机密钥校验；第一台隔离目录 /var/tmp/scert-report-race-20261002-1801/renewal-source，以 nobody 运行原生测试，不创建辅助验收脚本。
- [x] 最新后端增量 renewal-changes-second-20261002.tar 两端 SHA256 一致：ac2fa2715cb83848f54bbb25e235fe1bb079f5e21c39520dda03bd6b6a5b5f7d。完整基线 renewal-full-baseline-4ab3f93-20261002.tar 两端 SHA256 一致：e8efd10986484bad1d9d4bb0ffb01ef72b40f14c2d53f4fbffe253370ad3ae09。归档为测试源码，不是正式发布包。
- [x] 第一版专项 renewal-first.log 完成，RENEWAL_FIRST_EXIT=0；断开 SSH 后重新核验进程及日志确认终态，未因会话断开重复开测。该版不含后补审计隔离及真实调度器联调用例，不用它代表最终全部改动。
- [!] 最新源码第一次全量 renewal-second.log 退出一：后端单独基线漏带 deploy、build、.gitignore 等源树依赖，安装器、发布边界及服务单元测试失败；HTTP 与仓储包分别通过。没有删除失败日志或跳过检查。确认终态后补齐完整同提交基线、重新覆盖最新增量，并为源码边界测试建立隔离 Git 索引。
- [x] renewal-final-full-vet.log：go test -p 1 ./... -count=1 与 go vet -p 1 ./... 均完成，RENEWAL_FINAL_FULL_EXIT=0、RENEWAL_FINAL_VET_EXIT=0。使用 Go 1.26.6、GOMAXPROCS=1、GOMEMLIMIT=384MiB 和现有本地模块代理；测试自行创建独立空数据库，外部实库默认跳过，不算三数据库完整验收。
- [x] renewal-final-repeat.log：TestAsyncRenewal、TestRenewalAPI、TestRenewalReport、TestRenewalCheck 重复二十次通过，RENEWAL_FINAL_REPEAT_EXIT=0；HTTP 包 45.438 秒、仓储包 2.480 秒。包含十四种生命周期场景、运行取消保留计数、真实调度器管理与业务双路径、权限隔离及审计故障事务回滚；不是竞争检测。
- [x] .runtime/renewal-frontend-second-20261002.log：272 项通过、零失败、零跳过；.runtime/renewal-frontend-build-20261002.log：生产构建退出零。新增测试已纳入正式入口，第一版 266 项结果不作为本批完整证明。
- [x] 21:30 复核第一台 Scert、Nginx、MySQL 均 active，磁盘约剩 13GB；本批未替换线上程序、未重启生产服务、未清空线上数据库、未新增公网监听。
- [!] 尚无本批竞争检测、浏览器或正式部署验收通过证据；不关闭其他长操作、历史恢复、升级、通知重置、三库首次 Web、备份恢复、监控及整体生产门禁。

## 2026年10月2日20:34 证书校验后台任务及幂等契约回归（未部署）

### 代码与隔离边界

- [x] 接续 fce5a84 校验开发批次，修复前精确提交推送 24a9683；没有暂存测试归档、日志、密钥或凭据。本批只使用原生 Go／前端用例，没有创建辅助验收脚本。
- [x] 最终后端增量 validation-async-second-20261002.tar 的本机与双机 SHA256 均为 3f6d01008441f2053b7098732bf622aab16b6d9a34ee513967ff1deaa437227b。第一台额外使用 fce5a84 源码基线归档，SHA256 为 a82b68293c7ce1d0a8155e08164a3a6ad2b6ec57df8465731da1a6fad673aca2；均只包含源码，不是正式发布包。
- [x] 密码 SSH/SCP 沿用严格主机密钥校验，未启用公钥登录。第二台复用 /var/tmp/scert-async-20261002-1556/source/backend；第一台使用 /var/tmp/scert-report-race-20261002-1801/validation-source/backend。测试以 nobody 执行，GOMAXPROCS=1、GOMEMLIMIT=384MiB、-p 1。

### 后端与实库证据

- [!] validation-second-full.log：第一次全量测试在 SSH 被重置后收到 Hangup；重连确认没有测试进程才重新启动。此记录不记为通过，未覆盖原始日志。
- [x] 第二台 validation-final-full-vet.log：go test -p 1 ./... -count=1 与 go vet -p 1 ./... 完成，VALIDATION_FINAL_FULL_EXIT=0、VALIDATION_FINAL_VET_EXIT=0。外部实库默认跳过，不把此结果当成三库完整验收。
- [x] 第二台 validation-final-repeat.log：TestAsyncValidation、TestAsyncExecutionRechecksQueuedAuthorization、TestTaskValidation 重复二十次通过，VALIDATION_FINAL_REPEAT_EXIT=0；HTTP 包 158.647 秒，仓储包 16.395 秒。覆盖独立结果、无伪报告、失败审计原子回滚、撤权与排队取消等路径，不替代竞争检测。
- [x] 第一台 validation-mysql21.log：既有 /usr/sbin/mysqld 8.0.46 启动独立空实例，仅监听 127.0.0.1:42647，严格 TLS。TestMySQLRealTaskValidationMigrationRecovery 八场景通过，VALIDATION_MYSQL21_EXIT=0，45.369 秒。
- [x] 第一台 validation-mysql57.log：复用既有 MySQL 5.7.44 程序，独立空实例仅监听 127.0.0.1:44333，严格 TLS；同一八场景通过，VALIDATION_MYSQL57_EXIT=0，21.575 秒。覆盖建表前后恢复、已有详情保持，以及字段容量、主键顺序、空值、引擎和额外字段冲突拒绝。
- [x] 两个独立 MySQL 测试实例均已退出；正式 MySQL 进程仍为 1092。第一台 Scert、Nginx、MySQL、Xmail 和第二台 Scert、Nginx、MySQL 均 active。第二台磁盘剩余约 774MB，未在该机新增数据库实例或擅自清理产物。

### 前端证据与未关闭项

- [x] .runtime/validation-async-frontend-second-20261002.log：上一轮组件注册修复后 263 项通过，生产构建通过。但审查随后发现校验幂等键含斜线，旧测试只判断请求头存在，未覆盖后端合法字符契约。
- [x] .runtime/validation-idempotency-contract-before-20261002.log：补充断言后四个相关测试中两个失败，明确复现 certificate/validate 前缀问题；申请、部署对照用例通过。修复使用 certificate-validate 前缀，没有放宽后端输入限制。
- [x] .runtime/validation-async-frontend-final-20261002.log：最终 266 项全部通过，零失败、零跳过；.runtime/validation-async-build-final-20261002.log：最终生产构建退出零。新详情组件包含真实 Vue 模板编译和服务端渲染回归，不称为浏览器实机验证。
- [!] 未替换线上程序、未清空线上数据库、未新增公网监听。本批不是正式包或生产部署验收；PostgreSQL 实库、三库首次 Web、历史恢复、其他长操作、升级、通知重置、备份恢复和完整生产门禁仍未关闭。

## 2026年10月2日19:34 证书导入异步自动回归（未部署）

- [x] 修改前确认 main 工作区干净，09616e7 基线推送返回已同步。第二台复用已核验主机指纹的密码 SSH 会话；隔离路径 /var/tmp/scert-async-20261002-1556/source/backend，不触碰正式安装或线上数据。
- [x] 最终后端输入 import-async-second-20261002.tar 两端 SHA256 一致：748f229134f568e7269d15d9cdd8412c5a62cec1f97ca65d28cda57e35f118c4。仅上传本批源码与原生 Go 测试，不是正式发布包。
- [!] import-after.log：首轮专项退出一，仅排队取消子场景因测试调用不存在的 POST /cancel 返回 404；修正为真实 DELETE /tasks/:id、202 响应，不放宽最终 cancelled 和零外部调用断言。
- [x] import-second.log：专项通过，IMPORT_SECOND_EXIT=0，6.967 秒。包含十一种上传生命周期场景、缺少任务仓储回收、导入新增十二个实时授权场景，以及原有申请／部署／重试授权回归。
- [x] import-full.log：全后端 go test -p 1 ./... -count=1 通过，IMPORT_FULL_EXIT=0；import-vet.log：go vet -p 1 ./... 通过，IMPORT_VET_EXIT=0。使用 nobody、GOMAXPROCS=1、GOMEMLIMIT=384MiB，测试自行创建独立空 SQLite 数据库，不将此记录当作三库完整实库验收。
- [x] import-repeat.log：新增 TestAsyncImport 专项重复二十次通过，IMPORT_REPEAT_EXIT=0，29.448 秒；普通重复测试不替代竞争检测。
- [x] 本机 .runtime/import-async-frontend-final-20261002.log：260 项通过，零失败、零跳过；.runtime/import-async-build-final-20261002.log：生产构建通过，退出码均为零。
- [x] 测试后第二台 Scert、Nginx、MySQL 均 active；磁盘余量约 863MB。未新增公网监听、未重启生产服务、未清理线上数据库。
- [!] 本批无正式部署或浏览器生产上传验收，无强制终止后的上传残留清理证明，无竞争检测通过结论。历史恢复、其他长操作及原有整体门禁继续未关闭。


## 2026年10月2日19:01 排队任务实时授权回归（未部署）

- [x] 修改前基线 657a9c0 已推送。第二台既有 SSH 会话已实时复核，测试目录为 /var/tmp/scert-async-20261002-1556/source/backend，磁盘剩余约 961MB；未清理或连接线上业务数据库。
- [x] 新测试文件两端 SHA256 为 18047f955d7509f9a1258aaffd1f5df92f5ba5e64b98ddc082f8b56aa30fb8de。authorization-before.log 中旧代码在排队后权限变化时仍执行一次外部操作，AUTHORIZATION_BEFORE_EXIT=1，耗时 3.498 秒。
- [x] 修复及扩展重试测试归档 authorization-fix-20261002.tar，两端核验 SHA256 为 acb218c45242e08d2637fb7df397778daea7da8eb4671a1665c28e8ebf3603f4；仅展开至隔离源码目录，不替换线上程序。
- [x] authorization-after.log：二十四个申请／部署场景及四个重试场景通过，AUTHORIZATION_AFTER_EXIT=0，耗时 3.476 秒；拒绝时外部调用为零，任务与尝试终态一致且保留失败审计；错误详情未进入任务消息。
- [x] authorization-full.log 和 authorization-vet.log：本批原生 Linux 全后端与静态检查通过，AUTHORIZATION_FULL_EXIT=0、AUTHORIZATION_VET_EXIT=0。采用 nobody、单并行、GOMEMLIMIT=384MiB，不提权执行测试；未启用外部数据库的全量测试不算三库实库验收。
- [x] authorization-repeat.log：新增两组专项重复二十次通过，AUTHORIZATION_REPEAT_EXIT=0，耗时 74.602 秒；旧代码失败子场景实数为二十。既有 Scert、Nginx、MySQL 均 active；磁盘剩余约 920MB，不新增数据库实例或无限制竞争检测。
- [!] 仅证明 HTTP 异步工作线程开始执行前的实时复核，不代表自动续期链路、操作开始后撤权强制中止或全部权限竞态均已验收；本批未部署，三库首次 Web、竞争检测及总体生产门禁仍未完成。


## 2026年10月2日18:35 后台原子收尾整改（未部署）

- [x] 上批 background-full.log 与 background-vet.log 已取回：BACKGROUND_FULL_EXIT=0、BACKGROUND_VET_EXIT=0。第二台既有服务 active，进程检查仅保留系统 MySQL；磁盘剩余约 1014MB，不新增数据库实例。
- [!] 第一台 scert-report-race-20261002-1801 的 Result=timeout，运行十分钟退出一；内存上限 512MB、CPU 上限 50% 实际生效。没有竞争检测通过结论，无残留测试／编译进程，Scert、Nginx、MySQL active，可用内存约 903MB。
- [x] 修改前已推送 cf3bc7a。后台原子化测试归档 SHA256 为 d32c8d1849ebbd542523006cb131275e2f6ed925401e143ac69d4f1dc5a3ff40，两端一致；旧实现 background-atomic-before.log 退出一，报告缺失、任务／尝试／审计部分提交被复现。结果写入失败场景还触发重复重试后的同名触发器错误，测试已改为幂等安装触发器，不将该错误作为独立产品缺陷。
- [x] 修复归档 background-atomic-fix-20261002.tar，SHA256 为 78a140e9f16f83ca3eeac7e86635afe6f199cc1351210b48589f9ece7acb1123，两端校验通过；background-atomic-after.log：应用 21.663 秒、仓储 10.407 秒、HTTP 29.936 秒，BACKGROUND_ATOMIC_AFTER_EXIT=0。
- [x] 新测试覆盖五处写入故障整体回滚、每次重试完整报告、重试等待取消、禁止重试重新激活取消任务、无尝试收尾不能绕过活动尝试，以及无尝试路径的审计失败回滚。
- [x] background-atomic-full.log 与 background-atomic-vet.log：全后端测试及 go vet 均通过，BACKGROUND_ATOMIC_FULL_EXIT=0、BACKGROUND_ATOMIC_VET_EXIT=0；既有三项服务 active，磁盘剩余约 961MB。外部数据库未启用时的全量测试不当作三库实库回归。
- [x] background-atomic-repeat.log：新增收尾和取消专项重复二十次通过，应用 48.982 秒、仓储 49.985 秒，BACKGROUND_ATOMIC_REPEAT_EXIT=0；普通重复测试不替代竞争检测。
- [!] 本批仅隔离源码开发及自动回归，不修改正式程序和线上数据库；未运行三库首次 Web 或正式部署，不代替生产验收。

## 2026年10月2日18:11 后台取消与 MySQL 报告迁移回归（未部署）

- [x] 新增原生 Go 回归 TestCertificateCancellationPreservesDeploymentOutcome；background-cancel-before.log 中四个场景全部失败，BACKGROUND_CANCEL_BEFORE_EXIT=1。三个场景被错误标为 cancelled，已恢复场景未保存逐服务器结果。
- [x] 修复为部分成功优先、恢复未确认／失败不被取消掩盖，并使用独立十秒收尾上下文保存实际结果。background-cancel-after.log 重复二十次通过，BACKGROUND_CANCEL_AFTER_EXIT=0，耗时 17.847 秒。普通重复回归不替代竞争检测。
- [x] 新增原生 Go 实库测试 TestMySQLRealTaskReportMigrationRecovery；使用系统已有 /usr/sbin/mysqld 8.0.46，不安装额外数据库，不接受已有实例连接串。运行前检查磁盘余量；新实例仅监听 127.0.0.1:3887，严格 TLS，测试使用随机凭据。
- [x] report-mysql8.log：三条迁移恢复路径与五类冲突全部通过，REPORT_MYSQL8_EXIT=0，耗时 49.100 秒。覆盖超过普通 TEXT 容量的中文报告保持、主键顺序、可空字段、存储引擎及额外字段；冲突重复拒绝且结构和报告不被覆盖。
- [x] 实库产物保留于 /var/tmp/scert-report-mysql-20261002-1808/scert-mysql-integration-1850050069；没有连接或清理线上业务数据库，没有创建辅助验收脚本。
- [~] background-full.log 与 background-vet.log 为新增后台修复后的全量回归，当前尚待收集最终结果。
- [~] 第一台从干净源码归档 4e8d509 启动受限竞争检测，归档 SHA256 为 401d480814aa3f777fcb51b36b7837ced08ba6964475b2d996066167da48eec8。独立单元 scert-report-race-20261002-1801，MemoryMax=512M、MemoryHigh=384M、MemorySwapMax=0、CPUQuota=50%、RuntimeMaxSec=600；限定任务与 HTTP 异步专项，不扩展成全项目竞争检测通过。
- [!] 当前 MySQL 5.7、PostgreSQL 新迁移、后台全报告原子化、其他长操作、关停 502、通知重置及统一生产门禁仍待完成；本批未正式部署。

## 2026年10月2日17:56 完整报告与审计事务回归（未部署）

- [x] 源码 4e8d509 已推送；第二台隔离目录 /var/tmp/scert-async-20261002-1556/source 执行，不修改正式安装目录或线上数据库。
- [x] 最新输入 async-report-20261002-1743.tar 在两端核对 SHA256 为 0b3ec412aa6d8d0c93e03d4c01b067ebe8e6047ecfc03eea4a0cdf4a9a2158d8；第一次等待密码的 SCP 已断开，确认终止后重新上传，重传退出 0。
- [!] report-first.log：第一轮失败，仓储测试错误绕过正式重试入口，SQLite 取消测试在单执行线程下没有覆盖取消；已改用正式重试入口和显式让出调度，未放宽终态或取消覆盖断言。
- [!] report-second.log：仓储和数据库通过，HTTP 三项失败；修复重复完成审计及 failed 到 error 的既有审计状态契约，更新启动审计失败时真实失败审计的断言。
- [x] report-third.log：仓储 9.488 秒、HTTP 30.477 秒、数据库 31.531 秒，REPORT_THIRD_EXIT=0。覆盖完整报告重读、重试隔离、权限撤销、脱敏及报告／逐目标／审计故障整体回滚；使用隔离 SQLite，不当作 MySQL 或 PostgreSQL 实库通过。
- [x] 本机 .runtime/async-report-third-frontend-20261002.log：256 项通过，零失败、零跳过；.runtime/async-report-third-build-20261002.log：生产构建通过，退出码均为 0。
- [x] report-full.log 与 report-vet.log：全后端测试与静态检查通过，REPORT_FULL_EXIT=0、REPORT_VET_EXIT=0；使用 nobody、GOMAXPROCS=1、GOMEMLIMIT=384MiB、-p 1。未启用外部数据库的测试不计实库通过。
- [!] 本批无正式部署、无首次 Web 验收、无竞争检测通过结论。第一台旧竞争检测已中断退出 130，不重启无限制负载；不新增公网端口、不放宽 TLS 或 SSH 主机校验。

## 2026年10月2日17:11 任务收尾事务与服务配置回归（未部署）

- [x] 第二台隔离源码首次关联回归：internal/httpapi、internal/repository、internal/servicehost、internal/app 全部通过，COMPLETION_FIRST_EXIT=0；日志为 /var/tmp/scert-async-20261002-1556/completion-first.log。
- [x] 补充回滚失败的取消边界后，使用非特权用户、GOMAXPROCS=2、包并行数一执行完整后端测试和 go vet，COMPLETION_FULL_EXIT=0、COMPLETION_VET_EXIT=0；日志为同目录 completion-full.log 与 completion-vet.log。
- [x] 终态仓储失败注入、任务与尝试一致性、启动恢复接管、取消不覆盖实际结果、动态及静态单元预算断言均纳入本批测试；数据库用例为隔离 SQLite，未触及线上业务库。
- [~] 同目录 completion-repeat.log 正在记录异步、终态事务及服务配置二十次普通重复回归；completion-unit.log 将记录静态单元检查。
- [!] 第一台 race-second.log 的竞争检测尚未取得退出状态。新建本机 SSH 会话无握手结果；从第二台按固定 ED25519 指纹和密码方式连接同样在 banner exchange 超时。原执行会话无输出，已发送中断但无确认，不以本地句柄存活断言远端仍正常运行，也不擅自重复启动测试。
- [!] Git 推送出现两次连接超时，本地提交 9e666be 已保存。网络问题不能算产品用例失败，也不能掩盖缺失的竞争检测证据。
- [!] 本批没有安装线上程序、清空业务数据库、开放外部端口、关闭 TLS 校验或创建辅助验收脚本。最终构建部署、三数据库首次 Web 流程和生产复审仍需独立完成。

## 2026年10月2日16:47 异步任务开发回归记录（未部署）

### 已完成的开发回归

- [x] 本机前端：npm.cmd test 为 252 项通过、零失败、零跳过；npm.cmd run build 通过。日志为 .runtime/async-frontend-20261002-final.log 与 .runtime/async-frontend-build-20261002-final.log。
- [x] 第二台隔离源码、非特权用户执行 go test ./... -count=1 和 go vet ./... 均返回零；日志位于 /var/tmp/scert-async-20261002-1556/backend-full.log 与 backend-vet.log。首次旧同步接口断言失败已修复，原始日志保留。
- [~] 第二台竞争检测因未启用 CGO 且无本机编译器未执行；第一台首次离线代理缺少依赖也未执行用例。已复用第二台公共模块缓存、三端校验归档摘要一致，在第一台启用 CGO 后重新执行异步专项竞争检测二十次，等待结果。

### 证据边界和剩余任务

- [~] 复核发现分发单元与动态安装模板的停止预算尚未同步，继续修复并回归，不以静态文件修改代替实际安装行为。
- [!] 本批未替换线上候选，未执行浏览器首次初始化，未清理线上数据库，未新增辅助验收脚本，也未降低 TLS 或 SSH 主机身份校验。
- [ ] 正式候选统一部署后仍需验证长任务超过反代等待窗口、断线后查询、取消回滚、进程重启恢复及三数据库首次完整 Web 流程。
- [!] 下方 504 与 502 为真实历史失败，异步开发回归通过并不自动关闭实机问题；旧事务、系统通知、备份恢复及总体生产门禁继续保留。

## 2026年10月2日15:31 正式候选实机负向恢复与重试

### 范围和准备

- [x] 沿用已部署 7ba4046 正式候选，复用 scert-multi-20260930.apmmk.cn 当前证书，不重新签发。第二台服务器配置经正式 API 临时使用九十秒等待后失败的校验命令，重载命令不变；未创建脚本文件或新增远端备份。
- [x] 故障前无活动任务，目标目录无事务残留；证书和证书链 SHA256 为 7e8b56227b345ed9b66659e428f9bd4421604f11253ab88d65b3ed61737f35cf，私钥仅记录摘要，不输出内容。

### 摘要不匹配与锁归属不匹配

- [x] 正式部署任务 e3a9e1c8-c930-4fa0-b2aa-2730da019503，第二台事务 20261002-072057.661993684-c999333470ae。只读确认 installed/pending、尚未重载及等待进程存活后，向本事务生成的 cert.pem 旧文件追加一个换行。注入前校验事务记录摘要、普通文件类型、大小与 inode；第一次因 SQL 路径转义错误拒绝注入，修正后才执行。
- [x] 15:22:37 第一次尝试失败：恢复备份身份不一致，状态 recovery_required。注入后及任务终态的整个目标目录快照一致，输出 PRESERVED_EXIT=0，产品未覆盖或删除未知内容。
- [x] 按 inode、大小、注入后摘要核对后，仅截去本次追加的一个字节；恢复原摘要成功。随后核对锁标识与事务证据一致、文件类型和六十四字节长度，受控替换本事务 cert.pem 锁标识，未改数据库。
- [x] 15:24 重启正式服务：第二台因部署锁不属于本次事务而拒绝恢复，OWNER_PRESERVED_EXIT=0；第一台事务 20261002-072230.603680358-5718e8942e14 自动 rolled_back。第二台进程变为 39183。
- [x] 核对 inode、长度及受控标识后还原原锁内容，未手工释放锁。15:25:03 再次由正式服务启动恢复，两事务均 rolled_back，第二台进程 39281；目标三个文件恢复基线摘要，本次锁、临时文件和旧文件均由产品收尾。

### 原任务重试与最终状态

- [x] 重启后的旧会话返回 401；通过正式登录接口重新认证，再经正式服务器 API 恢复标准 nginx -t 与 nginx -s reload，持久化配置确认两台一致。
- [x] 同一任务第二次尝试于 15:27:36 成功，正式重试接口 HTTP 200；新事务 20261002-072722.386340837-1d6236abe88e 与 20261002-072731.782859344-4f9f668d2629 均 committed。成功后再次重试 HTTP 409，任务尝试仍只有首次失败及第二次成功。
- [x] 15:28 双机三个文件摘要一致，本次事务无残留；两台本地回环 SNI、系统 CA 和主机名验证通过，第一台 Scert、Nginx、xmail 均 active。双机 Scert 仍为 127.0.0.1:8443；第一台访问第二台普通公网域名 readyz 为 HTTP 200、TLS 校验零。内部 SNI 检查不替代第一台公网平台限制验收。
- [x] 当前活动任务为零、历史缺证据 recovery_required 仍为两条，未补造证据。管理证书续期阈值仍为六天、十天。

### 保留的失败和新问题

- [!] Nginx 日志确认 15:21:56 初次部署请求 upstream timed out，HTTP 504、响应为 HTML；任务到 15:22:37 才实际失败。浏览器探测的 JSON 解析错误不是任务终态，需要整改正式长任务提交及查询机制，不通过扩大超时掩盖问题。
- [!] 15:24:19 受控重启期间 readyz 返回 502，原因是上游连接拒绝；后续严格 HTTPS 首页及 readyz 恢复 200。该中断不能记作无中断升级通过。
- [~] 本轮为保留数据的故障注入复核，没有清库，不替代首次完整 Web 流程、邮件通知、备份恢复及最终生产验收；上述局部实机证据等待复审。


## 2026年10月2日15:15 管理入口接线修复与正式轮换

- [x] 14:58 环境维护：在既有协调锁和辅助激活锁保护下，使用上游现有受信材料修复 Nginx 专用引用，配置检查后平滑重载。旧事务摘要保持不变，旧证据仅保存本机，远端未新增备份；该人工接线步骤不算产品自动协调验收。
- [x] 15:03:15 正式 Web 策略更新触发 management_renewal 任务 a1c7ffd8-5623-4740-9deb-b7cdef88c3f2；审计终态 success 时间为 15:03:31，任务阶段 activate。ACME 复用了仍有效的授权，未重新执行 DNS 挑战，因此本次不能增加 DNS 新挑战通过结论。
- [x] 应用 coordination.json 的 phase 与辅助 transaction.json 的 status 均为 committed；request_id、proxy_id 与辅助 id 均为 6ce45429e2cbef69f9083dc2a88147c6。旧失败预检任务 f11c8768-b280-4f7e-ace2-26a132af038a 继续保留。
- [x] 代理 443 与上游 8443 实际新叶指纹均为 237e637e60f5553a8dba343340ed63a4db20cb5a14ec02eff9381aa2ba3c7d6d；有效期至 2026-12-31 06:04:52 UTC。普通域名首页、readyz 严格 HTTPS 返回 200；nginx -t 通过。
- [x] Scert 主进程仍为 37512，监听 127.0.0.1:8443，未新增公网端口。辅助新版本目录 root:root 0700，私钥 root:root 0600、单硬链接。
- [x] 15:14 经正式 Web 保存原续期阈值六天、临近阈值十天；只读读取持久化配置确认。管理续期仍只有本次成功及此前失败两条，没有再次签发。
- [~] 本次未清库、未运行三数据库首次流程，不代表故障恢复矩阵或整体生产门禁通过；正常轮换结果等待独立复审。


## 2026年10月2日14:52 独立辅助程序与管理续期安全预检

- [x] 第二台独立辅助程序于 14:26:38 完成原子替换，核心服务和 socket 恢复 active。当前程序 SHA256 为 651caa143f7ffcb6411ad9231e7e3418c43a65088ee11fa5a10173961c33e88e，与正式候选一致；unit 模板一致，未改 unit。旧程序仅下载到本机，远端未新增备份。
- [x] 升级前后策略 SHA256 为 5b701811a3d6c0759f5f4e6cd440a7c3b1ab127640ed6f5606767a9786755876，历史事务为 36ea481ae6e9cc1bfd0da5450a2d604c9ea46a759a05ae62f7b58a873ccdd76f，引用为 8b58e905141176791ad155e366495213c5a08da0ba440801313486174f334cfc。升级没有重建状态或证书目录。
- [!] 14:45 至 14:47 使用带主机名验证的 OpenSSL 分别核查回环代理与上游：代理叶指纹为 1D5A7F2C2E6D229DF56F87CFE18DD37C1E4C9638C8B56F99AB50022872D526A0，到期 2026-12-24 15:56:36 UTC；上游为 1C033D1628968235132D9D6E5B0AAEC1C9411C5C884554950A83AA7B3759B8CC，到期 2026-12-28 19:43:37 UTC。二者不一致，不认定协调就绪。
- [x] 14:48 经正式 Web 系统设置保存续期阈值八十八天、临近阈值九十天，策略更新自动触发调度。管理任务 f11c8768-b280-4f7e-ace2-26a132af038a 终态 failed、阶段 preflight；审计保留 running 与 failed。辅助程序实际由 socket 启动，主进程 38462；核心日志记录本地激活操作未完成。未重复发起签发或使用自制协议脚本。
- [x] 已通过同一 Web 表单恢复阈值六天、十天，14:50 只读核查持久化配置一致。原证书签发任务计数不变，管理任务只有上述一条；策略、旧辅助事务及引用摘要不变，未产生应用协调记录。此为签发前拒绝的负向证据，不是正常轮换成功。
- [x] 普通域名首页和 readyz 继续返回 200 且 TLS 校验为零，Nginx 配置检查通过；双机 Scert 均仅监听回环 8443，未新增公网端口。
- [~] 本轮为保留数据的升级及负向复核，没有清业务库，不代替三数据库首次流程。接线一致性、正常双层提交、故障恢复与整体生产门禁仍待完成。

## 2026年10月2日14:20 候选正式构建与双机核心升级

### 构建与候选检查

- [x] 源码提交：7ba4046135bae9d55468c5c132019073db280c37。第二台 `/var/tmp/scert-recovery-release-20261002-1401/source` 为独立干净检出，正式构建入口返回 `RELEASE_BUILD_EXIT=0`，manifest 的 source_dirty=false，构建后 Git 状态仍干净。
- [x] 正式归档 SHA256：042f25acc87d8a68b0629f0b0bbacaa987204c00776e67ba0f5c3ddf2fd58edb。第二台、本机、第一台传输摘要一致，两台从正式归档解包后 SHA256SUMS 均通过，安装器具备归档声明的执行权限。
- [x] 前端测试日志 `frontend-test.log`：二百四十项通过、零失败、一项 Windows 专用发布脚本用例跳过。Linux 范围不把该项记为通过。
- [x] 候选全量模块检查 `module-verify.log` 返回 `RELEASE_MODULE_VERIFY_EXIT=0`，输出 all modules verified。第一台额外的全量模块检查因离线缓存仅含竞争用例所需模块而失败，不能记为全量模块通过；此前竞争检测本身退出零，范围不变。
- [x] `listen-negative.log`：发行程序使用 nobody、独立且不存在的配置路径，0.0.0.0、::、192.0.2.1、localhost 均退出 1，未创建配置文件；没有开启额外公网监听来完成测试。

### 第二台管理入口升级

- [!] 北京时间 14:09:05 首次误从构建工作目录调用安装器，因文件无执行权限退出 126，产品安装未发生；停服已发生，立即恢复旧服务，14:09:34 严格域名 readyz 恢复成功。失败保留在 `install-second.log`，这次额外停服不得从记录中隐去。
- [x] 随后使用正式归档解包目录 `package-second`，14:11:04 开始升级、14:11:06 服务 active；`install-second-archive.log` 对应 `SECOND_ARCHIVE_UPGRADE_EXIT=0`。未通过 chmod 构建目录文件来替代正式归档校验。
- [x] Scert 主进程 36639，Type=notify，监听 127.0.0.1:8443；首页与 readyz 的普通域名 HTTPS 均为 HTTP 200、TLS 校验零，HTTP 入口为 301。后续五次 readyz 均 HTTP 200、TLS 零、curl 退出零。
- [x] Nginx 与 MySQL 主进程分别仍为 792、775，Nginx 配置摘要未变；Scert-admin 保持 inactive。

### 第一台部署目标升级与范围

- [x] 第一台正式归档与包内摘要通过，14:13:30 至 14:13:32 完成核心升级，`install-first-archive.log` 返回 `FIRST_ARCHIVE_UPGRADE_EXIT=0`，Scert 主进程 25145，只监听 127.0.0.1:8443；未新增管理站点。
- [x] Nginx、MySQL 主进程仍为 1015、1092，Nginx 配置摘要未变；xmail.service 升级前后均 active，未对它执行停止、重启或配置操作。首次只读查询误用大小写不同的 Xmail.service，随后按实际单元名核实，不据此误报邮件服务故障。
- [x] 双机 scert、scert-admin、scert-storage 与对应候选包逐字节一致；主程序 SHA256：d7e05a0dcf2afc06b68dc00959fe2057eb40cc54bbfdabb26b12f84bcc88073f。
- [x] 双机 unit 校验均退出零。升级后双向 IPv4 探测对方的 8443、3306、33060 均超时未建立连接，仅证明这两个来源的对应路径，不能扩大为全部来源或 IPv6 外部探测结论。
- [x] 第一台跨机普通域名 https 检查 odacm.cn 的 readyz 返回 ready、HTTP 200、TLS 零、curl 退出零。
- [ ] 第二台独立 `/usr/local/libexec/scert-nginx` 仍记录旧提交 431fd043，和候选包摘要不同；普通安装器不安装该独立组件，本轮未改其程序、策略或事务。当前只读事务状态为 rolled_back，socket active；这不是新候选的双层协调验收，后续需按独立升级流程完成配套验证。
- [~] 本轮没有清业务库、没有执行浏览器首次流程，没有删除历史锁或事务；P0-10 业务负向矩阵、升级停服窗口、系统通知、三数据库首次流程和最终生产门禁仍未通过。

## 2026年10月2日13:59 测试隔离修复与完整检出回归

- [x] 十个测试文件共二十一行调整：重置状态文件、敏感配置及 SQLite DSN 均进入临时目录；共享连接池和子进程仍复用同一个测试数据库。不改产品行为、不提权写正式目录。
- [x] `/var/tmp/scert-recovery-full-20261002-1344/full-backend.log` 保留归档复测失败：三个原路径污染包已通过，仅发布边界测试因没有 Git 元数据失败；同目录 `full-vet.log` 退出零。
- [x] `/var/tmp/scert-recovery-checkout-20261002-1348/source` 为完整 Git 检出 3b25449，叠加十个测试文件修复；nobody 执行 `go test ./... -count=1`，输出 `CHECKOUT_FULL_EXIT=0`，`full-backend.log` 记录三十一个通过包。未跳过发布边界检查。
- [x] 13:55 双机监听核查：Scert 均仅监听 127.0.0.1:8443，Nginx 占用 80/443，Scert 与 Nginx 均 active；未改公网端口或共存业务。
- [!] 第一台 `/var/tmp/scert-recovery-race-20261002-1345/race.log` 的竞争检测未完成：依赖下载连接持续 SYN-SENT，13:58 中断后退出 130，不能记为通过。
- [x] 缓存归档三端 SHA256 一致，使用独立离线代理及新 GOPATH 执行 `go test -p 2 -race ./internal/deployment -run 'TestRecovery|TestJournal' -count=20`；保留 go.sum 校验，13:58:59 返回 `OFFLINE_RACE_EXIT=0`，`race-offline.log` 记录通过。仅覆盖恢复与事务用例，不声称全后端竞争检测通过。
- [x] 完整 Git 检出目录重新执行全后端 go vet，日志为同目录 `full-vet.log`，返回 `CHECKOUT_VET_EXIT=0`。
- [~] 本批修复未正式构建部署，线上仍为此前 1150c39 对应版本；未清业务库，未显式开启外部数据库用例，不扩大为三数据库生产验收。

## 2026年10月2日13:41 上传归属与扩大回归

- [x] 同一隔离目录的 `upload-baseline.log`：带事务日志的上传碰撞保护和未知临时文件恢复保护两个场景均失败，退出码 1，确认原实现会误删。
- [x] 增加临时文件归属摘要字段，只有独占创建确认后才登记并持久化；回滚及提交恢复在任何清理前核验完整临时文件集合。旧日志仍有临时文件但缺少归属证据时保留现场，无残留文件且其他证据完整的旧事务仍可恢复。
- [x] `upload-fixed.log` 中 deployment、repository、app 均通过，退出码 0；`upload-repeat.log` 中恢复及事务用例重复二十次退出 0；`full-vet.log` 全后端静态检查退出 0。
- [x] 真实回环密码 SSH/SFTP 用例扩展为七个场景，包括临时文件变更和归属未落库，不能用此替代业务服务器完整恢复流程。
- [!] `full-backend.log` 全后端测试退出 1：admin 重置状态文件、certificate 敏感信息目录及部分 database 测试仍错误使用 `/scert` 正式路径，被 nobody 拒绝；发布边界测试缺 `.gitignore` 及正式构建入口。没有提权或放宽生产权限，待测试隔离修复及完整源码复测。
- [~] 第一台只读检查发现已有 gcc，下一步可在独立非特权目录补做 Linux 竞争检测；此前第二台的失败不改写为通过。尚未构建部署本批修复。

## 2026年10月2日13:30 恢复预检原生 Linux 回归

本轮为第二台隔离源码测试，不是清库首次生产验收。使用 nobody、Go 1.26.6 和临时目录；未修改业务数据库、服务配置、Nginx 或公网端口。线上仍为此前 1150c39 对应发行包。

- [x] 隔离证据目录：`/var/tmp/scert-recovery-preflight-20261002-1308/`。`baseline.log` 记录旧代码八个不可信状态、两个部分解锁重入场景失败，退出码为 1。
- [x] 修复后部署包完整测试退出零；`regression-final.log` 中 deployment、repository、app 三包均通过，退出码为 0；`vet.log` 对应三包 go vet 退出零。
- [x] 新增八个不可信状态、两个已验证阶段重入、六个合法中断状态、HTTPS 失败后再恢复、五个认证前身份检查及五个真实回环密码 SSH/SFTP 子场景。SFTP 测试检查磁盘内容和权限，拒绝不可信现场时不能有文件变更。
- [x] `repeat-final.log`：`go test ./internal/deployment -run 'TestRecoveryPreflight|TestRecoveryVerifiedCleanup' -count=20` 实际退出零。
- [x] 首次扩大回归中仓储测试错误尝试写入 `/scert/data`，被 nobody 拒绝；六个相关测试改用临时 SQLite DSN 后完整重跑。未提权、未放宽正式路径权限；原失败保留在 `regression.log`。
- [!] `race-repeat.log` 首次因 CGO 未启用退出 2；`race-repeat-cgo.log` 显式启用后因找不到 gcc 退出 1。均未完成竞争检测，不记为通过。
- [x] 北京时间 13:25 第二台 Scert 为 active，Scert 8443、MySQL 3306/33060 均仅监听 127.0.0.1；这不代表修复版已经部署。
- [~] 上传碰撞与事务日志组合仍需核查，本批正式构建部署、业务实机负向恢复和历史事务证据核查待完成。可选真实数据库用例未显式开启，不宣称三数据库验收通过。


## 2026年10月2日12:54 双机正式候选升级记录

本节属于升级与安全复核，不是清库首次生产验收。未开放额外端口，未修改 Nginx，未关闭 TLS 或 SSH 主机指纹校验，未创建辅助验收脚本。只使用正式安装器自带发布事务，没有额外远端备份。

### 正式候选及拒绝路径

- [x] 原生 Linux 构建实际退出零，源码提交 1150c3904eb82d9274fbedf7bfd3e6d402623216，source_dirty=false，local_path_check=passed；四个二进制内嵌同一提交、vcs.modified=false、trimpath=true。
- [x] 专属 TMPDIR 消除了通用 /tmp/ 与安装器目录拒绝规则的误匹配；未修改路径检查代码，原有检查继续覆盖实际构建目录和临时目录。
- [x] 正式归档 SHA256：cf0529095eefc39de44d55da95fbd196a7340773c8e9701b9bcdf15b308126c9。双机传输后归档和包内 SHA256SUMS 全部校验通过。
- [x] 发行程序以非特权用户、独立且不存在的配置文件路径启动，0.0.0.0、::、192.0.2.1、localhost 均被明确拒绝，退出码为 1，未创建该配置文件。

### 第二台管理入口

- [x] 12:47:55 开始升级；12:47:56 主进程启动，12:48:01 收到就绪通知后进入 active。Type=notify、NotifyAccess=main，状态文字明确要求通过 readyz 检查业务就绪。
- [x] 主程序仅监听 127.0.0.1:8443；严格普通域名 HTTPS readyz 返回 ready、HTTP 200，首页 200，HTTP 入口 301 跳转 HTTPS。12:51 后续连续 10 次 readyz 均 HTTP 200、TLS 校验零、curl 退出零。
- [x] Nginx 配置摘要未变，Nginx 与 MySQL 主进程编号均未变。
- [!] 第一台跨机连续 60 次采样包含 7 次 HTTP 502，发生在 12:47:56 至 12:48:00；12:48:01 恢复 200。另有 2 次一秒连接超时发生于升级窗口外，不能仅凭该采样归因于 Scert，也不将 HTTP 000 时的默认 TLS 数值解释为校验成功。其余 51 次为 HTTP 200。
- [!] 启动通知已真实验证，但单实例停机窗口仍存在，不能声明零 502 或无中断升级。

### 第一台部署目标及边界

- [x] 12:51:39 开始升级，12:51:40 进入 active；同样收到真实就绪通知，仅监听 127.0.0.1:8443。未新增管理站点，未将其默认站点当作管理入口验收。
- [x] 第一台 Nginx、MySQL、Xmail 主进程编号和运行态保持不变；未操作其他业务服务。
- [x] 两台已安装的 scert、scert-admin、scert-storage 摘要对应一致。主程序 SHA256：91f32ea0e6971c7c66668d395ec91bcb6e78ebbd60bee1ddd82910d9f8f14a8e。
- [x] 升级后双向 IPv4 探测对方的 8443、3306、33060 均未连接成功；仅作为这两个来源的 IPv4 路径证据，不扩大为所有来源或 IPv6 外部探测结论。
- [x] 双机 systemd-analyze verify 均退出零；主机已有 snapd 警告、第一台已有 cloudmonitor 警告保留，未修改其他服务配置。
- [!] 三数据库清库首次 Web 流程、系统通知实发与重置闭环、P0-10 负向恢复矩阵及最终统一验收仍未完成。

## 2026年10月2日12:42 Linux 回环监听回归

- [x] 第二台隔离源码目录以 nobody 用户、Go 1.26.6 执行 config、bootstrap、cli、app、serviceready、servicehost 六包完整回归，实际退出零；监听相关新用例重复 20 次通过，六包 go vet 退出零。
- [x] 覆盖默认与空值回环、IPv4/IPv6 通配地址拒绝、非回环 IP 和域名拒绝、环境覆盖后校验、无环境覆盖时历史通配配置拒绝、初始化及正式入口在建立资源前拒绝。
- [x] 首轮失败原因分别为旧配置测试误写 /scert/conf 以及隔离源码未复制 deploy 目录；修正测试临时文件路径并补齐原有部署文件后完整重跑。没有提权绕过权限，没有触碰生产配置或数据库。
- [x] 路径扫描命中的 install-linux.sh 与源码 SHA256 相同；命中内容为已有的 /tmp/ 禁止目录规则。下一次构建配置专属 TMPDIR，扫描仍覆盖实际临时目录，不删除检查项。
- [ ] 正式包构建、安装及运行态复核待进行，本节不计作清库首次生产验收。

## 2026年10月2日12:19 启动通知原生回归记录

范围为开发回归及旧线上候选只读检查，不是清库后的首次生产验收。未创建辅助验收脚本，未额外备份或删除现有文件，未开放端口，未部署新候选。

### 源码与执行环境

- 基线为 b6b9642，加本轮启动通知、生命周期测试与 CLI 测试路径隔离改动。
- 第二台 Ubuntu 22.04.5，原生 Go 1.26.6；测试用户 nobody，独立目录 /var/tmp/scert-startup-20261002-1206，复用既有工具链及模块缓存，未以 root 运行业务测试。
- 最终测试源码增量归档 SHA-256：1731da7eafa720398bcfe784277f00f8674ce5f6e5ec7c8ce28c7f4f59da8cea；仅作为本次回归源码传输核对，不是正式发行包摘要。

### 实际结果

- [x] serviceready、servicehost、bootstrap、cli 四包执行 go test -p 1 -count=1 -timeout 180s 全部通过；耗时分别为 0.005、0.004、8.820、7.070 秒，退出码零。
- [x] 正式服务通知顺序、通知失败、启动取消、真实初始化入口通知、初始化 readyz 503、测试路径隔离等选定用例重复 20 次通过；两包耗时 0.668、0.216 秒，退出码零。
- [x] 四包 go vet -p 1 退出码零。
- [x] systemd-analyze verify 对新 unit 返回零。Windows 源码归档传入的测试 unit 初次提示权限过宽，已在隔离目录收紧为 0644 后重验；仍存在主机原有 snapd RestartMode 警告，与本次 unit 无关，未修改共享主机其他服务。
- [x] 首轮 CLI 包出现正式目录权限拒绝，判定为失败而非通过；定位到旧测试的 config.Default 在 Linux 使用固定正式路径。修复测试配置显式隔离全部目录与 SQLite DSN 后重跑成功，未提权写入 /scert，未触碰线上业务数据。
- [x] 12:18:38 北京时间：Scert、Nginx 均 active，ss 显示 127.0.0.1:8443；正常域名严格 HTTPS readyz 返回 ready，curl 退出码零。此时线上仍为此前候选。

### 未完成边界

- [ ] 新候选正式构建、二进制与 notify unit 配套安装、真实 systemd 启动及重启期间 Nginx 采样。
- [!] 启动通知不能消除单实例停机窗口，不能将 active 解释为业务 readyz、无中断升级或零 502。
- [!] 三数据库清库首次 Web 流程、系统通知实发与重置闭环、P0-10 负向恢复矩阵及最终统一生产验收继续待办。


## 2026年10月2日运行态采样及阻塞归因补充

### 11:49 至 11:50 邮件入口独立 DNS 诊断

- [x] 从第二台执行普通 A/AAAA 查询及严格 HTTPS 请求，curl 退出码为 6，未进入 TLS 握手；不能把 ssl_verify_result 的默认零值解释为 TLS 校验成功。
- [x] 本机解析器、1.1.1.1、8.8.8.8 对 web.solit.cn 的 A 查询均返回 NXDOMAIN，SOA 序列号为 2026090620。
- [x] dns13.hichina.com、dns14.hichina.com 的非递归查询同样返回 NXDOMAIN，带权威应答标志。第二台权威查询首次 IPv6 传输超时，后续实际权威应答为 NXDOMAIN，不把超时当作不存在依据。
- [!] 用户提供的 Smail Web 入口当前无法解析；需确认新入口或修复 DNS 后，再通过正式系统通知授权流程取得参数。本轮未修改 DNS、Smail 配置或账号凭据。
- [x] 同时复核 Scert、Nginx 均 active，本机 Nginx 严格 TLS readyz 返回 ready。该结果不替代三数据库首次流程或生产总体验收。


本节记录北京时间 10:18 至 10:34 的只读诊断。未清库、未重启业务、未创建辅助验收脚本，不计作首次生产验收，历史记录保留。

### 第二台管理入口

- [x] 密码 SSH 进入远端交互 shell，执行 20 次 odacm.cn 严格 HTTPS 请求，解析至本机 Nginx。10:18:39 至 10:19:00 均返回 ready、curl 退出码零；访问日志对应 20 次 HTTP 200。
- [x] CN、SAN 均包含 odacm.cn，签发者为 Let's Encrypt YR2；有效期为 UTC 2026-09-25 15:56:37 至 2026-12-24 15:56:36。现有证书验证完成、等待复审，不代表续期轮换通过。
- [x] Nginx 上游为回环 HTTPS 8443，proxy_ssl_verify 为 on，SNI 为 odacm.cn，使用系统 CA；没有关闭 TLS 校验。
- [!] 较早采样因 PowerShell 到 SSH 的参数转义失败，出现额外主机解析错误，不采纳为证据；仅采纳后续独立采样与访问日志。

### 启动窗口及外部端口

- [x] 10:09:05 的 Nginx 错误明确为连接回环 8443 被拒绝；Scert 进程于 10:09:03 启动，Type=simple 显示 active 不等于 HTTPS 就绪。启动窗口仍未完成整改。
- [x] 第二台 Scert、MySQL 仅监听 127.0.0.1；防火墙默认拒绝入站，未见相关端口 NAT 转发，IP 转发及 route_localnet 均为零。
- [x] 第一台跨机探测第二台 80、443、22 成功；8443、3306、33060 在四秒限时内均超时。仅证明该外部来源的 IPv4 路径，不扩大为所有来源或 IPv6 结论。
- [!] 本地连接来自代理网络地址，短时抓包未观察到对应报文；不将本地 TCP 成功当作公网开放证据，也不单凭零报文证明全网隔离。

### 第一台角色与邮件阻塞

- [x] 第一台 Nginx 未见 odacm.cn 管理站点或 8443 上游。odacm.cn 探测返回默认 salg.cn 证书；改为 salg.cn 后 readyz 返回 404，不是 Scert 健康证据。不新增管理站点、不复制证书私钥，也不将默认站点证书不匹配误记为 Scert 签发失败。
- [!] Scert 页面显示 SMTP 未启用，主机、用户名为空且专用凭据未配置。Smail 系统通知状态接口提供真实连接参数，独立凭据受账号授权保护，不能用登录密码或猜测值代替。
- [!] 本地浏览器访问 Smail Web 入口连接关闭，仅证明当前路径失败，不证明服务整体离线，需独立复核。

本轮未新增生产通过结论。三数据库清库 Web 流程、Smail 实际投递与密码重置、历史恢复证据及重启窗口仍未完成。


## 2026年10月1日实机进度更新（MySQL 5.7）

本节仅记录非敏感测试结果，不写入口令、RAM 密钥、数据库密码、令牌或其他认证信息。测试均使用隔离目录和正式发行程序副本，未创建辅助验收脚本，未开放额外外部端口；总体生产验收仍未放行。

### 第二台 Linux 实机：MySQL 5.7.44 隔离实例

- 官方 MySQL 5.7.44 Linux x86_64 包 SHA-256：`798ff0a14cdc1ab259f8a3b6a7f683403e9bbaa85957d82e5370f82b27ba6805`。
- 隔离实例位于 `/var/tmp/scert-mysql57-20261001`，由非 root 用户运行，随机端口且仅监听 `127.0.0.1`，启用严格 TLS、CA 校验和主机名校验。
- 数据库迁移全套测试退出码为 0；0 至 18 版本升级、DDL 中断恢复边界、业务数据保留、TLS 与错误主机名、强制安全传输、重启持久化、连接池断线与恢复、项目成员资源迁移重试及历史范围迁移均通过。
- 线上 MySQL 8.0、Scert 和 Smail 未被测试实例替换或修改。

### 第一台 Linux 实机：正式发行程序 CLI

- 正式发行程序复制到隔离测试目录后哈希与线上 `/scert/scert` 一致，未修改线上文件权限或内容。
- CLI 的全部 `TestMySQLReal*` 实库测试退出码为 0；官方 MySQL 客户端与 `mysqldump` 互操作，以及表、视图、触发器、例程、事件、权限、目录可见性、快照和恢复规划测试均通过。
- 完整日志保存在远程隔离测试目录的 `test-results/cli-full.log`，未将认证信息写入日志或提交。

### 误报修正与安全边界

- 连接池专项首次失败不是产品故障：MySQL 5.7 正常关闭约需 17 秒，测试夹具固定等待 15 秒后误报；提交 `a139e6b` 将等待上限调整为 30 秒，修复后专项及完整数据库套件均通过。
- 双机 Scert、Smail 和系统数据库服务保持原有运行状态；内部数据库及管理端口仅回环监听，公网入口继续限制为 Nginx 80/443。
- 本轮未清库、未触发真实密码变更或邮件投递、未删除历史事务或锁、未补造恢复证据；因此本轮结果不能替代最终清库首次生产验收。

### 尚未放行的生产门禁

- [~] P0-10 其余负向窗口、锁归属和摘要不匹配的实机验证。
- [!] 以下两条历史 `recovery_required` 事务仍缺可信恢复证据，不得删除锁、修改事务终态或补造证据：`20260930-231310.231207974-5bf9d1e37542`、`20260930-231313.144123222-ce71b1dfe14e`。
- [ ] 完整三数据库 Web 生产流程、最终清库后的首次初始化与完整业务验收。
- [ ] Smail 系统账户真实 SMTP 发信、验证码密码重置和改密后重新登录闭环。
- [!] 第一台公网平台或备案限制不能用本机结果替代；Windows 不在本轮范围。

## 公开密码重置账户枚举修复与双机复核（2026年10月1日22:50，北京时间）

### 范围与开发

本轮沿用Linux范围，保留现有数据执行升级与专项安全复核，不是最终清库首次验收。未触发验证码、发信或密码变更，未启动临时重置入口。运维仍为密码SSH/SCP及严格主机身份校验，未创建辅助验收脚本。

- 修改前检查点df0be7f已提交推送；修复提交51ab0a6d0c6853a68e36cbfb7918988d352ba8d9已推送。
- 新增password_reset_guidance.go，公开路由改为showPasswordResetGuidance。仅校验请求格式、标识格式及初始化状态，不查询或比较账户；使用独立结构返回title、description、action，设置Cache-Control: no-store。历史分类处理器未绑定路由。
- 新增七类已知/未知用户名、邮箱、大小写、首尾空格的一致性回归及输入边界测试；新测试在旧实现下实际失败，修复后通过。保留严格JSON、体积和初始化边界，不以泛化错误掩盖格式错误。
- 本地go test ./...、go vet ./...、go build ./...通过；重置查询专项go test -race通过；前端新增通用响应兼容测试，npm test共242项通过。未把条件跳过项算作实库验收。

### 正式发行及安装

- 使用固定正式构建入口build/build-release.ps1，仅选择linux-amd64；干净源码克隆构建，source_dirty=false，发布路径检查通过。
- 归档：build/release-20261001-public-reset/Scert-20261001-V0.1.0-linux-amd64.tar.gz。
- 归档SHA-256：3460577ee530350479c1461dbdcf44a8a376c6904ab2ce5cd121c7e823135d54。
- 主程序SHA-256：79799ebb6ceebd3819b5285baa8333017fe050848ef6f77b7eed693ba6ef5cf8。
- 双机密码上传到/tmp/scert-51ab0a6-linux-amd64.tar.gz，解包到/tmp/scert-51ab0a6-release，校验归档及包内SHA256SUMS全部通过。按正式安装器要求仅停止Scert相关服务和存储socket，安装并重新启动；双机upgrade_exit=0，主程序摘要相同。
- 安装器自身保留的发布文件事务：第一台/scert/.install-v9Ef2X3w，第二台/scert/.install-O5wFWo5q。未额外备份业务数据，未删除安装现场。
- 第二台MainPID=176134、NRestarts=0；第一台MainPID=2111996、NRestarts=0。第一台xmail.service保持active、MainPID仍1319，未重启Nginx或邮件服务。

### 无凭据公开接口及页面

- 操作前检查前后端健康和Scert专用MCP监听；沿用项目独立上下文，忽略缓存重新加载页面，不使用缓存响应作为证据。
- 浏览器以credentials=omit、cache=no-store请求正式公开接口，分别提交配置中的用户名、邮箱、未知用户名、未知邮箱及大小写变体。五者均HTTP200、Cache-Control: no-store，响应正文逐字一致，仅含通用标题、说明及contact_administrator动作。
- 只读核对第二台配置，确认测试使用的已知用户名和邮箱均与真实配置相符；不以未知邮箱冒充已知邮箱测试。
- 第一台作为独立公网客户端正常DNS、严格TLS访问第二台，四类标识均HTTP200、Content-Length=203、no-store、无Set-Cookie，正文相同。
- 浏览器实际填写已知账号并点击查询，页面仅显示“密码重置”与通用安全说明、重新输入按钮；没有角色识别、维护命令、重置链接或会话凭据。
- 第一台本机原有IP自签证书通过密码SSH读取核对，显式信任其证书，并按证书IP名称将连接指向回环8443；健康返回ready，真实配置用户名、邮箱及两类未知标识的响应SHA-256均为eaa3acf56700ce902fff1f1e72f65d7bda018c0bd7138163878117b548f76fe7。仅计作本机接口复核，不冒充公网受信管理入口验收。
- 过程中首次读取使用jq失败（目标机未安装），随后直接访问127.0.0.1因证书名称不符失败；这些失败不计通过，空响应摘要不作证据。改用现有Python只读配置、正确证书名称和连接地址后复核成功，未关闭TLS校验、未安装额外工具。

### 安全收尾及结论

- 双机8443、3306、33060仍只监听127.0.0.1；双方互探对方公网三个内部端口，六次退出124超时。未新增外部业务端口。
- 双机nginx -t通过；第二台管理上游proxy_ssl_verify on、SNI正确；管理首页和密码重置页200，/readyz返回ready，未开启的/reset返回404。
- 第二台任务仍为失败17、成功29，无新增部署任务；两条历史recovery_required事务及JSON null恢复证据保持原状，未改库或释放历史锁。
- [✓] 本轮公开查询账户枚举漏洞经源码回归、正式发行及实机复核通过，更新22:31的未修复状态。
- [!] 实际系统通知发信、超级管理员密码重置全流程、历史事务与其他负向恢复、MySQL5.7及完整Web矩阵、最终清库首次流程仍未全部通过；总体生产验收不放行。


## 公开密码重置只读安全复核（2026年10月1日22:31，北京时间）

- 当前部署候选源码仍为 00aeedc；上一轮进度文档已通过 94c674d 提交推送。本轮为保留现场的只读检查，不是清库首次验收。
- 双机实际服务名称为 Scert.service，核对均 active；小写 scert.service 不存在，不能据此误报服务停止。第二台 MainPID=175308、NRestarts=1，与前轮受控中断记录一致。
- 双机 8443、3306、33060 均只监听 127.0.0.1；nginx -t 均成功，第一台邮件服务 active。第二台管理反代 proxy_ssl_verify on、proxy_ssl_name odacm.cn，管理首页为 200，/readyz 返回 ready，/reset 为 404。
- 只读查询最终两条事务 20261001-141918.826867221-0ff9674d0969 和 20261001-141921.961222681-692080e5aeca，phase/status 均 committed，补齐上一章节收尾数据库终态证据。
- 浏览器使用原 Scert 专属上下文和页面，操作前核对前后端健康及回环 MCP 监听。检查公开密码重置页，没有显示维护命令；未清除数据库或另开项目窗口。
- 使用 credentials=omit 分别向 POST /api/v1/password-reset/resolve 提交已知账户及本轮不存在的测试标识。两者均 HTTP 200，但前者明确返回 account_type=super_admin、action=contact_administrator，后者返回 account_type=unknown、action=check_identifier，标题和说明也不同。
- [!] 结论：未登录者仍能枚举超级管理员身份。没有泄露维护命令不代表没有信息泄露；本项是新确认的上线阻塞，当前未修复。未请求验证码、未改变密码、未启用维护入口。
- [ ] 后续需要统一合法标识的响应，覆盖用户名及邮箱的一致性回归，构建正式候选后再复测；不得以此次只读检查替代整改验收。


## 不同证书进程中断恢复与多目标部分失败（2026-10-01 22:22，北京时间）

### 范围与已确认需求

沿用原清单，仅推进 Linux。使用两台现有测试服务器及 scert-multi-20260930.apmmk.cn，保留数据库执行连续负向回归，不计作最终清库首次验收。人工连接使用密码 SSH/SCP 和已有主机身份校验；产品继续使用其托管 SSH 身份。未新增 ACME 签发、未手工改任务状态或删除锁、未建立辅助验收脚本、未额外制作业务备份。

本轮任务：核对正式复用入口、真实不同证书进程中断、修复发现的问题、正式构建及双机安装、原任务重试、不同证书多目标部分失败、恢复标准配置和安全复核。以下均为实际执行结果，不用同证书测试替代不同证书证据。

### 证书及准备

- 运行基线为较新的有效证书，叶指纹 `9D866F8360FB65365A766BF53DC087E91254FC3CC6137F9DEF25E39FA253C742`。
- 复用的另一张有效证书叶指纹 `43063C0726D34362B254CE28B66D7EB637E00B165F68EC0D07FCED0DF2C64AD2`，来源为产品已有备份目录 `/scert/backups/scert-multi-20260930.apmmk.cn/20261001-132352.396840332`。
- 当前正式历史接口只保留当前版本，历史恢复已停用；因此使用正式证书路径导入接口，不改证书目录或数据库来切换版本。
- 临时通过正式服务器接口将双机配置校验命令设为 /bin/false，导入旧证书保存为 20261001-V1.1。接口返回 502、imported=true、双机 validation_failed；两事务均 rolled_back、reload_attempted=0。任务 `a62dca21-d667-421b-beed-ffd667d9ebc6` 保持失败，双机文件与实际 TLS 均保持较新证书，无本次锁。
- 导入自动保留的较新证书位于 `/scert/backups/scert-multi-20260930.apmmk.cn/20261001-135641.353585470`，核对指纹正确。
- 校验命令随后恢复 nginx -t；仅第一台临时使用有条件的重载等待：正常 nginx 重载后，只有目标文件为旧证书摘要时等待 45 秒。恢复到基线证书时不等待；没有创建脚本文件。

### 旧候选真实复现

- 源码候选 2e6d992，正式部署任务 `3517c6d2-095a-419c-a2bc-268bb3c3eab8`，第一次尝试 `831008dc-2f8e-4b18-8b6d-04a41b8a0081`。
- UTC 13:59:34 两事务均 reload_attempted/pending，第二台实际 TLS 已变为旧证书指纹；13:59:47 第一台也实际提供旧证书，受控等待进程仍在运行。
- UTC 14:00:02 再次验证两个事务仍在预期阶段后，对第二台主进程 PID 173940 发送 SIGKILL。systemd 启动 PID 174649，NRestarts 从 0 变为 1。浏览器原请求返回 Nginx 502，未伪报成功。
- 文件和运行证书已恢复较新基线，但两事务留下 recovery_required，错误为恢复站点指纹不符；本次锁被保留。
- 两事务：`20261001-135920.017620191-f547126af33a`、`20261001-135922.655846921-a98fb068e5f4`。
- 只读核对事务的 previous_fingerprint 正是较新证书，expected_fingerprint 是待部署旧证书，证明不是记录了错误基线。
- 根因：启动恢复重载后仅立即核验一次，而正常部署已有有限次等待；不同证书实际切换尚未完成时，单次结果导致安全留锁。本问题是恢复误判，不是放宽校验或假成功。

### 修复、测试及正式安装

- 检查点 adbb398 已提交推送；修复提交 `00aeedc3efe9507b0fa04bcc1269755b3f599499` 已推送。
- 修改 app.go 的启动恢复核验，新增 recovery_verification.go，复用正常部署六次、间隔 500 毫秒的严格 HTTPS 指纹核验，支持上下文取消和截止时间；持续不符或校验失败继续返回失败并保留现场。
- recovery_verification_test.go 覆盖真实受信 TLS 的回滚/提交两种不同证书切换，以及持续指纹不符、空指纹、校验错误、取消后匹配、截止时间共五种失败保护。单次核验实现不能通过新增专项，有限次核验后专项通过。
- 后端 go test ./...、go vet ./...、go build ./... 通过；前端 npm test 241 项全部通过；正式前端构建、发布路径检查及差异检查通过。本轮未运行竞态测试，不把条件跳过的数据库场景算作实库验收。
- 正式入口 build/build-release.ps1 -Target linux-amd64，产物 `build/release-20261001-startup-verification/Scert-20261001-V0.1.0-linux-amd64.tar.gz`，source_dirty=false。
- 归档 SHA-256：`e3aaf5caf54bab6b26cbd43907a4cdcc12b95cf130fd5ba74eb8b6b3f23eb2bc`。
- 主程序 SHA-256：`04f873c7fbec900f50ac17db172225a2a882e667755406c8978fcc031ed75cf1`，两台安装后相同。
- 两台通过密码 SCP 上传，校验归档及包内 SHA256SUMS，停止 Scert 相关服务后运行正式安装器，再启动目录 socket 和主服务。Nginx 及第一台邮件服务未停止，不宣称管理入口升级零中断。
- 安装器自身保留发布文件：第二台 `/scert/.install-PDtxYNpM`、第一台 `/scert/.install-qbJ9Hdnf`，未删除安装事务或历史文件。
- UTC 14:10:53，旧候选复现产生的两事务已由新候选启动恢复自动 rolled_back，本次锁自动释放。未靠手工改库/删锁处理。
- 关闭旧浏览器页面，新建独立 Scert-startup-verification-00aeedc 上下文并通过正式登录页面重新登录，不复用旧缓存。

### 修复版真实中断、自动恢复与原任务重试

- 对同一任务执行正式重试，第二次尝试 `652ca8d0-e4a7-4e4b-966d-3e79c4b6bdc0`。
- UTC 14:13:19 两台实际 TLS 均切换到旧证书指纹，两新事务均 reload_attempted/pending，第一台等待进程仍存活。
- UTC 14:13:33 对第二台主 PID 175047 发送 SIGKILL，随后 systemd 启动 PID 175308，NRestarts=1；原 HTTP 请求返回 502。
- UTC 14:14:02 两事务已自动 rolled_back，原任务 failed/service_restart，readyz 为 200；第二台文件及实际 TLS 已恢复较新证书。14:14:19 第一台同样确认文件及实际 TLS 恢复，严格 HTTPS 200，无本次锁、临时文件或事务旧文件残留。
- 两个新事务：`20261001-141302.527037588-1ca22507895f`、`20261001-141306.622429896-38f7ca5e5ce9`。不需要再次人工重启或介入释放锁。
- 通过正式服务器接口将双机配置均恢复 validation_command=[nginx,-t]、reload_command=[nginx,-s,reload]，GET 复核一致。
- 第三次正式重试同一任务成功，尝试 `0102a206-639d-4bbe-960d-4c1d25609f50`，UTC 14:15:37 完成。HTTP 200、双机成功、verification_status=success；前两次失败记录保留。
- 成功事务 `20261001-141526.617981312-26d6f26fb817`、`20261001-141529.680082912-7006a376eaf9` 均 committed。双机实际提供所部署旧证书，严格 HTTPS 200；成功后重复重试返回 409。

### 不同证书多目标部分失败

- 复用前述较新证书备份，通过正式导入保存为 20261001-V1.2，没有再签发证书。
- 第二台使用标准校验/重载；第一台临时配置校验为等待 15 秒后失败，重载仍为标准命令。
- 导入任务 `38cd22e1-ceff-43ed-b43d-cec473bd7e8e`。UTC 14:17:44，第二台实际提供较新证书，事务 reload_attempted/pending；第一台仍提供旧证书，其事务 installed/pending、reload_attempted=0，受控校验仍在执行。
- UTC 14:17:57 接口返回 502、imported=true、部署失败；第二台 failure_code=recovery_restored，第一台 validation_failed，recovery_status=success。不是把部分推送成功当成整体成功。
- 两事务 `20261001-141727.636335840-f918958bcc2f`、`20261001-141731.015455136-3898de99c378` 均 rolled_back，分别记录已尝试重载和未重载。
- UTC 14:18:42 双机磁盘与实际 TLS 均为旧证书，目录无本次锁、临时文件和事务旧文件。确认已重载的目标也自动恢复了运行证书。
- 恢复第一台标准校验命令，GET 复核两台均为标准配置。通过正式部署当前证书而非重新导入/签发，任务 `39af8113-05a7-4075-8700-9fba2ca8608e` 于 UTC 14:19:36 返回 200，双机成功且 verification_status=success。
- 该任务事务：`20261001-141918.826867221-0ff9674d0969`、`20261001-141921.961222681-692080e5aeca`。

### 最终现场、安全边界及剩余门禁

- UTC 14:20:23 双机运行最新较新证书，叶指纹为 9D866F8360FB65365A766BF53DC087E91254FC3CC6137F9DEF25E39FA253C742。
- 双机 cert.pem 和 fullchain.pem SHA-256 为 `7e8b56227b345ed9b66659e428f9bd4421604f11253ab88d65b3ed61737f35cf`；证书和私钥导出的公钥 DER SHA-256 均为 `f71a91f1aaa8c7da7dd945b432520f3a6265d47586c432594091699ccaf0a7de`。未输出私钥。
- 双机本机 SNI 严格 HTTPS 200，第一台访问第二台公网同域名严格 HTTPS 200，nginx -t 成功，测试域名无本次锁或事务临时文件；活动任务为零。
- 双机 Scert 8443、MySQL 3306/33060 仅监听 127.0.0.1，双机互探对方公网三个内部端口，六次均退出 124 超时。Nginx 管理反代保持 proxy_ssl_verify on 和正确 SNI，未开放额外公网端口。第一台 xmail.service active。
- 两台服务器 API 最后 GET 仍为标准 nginx -t / nginx -s reload，故障注入命令已全部撤销。
- 两条旧历史事务仍 recovery_required，recovery_evidence_json 为 JSON null：20260930-231310.231207974-5bf9d1e37542、20260930-231313.144123222-ce71b1dfe14e。没有补造证据或人工清除。
- [✓] 本轮不同证书真实进程中断自动恢复、原任务重试/幂等、多目标部分失败回滚及收尾复核通过。
- [x] 修复代码、测试、正式发行与双机安装完成，等待整体发布复审。
- [~] 其余负向窗口、锁归属/摘要不匹配实机与历史事务仍待处理，不能据本轮结论将 P0-10 整项标为完成。
- [!] 系统通知 SMTP 参数及真实发信闭环、第一台测试域名公网平台限制、MySQL 5.7/完整三库 Web 等原清单剩余门禁、最终清库首次验收尚未完成。已有数据库迁移、独立正式 CLI 备份恢复和 Linux 原生构建证据继续有效。
- [!] 总体 Linux 生产部署验收未放行；Windows 不在本轮范围。

## 最新执行记录（2026年10月1日21:43，不同证书重载失败回滚修复）

### 范围、需求及基线

本轮从北京时间 21:17 开始，延续保留数据的 Linux 负向回归，不是最终清库首次流程。只操作测试域名 scert-multi-20260930.apmmk.cn 的正式部署功能及已关联服务器的重载配置，不修改管理域名证书，不操作第一台既有邮件配置；全部人工 SSH 和 SCP 使用密码及严格主机身份校验。未新增辅助验收脚本，未额外备份远端业务数据，未手工改任务状态或删锁。

- 双机旧 cert.pem、fullchain.pem 的 SHA-256：`ecf815ca6ba91abb43985663f7ff2df4ee7987362131e068b6ff32e4657f90bc`。
- 旧叶证书 SHA-256 指纹：`43063C0726D34362B254CE28B66D7EB637E00B165F68EC0D07FCED0DF2C64AD2`。
- 旧证书公钥与旧私钥导出的公钥，DER SHA-256 均为 `3cca90893f3edabd12398f85487452dbe45f913041b9c7ef505bf597b7897b84`。不输出私钥。
- 双机本机带 SNI 的严格 HTTPS 为 200。正式历史列表仅有一个可用版本，因此本轮只通过正式签发接口申请一次新证书；后续测试复用新证书，不重复签发。
- 通过正式服务器更新接口临时配置：执行 Nginx 重载，输出测试站点实际叶证书指纹，短暂等待，最后核对文件仍是旧证书摘要。新证书下此命令返回失败，旧证书恢复后返回成功；配置校验始终为 nginx -t。初次请求包含只读字段被接口以 400 拒绝，改为正式允许字段后双机均保存成功，没有绕过接口校验。

### 旧候选实机缺口

源码 9c58c5b 的正式签发任务 `829076c2-2db0-4204-a151-6acc76bab1dc` 于 UTC 13:23:45 创建，UTC 13:24:26 失败。证书申请成功，但双机重载失败，HTTP 502 为产品返回的失败响应，不是任务成功。

- 新版本：`20261001-V1.0`；新叶证书指纹 `9D866F8360FB65365A766BF53DC087E91254FC3CC6137F9DEF25E39FA253C742`，与旧证书不同。
- 两条事务：`20261001-132353.561367231-d6004095e899`、`20261001-132409.768963185-15f9d00ae683`。均为 reload_attempted/recovery_required，恢复证据存在。
- 失败码为 runtime_unconfirmed。UTC 13:25:09 双机磁盘文件摘要已恢复旧值，但实际 TLS 仍提供新指纹，证明文件回滚不足以恢复运行状态。
- 为先恢复现场，使用已有服务启动恢复机制；UTC 13:27:17 两条事务均自动成为 rolled_back，两台实际 TLS 均恢复旧指纹。未手工改库、未手工删锁。
- 随后通过正式接口恢复两台标准重载配置。原签发任务保留失败，不重试签发来生成多余证书。

### 修复及正式候选

- 修改前检查点 `7a08dfe`；修复 `2e6d992b985556f334fa237dd8b69cd44ec23c14`，均已推送。
- 修复使带站点恢复校验的部署流程在重载返回错误或非零退出码时，仍将失败目标交给完整恢复事务：回滚文件、校验旧配置、重新重载、验证实际旧指纹后释放锁。没有恢复证明或恢复失败时继续保留锁；不带恢复校验的旧接口继续失败关闭。
- 新增仓库正式回归文件 reload_recovery_test.go；五个子场景先在旧实现失败，再在修复后通过，包含多目标部分失败、连接返回错误、缺失恢复校验、指纹校验失败及恢复重载失败保护。
- 后端 go test ./...、go vet ./...、go build ./... 通过；前端 241 项通过；正式前端构建、发布路径检查通过。外部条件跳过项不计为真实实库验收。
- 正式包：`build/release-20261001-reload-recovery/Scert-20261001-V0.1.0-linux-amd64.tar.gz`，source_dirty=false。
- 归档 SHA-256：`1619525d2f5a595aa8c263ec174c2890ff37914a44b4a9bd9bdf3467b23f61b3`。
- 主程序 SHA-256：`973a41d763378d03bd757b7f75ec0c8fa80997fee916879630e56f1348e34da2`。两台安装后与本地产物一致。
- 两台均检查归档和包内 SHA256SUMS，停止 Scert 相关服务后运行正式 install-linux.sh，再启动主服务和目录 socket。Nginx 未停止，第一台邮件服务保持运行；不宣称升级零停机。
- 正式安装器自行保留发布文件：第二台 `/scert/.install-C3bYY04K`，第一台 `/scert/.install-sUT2BDU5`，没有额外制作数据备份。
- 浏览器关闭旧页面并创建新的 Scert 专属隔离上下文，重新通过正式登录页面登录；不复用旧缓存及登录状态。

### 修复后的双机失败恢复

部署任务：`87166c03-5501-482f-a4f6-db83ffc87a05`，UTC 13:37:04 创建，首次尝试 `159f5a56-fc5f-49fa-8e9b-4dc4ed6a1eb3` 于 UTC 13:38:04 结束。通过正式部署接口复用新证书，保留与复现时相同的受控重载失败条件。

- UTC 13:37:24 在第二台、13:37:35 在第一台实际 TLS 观测到新指纹；第一台此时严格 HTTPS 仍为 200，确认不是同证书重部署。
- 产品返回 HTTP 502、任务 failed，双机 failure_code=recovery_restored、recovery_status=success。部署失败与恢复成功分别记录，没有误报为部署成功。
- 两事务 `20261001-133705.924556306-b00c8eb271c9`、`20261001-133721.129018393-efcbf42821fa` 自动成为 rolled_back。
- 双机旧文件摘要、公钥对应摘要均恢复基线；UTC 13:38:21 双机实际 TLS 指纹恢复旧值。双机严格 HTTPS 均 200，Nginx 配置检查通过。
- 对目标目录不限制文件类型地检查锁、临时文件和事务旧文件，无本次残留。恢复及后续重试期间，第二台 Scert MainPID 保持 173940，NRestarts=0，未依靠再次重启触发恢复。

### 正常配置恢复、原任务重试及终态

- 两台正式服务器配置均恢复 validation_command=[nginx,-t]、reload_command=[nginx,-s,reload]，PUT 为 200，后续 GET 核对一致。
- 正式重试原任务，UTC 13:40:13 返回 200，双机 total=2、success=2、failed=0、verification_status=success。
- 第二次尝试 `c5078566-0987-49fd-956d-2c270f8288ca` 为 success；第一次 failed 仍保留，父任务仍是同一个任务。
- 第二次事务 `20261001-133952.659511951-b8fb25286b52`、`20261001-133955.482383445-3bcd46a09e98` 均 committed；首次两事务仍 rolled_back。
- 双机新 cert.pem、fullchain.pem 的 SHA-256：`7e8b56227b345ed9b66659e428f9bd4421604f11253ab88d65b3ed61737f35cf`；证书和私钥对应的公钥摘要均为 `f71a91f1aaa8c7da7dd945b432520f3a6265d47586c432594091699ccaf0a7de`。
- 双机实际 TLS 均为新指纹，双机本机严格 HTTPS 200，第一台访问第二台公网测试域名严格 HTTPS 200。第一台本机成功不代替其已有公网平台拦截问题的验收。
- 成功后再次正式重试返回 409；活动任务为零，无本次锁、临时文件或事务旧文件残留。
- 双机 8443、3306、33060 均仅监听 127.0.0.1；双方互探这些公网端口，六次退出码均为 124。第二台管理入口 ready，Nginx proxy_ssl_verify on；第一台邮件服务 active。

### 状态与剩余门禁

- [✓] 本次不同证书重载失败后的自动运行恢复、原任务重试及重复请求保护复审通过。
- [x] 修复代码、正式候选及双机安装完成，等待整体发布复审。
- [~] 其他负向窗口、不同证书进程崩溃、多目标部分失败等仍需继续核对；不能把本次子场景扩大为 P0-10 全部通过。
- [!] 两条历史无可信恢复证据事务保持原状态：20260930-231310.231207974-5bf9d1e37542、20260930-231313.144123222-ce71b1dfe14e。
- [!] Smail 系统通知参数与真实发信闭环、第一台测试域名公网限制、MySQL 5.7 与完整三库 Web 等剩余门禁及最终清库首次部署未完成。已完成的 MySQL 8.0 迁移、独立正式 CLI 备份恢复和 Linux 原生构建证据不作废；Windows 不在本轮范围。
- [!] 整体生产部署验收未放行。

## 最新执行记录（2026年10月1日21:12，双机密码重置接口安全收敛）

本节覆盖下方同项待部署状态；本轮是保留业务数据的安全升级回归，不是清库首次部署验收。没有新增辅助验收脚本，未操作历史恢复锁和事务，也未关闭 TLS 或 SSH 主机身份校验。

### 候选与代码

- 源码修复：`9c58c5b`，已推送；此前仅检查说明文字，公开响应仍含本机命令处理标识，本次已纠正为通用联系管理员标识。
- 前端 241 项通过；后端 `go test ./...`、`go vet ./...`、`go build ./...` 通过；差异检查通过。外部依赖的条件跳过项不计作实机通过。
- 正式包：`build/release-20261001-reset-api/Scert-20261001-V0.1.0-linux-amd64.tar.gz`；构建记录 `source_dirty=false`，发布路径检查通过。
- 归档 SHA-256：`629f99ae7fdc068cad2fab19900dd4079df023576d148a8a252c6e3ee24cf66f`。
- 主程序 SHA-256：`8c20f38d0bf2f38d33ed97e75871e3d7bda4cd9a980ab44e495550e48ac8d41a`，本地候选与两台安装后的程序一致。

### 双机部署与运行边界

- 两台均使用密码 SCP、密码 SSH 和已登记主机严格校验；归档和包内 `SHA256SUMS` 全部通过，在全新临时目录解包，停止 Scert 相关服务后运行正式安装器，随后恢复主服务与目录 socket。
- 第二台安装事务：`/scert/.install-NcgAAfQK`；第一台安装事务：`/scert/.install-iJiDqDcn`。由正式安装器按既有保护机制保留旧发布文件，未额外创建备份，不据此宣称备份恢复验收通过。
- 两台 Scert、目录 socket、Nginx 为 active，主程序哈希一致；两台 `nginx -t` 通过。第一台既有邮件服务仍为 active。
- 双机 Scert、MySQL、MySQL X Protocol 分别仅绑定回环 8443、3306、33060；两服务器跨机探测对方这些公网端口，六次均在三秒后超时（退出码 124）。本机 TCP 探测曾显示连接建立，与服务端监听证据不一致，因此不采纳本机探测为直接公网可达证据。
- 第二台公开首页与就绪检查正常，Nginx 到 Scert 保持 HTTPS 与上游证书校验。第一台使用既有管理 IP 证书作信任锚，保持 IP SAN 校验，通过连接映射访问回环服务，就绪返回 ready；不把此项算作第一台公开域名入口验收。

### 页面与接口实证

- 第二台关闭旧页面并创建全新隔离浏览器上下文，通过正式页面输入账户并查询；页面没有维护命令、管理入口名称、操作系统命令指引或代码块，未出现水平溢出。
- 实际公开查询 HTTP 200，处理标识为 `contact_administrator`；不含本机命令标识、可执行维护命令、重置地址、令牌或 nonce，也不设置 Cookie。
- 第一台通过校验证书的回环 HTTPS 连接查询，返回同一通用处理标识；并未关闭 TLS 验证。
- 本次修复不改变本机受控重置权限，也不证明真实验证码发送和新密码登录通过。

### 未完成项

- 两服务器系统解析器查询 `web.solit.cn` 均返回 NXDOMAIN；第一台对公共解析器的额外查询超时，超时不计作 NXDOMAIN 证据。真实系统通知 SMTP 参数及实际投递仍缺证据。
- 继续执行 Linux 部署负向恢复、历史事务核查、数据库支持矩阵、备份恢复、清库首次初始化与完整业务验收；Windows 按用户要求暂不处理。
- [x] 本次页面和接口安全信息收敛及双机升级回归完成，等待复审。
- [!] 整体生产部署验收保持未放行。

## 最新执行记录（2026年10月1日，密码重置页面安全信息收敛）

- [x] 公开密码重置页面已移除本机管理命令、操作系统命令和 `Scert-admin` 入口名称。
- [x] 超级管理员查询接口不再在说明中返回可执行维护命令；重置会话、令牌和验证码仍不通过公开查询接口返回。
- [x] 后端全量测试、静态检查、构建、前端 241 项测试和生产前端构建通过。
- [~] 待使用最新候选包部署到 Linux 实机，清理浏览器缓存并复测公开密码重置页面；本项通过不代表 Smail 发信、密码重置闭环或最终生产门禁通过。

## 最新实机回归记录（2026年10月1日，可信恢复证据候选）

- 最新提交：`0e48dc9`，已推送远程仓库。
- 正式 Linux amd64 包：`build/recovery-evidence-20261001/Scert-20261001-V0.1.0-linux-amd64.tar.gz`。
- 包 SHA-256：`9b6748b6f25446adcc454fb9ecf88a31cbf70554bcbfcae3451670f9a81e4afe`。
- 使用密码 SSH 将候选包安装到 `45.136.13.241`；Scert、存储 socket、Nginx、MySQL 均 active。
- 通过 `https://odacm.cn/` 登录超级管理员，执行同一域名绑定两台服务器的正式部署两次；两次均为两台推送成功、配置校验及 reload 成功、站点 HTTPS 校验通过。
- 执行 `systemctl restart Scert.service` 后，服务保持 active，HTTPS 管理入口返回 200。
- 数据库只读核查：新事务进入 `committed`；现场保留两条缺少可信恢复证据的历史 `recovery_required` 事务，未删除锁、未修改状态、未伪造验收结果。
- 本记录仅证明候选安装、正常双目标部署和重启后的基础回归；异常中断、负向 HTTPS 恢复、三数据库、双平台、SMTP、密码重置、备份恢复和最终生产门禁仍未完成。

## 当前核查与开发记录（2026年10月1日，北京时间）

### 候选构建收尾

- 已提交并推送 `8899f7a`，从新的规范克隆构建，源码与依赖锁文件无变化，发布清单记录源码未修改、后端五百六十三个与前端一百零五个受跟踪文件均采用规范换行。
- 正式包：`build/rollback-state-20261001/Scert-20261001-V0.1.0-linux-amd64.tar.gz`，大小四千九百二十七万四千零三十六字节。
- 归档 SHA256：`69e6389bfedb297997017a608b38d82c5f8f4c2f0102efe2cb5a3c323ae2be24`。
- 逐项重算清单二十三项产物的大小与哈希通过；实际解包后核对二十二个文件的完整集合、内容和大小通过；五个执行入口归档权限为 `0755`。
- 尚未部署。当前启动恢复只判断锁目录存在，并未证明属于哪笔事务；也没有持久化旧站点指纹供重启后严格核验。不能以重启后锁消失替代可靠恢复验收，更不能直接删除现场锁。
- 本条更新本批早期候选未构建状态；本地成功不等于 Linux 原生构建、线上恢复或最终生产验收。

本批本地验证收尾：后端 `go test ./...`、`go vet ./...`、`go build ./...` 均通过；部署、应用、仓库三个包的竞态回归分别约八秒、五十九秒、三十二秒，全部退出零。条件跳过的外部数据库和 SSH 场景没有据此宣称通过，正式生产验收仍未完成。

### 只读现场核查

- 10:24 使用密码 SSH 并启用已登记主机指纹严格校验连接第二台服务器；Scert、存储 socket、Nginx 均为运行状态。
- 使用 SQLite 只读连接确认：最新任务仍为失败；两笔事务 `20260930-231313.144123222-ce71b1dfe14e`、`20260930-231310.231207974-5bf9d1e37542` 仍为需要恢复，提示旧文件已经恢复但等待站点恢复核验。
- 目标域名目录仍有三个部署锁；中断测试证据目录也有三个锁。本批没有清库、重启服务、删除文件或改变这些记录。本次只读诊断不是清库首次生产流程验收。
- 上轮报告中的两个域名、两台服务器严格 HTTPS 通过，属于当时站点状态证据；不能据此把当前失败任务判为成功。本批没有重新执行该四次 HTTPS 检查。

### 确认的代码缺陷与修复边界

- 新回归在修改前稳定失败：双目标恢复核验已经通过，事务终态却为需要恢复而非已回滚。
- 根因是终态判断先匹配所有失败结果，随后已回滚分支永远不可达。修改后，只有恢复核验成功且锁释放成功才记录已回滚，并复用现有中文恢复完成提示；部署任务仍保持失败。
- 补充双目标成功恢复、恢复核验失败、文件回滚失败、锁释放失败、部分目标连接失败场景。没有创建辅助验收脚本。
- 这不是旧事务的自动修复：旧数据不重写，现场锁不移除。崩溃后的站点基线验证、锁归属证明和历史现场接管仍需继续整改与实机验证。
- 修改前检查点为 `c96cd43`；第一次推送认证失败，第二次推送成功。最终测试、构建与部署状态以本批后续收尾记录为准。

## 最新任务状态（2026-09-30，双服务器候选部署完成）

- 本轮代码提交 `07229ad` 已推送；正式 Linux amd64 候选包 `build/acceptance-20260930-smail/Scert-20260930-V0.1.0-linux-amd64.tar.gz` 已构建并完成归档校验。
- 已使用密码 SSH 将候选包正式安装到 `8.137.23.95` 和 `45.136.13.241`；两台服务器的 Scert、`Scert-storage.socket`、Nginx 均为 active，管理入口 `/readyz` 与基础 HTTPS 校验通过，未关闭 TLS 校验。
- 本条只记录候选包和基础服务部署证据，不将其当作 Smail 发信、清库首次初始化、多服务器业务推送或最终生产门禁通过。
- 当前继续执行：清库并重新初始化、Smail SMTP 验证码和密码重置、多服务器多域名推送、失败恢复与重试幂等、三数据库和 Windows 服务矩阵。

## Smail 系统通知通道收敛专项（2026-09-30）

- 代码层识别 Smail `notify:` 系统通知账号，并固定使用 `notification+system@邮件域名` 作为 SMTP 发件身份；不使用管理员邮箱替代。
- 证书任务通知和超级管理员验证码在该账号模式下都只调用 SMTP，不调用普通 HTTP 发信接口；验证码收件人仍严格限定为当前绑定邮箱。
- SMTP 未启用时返回明确配置错误；未关闭 TLS 校验，未记录 SMTP 密码、验证码或其他真实凭据。
- 通知专项、后端全量测试、静态检查和构建均已通过；本记录仅证明代码候选通过，尚未证明远端 Smail SMTP 参数、验证码、密码重置和生产上线流程通过。
- 代码、构建和双服务器基础安装已完成；下一步按生产流程清理测试数据后验证发信与密码重置；总体验收继续保持 P0-03 完成、等待复审。

## 恢复标准 Nginx 重载命令后正式页面复测（2026-09-30 17:39 UTC）

- 通过正式服务器配置页面将临时测试用的重载命令 `/usr/bin/true` 恢复为标准 Nginx 参数 `nginx`、`-s`、`reload`，保存提示“服务器配置已更新”。
- 通过正式页面对 `scert-multi-20260930.apmmk.cn` 执行“部署证书”，结果为“证书部署完成：1 台推送成功，站点 HTTPS 校验通过”；服务器结果为证书部署、配置校验并 reload 成功。
- 本轮未创建辅助验收脚本，未改变 SSH 主机指纹校验，未清理业务数据；本地 Git 工作区保持干净。
- 结论：临时重载命令已恢复，单服务器标准部署路径复测通过；不代表后置 HTTPS 失败自动恢复、进程崩溃事务自动接管、真实多服务器和最终清库首次生产流程已经通过。

## 陈旧部署锁清理后正式页面复测（2026-09-30 16:55 UTC）

- 通过 SSH 核查 `scert-multi-20260930.apmmk.cn` 目标目录中的 `cert.pem.scert-lock`、`fullchain.pem.scert-lock` 和 `key.pem.scert-lock`：三个均为空目录，创建时间早于本次任务；同时确认没有活动 Scert、SSH 或 SFTP 部署进程，`Scert` 与 `nginx` 均为 active。
- 按允许的现场清理范围，仅使用 `rmdir` 删除上述三个已确认陈旧且为空的锁目录；未删除证书、私钥、Nginx 配置、业务数据或证据目录中的历史中断现场。
- 通过正式页面重新部署 `scert-multi-20260930.apmmk.cn`，页面显示“证书部署完成：1 台推送成功，站点 HTTPS 校验通过”；服务器结果显示证书部署、配置校验并 reload 成功，域名状态恢复为“正常”。
- `Scert`、`Scert-storage.socket`、`nginx` 均为 active，公网 `https://odacm.cn/readyz` 返回 `{"service":"scert","status":"ready"}`；两个测试域名 HTTPS 均返回 200。
- `scert-20260930.apmmk.cn` 目标证书和公网证书 SHA-256 指纹均为 `5DE99CD681FB8A84745CE4ED6091D76A7B86616421B3048D3C42844B8A4460ED`；`scert-multi-20260930.apmmk.cn` 两者均为 `43063C0726D34362B254CE28B66D7EB637E00B165F68EC0D07FCED0DF2C64AD2`。两个目标托管目录无 `.scert-lock`、`.scert-backup-*` 或 `.scert-tmp-*` 残留。
- 结论：陈旧锁人工核查、清理后的正常恢复路径通过；不代表后置 HTTPS 失败自动恢复、进程崩溃事务自动接管、第二台真实服务器、多服务器任务或最终清库首次生产流程已经通过。

## SSH 密码部署公钥、密钥回连及后续部署复测（2026-09-30 23:55）

- 通过正式服务器配置页面临时使用已授权的密码保存服务器配置；Scert 自动生成服务器专属托管密钥，通过密码 SSH 将公钥写入远端 `/root/.ssh/authorized_keys`，随后关闭密码连接并使用托管私钥执行 `true` 回连验证。
- 页面服务器状态由“已配置，待验证”更新为“已部署并验证”；托管私钥指纹为 `SHA256:iDWSQY7LgGdy6aYb47C0TMvZfktJxrfjVSGYLFrqhoc`，远端 `ssh-keygen -lf /root/.ssh/authorized_keys -E sha256` 查询到相同指纹。
- 远端授权目录权限为 700，`authorized_keys` 权限为 600；主机指纹仍为已核对的 `SHA256:cVoSXsYmhALjAdN2Gso3JvbnvYQCQBFuRv3IZjFvzsE`，未关闭 SSH 主机校验。
- 自动切换为私钥认证后，通过正式页面再次部署 `scert-multi-20260930.apmmk.cn`，页面显示 1 台推送成功、站点 HTTPS 校验通过；配置校验、reload 和两个域名 HTTPS 200 均保持正常。
- 结论：单服务器“密码首次接入—自动部署公钥—私钥回连—后续密钥部署”闭环通过；不证明第二台真实服务器、多服务器任务、后置 HTTPS 失败自动恢复或最终首次生产流程已通过。

## 3cc8fb6 正式候选双域名正常部署复测（2026-09-30 23:42）

- 使用密码 SSH 连接 45.136.13.241，未使用密钥登录，未创建辅助验收脚本；线上运行程序为 3cc8fb6，管理入口重新登录成功。
- 通过正式页面对 `scert-multi-20260930.apmmk.cn` 执行“部署证书”，页面结果为“1 台推送成功，站点 HTTPS 校验通过”；服务器结果为证书部署、配置校验并 reload 成功。
- `Scert.service`、`Scert-storage.socket` 和 `nginx` 均为 active，`/readyz` 返回 `{"service":"scert","status":"ready"}`，活动任务为零。
- `scert-20260930.apmmk.cn` 公网与目标证书 SHA-256 指纹均为 `5DE99CD681FB8A84745CE4ED6091D76A7B86616421B3048D3C42844B8A4460ED`；`scert-multi-20260930.apmmk.cn` 公网与目标证书 SHA-256 指纹均为 `43063C0726D34362B254CE28B66D7EB637E00B165F68EC0D07FCED0DF2C64AD2`。
- 两个域名真实 HTTPS 请求均返回 200；`/ssl` 下两个托管目录没有残留 `.scert-lock`，证书权限保持公有证书 644、私钥 600。
- 结论：本轮证明 3cc8fb6 的正常双域名部署闭环，不证明后置 HTTPS 校验失败自动恢复、进程崩溃事务自动接管、真实多服务器和最终清库首次生产流程已经通过。

## 固定诊断版本部署与锁测试收尾（2026-09-30 22:55）

- 6efc89061bc87fdcdc41182203749b82e598865a 干净源码正式构建部署；归档 SHA256 为 443cd7db2a98832c5b1d2eb5d27b0ee704192c5be97ddd0f7585531008c13b5c，主程序 SHA256 为 ee0a5a98c993eb8f512cb53936dd05e0fde079505d2ea78af8f2cde24cbe220f。
- 保留已有数据与证书，仅升级及负向复测，不作为最终清库首次流程。复用隔离证据中的空锁进行锁冲突测试，没有新增辅助脚本。
- 失败任务 d6b3be69-c4e1-4a10-b86d-70b8809fcfd8：页面与部署结果接口均显示固定中文核查提示；接口返回 200，不泄露底层错误或凭据。
- 空锁已移回原证据目录；22:52 确认活动任务为零，原六项证据仍保留。正式页面重新部署任务 179bb071-486d-4356-8ba2-e15ce1164eb5 成功，页面两域名均正常。
- 22:55 核对目标目录无锁；两个站点真实 DNS、可信 TLS 请求均返回 200，公网指纹分别为 5DE99CD681FB8A84745CE4ED6091D76A7B86616421B3048D3C42844B8A4460ED、43063C0726D34362B254CE28B66D7EB637E00B165F68EC0D07FCED0DF2C64AD2。
- 本节只证明诊断与正常重新部署，不代表崩溃事务自动恢复或后置 HTTPS 失败自动恢复已完成。

## 真实部署中断与人工恢复（2026-09-30 22:35）

- 候选仍为 91e1123。沿用已有证书和数据执行破坏性中断验证，没有清库，不能替代最终首次生产流程；未新增辅助验收脚本。
- 通过正式服务器配置接口临时设定校验命令为有界延迟后执行 nginx 配置校验，重载命令保持正常。22:25:00 从正式页面触发第二域名部署，任务 8aa3db95-ab7a-4559-a558-a1a9c8fa5f00、尝试 656a9679-33ab-458e-bcbe-2ed6694735eb 均为 running，远端可见 sleep 45 进程及三个部署锁和对应事务旧文件。
- 首次 systemctl 命令使用当前系统不支持的参数，未终止进程；确认任务与延迟进程仍活动后改用该系统支持的 --kill-who=main。22:25:30 SIGKILL 主 PID 81906，22:25:31 systemd 自动启动 PID 83070，NRestarts=1。不是空闲服务重启，也没有将首条失败命令记为成功。
- 任务恢复为 failed/service_restart，尝试为 failed，task_recovery 审计为 failed；三者旧提示均为“服务重启导致任务中断，请重新执行”。无自动重新签发或重放远端命令。
- 22:26:42 重新登录后通过产品接口恢复 nginx -t 与 nginx -s reload。延迟进程已结束；远端保留三个锁和三个事务旧文件，nginx worker 未变，新旧 cert/fullchain/key 逐一内容一致。
- 22:27:09 产品直接重试，任务 0d6dfdf0-65a0-4af5-8544-0f9c3b604018 失败，接口 502，没有误报成功；原有脱敏只显示“证书部署失败”，不能告知用户遗留锁原因。
- 确认活动任务为零、延迟进程结束、目录真实路径正确、三个锁为空且非符号链接、新旧文件一致后，将六个现场项目移到 /ssl/scert-interruption-evidence/8aa3db95-ab7a-4559-a558-a1a9c8fa5f00。受限目录权限 700，私钥旧文件 600；未删除文件、未自动抢占未知锁，也未覆盖另一域名文件。
- 22:28:14 正式产品接口部署已有证书，任务 800815bd-7fcd-4262-87c0-63fd6174dd29 成功，返回 verification_status=success。22:33 再查主服务、目录 socket、nginx 均 active，活动任务为零，目标目录无残留锁；两域名正常 TLS HTTPS 200，公网指纹各自保持与托管证书一致。
- 结论：启动任务恢复、遗留锁拒绝和人工核查后重新部署取得证据；远端自动回滚及自动解除崩溃事务未实现。已开发固定阶段安全诊断及中断核查提示，尚未部署，不能将本次人工恢复记为自动恢复通过。
- 22:41 开发回归完成：后端全量测试及静态检查退出零，部署、仓储、应用、接口四包竞态通过，前端 240 项通过；开始干净源码正式构建，构建通过不替代实机复测。

## 单台服务器双域名闭环（2026-09-30 22:18）

- 新域名 scert-multi-20260930.apmmk.cn，资源 ID fd0b6b70-9cca-48d7-9d9f-40551cb95fc5，与第一域名关联同一服务器 3cfe6b82-6807-421d-88ab-a2034370375b。
- 仅新增独立 A 记录，记录 ID 2105298673814337536，TTL 600，未改原有泛解析。DescribeSubDomainRecords 返回未授权，改用产品已有授权的 DescribeDomainRecords 精确组合查询，没有扩权；确认新名称没有已有记录后才新增。
- 正式页面保存域名返回 201、绑定返回 200。22:10:38 任务 603a01dc-1557-4976-ae56-501c944493a4 实际执行 DNS-01；22:11:11 验证通过并清理挑战，22:11:13 签发成功。两台权威服务器查询挑战 TXT 均无返回。
- 产品创建 /ssl/scert-multi-20260930.apmmk.cn/single，三个目标文件与版本化托管文件哈希逐一一致；公有证书文件 644、私钥 600。第一域名三个文件哈希前后不变。
- 首次任务未误报成功：新域名尚无已加载的 Nginx 站点，默认 DNS 仍命中旧泛解析缓存，后置 HTTPS 报 tls: unrecognized name，任务为 failed。随后新增独立 Nginx 站点配置，nginx -t 通过；未覆盖旧站点。仅清本机解析缓存不能消除上游旧缓存，等待实际 TTL 到期，22:15:27 默认解析变为授权目标，没有固定 IP 或禁用 TLS 验证。
- 22:13 通过产品接口精确解除第二域名关联：第二域名服务器列表为空，第一域名仍关联原服务器，服务器总数仍为一；随后重新绑定第二域名，返回 200。
- 22:15:43 正式页面部署已有证书，任务 745a66ff-3b75-47ca-88fa-e4f101a9f879 成功，页面报告一台推送成功、站点 HTTPS 校验通过。
- 第二域名公网、托管和目标证书指纹均为 43063C0726D34362B254CE28B66D7EB637E00B165F68EC0D07FCED0DF2C64AD2；第一域名仍为 5DE99CD681FB8A84745CE4ED6091D76A7B86616421B3048D3C42844B8A4460ED。服务器端两站点和外部 Windows 客户端第二站点正常 TLS 访问均为 200。
- 无残留部署锁；主服务、目录 socket 和 nginx 均 active。没有创建辅助验收脚本；独立 Nginx 配置是实际证书消费端配置，不是测试替身。
- 该结果证明一台服务器多个域名及精确关联隔离，不证明一个域名多台真实服务器；后者仍缺第二台服务器。

## 新 DNS 候选部署与旧有效证书负向恢复（2026-09-30 22:05）

本轮为保留数据升级复测，不是清库首次生产验收。没有新增辅助验收脚本，没有关闭 TLS 或 SSH 身份验证；通过现有密码 SSH 会话运维。

### 正式包和部署

- 源码：91e1123fa688ac3660b0c8d0276ee562c3ef9a79，source_dirty=false；规范构建记录后端 549、前端 105 个文件换行检查，23 项清单与 22 个归档文件核对通过，五个执行入口权限 0755。
- 归档 SHA256：7b84e363db748be19a6c932c9cd4d4cd9fc3a409f3d67c3763310e1d8eaeb49f。
- 主程序 SHA256：7e3f07ab9998a43e6d0a3f647026c994e896af93a62ebe717061352e6fce5deb。
- 21:55 后在新目录解包并通过 SHA256SUMS，停止主服务和目录 socket 后执行包内 install-linux.sh，再启动服务；线上程序哈希一致。安装器自行保留旧发布文件，未额外制作备份。
- Scert.service、Scert-storage.socket、nginx 均 active；公开 readyz 返回 ready，管理入口 200；浏览器忽略缓存刷新后重新登录成功。

### DNS 与旧证书负向

- 21:58 产品权限检查连续四次全部返回 200，报告 verified、writable、readable、cleanup 均为 true，随机后缀均为三十六位小写十六进制。该有限样本不证明所有网络超时已经消失。
- 22:00 临时将产品服务器重载参数设为 /usr/bin/true，配置校验仍为 nginx -t；22:01 通过正式页面申请同域新生产证书。
- ACME 复用已有有效授权，返回新证书；此次没有重新执行 DNS-01 挑战，不能把它算作新挑战全流程。DNS 写读清理由前述权限探测独立证明。
- 负向任务 d08caabd-ac44-4902-beea-a36f4f3d860c，尝试 9e23e034-0507-4b43-a3e9-91b99dac4469 均 failed；申请接口 502，页面显示“证书申请或部署失败”和域名错误。
- 新托管版本 20260930-V1.1 和目标文件指纹为 5DE99CD681FB8A84745CE4ED6091D76A7B86616421B3048D3C42844B8A4460ED；公网仍为 9A50D4A0E551EC728D4900E595029C52B4F5EDC9E24B518F123646289101FC53，worker 仍为 81412、81413。
- 服务日志明确六次 HTTPS 校验后仍为指纹不一致，而不是 DNS 或连接失败。旧有效证书未被判为本次部署成功。

### 恢复与边界

- 22:02 已通过产品接口恢复 nginx -s reload，再通过正式页面“部署证书”；任务 ed7770b0-bf23-41c9-b556-972d125c25cd 为 success，页面明确一台推送成功、站点 HTTPS 校验通过。
- worker 更新为 82219、82220；公网、目标、托管指纹均为上述新指纹；正常 TLS 的站点访问返回 200，无 .scert-* 残留。
- 本次证明后置 HTTPS 错误检测与恢复正常配置后重新部署，不证明自动运行态回滚；申请失败页面的通用错误尚未提供阶段诊断，不提前标记可观测性完整通过。
- 多域名、多服务器、任务恢复、数据库矩阵和最终清库首次初始化仍需完成。

## DNS 标签整改本地验证（2026-09-30 21:48）

- 修改前检查点 0864300 已推送。新增正式回归在旧代码下全部失败：随机后缀不是规定的三十六位安全编码，InvalidRR.Format 被误归入网络或权限通用提示。
- 记录名随机后缀改用十八字节密码学随机数的完整小写十六进制编码，TXT 随机值继续独立生成三十二字节，避免尾部连字符及大小写折叠问题，不依赖失败重试。
- 四类域名各三十二次回归检查后缀可还原十八字节、标签不超过六十三字节、范围不变、TXT 可还原三十二字节、调用顺序 add/describe/delete 不变。随机样本唯一性检查不是独立的熵证明；随机来源和完整编码由源码确定。
- DNS 包完整测试、静态检查和竞态通过；后端全量测试与静态检查退出 0。新修复尚未部署，不提前声明云端问题已复审解决。

## 重载失败恢复与新增 DNS 阻塞（2026-09-30 21:45）

- 21:33 正式页面部署已有证书成功，报告 status=success、verification_status=success，任务 fe00eb0d-dba8-400e-8c8a-852ab4abb451；页面区分推送与 HTTPS 验证。
- 21:38 通过服务器配置将重载命令设为 sh、-c、nginx -s reload && /usr/bin/false；不是新增辅助脚本。任务 76fd45fe-4495-4ae0-999a-c9107a274243 失败，Nginx 工作进程从 81047/81048 换为 81183/81184，证明重载已实际执行。
- 三个原文件 inode 2580557/2580558/2580559、大小、校验和与 644/644/600 权限恢复一致，无临时文件或锁；网站正常 TLS 返回 200。此处部署内容与原内容相同，仅证明文件回滚及错误状态，不证明新旧证书切换后的自动运行态恢复。
- 21:40 恢复正常重载命令并通过页面重新部署，任务 c0f2ad2c-7bdf-4ebc-8c93-71fe831c1869 成功；工作进程再次更新，公网指纹与托管证书一致。
- 为验证旧有效证书不能误通过，暂设不重载命令并尝试一次同域名签发；21:41 任务 3ba35861-151e-4157-9077-c35461582b0f 在 DNS 权限探测阶段失败，阿里云错误 InvalidRR.Format，未生成新证书，尚未进入 HTTPS 身份验证。
- 21:44 产品权限检查接口相邻两次返回成功与失败。失败记录名为 _scert-authorization-W_BjXXHEGzEC5CbAXQ6Tpw7-.scert-20260930.apmmk.cn.，首标签以连字符结尾；源码使用 RawURLEncoding 生成随机标签。继续整改该偶发格式问题，不能通过反复重试碰运气作为解决方案。
- 失败后已恢复服务器正常 nginx 重载命令并重新部署既有证书。没有修改受保护域或关闭 TLS 校验，未清库。

## 最新候选升级记录（2026-09-30 21:33）

- 源码 fc57b8e1da5f1e7e145ae191748451deabbea96c；规范克隆正式 Linux 构建成功，source_dirty=false，后端 548 个、前端 105 个受跟踪文件通过 LF 检查。构建后克隆无源码或锁文件差异。
- 清单 23 项大小和 SHA-256 通过；实际归档 22 个文件的名称集合、无重复项、内容哈希及五个执行入口 0755 权限通过。归档位于发布版本目录的父目录；首次核对命令误按版本目录定位归档，修正只读路径后通过，未修改产物。
- 归档 SHA-256：4efd67bccbb871f58ad5e0ec2197bc9131bdecfc1a215e070bfc5d7c1d89b23c；主程序 SHA-256：1d360b720f023f7511ee3f2d54f3a4e54ad74e9ac2fbb3a86b74cf664520d369。
- 密码 SCP 上传，严格主机指纹校验；远端归档哈希及 21 项 SHA256SUMS 通过。在 /tmp/scert-https-verification-20260930 使用正式安装器，先停止 Scert.service、Scert-storage.socket 和目录服务，升级后恢复主服务与目录 socket。
- 安装器保留旧发布文件于 /scert/.install-xPFV6M23；未新建独立远端备份或辅助验收脚本，未删除历史现场。升级后主程序哈希一致，两单元 active，公开 HTTPS readyz 返回 ready，浏览器忽略缓存刷新并重新登录成功。
- 上一批专项竞态全部退出 0：应用 98.960 秒、接口 231.215 秒、部署 13.850 秒。此前“竞态收尾中、未部署”属于历史记录，本节更新当前状态。
- 本轮是保留数据升级复测，没有清库，不代表首次初始化或完整生产门禁通过；正在验证新版本实际部署结果与失败恢复。

## 部署后 HTTPS 误报整改（2026-09-30，本地验证，未部署）

### 问题与修复

- 原验证器会验证证书有效期、域名与可信链，但工作流忽略其返回指纹；同域名旧证书仍有效时可直接成功。新增回归覆盖申请、部署已有证书、导入三条入口，旧指纹及空指纹均在修复前复现误成功。
- 原后置 HTTPS 报错只返回外层错误，report.status 仍为 success。修复前独立回归复现，修复后整体状态 failed，域名 error；单台推送结果保留原事实，不伪称文件推送失败或已回滚。
- 部署前校验本地证书、完整链与私钥，从实际文件获取指纹，不依赖 saved 元数据。远端仍沿用可信链校验，再比较指纹；不关闭 TLS 验证，不增加强制 IP 的生产验收绕过方式。
- 本地隔离测试使用临时证书与自有信任根启动真实 TLS 服务，旧有效证书触发重试，新证书才成功。该用例不是外部真实站点、多服务器或重载恢复验收。

### 部署报告补充约定

申请、部署已有证书、导入响应中的 deployment 新增可选 verification_status：success 表示站点校验通过且指纹相同，failed 表示校验失败或未能确认，省略表示未执行该阶段。旧响应无该字段时不能推断已经校验站点。

total、success、failed、results 继续记录每台服务器的文件推送结果；整体 status 还受后置校验影响。因此可能出现 success=1、failed=0，但 status=failed、verification_status=failed，这表示文件已推送但生效校验未通过，并不矛盾。前端需优先显示生效校验失败，不能单看成功数量或一台结果放行。

任务和尝试整体持久化为 failed；服务器结果表保留成功的文件推送记录。verification_status 当前随执行响应返回，不是新增数据库持久化字段，也不代表对每台服务器独立验证。

前端 239 项及生产前端构建、后端全量测试、静态检查通过。专项竞态结果待本批收尾；未部署本批代码。重载失败、后置 HTTPS 失败后的实际恢复、任务恢复、三数据库和首次清库流程继续保留待办。

## 最新构建验证记录（2026-09-30 21:07，未部署）

- 本轮沿用既有规范克隆，签出精确提交 9e89edae282d8c18ddf9588751961015d220efcf，正式发布入口输出到 build/review-release-eol，未覆盖历史包。
- Windows 和 Linux amd64 两个目标构建成功，清单 33 项产物大小及校验和通过；Windows 包 9 个文件、Linux 包 22 个文件，实际压缩包读取后的文件集合和内容哈希均与清单一致。Linux 主程序、三个运维程序及安装器权限均为 0755。
- source_dirty=false；后端 547 个、前端 105 个受跟踪文件通过 LF 策略检查；本轮完整后端测试、静态检查和前端 237 项测试通过，结束后源码及锁文件没有变化。
- 首次手工读取 SHA256SUMS 时未指定 UTF-8，导致 PowerShell 把中文文件名错误解码并报找不到路径；改为显式 UTF-8 只读检查后，Windows 8 项、Linux 21 项内容校验全部通过。未改写文件、未替换中文，也没有把核对命令错误当成产品缺陷。
- 21:03 在既有密码 SSH 会话只读核对，实际服务名 Scert.service 为 active，管理 readyz 为 ready；小写 scert 并不是该服务单元，不能据其 inactive 判断产品停止。
- 没有清库、重新初始化、升级服务器或注入新故障。本次仅完成本地正式包验证和线上健康只读检查，不代替实机生产流程。线上仍为此前验证的 0bb427d，新包身份见《构建版本与数据库支持矩阵》最新章节。

## 构建整改补充（2026-09-30 20:58）

本批只修改正式构建工具与回归，不修改应用业务代码，也未重新部署。修改前检查点 70f19d0 已推送。

- 原工作区正式工具链拒绝非规范换行，发生在依赖安装之前，没有自动改写中文文件。规范独立克隆则通过检查，后端 547 个、前端 105 个受跟踪文件采用允许的换行状态。
- 工具链 31 项、前端全量 237 项测试通过；新增真实 Git 夹具证明 Git diff 可忽略 CRLF，但正式入口仍退出 1，且不生成 node_modules 或锁文件。
- 20:58 管理 API 返回 401，明确提示重新登录，确认此前浏览器会话已失效；公开管理入口和就绪检查仍正常。未把未认证接口的错误当成有效任务列表。
- 后续需在规范克隆用本批提交完成正式发布构建。P0-03 目录边界复审结论不变；P0-04 整项及生产完整门禁仍未完成。

## 最新实机与复审记录（2026-09-30 20:53）

### 正式页面配置校验失败与恢复

- 20:06 确认 /etc/nginx/scert-acceptance-missing-20260930.conf 不存在后，通过服务器编辑表单将校验命令临时设为 nginx -t -c 加该路径；不创建辅助脚本、不修改真实 Nginx 配置，不停止站点。
- 20:07:25 正式页面部署已有证书返回 502，任务 434b370b-72a2-41fe-a507-fbc5abe955c9 为 failed，持久化结果记录尝试 10f6606c-3ce2-45bf-b5de-74164af80c11 失败。受保护服务日志明确记录“配置校验失败：退出码 1；文件已回滚”。页面使用脱敏通用失败提示，具体原因从受保护日志核对。
- 失败前后 cert.pem、fullchain.pem、key.pem 的 inode 分别保持 2580557、2580558、2580559，校验和均与成功签发时一致，权限为 644、644、600；目录仅剩这三个文件，没有部署锁、临时文件或备份残留。Nginx 主进程 823 和工作进程 80097、80098 未变，HTTPS 返回 200。
- 20:10:01 正式页面将校验命令恢复 nginx -t；20:10:44 再次部署返回 200、耗时 1375 毫秒，页面显示 1 台成功、域名恢复正常。文件 inode 更新为 2580560、2580561、2580562，内容校验和不变；工作进程变为 80397、80398，配置检查通过、HTTPS 为 200。没有再次申请生产证书。
- 20:49 再查服务 active、管理入口 ready、测试 HTTPS 200。浏览器会话随后已失效，读取任务接口未获得新的有效列表，未把此读取异常当成部署失败或伪造新的报告。

### P0-03 当前源码独立复审

- 修改前检查点 1857d72 首次推送认证失败，第二次推送成功。全程没有删除工作区既有文件。
- 20:48 从本地仓库独立克隆到 .runtime/source-boundary-review-20260930-2015，使用 no-hardlinks 并签出精确部署源码 0bb427d5f8e230e7f9b600dda5546ef7fdd59ddb；没有复制现有依赖、构建目录或运行配置。
- 正式 build-release.ps1 按锁文件安装前端依赖并构建 Linux amd64 发布包，source_dirty=false。Go 1.26.6、Node 24.19.0、npm 11.17.0；发布路径检查通过。
- 原工作区源码边界专项测试通过（1.251 秒），独立克隆发布模块完整测试通过（3.762 秒）；测试核对仓库规则和实际索引，不依赖用户 Git 排除规则。
- 发布清单共 23 项 SHA-256 全部核对一致，Linux 目录共 22 个文件；包内仅正式可执行文件、安装器、服务与 Nginx 模板、中文部署说明及 Web 文件。未携带数据库、私钥、浏览器缓存、源码映射或依赖目录。首次检查误把克隆父目录 .runtime 算作包内容，已改按包内相对路径复查；这是核对命令修正，不是产品缺陷。
- 构建后独立克隆 git status 为空，锁文件差异为空。远端既有正式包 SHA256SUMS 再次全部通过，22 个文件；线上程序仍为此前已验证的 944f2f787089942dd33ffb8b6e1e721b9f624c37cec220c3ca4801ed4e637704。
- 独立克隆复审包归档 SHA-256 为 feb24b5876628e71ad6354063032b82845ef82628e7015e3be11a6697728980a，程序为 d854e343373c0ba15b50075f1b4a70470b413cfb46d11b7d59248b7c0ed13fc6。此包没有部署，不与线上候选混用。
- 结论：P0-03 的源码与产物边界复审通过；不外推为其他 P0 或生产功能全部通过。

### 新发现与剩余门禁

- 原工作区与独立克隆有 27 个前端输入原始字节不同，比较后全部仅为 CRLF/LF 差异；规范化比较没有其他内容差异。Git 的干净状态不能单独证明原始构建输入字节一致。P0-04 需增加构建前拒绝非规范换行的检查，不自动改写中文源码。
- 配置校验失败的文件回滚已验证，不代表重载部分执行失败、后置 HTTPS 校验失败或中途断电全部通过。
- 多域名、多服务器、任务恢复、三数据库与清库首次初始化仍待完成；当前仍不满足完整生产放行。

## 最新实机记录（2026-09-30 20:06，正式证书单台部署成功路径）

### 范围与发布身份

本轮保留失败现场进行正式升级针对性复测，不是清库首次初始化。复用已有服务器，没有创建重复服务器记录；测试域名和地址仅为本次输入，不硬编码为验收条件。未修改禁止操作的业务域名。修改前检查点 18bfd16 已推送。

- 正式源码：0bb427d5f8e230e7f9b600dda5546ef7fdd59ddb，构建清单 source_dirty=false。
- 发布归档 SHA-256：22b52ce1fb822c4e86ac91d27f207711331eeec820d21452870ea1ed68d39a34。
- 本地与远端主程序 SHA-256：944f2f787089942dd33ffb8b6e1e721b9f624c37cec220c3ca4801ed4e637704。
- 密码 SCP 上传、完整 SHA256SUMS 校验后使用正式安装器；服务、管理入口和就绪检查正常。SSH 严格核对已登记主机指纹，未关闭 TLS 验证。

### 签发、推送和消费端证据

- 测试域名：scert-20260930.apmmk.cn；域名 ID：dfcd80c0-1c58-4aa2-b156-d558d4d2b9a1；既有服务器 ID：3cfe6b82-6807-421d-88ab-a2034370375b。
- 19:54 第一次申请在权限探测阶段超时，HTTP 504，未进入 ACME；随后正式产品命令的 DNS 写入、读取、清理检查均通过，才执行下一次页面申请。不能把该超时解释成确定的 RAM 权限不足。
- 19:57:21 第二次页面申请返回 200，耗时 23219 毫秒；任务 fbefdb52-7fc0-4942-8512-850b0869c07d 为 success，消息为“证书申请和部署完成”。
- 正式任务部署结果接口返回成功，消息为“证书部署、配置校验并 reload 成功”，尝试 ID 为 538a69d5-b4e9-4ba9-9cbf-6b8db86d242e。
- 托管证书实际路径为 /ssl/scert/scert-20260930.apmmk.cn/single/20260930-V1.0/，不是无版本目录；目标为 /ssl/scert-20260930.apmmk.cn/single/。三个文件逐一 SHA-256 相同，私钥权限为 600。检查未发现残留部署锁或临时文件。
- 证书指纹：9A50D4A0E551EC728D4900E595029C52B4F5EDC9E24B518F123646289101FC53；托管接口、部署文件和实际 HTTPS 返回一致。
- 签发者为 Let's Encrypt YR2；有效期为 2026-09-30 10:58:47 UTC 至 2026-12-29 10:58:46 UTC；SAN 包含本次测试域名。
- Nginx 配置校验通过，11:57:20 UTC 存在 signal process started 重载日志；服务器和外部 Windows 客户端正常域名 HTTPS 均返回 200，没有使用忽略证书校验参数。
- dns21.hichina.com 与 dns22.hichina.com 的权威非递归查询均对本次 _acme-challenge 名称返回带 aa 的 NXDOMAIN；递归缓存的旧 TXT 不作为云端清理失败依据。

### 尚未通过的门禁

- [~] 正式页面失败回滚及恢复后重复部署。
- [ ] 一台服务器多域名、任务恢复、第二台真实服务器、三数据库及清库完整首次初始化。
- [!] DNS 偶发超时仍需评估；本次成功不证明所有网络失败场景通过，也不证明后置 HTTPS 校验失败时服务自动恢复。整体验收仍未放行。

## 最新实机记录（2026-09-30 19:43，授权探测及测试子域准备）

本轮继续保留现场的升级针对性复测，不是清库首次初始化验收。线上源码仍为 7147532，尚未部署本轮 DNS 修复。

- [x] 19:25:28 页面创建域名失败，HTTP 400，耗时 2661 毫秒，报权限验证超时。
- [x] 19:30:54 产品正式命令 dns check 在 scert-20260930.apmmk.cn 返回 writable/readable/cleanup/verified 均为 true。
- [x] 19:32:01 正式页面创建域名 HTTP 201，耗时 2395 毫秒；域名 ID dfcd80c0-1c58-4aa2-b156-d558d4d2b9a1，关联既有服务器 HTTP 200。
- [x] 实机发现初次请求遗留随机授权 TXT，创建时间与失败请求一致；精确核对后删除该测试记录，不修改其他 TXT。
- [x] 云端实测确认 COMBINATION 按 RR 和类型过滤，EXACT 忽略这两个参数；本地 SDK 文档同样明确该语义。
- [x] 通过阿里云官方 CLI 为新子域新增 A 记录；只修改明确授权测试子域，保留原有泛解析。CLI 来自官方发布并验证校验和，不是自建辅助验收脚本；临时凭据只在会话环境使用。
- [~] 真实证书尚未签发，本轮目录创建、文件推送、Nginx 和公网 HTTPS 未通过，不能标记完整生产验收成功。

## 最新实机结果（2026-09-30 19:20，SSH 自动部署通过一次复测）

### 候选身份与部署

- 源码：714753236f584a2bc705e271334c697dc7f9adbd，发布清单 source_dirty=false。
- 本地目录：build/release-ssh-reload-20260930/Scert-20260930-V0.1.0。
- 归档 SHA-256：716c5167bfb2532bc2276c84d1bf087cfd686e1cb736a2e18765a131f6c9855e，本地和远端一致。
- 主程序 SHA-256：a39c8b0beaf2dc31e3bdc3b2aa774651e6d8e934ab956058518b10f71d9daeb2，本地和远端一致。
- 密码 SCP 上传后在 /tmp/scert-ssh-reload-20260930 校验全部 SHA256SUMS，再使用正式 install-linux.sh。第一次因 Scert-storage.socket 仍在监听被安装器拒绝；停止相关服务后安装成功，Scert 与目录服务 socket 已恢复 active。
- 安装器自动保留旧发布文件于 /scert/.install-jD3pl0Pp，没有把该目录作为验收条件，也未擅自删除。未创建独立远端数据备份或辅助验收脚本。

### 页面、接口与目标机交叉验证

- 管理入口返回 200，healthz=ok、readyz=ready，正常域名 HTTPS 校验未关闭。浏览器忽略缓存刷新并重新登录，编辑既有服务器记录，没有新建重复资源。
- PUT /api/v1/servers/3cfe6b82-6807-421d-88ab-a2034370375b 于 11:18:58.026842 UTC 返回 200，耗时 1844 毫秒。
- GET /api/v1/servers 复读：auth_method=private_key，ssh_key_status=deployed，ssh_key_error 为空，ssh_key_deployed_at 和 ssh_key_tested_at 均为 2026-09-30T11:18:58Z。
- 目标机 SSH 日志：11:18:56.523794 UTC 密码认证成功，11:18:57.519436 UTC 收到重载信号，11:18:57.541562 UTC 恢复监听，11:18:57.890368 UTC 公钥认证成功；对应指纹存在于 authorized_keys。
- sshd -t 通过，公钥认证与密码认证均启用，Scert 与 ssh 均 active。Scert 只有在私钥连接执行 true 成功后才保存 deployed，本次页面结果和目标机日志一致。

### 验收边界

- 本批后端全量测试、静态检查、构建和三包竞态检查全部通过，不等同于外部数据库矩阵通过。
- 本轮保留现有数据库和失败记录进行针对性修复验证，不是清库首次初始化验收；未重新申请证书、未执行实际证书推送或 Nginx 重载／回滚，不重复沿用历史通过记录。
- 第二台真实服务器、域名服务器多对多生产推送、三数据库和最终生产门禁尚未完成。总体验收仍为 P0-03 完成待复审，不新增整体放行结论。

### 2026-09-30 19:10 现场证据补充

本地竞态检查已全部通过。远端密码 SSH 读取精确日志显示：SSH 于 10:27:40.559700 UTC 收到重载信号，Scert 于 10:27:40.566606 UTC 私钥连接被拒绝，SSH 于 10:27:40.583273 UTC 恢复监听。该次失败处于监听重建窗口；候选仍需补充公钥安装后有界重试，再构建部署复核。此处仅是历史失败日志的定位证据，不是重新通过生产验收。

## 最新状态（2026-09-30 19:08，SSH 修复候选本地验证）

- [x] 已用密码 SSH 和严格已登记主机校验连接授权目标；只读执行服务状态及日志查询，Scert 和 SSH 均为 active。
- [x] 本地默认单次协商回归通过；后端全量测试、静态检查及构建通过。
- [~] 竞态检查正在运行；候选尚未重新发布到远端。
- [ ] 自动密钥登录、真实证书推送与 Nginx 重载尚未取得本轮通过证据。
- [!] 下方历史记录保留原日期，不代表当前候选的验收状态。本轮未清库、未重启服务、未修改远端文件，不是首次初始化重测，不新增生产放行结论。


## 当前进展：发布说明修正与入口、依赖审计

2026-09-20 03:12 起接续，03:22 完成本节所列检查。本节为最新状态，下文按各自时间保留历史证据。实际仓库仍为 `D:\Desktop\Scert`，目标为 `45.136.13.241`，本次管理域名为 `odacm.cn`；这些测试参数不硬编码进产品，也不构成验收依据。本轮未登录服务器、上传产物、修改 DNS/Nginx、修改系统信任库或清空共享数据库，完整生产部署验收尚未通过。

### 需求与任务状态

- [✓] 接回 Windows 与 Linux 目标的两项 `govulncheck`，均已正常退出；没有重复启动未结束任务。
- [✓] 修改前检查点 `71fe48c` 已推送，暂存区事先为空；旧补丁、二进制、脚本及历史构建目录全部保留。
- [✓] 修正 `deploy/README.md`：生产探测不再跳过 TLS，分别说明回环上游和正常 DNS 公开入口验证；统一标准 `443` 示例，旧 `8003/8004` 仅保留为自定义端口示例。
- [✓] 按当前实现修正 PostgreSQL 备份恢复说明，注明官方工具、版本、严格 TLS、同目标同对象清单限制及独立保护的集群资源，不扩大为灾难恢复已验收。
- [✓] 复核 Go/Wails 的 `-trimpath` 和前端禁用源码映射，发布检查及 CLI 定向回归通过，已有 Linux 产物路径扫描再次通过。
- [!] SSH 新指纹尚未通过可信控制台核对；没有发送密码、更新 `known_hosts` 或接受新主机身份。
- [!] 两家 HTTPS DNS 查询仍指向旧地址，当前入口 TLS 检查失败；不可用固定解析探测替代域名生产验收。
- [!] 本机 PowerShell 有效策略仍为 `Restricted`，没有修改或绕过；最新正式发布脚本、Linux 双层集成与部署流程仍未完成。

### 入口与主机身份

本机 `Resolve-DnsName odacm.cn -Type A -DnsOnly` 返回 `198.18.1.249`。严格 HTTPS DoH 查询 Cloudflare 与 Google 均返回 `odacm.cn A 47.108.78.12`、TTL 600，不是本次目标 `45.136.13.241`。这是两个递归解析器的当前观察，不宣称已直接验证权威 DNS；本机普通 DNS 的结果不能替代公共解析结论，也没有据此更改网络或 DNS 设置。

以下三项均已实际执行，无跳过证书验证：

```text
curl.exe --noproxy "*" --fail --silent --show-error --max-time 15 https://odacm.cn/readyz
curl.exe --noproxy "*" --fail --silent --show-error --max-time 15 --resolve odacm.cn:443:45.136.13.241 https://odacm.cn/readyz
curl.exe --noproxy "*" --silent --show-error --max-time 15 --resolve odacm.cn:80:45.136.13.241 -I http://odacm.cn/
```

前两项返回 curl 错误 35：`schannel: failed to receive handshake, SSL/TLS connection failed`；第三项返回错误 52：`Empty reply from server`。只能说明本机所经网络路径上的请求失败，不能证明目标 Nginx 配置、服务状态或证书根因；透明代理等网络因素仍未排除。

03:21 左右，Paramiko 无认证握手读取到 Ed25519 指纹 `SHA256:cVoSXsYmhALjAdN2Gso3JvbnvYQCQBFuRv3IZjFvzsE`，`authenticated=False`。它与历史可信值 `SHA256:XN9mJ75b4CJsFPZ7fbkAmU1yFxKZ4Tw01z8TN2EJMlk` 不同；网络上反复读到同一新公钥不构成可信确认。须在服务商可信控制台执行 `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` 后核对，再决定是否认证。

### 依赖与回归证据

```text
go mod verify
govulncheck ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 govulncheck ./...
govulncheck -show verbose ./...
npm.cmd audit --omit=dev --json
go test -trimpath -count=1 -p=2 ./internal/releasearchive ./internal/cli
git diff --check
```

Go 命令在 `backend` 执行，npm 命令在 `frontend` 执行；Linux 环境变量一行是目标配置的简写，实际通过 PowerShell 子进程环境设置。`go mod verify` 返回 `all modules verified`。两平台漏洞扫描均报告调用路径零项、导入包零项、依赖模块一项；Windows 详细报告将其定位为 `GO-2026-5932`，涉及 `golang.org/x/crypto@v0.56.0` 中不再维护的 `openpgp` 包，无修复版本，当前代码未导入受影响包或调用其符号。没有为消除模块提示而移除仍需要的加密依赖，也不能将本结果描述成所有依赖完全没有已知风险。

前端生产依赖审计报告零漏洞，不包含开发依赖，也不代表未来漏洞保证。发布检查包与 CLI 回归均通过；真实 PostgreSQL 集成入口未提供官方工具和隔离环境参数，本轮不算新增真实数据库演练。部署说明的两个 JSON 配置块通过解析检查，差异检查无错误；没有在 Linux 上执行新文档中的 Nginx 配置或健康探测示例。

### 本地路径要求与产物边界

重新调用 `scert-release --verify-only`，对 `.runtime/clean-path-artifacts-20260920-0243/` 的三个 Linux 程序与整个资源目录，以及 `.runtime/app-integration-20260920-0307/app.test` 执行构建标记和路径扫描，均通过。扫描参数包含当前与旧工作区、用户目录、实际 Go 工具链及缓存、Go/Node 安装目录与临时目录；这些参数不写入产物。已有 Web 产物未找到 `.map` 文件。

这两处是前序已生成的核验产物，本轮没有重新编译完整发布包，也没有把新部署说明复制到旧产物中；此前记录的摘要及源码版本边界继续适用。正式候选必须从最终提交重新运行完整发布脚本，生成清单和校验文件并通过同样检查后再部署。模块导入路径、相对文件名和运行时部署路径不等于本机构建绝对路径，裁剪要求不删除这些运行所需信息。

### 待解除的验收阻塞

- [ ] 通过可信控制台确认当前 SSH 主机身份；确认后先只读检查现场，不能沿用旧服务器 PID、端口或配置结论。
- [ ] 核对 `odacm.cn` 的实际 DNS 配置与迁移范围，使其指向授权目标；分别验证权威/公共解析、本机解析及正常 DNS 的严格 HTTPS 入口。
- [ ] 在获准的构建环境运行完整发布脚本，并在隔离 Linux 单元执行五个应用与 Nginx 双层场景，确认没有跳过。
- [ ] 完成 root 辅助服务/systemd、中断恢复、最新候选部署、管理证书续期、目标推送、通知送达、升级回退与容量验收；未完成前不能宣告生产部署通过。

## 当前进展：真实应用与 Nginx 双层切换集成用例

2026-09-20 02:54 起接续。本节为最新状态，后续章节保留为历史证据。本轮没有登录服务器、部署程序、修改系统信任库、修改正式 Nginx 或清空共享数据库。完整生产部署验收尚未通过。

### 需求与任务状态

- [✓] 核对实际仓库 `D:\Desktop\Scert`，保留旧补丁、未跟踪二进制及历史产物；修改前中文检查点 `53ed3d6` 已推送。
- [✓] 延续用户要求，所有本轮 Go 测试及交叉编译均使用 `-trimpath`；最新 Linux 测试程序通过构建标记及本机构建路径扫描。
- [✓] 新增真实应用重建回归，验证 HTTPS、SQLite 和凭据存储重建后的就绪状态与数据库内容保留。
- [x] 新增五个真实应用与 Nginx 双层集成场景，源码复审、交叉编译及 Linux 目标静态检查通过，等待 Linux 实际运行复审。
- [✓] 测试提交 `c7d5344` 已推送；本节更新前已检查暂存区，仅包含三个新增测试文件。
- [!] SSH 主机身份变化仍未获得可信确认，没有发送密码或接受新指纹；远端部署继续等待身份核对。
- [!] 本机无可用 WSL、Docker 或 Podman；PowerShell 有效执行策略仍为 `Restricted`，没有修改或绕过策略，完整发布脚本仍待执行。

### 集成范围与边界

`backend/internal/app/management_integration_test.go` 复用正式 `NewWithConfigPath`、`HTTP.ListenAndServe`、SQLite 迁移和敏感信息存储，使用独立临时目录、随机回环端口和测试证书。跨平台用例关闭并重建应用，严格 TLS 探测 `/readyz`，同时检查预置数据库记录保留；没有访问真实 DNS 或申请证书。

`management_nginx_linux_test.go` 与 `management_nginx_fixture_linux_test.go` 通过正式应用适配器和 Unix IPC 连接独立 Nginx，覆盖以下场景：

1. 正常双层切换，证书路径持久化，应用重建后保持已提交候选。
2. 暂存已完成但应答丢失，回退代理，应用保持旧证书。
3. 提交已完成但应答丢失，通过事务与实际双层证书核验确认成功。
4. 暂存后恢复查询不可达，保留 `prepared` 与代理 `staged`，重建应用后恢复旧证书。
5. 上游配置持久化并切换后，提交与恢复查询不可达，保留 `upstream`，重建后完成提交。

故障通过客户端包装层注入，不是真正丢弃网络帧或杀死辅助进程。应用关闭后由配置重建，使用实际恢复工作循环处理持久化事务；不等同于进程强杀、断电、固定生产 socket 或 systemd 服务重启验收。测试辅助服务与应用使用同一隔离 UID，不替代此前不同 UID/root IPC 边界用例。

每个 Linux 场景在独立子进程中通过 `SSL_CERT_FILE` 加载测试 CA，避免系统根证书缓存影响；没有给生产 API 增加信任注入接口，也没有关闭证书验证。独立 Nginx 开启上游可信链验证，只使用本轮配置、PID、版本目录和回环端口。重建前后均检查真实两层叶证书、协调记录、配置未被无关修改及数据库内容。

### 本轮执行证据

以下 Windows 本地命令已执行通过：

```text
go test -trimpath -count=1 -p=2 ./...
go vet -p=2 ./...
go test -race -trimpath -count=10 -p=1 -run '^TestApplicationManagementListenerRestartsWithPersistentReadiness$' ./internal/app
go test -race -trimpath -count=1 -p=1 ./internal/app
```

新增真实应用重建测试首跑通过；连续 10 轮竞态测试包内用时 10.034 秒，应用包全量竞态测试包内用时 19.710 秒。Windows 全量测试不包含 Linux 专用用例。本轮未修改前端，未重复浏览器或前端构建验收。

Linux 目标 `go vet -p=2 ./internal/app ./internal/nginxactivation ./cmd/scert-nginx` 通过；最终测试源码调整后再次运行应用包 Linux 目标 vet 与交叉编译，均通过。最新测试程序为 `.runtime/app-integration-20260920-0307/app.test`，Go 1.26.6、`GOOS=linux`、`CGO_ENABLED=0`、`-trimpath=true`，SHA-256：

```text
ce3a89d9450c88ffcdb0802234a757bbe0d8888e88f737a21e9427d5f19fca77
```

扫描覆盖当前与旧工作区、用户目录（包含实际 Go 工具链、模块缓存、构建缓存与用户临时目录）、常见 Go/Node 安装目录及系统临时目录，结果通过。该文件是测试程序，不是正式安装包，也没有 Linux 执行通过的证据；旧 `.runtime/app-nginx-20260920-0303.test` 保留，不作为最新用例产物。

### 下一步验收

- [ ] 通过服务商可信控制台核对 `45.136.13.241` 的 SSH 主机公钥，再只读检查实际服务、DNS、端口、数据库和容量。
- [ ] 在非特权隔离 Linux 单元中显式指定 `SCERT_TEST_NGINX_BINARY`，执行应用双层集成测试，确认五个场景均实际运行、没有跳过，并重复执行检查稳定性。
- [ ] 验证独立 root 辅助服务初始化与 socket/service 模板，补齐进程强杀及 systemd 恢复证据。
- [ ] 执行完整发布脚本，生成具备清单、校验和且路径扫描通过的最新正式候选，再完成管理证书续期、目标推送、通知送达、升级回退与容量验收。

## 当前进展：辅助服务初始化、真实监听回归与干净源码路径复验

2026-09-20 02:25 至 02:47 接续。本节为最新状态，后续各节保留为历史证据；不把旧服务器记录当作当前环境。本轮仅完成本地修改、交叉构建和测试，没有登录服务器、部署程序、修改 Nginx 或清空数据库。

### 需求与完成状态

- [✓] 路径裁剪提交 `a212401` 已推送；本轮再次复审发布入口与扫描器，确认 Go/Wails 使用 `-trimpath`，前端关闭源码映射，归档前检查构建信息及本机构建路径。
- [✓] 修改前检查点 `aa5d669` 与辅助服务提交 `d8b4850` 已推送。补齐 root 策略、安全初始化入口、systemd socket/service、中文部署说明与打包接线。
- [✓] 新增真实监听回归提交 `549d6a832eec0cbc8dfeb01a43eea2eecfdba2fc` 已推送，远程 main 已核对一致；该提交也是更新本节前的检查点。
- [✓] 使用独立干净克隆重新构建三个 Linux 程序与 Web，全部通过路径扫描，构建标记为 `vcs.modified=false`。
- [!] 完整 PowerShell 发布脚本仍受有效执行策略 `Restricted` 限制，本轮未改变或绕过执行策略；直接工具构建不等于发布脚本已验收。
- [!] SSH 主机身份变化仍未获可信确认，不能发送 root 密码或继续远端部署。完整生产部署验收尚未通过。

### 辅助服务与真实监听测试

`scert-nginx --initialize` 验证输入文件、可信路径、权限、证书链、两层实际 TLS 证书、就绪响应与 `nginx -t`，只在专用目录生成私有版本及引用。已有状态、链接、不可信路径或宽权限私钥均拒绝；初始化不覆盖、不重载，也不安装服务。普通安装器仍不启用该 root 辅助服务。

新增 socket/service 保持本地 Unix IPC 与唯一允许 UID，不给应用进程增加权限；辅助单元仅保留 `CAP_DAC_OVERRIDE CAP_CHOWN`，并依赖、跟随 `nginx.service`。模板与初始化已完成源码和交叉构建检查，尚无本轮目标 Linux/systemd 执行证据。

`backend/internal/httpapi/management_session_integration_test.go` 通过真实 `Server.ListenAndServe()`、临时 SQLite 及迁移、受保护凭据存储和严格 HTTPS 就绪探测覆盖三项：

1. 独占会话中切换证书，只修改配置中的证书路径；监听器重建后保留新证书与数据库内容。
2. 模拟证书配置已落盘而内存快照尚旧的中断窗口，拒绝继续协调；服务重建后从持久化配置恢复候选。
3. 数据库关闭后，即使 HTTPS 仍可连接，就绪探测也必须失败。

三项首跑及连续 20 轮通过。HTTP API 全包竞态测试 `go test -race -trimpath -count=1 -p=1 ./internal/httpapi` 完成并通过，包内用时 67.667 秒。这是 Windows 真实上游监听测试，不是完整 Nginx 双层协调、进程强杀或 systemd 重启验收。

### 干净源码与产物证据

独立克隆 `.runtime/clean-path-20260920-0243/` 对应提交 `549d6a8`，构建前后 Git 状态均干净。执行以下检查全部通过：

```text
go test -trimpath -count=1 -p=2 ./...
go vet -p=2 ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go vet -p=2 ./internal/app ./internal/nginxactivation ./cmd/scert-nginx
npm test
npm run build -- --outDir <独立新建目录>/web --emptyOutDir false
```

前两项为 Windows 本地执行；第三项仅为 Linux 目标静态检查。前端 54 项测试通过，生产构建无 `.map` 文件；本轮未修改页面，也未重复浏览器渲染验收。

`.runtime/clean-path-artifacts-20260920-0243/` 保留三个 Linux 程序、Web、安装器、服务模板、Nginx 模板和说明，共 18 个文件。三个程序均由 Go 1.26.6 构建，`-trimpath=true`、`CGO_ENABLED=0`、`GOOS=linux`，内嵌提交为 `549d6a8`、`vcs.modified=false`。整目录扫描覆盖当前与旧工作区、干净克隆、产物目录、用户目录、Go/Node 安装目录、Go 缓存和临时目录，结果通过。

| 程序 | SHA-256 |
| --- | --- |
| `scert` | `aadc2c3d08fc1ab0e466ac7f332dbe024bb17009553901bdb5d91f108d65c038` |
| `scert-admin` | `f41cdc58721231512656bf1a41bc25faa671d7aef6d05955e0553111ff4604f0` |
| `scert-nginx` | `42d957f5f0263419d8c3b0a935b5882b80b6be1ec81e11d2d82c6bdb83c5fca8` |

该目录没有正式发布脚本生成的 `manifest.json` 和 `SHA256SUMS`，仅用于核验，禁止当作正式安装包。路径扫描不会删除模块导入路径、相对源码文件名或运行时必需部署路径，也不保证识别源码中手写的任意其他机器路径。

三类 Linux 测试程序从同一干净克隆以 `go test -trimpath -c` 生成，保留在 `.runtime/clean-tests-20260920-0246/`：`httpapi.test`、`nginxactivation.test`、`scert-nginx.test`。仅交叉编译通过，尚未在 Linux 运行。前轮 `.runtime/path-audit-20260920-0236/` 及其他旧产物全部保留，其 `vcs.modified=true` 标记不再作为新干净产物的证据。

### 连接边界与待验收项目

02:47 左右，无认证 SSH 握手取得 `45.136.13.241` 的 Ed25519 指纹 `SHA256:cVoSXsYmhALjAdN2Gso3JvbnvYQCQBFuRv3IZjFvzsE`，与历史可信值 `SHA256:XN9mJ75b4CJsFPZ7fbkAmU1yFxKZ4Tw01z8TN2EJMlk` 不同。Windows 自带 `ssh-keyscan` 因不支持服务器选择的 KEX 未取得公钥，随后使用 Paramiko 只完成握手与公钥摘要读取；没有认证、写入 `known_hosts` 或传送业务数据。网络上读取到的公钥不能自证可信，仍须在服务商可信控制台执行 `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` 核对。

- [ ] 获准执行完整发布脚本，生成带清单和校验文件的正式候选；不得以核验目录替代。
- [ ] 确认主机身份后只读检查现场服务、域名、端口、数据库及容量，不沿用历史部署参数。
- [ ] 在隔离 Linux 环境执行应用适配器、真实 Nginx 双层协调、root IPC、systemd、应答丢失及中断恢复测试。
- [ ] 完成最新候选部署、真实管理证书续期、目标机推送、通知送达、重启、升级与业务回退及容量验收。

## 当前进展：路径裁剪与本地发布检查

2026-09-20 接续。本节为最新状态，后文为各阶段历史记录。实际仓库位于 `D:\Desktop\Scert`，原 `D:\Desktop\SSL` 已不存在。本次修改前检查点 `ba6c6d1` 已推送；本节产物来自修改中的工作区，仅用于构建验证，不是正式可部署发布包。

### 需求与完成状态

- [✓] 按用户最新要求，Go 与 Wails 发布入口均使用 `-trimpath`，手动构建示例同步补齐；前端明确关闭源码映射。
- [✓] 发布检查读取每个 Go 二进制的构建信息，缺少 `-trimpath=true` 时拒绝归档；扫描发布目录的全部文件，发现本机构建路径时拒绝归档。
- [✓] 扫描覆盖正反斜杠、JSON 转义、UTF-8、UTF-16 和跨读取边界；单元测试覆盖未裁剪二进制、静态资源残留、读取错误与缺失文件。
- [✓] 三处安装器及服务模板测试不再依赖编译时 `runtime.Caller` 路径，支持 `go test -trimpath`。
- [✓] Linux 发布入口补入 `scert-nginx`，归档可执行权限由实际二进制列表生成；单元测试和实际归档检查通过。普通安装器仍不安装或启用 root 辅助服务。
- [!] 当前主机禁止运行 PowerShell 脚本，完整发布脚本未实际执行通过；没有修改执行策略。语法检查通过，实际构建和扫描通过直接调用工具验证。
- [!] SSH 主机身份变化尚未确认，没有发送密码、上传文件、变更 `known_hosts`、重载 Nginx 或清空数据库。

### 本轮验证证据

- Windows 后端 `go test -trimpath -count=1 -p=2 ./...` 全部通过。
- Windows `go test -race -trimpath -count=1 -p=1 ./internal/releasearchive ./internal/admin` 通过；桌面模块 `go test -trimpath -count=1 -p=2 ./...` 通过。
- Windows 与 Linux 目标的发布检查、安装管理包及发布命令 `go vet` 通过；Linux 额外检查 `cmd/scert-nginx`。这是静态检查，不是 Linux 测试运行。
- 前端 54 项测试通过，Vite 生产构建成功，无源码映射文件。
- `.runtime/path-check-20260920-0151/` 保留本轮 Linux 的 `scert`、`scert-admin`、`scert-nginx`，Windows 的 `scert.exe`、`scert-admin.exe`、`ScertDesktop.exe` 和 Web 资源。
- Wails 2.11.0 使用 `-trimpath -nopackage` 完成 Windows 产物构建。为保留已有生成文件且避免依赖文件改写，本轮先构建前端，再传入 `-s -skipbindings -m -nosyncgomod`；这不是完整发布脚本的桌面步骤验收。
- 六个二进制均通过构建信息检查；包含部署说明、安装器和服务模板的目录通过路径扫描，额外覆盖旧工作区和当前 Go/Node 安装目录。扫描检查本次指定的构建环境路径，不等于识别所有可能手写的任意机器路径。
- `.runtime/path-check-linux-20260920-0158.tar.gz` 为直接调用归档工具生成的验证包，确认三个 Linux 程序和安装器均为 0755。该包没有完整发布脚本生成的清单及校验文件，不用于安装。SHA-256：`cda83659c0b8a6382efda4907b1adaa26170e35e4232e9bffc8c494407db7f4a`。
- 新交叉编译的 `.runtime/nginx-request-trimpath-20260920-0158.test` 构建信息包含 `-trimpath=true`，SHA-256 为 `ecfe71a2562fde94755339270988910e01b0ac76e2e21a2472e7e1484fc972cd`。未在 Linux 执行；旧测试程序保留，不作为后续部署产物。

### 前序交接与下一步

接手时的源码检查点 `822e204` 已包含请求取消栅栏、请求编号恢复、辅助服务能力检查及应用 HTTPS 监听保护。本轮未重复将交接中的 Linux 交叉编译结果算作真实执行，也未重跑历史服务器夹具。

待从可信控制台核实目标 SSH 主机身份后，先核对现场服务、端口、DNS、数据库和容量，再执行隔离的 Linux/Nginx/Unix IPC 测试。仍需完成辅助服务安装策略与 socket 模板、真实应用配置持久化协调验收、正式候选构建、管理证书续期、目标机推送、通知、重启及升级回滚。当前未达到生产部署验收条件。

## 当前进展：受限本地激活服务与身份边界验证

2026-09-18 02:20 起接续，修改前检查点 `cc94a61` 已推送。本轮继续验证独立辅助服务，没有替换正式二进制、安装正式 socket、重载正式 Nginx 或清空共享数据库。代理模式续期仍在申请前拒绝，完整生产验收尚未完成。

### 已完成的修改

- [✓] 补齐受限 Unix socket 服务、客户端及独立 root 策略的测试；仅开放 `ready/state/stage/commit/reconcile`，请求不能指定命令或路径。
- [✓] 修复监听器异常关闭被误报为正常退出；请求校验失败也清理已解析的私钥切片，并清理编码、读取持有的帧缓冲。
- [✓] 策略加载增加 socket 父目录信任校验，覆盖非法 UID、地址、域名、权限、符号链接、硬链接、FIFO、超限及错误 JSON。
- [✓] 激活等待改为连续三次新连接的严格 TLS/就绪探测；补测新旧证书交替时需重新累计成功次数。
- [✓] 实际不同 UID 子进程验证授权与拒绝；伪装服务端即使将 socket inode 改为 root 所有，客户端仍通过内核身份拒绝，不发送请求。
- [✓] 继承 FD3 的子进程验证合法 Unix socket、错误 PID、错误 FD 数量、错误路径及 TCP 监听器拒绝，关闭不删除托管 socket。

### 验证证据

本地 `go test -p=2 ./...`、`go test -race ./internal/nginxactivation -count=1` 通过；竞态测试仅覆盖 Windows 通用代码。Linux 目标 `go vet ./internal/nginxactivation ./cmd/scert-nginx`、辅助命令构建及两类测试程序交叉编译通过。

远端测试程序位于 `/scert/acceptance-20260917-1044/`，同名本地文件位于 `.runtime/`；上传前后 SHA-256 一致：

| 文件 | SHA-256 |
| --- | --- |
| `nginx-ipc-20260918-0240.test` | `0c41d5a0f0fd7990bb33377280af651054198529eef076adeedf7d0a3bd5d497` |
| `nginx-listener-20260918-0230.test` | `c99dc7ef748ec8c6b3219859d7c070935c77312449b4338ce9e8539e28e3ea1e` |

`scert-nginx-ipc-20260918-0240.service` 以 Scert 用户运行，私有网络与临时目录、只读系统、隐藏用户目录、禁止提权，限制 192 MiB 内存、禁用 Swap、CPU 50%、64 个任务。通过 `InaccessiblePaths` 隐藏 `/scert/conf /scert/data /scert/log /scert/backups /ssl-scert-acceptance -/ssl`。显式使用 `/usr/sbin/nginx`，以 `-test.timeout=150s -test.count=10` 连续十轮通过，退出零，运行 111.123 秒、CPU 7.418 秒、峰值内存 21.9 MiB。不同 UID 用例在非 root 单元中按设计跳过，在下列独立 root 单元中执行。

`scert-nginx-listener-20260918-0230.service` 使用同类非特权隔离，继承监听器五个场景连续三轮通过，退出零，运行 170 毫秒、峰值内存 6.1 MiB。

`scert-nginx-identity-20260918-0242.service` 是仅执行身份边界用例的临时 root 隔离单元，保持私有网络、私有临时目录、只读系统和正式数据不可访问，能力限制为 `CAP_CHOWN CAP_SETUID CAP_SETGID CAP_DAC_OVERRIDE` 并显式设为环境能力。设置 `SCERT_TEST_ROOT_IPC=1`，以 `-test.run=^TestRootPeerIdentity$ -test.count=3` 验证实际 UID 65534 的授权请求、UID 65533 的拒绝及非 root 服务身份伪装，全部通过，运行 310 毫秒、峰值内存 32.6 MiB。没有向正式 Scert 服务增加能力。

最终留档单元 `scert-nginx-ipc-final-20260918-0244.service` 使用相同最终测试程序再运行一轮，31 项顶层测试通过，仅 root 身份用例按设计跳过；该用例已在上面的独立单元通过。最终单元运行 11.094 秒、峰值内存 20.3 MiB，详细日志保存在 systemd journal，可使用 `journalctl -u scert-nginx-ipc-final-20260918-0244.service` 复核。

2026-09-18 02:42 结束检查：正式 Scert PID `589832`、Nginx PID `578671`、MySQL PID `589785` 均未变化；正式程序 SHA-256 仍为 `f5b08bf0fb88f7b19281e91f942aa2995866f96420e422c015890b841bae6366`，Nginx 主配置及 Scert 站点摘要与本轮开始一致。最终测试与身份测试单元均已退出，PID 为零。`nginx -t` 通过，使用管理域名严格验证回环 TLS 后 `/readyz` 返回 200 和 `ready`。本轮不包含浏览器或新增公网验收。

### 失败与修复记录

初始测试单元因误写不存在的 `/scert/logs` 在执行测试前出现 `226/NAMESPACE`；核实正式日志目录为 `/scert/log` 后修正隔离参数。旧测试程序 `nginx-ipc-20260918-0230.test` 重复运行暴露应答丢失用例的抢锁竞争，以及真实 Nginx 单次探测成功后仍遇到旧工作进程。前者改为先确认暂存已获得锁再模拟应答丢失；后者加强连续探测并增加回归测试，而非简单增加测试超时。旧程序和失败记录均保留。

不同 UID 单元最初只设置能力边界，子进程在执行前报 `operation not permitted`；独立诊断确认有效能力中缺少 `CAP_SETUID`，`setpriv` 同样失败。显式设置所需环境能力后，同一测试程序通过，不将首次未进入校验逻辑的失败计为安全验证。

### 仍需完成

- [ ] 安装 socket/service、专用版本目录和目标站点 include；当前正式站点仍直接引用原证书文件。
- [ ] 应用持久化双层协调与启动恢复、完整生命周期串行化、应答丢失及中断恢复；目前不能自动处理任意应用崩溃窗口。
- [ ] 最新完整候选构建部署、管理证书续期闭环、目标机真实推送、通知送达、完整灾难恢复和容量验收。

## 前序进展：Nginx 证书激活事务组件实机验证

2026-09-18 01:45 至 02:03 本轮接续，修改前检查点 `10645ea` 已推送。本节为最新状态；本轮没有替换正式程序、重载正式 Nginx 或清空正式数据库，组件验证通过不代表完整生产验收完成。

### 已完成的修改与复审

- [✓] 探测仅允许规范化回环 IP、端口和具体管理域名；每次建立新 TLS 连接，严格验证可信链、域名和预期叶证书，并检查受限大小的 Scert 就绪响应。不使用环境代理，不跟随重定向。
- [✓] 激活器专用目录及父路径检查所有者、权限和符号链接；专用目录要求当前激活器用户所有且权限为 0700。仅例外允许 root 所有、带粘滞位的共享父目录，不允许其他可写父目录。
- [✓] 状态读取使用禁止跟随符号链接的文件打开方式，检查普通文件、文件身份、大小、所有者、写权限及硬链接数。新证书、私钥和事务快照以 0600 写入并同步。
- [✓] 切换前验证代理、上游和专用目录内旧材料一致；候选证书必须符合两侧信任配置、覆盖管理域名、处于有效期内并延长实际有效期。
- [✓] 专用 include 仅接受固定格式和随机版本目录，事务记录验证版本编号、引用和证书。切换、配置检查和就绪探测后重新核对 include，发现外部修改则拒绝覆盖或报告成功。
- [✓] 暂存只切 Nginx；上游切换后双层证书一致才允许提交。取消、检查失败、重载失败和探测失败进行有界回退；未完成事务可恢复，已提交事务不自动回退。日志写入失败不会被误报为成功。

### 验证证据

本地 `go test -p=2 ./...` 通过，未变更包使用 Go 缓存；`go test -race ./internal/nginxactivation -count=1` 通过，Windows 只覆盖通用探测代码。使用 Linux 编译目标执行 `go vet ./internal/nginxactivation` 通过。Linux 测试覆盖目录、文件、事务和受限命令实现。

最终测试程序为 `.runtime/nginx-activation-20260918-0202.test`，远端 `/scert/acceptance-20260917-1044/nginx-activation-20260918-0202.test`；两端 SHA-256 一致：`26db07a3ed15f4324af4e742cd252e0e8700253b0543bee3757a13cc5845f796`。此前两版测试程序保留，没有覆盖上传。

最终单元 `scert-nginx-final-20260918-0202.service` 以 Scert 用户运行，私有网络、私有临时目录、只读系统、隐藏用户目录、禁止提权；正式数据、配置、日志、备份及证书目录禁止访问。限制内存 192 MiB、禁用 Swap、CPU 50%、64 个任务和 120 秒运行上限。通过 `SCERT_TEST_NGINX_BINARY=/usr/sbin/nginx` 显式启用真实 Nginx 用例，执行 `-test.v -test.timeout=90s -test.count=3`：16 项顶层测试连续三轮通过，无失败、无跳过；运行 4.492 秒，CPU 1.102 秒，峰值内存 13 MiB，Swap 为零。

真实 Nginx 用例使用独立配置、PID、临时目录与随机回环端口，启动独立非特权进程；验证暂存重载、上游切换后提交、下一轮未提交事务恢复及实际入口证书。退出时等待独立 Nginx 正常终止，不操作正式 Nginx 服务。测试还覆盖跨进程锁互斥及释放后重新获取、候选错误、双层不一致、日志失败、模拟重启阶段、外部 include 或材料变更、符号链接、硬链接、FIFO 和可写父目录。

最初单元 `scert-nginx-unit-20260918-0155.service` 在测试开始前因 `/ssl` 不存在而出现 systemd `226/NAMESPACE`；调整为 `InaccessiblePaths` 中 `-/ssl` 后，缺失路径可忽略，而现有正式目录仍强制隔离。后续单元 `scert-nginx-unit-20260918-0156.service`、`scert-nginx-real-20260918-0159.service` 及最终单元均退出零。

结束时正式 Scert PID `589832`、Nginx `578671`、MySQL `589785` 与本轮起点一致。正式二进制摘要仍为 `f5b08bf0fb88f7b19281e91f942aa2995866f96420e422c015890b841bae6366`；Nginx 主配置和 Scert 站点摘要未变。`nginx -t` 通过，按管理域名严格 TLS 验证的回环 `/readyz` 返回 200 和 `ready`。最终隔离单元为 inactive、PID 为零；本轮未进行浏览器或独立公网探测验收。

### 接线前必须完成

- [ ] 独立受限辅助进程、受保护本地 IPC、请求身份鉴别及固定本地策略；不能将 Nginx 重载权限赋予 Scert 主进程，也不能通过 HTTP 接收任意命令或配置路径。
- [ ] 安装时将当前证书复制到激活器专用版本目录并生成 include；现有线上配置仍直接引用原证书目录，不满足组件专用目录契约，不能直接切换。
- [ ] 应用协调者持久化双层激活状态，处理上游已持久化但代理未提交、提交应答丢失、调用者退出及超时恢复；组件 Recover 只回退 Nginx，不替代上游配置恢复。
- [ ] 确认上述接线后再解除代理模式申请前保护，部署最新候选，继续真实目标机推送、通知送达、灾难恢复、容量及完整生产验收。

## 当前进展：Linux 测试隔离与跨平台路径复验

2026-09-18 本轮修改前检查点 `5a96623` 已推送。下方旧阶段的“当前进展”和待办按各自记录时间理解，以本节为最新状态；本轮没有替换正式程序，也不表示完整生产验收通过。

### 问题与修复

- [✓] 定位上一阶段管理任务测试的只读数据库错误：Linux `config.Default` 固定使用正式安装路径，HTTP 测试辅助函数仅传入临时目录并不能隔离数据库。现显式设置临时安装、配置、数据、日志、证书、备份目录及 SQLite DSN，不改变生产默认路径。
- [✓] 新增路径边界与数据库隔离测试，使用 SQLite `PRAGMA database_list` 核对实际打开的文件，并验证两个新数据库均为空且可分别写入相同任务 ID。
- [✓] 扩大全量测试后发现设置接口的保存失败用例以 Windows 反斜杠拼接路径，Linux 上未真正制造目录阻塞。该文件的路径拼接已改为 `filepath.Join`；重新验证了保存失败时凭据回退与令牌不消费。
- [✓] 已提交代码 `c8c7121` 包含独立管理证书续期任务、调度接入、审计及通知；当前仍只接通直连激活，代理模式会在申请前拒绝，不能标记 Nginx 自动续期完成。

### 本地验证

```sh
go test -p=2 ./...
go test -race -p=2 ./internal/httpapi -run 'TestReadyTestConfig|TestTaskAPI|TestManagement|TestTLSCertificate' -count=1
go test -race -p=2 ./internal/httpapi -count=1
go vet -p=2 ./internal/httpapi
```

以上均通过。后端全量测试在隔离辅助函数修改后执行；随后设置测试路径修改由 HTTP 全量竞态测试覆盖，耗时 63.647 秒。格式检查无待格式化文件。

### Linux 实机验证

测试以 `Scert` 用户运行，不清空或复用正式数据库。开启私有临时目录、私有网络、只读系统、隐藏用户目录和禁止提权；额外将 `/scert/data`、`/scert/conf`、`/scert/log`、`/scert/backups`、`/ssl`、`/ssl-scert-acceptance` 设置为不可访问，防止测试再次读取正式数据。测试只访问独立临时数据和本机隔离网络，不调用真实 DNS 或 ACME。

- 定向复验单元 `scert-http-isolation-20260918-0126.service` 成功退出，覆盖原失败管理任务、任务 API、管理激活、TLS 与新隔离用例。
- 首次全量单元 `scert-http-full-20260918-0128.service` 暴露上述 Windows 路径拼接问题，退出 1，原日志与测试程序保留。
- 修复后的全量单元 `scert-http-full-20260918-0132.service` 退出 0：169 项顶层测试通过、失败零、跳过零。运行 12.976 秒，CPU 时间 6.416 秒，峰值内存 24.1 MiB，Swap 为零；限制为内存 192 MiB、CPU 50%、64 个任务、180 秒运行上限。

最终测试程序为 `.runtime/http-isolation-20260918-0130.test`，远端路径为 `/scert/acceptance-20260917-1044/http-isolation-20260918-0130.test`。两端 SHA-256 一致：`2af3d17ba5bca2be1dd2052ddab8e5a836777caee1569a471d398f8cec8c1a5e`。使用新文件名上传，没有覆盖原失败证据。

结束时测试单元为 `inactive`、PID 为零。正式 Scert PID `588768`、Nginx `578671`、MySQL `588722` 与本轮检查起点一致；Scert 二进制摘要仍为 `f5b08bf0fb88f7b19281e91f942aa2995866f96420e422c015890b841bae6366`。这些 PID 与更早阶段不同，不能沿用旧记录声称跨阶段始终未重启。本轮没有执行正式服务重启。

`nginx -t` 通过，回环地址按管理域名严格验证 TLS 后，`/readyz` 返回 200、`status=ready`。Nginx 主配置与 Smail 站点摘要未变。入口仍为 `web.apmmk.cn` 的 80 跳转 443，443 反代本地 HTTPS 9004；本轮没有浏览器验收。

### 尚未完成的验收

- [✓] 本轮 HTTP 测试隔离和跨平台路径复审通过，不代表所有包的 Linux 全量测试已通过。
- [ ] 受限 Nginx 管理证书激活器、双层 TLS 就绪确认及失败回退。
- [ ] 包含管理存储隔离与续期调度的最新候选部署，以及真实目标机证书推送、通知送达、灾难恢复、容量和最终生产验收。

## 当前进展：管理证书存储隔离

2026-09-18 接续优化，修改前检查点 `6d7972f`、代码提交 `ec432f0` 均已推送。已修复同名业务证书更新可能清理管理入口引用目录的问题；正式程序尚未替换，本节不表示整体验收完成。

### 已验证的存储边界

- [✓] 新申请的管理域名证书存入 `<证书根目录>/management-domain/<域名>/<类型>/<版本>`，管理备份也使用独立的 `management-domain` 子目录；域名来自配置，不写死测试域名。
- [✓] 单域名、泛域名和 SAN 的管理申请与同名业务签发互不覆盖；业务当前证书读取不会选中管理候选。
- [✓] 兼容既有配置：通过服务启动配置中的管理证书或私钥路径保护旧业务目录中的引用，不迁移、不删除已部署的管理证书。
- [✓] 引用保护识别符号链接，不把相同字符串前缀的相邻目录误认为子目录；引用无法解析时，在备份或候选写入之前返回错误。
- [✓] 既有管理引用经过同名业务签发和导入仍保持全部文件字节不变；无管理引用的业务旧版本继续按原单当前版本规则清理。
- [✓] 无效管理存储根目录在凭据读取和外部调用之前被拒绝；阿里云保护域名检查仍优先执行。

### 验证证据

Windows 工作区执行以下命令全部通过：

```sh
go test -p=2 ./...
go test -race ./internal/certificate -run 'TestManagement|TestFileStore|TestServiceIssuesSetup|TestAliyun' -count=1
go vet -p=2 ./internal/certificate ./internal/httpapi ./internal/app ./internal/bootstrap
git diff --cached --check
```

从提交 `ec432f0` 对应源码交叉编译 Linux 测试程序，本地 `.runtime/certificate-isolation-ec432f0.test` 与远端 `/scert/acceptance-20260917-1044/certificate-isolation-ec432f0.test` 的 SHA-256 均为 `e4828059a3deff60d86e4b39c1bc0f310785e395c185cf5c8b9ebf373f9465c8`。

在真实 Linux 上，以 `Scert` 用户运行独立单元 `scert-isolation-ec432f0.service`：内存上限 128 MiB、禁用 Swap、CPU 50%、最多 48 个任务，开启私有临时目录、隔离网络、只读系统、禁止提权及隐藏用户目录。测试匹配上述定向表达式，包含 Linux 符号链接用例，全部通过且无跳过；单元退出零，运行 1.861 秒，峰值内存 7.8 MiB、Swap 为零。日志可通过 `journalctl -u scert-isolation-ec432f0.service` 查询。所有测试使用新建临时证书与目录，不访问正式数据库、真实 DNS 或 ACME。

结束时隔离单元 `inactive`、PID 为零；正式 Scert PID `579364`、Nginx `578671`、MySQL `549584` 均未变。通过本机回环地址访问 Nginx 的管理域名、严格验证 TLS 后，`/readyz` 返回 200 和 `status=ready`，`nginx -t` 通过。此项是部署机回环复核，不替代上一轮独立 Linux 公网探测，也不代表新程序已正式部署。

### 剩余验收项

- [x] 管理证书存储隔离及旧引用保护已完成本地与 Linux 组件复审，等待包含该提交的正式候选部署验证。
- [ ] 将管理证书纳入续期调度，补齐持久化任务、审计及通知；当前调度仍只处理业务域名。
- [ ] 在不扩大 Scert 服务权限的前提下接通 Nginx 证书更新，完成双层 TLS 激活、就绪等待和失败回退。
- [ ] 最新候选构建与 Linux 部署、真实目标机推送、通知送达、完整灾难恢复及容量验收。

## 前序进展：管理证书热更新

2026-09-18 接续优化，代码提交 `9074d83` 已推送；修改前检查点为 `230cbaa`。本节是续期基础能力的本地和 Linux 隔离验证，不是线上自动续期已完成。

### 已通过的组件检查

- [✓] HTTPS 服务使用不可变证书快照，新握手可加载更新后的证书，不需要重启 HTTP 服务。
- [✓] 新证书需通过私钥配对、有效期、管理地址覆盖和可信链校验；缺失文件、错误域名、过期、尚未生效及不可信链均拒绝，旧证书继续可用。
- [✓] 先持久化证书配置再切换内存；配置写入失败不修改内存证书和内存配置。
- [✓] 并发握手、无 SNI 的 IP 握手、显式回退和服务重启后读取新证书通过严格 TLS 验证。
- [✓] 管理证书申请保留旧版本目录，重复申请和初始化回退不会破坏先前引用的文件；普通业务证书仍保持原来的单当前版本规则。
- [✓] 修复新增证书解析步骤暴露的关闭顺序回归：已关闭服务再次启动优先返回关闭状态，不能重新监听。

热更新入口仅供进程内部调用，没有增加上传证书、任意路径或远程执行命令的 HTTP 接口。证书读取状态不再依赖业务域名列表，可供后续管理续期调度使用。

### 验证证据

Windows 工作区与提交内容一致，执行以下命令全部通过：

```sh
go test -p=2 ./...
go test -race ./internal/httpapi ./internal/certificate -run 'TestTLSCertificateRotation|TestManagementCertificatePreserves|TestShutdownBeforeStart' -count=1
go vet -p=2 ./internal/httpapi ./internal/certificate ./internal/app ./internal/bootstrap
git diff --cached --check
```

使用 Go 1.26.6 交叉编译两个 Linux 测试程序，上传前后 SHA-256 一致。远端文件均在 `/scert/acceptance-20260917-1044/`：

| 文件 | SHA-256 |
| --- | --- |
| `httpapi-tls-rotation-9074d83.test` | `a3859533e83f7502f711c172481ebfb5f812ef3282c14d3b547e7eaa4b2e377e` |
| `certificate-management-9074d83.test` | `bed93ab19132f762ab013d5ca58f9f4354dd4f187f86b8a25df5cb5e3b6bd1a0` |

两个独立 systemd 测试单元以 `Scert` 用户运行，内存上限 128 MiB、禁用 Swap、CPU 50%、最多 48 个任务，并开启私有临时目录、只读系统保护及禁止提权。TLS 更新与关闭生命周期测试、管理存储与业务存储回归全部退出零；峰值内存分别约 8.7 MiB 和 7.2 MiB。测试使用全新临时证书与配置，不访问真实 DNS、ACME 或业务数据库。

线上 Scert 未替换二进制、未重启，仍运行此前版本。只读复核时 Scert PID `579364`、Nginx `578671`、MySQL `549584`、8002 上的 Smail `574584` 均未变化，管理 HTTPS 就绪响应仍正常。未清空共享数据库，未删除既有补丁、构建产物或服务器文件。

### 继续验收的门槛

- [ ] 将管理证书纳入续期调度，并补齐持久化任务、审计及通知；当前仍不是自动续期闭环。
- [ ] 管理候选证书与业务存储隔离，防止同名业务证书操作清理管理入口引用的路径。
- [ ] 在不扩大 Scert 服务权限的前提下接通 Nginx 更新，完成双层 TLS 激活、就绪校验及失败回退。
- [ ] 从最新已验证源码构建候选版本，再进行 Linux 部署、真实目标机推送、通知送达、完整回退与容量验收。

## 已通过：标准域名入口

2026-09-18 00:01 起，按用户确认改为复用已开放的 80/443，通过 Nginx 域名站点转发。以下结果覆盖旧 8003/8004 方案的公网入口阻塞，不改变管理证书续期等尚未完成事项的结论。

- [✓] `http://web.apmmk.cn` 返回 308，跳转到 `https://web.apmmk.cn`，保留路径及查询参数。
- [✓] `https://web.apmmk.cn` 转发到 `https://127.0.0.1:9004`，入口和上游均严格验证证书。
- [✓] 应用 `server.proxy_https_port` 已改为 443；未改变上游监听端口、域名或数据库。
- [✓] 独立 Linux 主机 `8.137.23.95` 通过正常公网 DNS 访问 `/readyz` 返回 200、`status=ready`，HTTP 跳转同样通过。未使用指定解析、隧道或跳过证书验证。
- [✓] 新域名地址的桌面浏览器登录、刷新、退出及 401 检查通过。此项使用独立 SSH 隧道和严格 TLS，证据目录 `.runtime/apmmk-browser-20260918-standard/`，与上一项公网协议测试分开记录。
- [✓] 原 8003/8004 已无监听；9004 仍仅回环监听。此前 UFW 放行规则暂保留，本轮没有删除规则或文件。
- [✓] 共享 Nginx 主配置和 Smail 站点内容未变；Smail HTTPS 返回 200、TLS 验证结果为零。

备份位于 `/var/backups/scert-standard-20260918-0005-v2`，含受限配置备份及原站点。只修改 Scert 站点，不新增重复默认站点。首次切换时立即探测收到 SNI 拒绝，脚本自动恢复旧配置；随后加入平滑重载后的有界就绪等待，第二次切换成功。Scert 为加载对外端口配置经过重启，旧会话需重新登录；未重启同机 Smail、MySQL 或 Nginx 主进程。

完成时 Scert PID `579364`、重启计数零；Nginx 主进程 `578671`、Smail `574584`、MySQL `549584` 均与本轮开始一致。Nginx 配置语法检查通过，1440/1024 桌面测试无横向溢出、损坏图片或页面脚本错误。

Windows 本机直接访问仍出现 HTTPS 握手错误及 HTTP `Server: Beaver` 403，与独立 Linux 公网测试结果不同；该本地网络路径尚未定位，不宣称所有客户端网络均验证通过。本轮没有更改本机代理设置。

部署模板已改为共用端口的命名虚拟主机，外部完整地址使用 `__MANAGEMENT_ORIGIN__` 占位符，默认测试值不再包含 8004。管理证书自动续期与双层 TLS 更新、真实目标机推送及完整故障回退仍需后续完成，本次仅完成用户要求的标准入口调整。

## 历史结论：管理入口接续验收

以下为 2026-09-17 23:16 至 23:35 的接续结果，覆盖后文早期阶段关于域名未确认、初始化未完成和入口未部署的旧状态。不能将各阶段的通过项混为完整生产验收。

### 需求与任务状态

已确认管理域名可配置，本次使用 `web.apmmk.cn`，Nginx HTTP 8003 跳转 HTTPS 8004，上游为 `https://127.0.0.1:9004`。实际仓库为 `D:\Desktop\Scert`，不使用已不存在的旧目录。

- [✓] 复核真实初始化与正式证书，上游严格 TLS 校验下就绪。
- [✓] 独立 Nginx 站点部署、主机防火墙放行、未知域名拒绝、HTTP 跳转及双层 TLS 校验。
- [✓] 隧道访问线上站点的桌面浏览器登录、刷新会话、退出及接口鉴权测试。
- [✓] 六组跨域写入拒绝、退出后旧 Cookie 重放拒绝。
- [✓] Scert 开机自启已启用；未重启共享主机，不能视为重启演练通过。
- [x] 通用反代配置样例和部署说明已补充，等待整体生产复审。
- [!] 公网 8003/8004 在独立 Linux 主机访问超时，需核对云安全组或上游网络。
- [!] 管理证书未进入续期队列，且尚无上游 TLS 与 Nginx 的证书更新生效机制。
- [ ] 真实目标机证书推送、完整故障回退、通知送达与容量验收仍需完成。

### 已核对的运行版本

正式程序来自已推送提交 `b6d1b96426437df412b508e2bfff06b1dde02871`。其阿里云修复使用限定域名及精确 TXT 记录查询，以兼容当前 RAM 权限，同时对 DNS 主机名进行不区分大小写比较，记录 ID 和 TXT 内容仍严格匹配。本阶段没有再次替换正式二进制。

23:30 重新计算正式程序 `/scert/scert` 的 SHA-256 为 `f5b08bf0fb88f7b19281e91f942aa2995866f96420e422c015890b841bae6366`；升级前快照 `/var/backups/scert-pre-dns-b6d1b96/snapshot.tar.gz` 的摘要为 `5dae6d9444768243a9440e69dfaf41870cbbc2a85702d2d8923579763ca77715`。

当前管理证书为 Let's Encrypt YR2 签发，主题 `web.apmmk.cn`，有效期截止 `2026-12-16 14:14:04 UTC`。上游及 Nginx 探测均使用公共 CA 和主机名验证，没有使用跳过证书验证选项。

本阶段延续已完成初始化的独立测试数据集，没有清空数据库或重跑初始化。管理员密码仅保留在管理机 root 可读的受限凭据文件中，不进入代码、报告和截图。

### 反向代理部署

新增 `/etc/nginx/conf.d/scert-apmmk.conf`，保留 `/etc/nginx/nginx.conf` 与 `smail.conf`，部署前后内容摘要一致。配置备份位于 `/var/backups/scert-nginx-20260917-2320-v2`。第一次配置因本机参数传递导致空字符串引号损坏，被 `nginx -t` 拦截并移出有效加载目录，未重载；修正后语法检查成功才平滑重载。

8003 对已配置域名返回 308，示例路径 `/login?acceptance=1` 正确跳转至 `https://web.apmmk.cn:8004/login?acceptance=1`；未知域名返回 404。8004 的未知 SNI 拒绝握手。已配置域名通过 `proxy_ssl_verify on` 和 `proxy_ssl_name web.apmmk.cn` 验证 9004 上游，保留 `Host $http_host` 中的外部端口。

UFW 新增 TCP 8003、8004 的 IPv4/IPv6 入站规则；9004 仍只监听回环地址。`Scert.service` 此前为 disabled，现已执行 enable；Nginx 原已启用。没有重启 Scert、Smail、MySQL 或主机。

23:30 状态：Scert PID `576928`、重启零；Nginx 主进程 PID `575746`；Smail PID `574584`、重启零；MySQL PID `549584`、重启一。应用进程均与本阶段开始时一致。

### 浏览器与安全测试

本轮没有可调用的浏览器 MCP，使用独立 Playwright Chromium 上下文作为替代。新建上下文并禁止 Service Worker，不复用旧缓存。通过本机回环 SSH 隧道连接线上 Nginx 8004，浏览器 URL、TLS SNI 和证书校验仍使用真实管理域名；没有忽略 HTTPS 错误。这是线上应用测试，不是公网入口测试。

通过真实表单登录、页面跳转、刷新后会话保持、设置和用户等只读接口访问、退出、退出后 401 检查。Cookie 为 `Secure`、`HttpOnly`、`SameSite=Lax`、路径 `/`。1440 和 1024 桌面宽度没有横向溢出、损坏图片或页面脚本错误；截图仅代表这些桌面尺寸，不代表移动端验收或完整视觉复审。

本地证据目录 `.runtime/apmmk-browser-20260917-2330/` 含 `result.json`、`login-1440.png`、`dashboard-1440.png`、`dashboard-1024.png`，保持 Git 忽略。脚本不把管理员密码或 Cookie 写入结果。

远端协议脚本 `apmmk_security_acceptance.py` 使用严格 TLS 测试三个错误 Origin：异域、缺失外部端口、错误协议。对设置写入及退出各测试一次，共六次均为 403，且原会话仍有效；正常退出后重放原 Cookie 为 401。

100 次就绪请求、并发四，全部返回正常；中位约 28.57 毫秒、P95 约 38.91 毫秒。测试前主机可用内存约 779 MiB、Swap 使用约 155 MiB。这仅是短时只读探测，不替代签发、部署、数据库写入或持续负载的容量测试。

### 当前阻塞与续期风险

Windows 本机 DNS 返回代理软件虚拟地址 `198.18.0.51`，默认访问失败，不能据此单独归因服务器。另从目标机 `8.137.23.95` 只读访问管理机公网，两个端口均连接超时；指定真实 IP 的 HTTPS 再测也超时。同期管理机监听与 UFW 正常，短时抓包未捕获该外部连接。当前证据提示云安全组或其他上游网络限制，尚未读取云安全组规则，不能断言已定位唯一原因。本次在目标机只执行网络探测，没有部署、清库或重启操作。

续期 API 实际返回 `checked=0`、`selected=0`；域名列表为空。源码核对表明 `IssueForSetup` 只保存管理证书，而当前调度器遍历托管域名仓库；HTTP 服务调用静态 `ListenAndServeTLS`，Nginx 也引用固定版本路径。因此证书有效不等于管理证书能够自动续期。必须补齐续期调度、证书事务切换、双层 TLS 更新、失败保留旧证书和恢复验证后再解除该门槛。

## 历史阶段记录

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

### 发布与收尾

- [✓] 修复提交 `6b6fc126868731003e70e1225f6ae455f26fb235` 已推送，远程 main 已核对一致；本地 `go test -race -count=1 ./internal/admin` 通过。
- [✓] 本地与远程实测安装器 SHA-256 一致：`08b7b04411b9250414d0429b10e99a4642c750163febf3da65281b445eb7fa57`。
- [!] 干净克隆位于 `C:/Users/ikun/AppData/Local/Temp/scert-state-20260917-1532`。调用仓库发布脚本时被 PowerShell 执行策略拒绝，未绕过策略，未生成或上传本次新发布包。此前 `8c3d878` 发布包不包含本次停服检查修复，不能用于宣称最新版本验收。
- [✓] 15:32 收尾核查：测试 unit 为 inactive、PID 零、结果 success；正式 Scert PID `530794`、重启次数零，MySQL PID `549584`、重启次数一，正式程序摘要未变。可用内存约 320 MiB，19017 无监听，SSH 随后关闭。
- [!] 正式初始化、维护窗口内停服升级、业务回退、DNS 与 ACME、目标机业务推送及最终入口仍未验收。继续需要发布脚本按本机管理策略获准执行，并确认维护窗口、域名及 DNS 操作范围、与既有 Smail 不冲突的入口方案。

## 初始化探针修复与正式升级复验（2026-09-17 晚间）

### 需求与结论

- [✓] 接续升级后的在用服务，核对 HTTPS 身份、健康与就绪状态。
- [✓] 修复首次初始化阶段健康探针缺失，保持未初始化就绪门禁。
- [✓] 干净提交构建、发布包校验、Linux 隔离协议及正式停服升级。
- [✓] 升级前后受保护文件一致性、完整快照及独立目录解包验证。
- [!] 正式初始化、公共信任证书、外部入口、业务推送与真实故障回退仍未通过，不能正式切流。

### 缺陷与本地验证

接手时正式进程 PID 为 `570938`，监听 `127.0.0.1:9004`，处于新的 Bootstrap HTTPS 模式。通过当前 systemd 启动日志取得指纹，核对网络证书后，再使用该证书作为专用信任根检查证书名称和有效期。页面与状态接口返回 200，设置接口返回 401，但 `/healthz` 和 `/readyz` 均为 404。旧记录中的 HTTP 503 不适用于这个新启动链路。

修改前检查点 `48c2996` 已推送；首次推送认证失败，重试成功。修复提交 `c1ac3e91c07447243ead56fb0d8eca0b6ca8a938` 已推送，仅修改 Bootstrap 处理器及测试。新增回归先复现两个 404，再验证健康返回 200、就绪返回 503 和 `setup_incomplete`，拒绝错误方法，不缓存、不创建会话，也不提前打开业务数据库。

以下本地验证均通过：

```text
go test -p 1 -count=1 -timeout=3m ./...
go test -count=1 -timeout=3m ./internal/bootstrap ./internal/httpapi
go test -race -count=1 -timeout=3m ./internal/bootstrap
go vet -p=1 ./internal/bootstrap ./internal/httpapi
npm test
git diff --check
```

前端共 47 项通过。本轮没有修改前端，也没有重新进行浏览器渲染验收；没有执行新的全量竞态检查或漏洞扫描。

### 发布与隔离实机

从独立干净克隆构建，发布清单为 `source_dirty=false`，工具链 Go 1.26.6。首次因克隆缺少前端依赖而构建失败，保留失败目录；执行锁定依赖安装后改用全新输出目录构建成功。

- 本地归档：`build/linux-health-20260917-2205/Scert-20260917-V0.1.0-linux-amd64.tar.gz`。
- 归档 SHA-256：`b44f5b2a1125af08fd15356477f25ea4e17f8cf3a29d4f07ccbe8d63467d76b0`。
- 主程序 SHA-256：`b91c1a88382b29b6016488e340087cb25a205f684273c0d10d473803b10b9a41`。
- 本地清单 14 项、远端包内 12 项均核对通过。目录时间后缀是标识，不代表实际执行时间。

远端脚本及包位于 `/scert/acceptance-20260917-1044/`，最终隔离目录为 `health-c1ac3e9-v2`。候选限制内存 96 MiB、无 Swap、CPU 25%、32 任务，使用全新 SQLite 路径与私有运行目录，监听回环 19017。通过项包括 HTTPS 指纹与完整验证、新探针、构建资源、未登录拒绝、跨域拒绝、一次性令牌、安全 Cookie、SQLite 写权限、缺少 DNS 验证拒绝提交、重启证书及令牌轮换、旧会话失效、配置字节不变。

第一次协议检查通过后，附加断言错误地要求 SQLite 文件不存在。实际写权限测试会留下空数据库，现场查询无业务表且完整性正常；修正测试断言后，在新目录重跑全部通过，退出码零。没有为通过测试而修改业务实现，也没有删除第一轮产物。

同一源码交叉编译的 Bootstrap 测试程序在 Linux 执行，探针、路由隔离和取消清理通过。另执行 `TestHTTPSBootstrapCompletesSQLiteAndStartsApplication` 的两个场景，严格 TLS、SQLite 初始化、正式服务切换、登录、用户接口及夹具 `/readyz=200` 全部通过。两组受限测试峰值内存分别约 52.4 MiB 和 53.4 MiB。该集成夹具不执行真实云端 DNS 或 ACME，不能替代在用服务初始化。

### 备份与正式升级

21:50 左右核验此前快照 `/var/backups/scert-pre-upgrade-20260917-211804/snapshot.tar.gz`，SHA-256 为 `a9ba56f7e7e9dd3f6ac2ae2b13d11f00a3ae5ff7a749493aa09d52f99a198288`。配置、凭据、密钥和 SQLite 与快照逐项一致，业务数据库只读完整性检查为 `ok`。之后解包至全新 `0700` 目录 `/var/backups/scert-restore-check-c1ac3e9`，29 个文件与归档逐项一致，恢复副本 SQLite 完整性正常。未启动恢复副本，不把文件恢复验证称为完整业务回退。

约 22:00 正式更新到 `c1ac3e9` 前，再次停止 Scert 并创建完整快照 `/var/backups/scert-pre-health-c1ac3e9/snapshot.tar.gz`，逐文件核对通过，SHA-256 为 `7d6c39afbd43cb22ae95a98f94b5a2a4042aa86057b7ce7623698569c1e1bac9`。快照目录权限 0700，文件按 0600 创建。安装器退出码零，主程序摘要与新包一致，随后启动正式服务。

升级脚本检查以下结果全部通过：

- 正式 Scert 为 active/running，PID `572525`，重启次数零。
- 当前进程证书与可信 SSH 日志指纹一致，随后完整 TLS 验证下 `/healthz=200`、`/readyz=503 setup_incomplete`。
- Bootstrap 状态与页面 200，未登录设置 401，业务用户接口 404。
- 配置、数据库、密钥及证书文件的内容、权限、UID/GID 升级前后完全一致。
- MySQL PID `549584`、重启次数一，与升级前一致；443 的 Smail PID `570415` 不变。
- 所有本轮候选测试已停止，没有清空正式数据库、修改 DNS 或操作推送目标机。

### 剩余生产门槛

本轮正常 TLS 连接正式 ACME 目录已返回 HTTP 200，证书验证结果为零，旧的出站超时暂未复现；这仅证明当时目录连接成功，不代表签发成功或网络长期稳定。

在用配置仍是 `setup_ready=false`，后台域名仍为 `scert-accept-20260914.epwn.uno`，未对其发起签发。下一步需要用户明确管理域名、对应 DNS 写入和精确清理授权，以及不影响 Smail 443 的入口与维护范围，才能继续正式初始化、生产签发、外部 HTTPS/SNI、浏览器登录及真实业务推送。真实故障回退仍未完成，机器可用内存约 300 MiB，容量风险也未解除。

22:17 最终只读复核：Scert PID 仍为 `572525`、重启次数零，MySQL 状态不变；四个本轮测试单元均 inactive、PID 零，19017 无监听。备份摘要及 0700/0600 权限再次通过。此时 Smail 的 443 进程已经变为 PID `571735`，系统有 4095 MiB Swap、可用内存约 634 MiB，与升级时状态不同；本轮未操作 Smail 或 Swap，变更来源尚未核查，不能把升级时的“保持不变”延伸为整个时段。容量风险需依据新环境另做负载验证。

## 双 Linux 正式候选健康复核（2026-09-30 21:12）

本轮使用密码 SSH 进行只读检查，未创建辅助验收脚本，未清理或修改远端业务数据库，未操作 Smail。检查目标为正式候选安装后的服务恢复状态，不替代完整生产业务验收。

| 检查项 | `8.137.23.95`（`ikun1`） | `45.136.13.241`（`yc`） |
|---|---|---|
| `Scert` systemd 状态 | `active` | `active` |
| `nginx` systemd 状态 | `active` | `active` |
| `Scert-storage.socket` 状态 | `active` | `active` |
| `https://127.0.0.1:8443/readyz` | HTTP `200` | HTTP `200` |
| Nginx 监听 | `80/443` | `80/443` |
| Scert 内部 HTTPS 监听 | `*:8443` | `127.0.0.1:8443` |

两台服务器均未发现候选安装覆盖 Nginx `80/443` 监听端口。第二台的 `8443` 仅绑定回环地址仍可由本机 Nginx 代理使用，但证书推送和业务流量尚未通过管理页面完成真实验证，不能据此记录多服务器部署验收通过。

### 管理域名只读检查

- `https://odacm.cn/` 返回 HTTP `200`，能够读取 Scert Web 首页。
- 当前本机 DNS 将 `odacm.cn` 解析为 `198.18.0.109`，不是本轮两台测试服务器地址。
- 因此本轮不把首页 200 记录为公网 DNS、HTTPS/SNI、ACME 或生产入口验收通过；仍需确认真实 DNS 目标、入口归属和证书链路。

### 当前结论

- [x] 双 Linux 正式候选服务安装后健康状态复核通过。
- [~] 一域名多服务器、单服务器多域名、证书推送、Nginx reload、失败回滚、恢复接管和重试幂等仍在复核中。
- [ ] 三数据库生产矩阵、Windows 服务、清库首次初始化、备份恢复、公网 DNS/ACME 和最终生产门禁尚未完成。
- [!] 本轮不新增 `[✓]`，不以 `/readyz=200` 或首页 200 替代完整生产部署验收。

## 代码整改复核记录（2026-09-30）

- [x] 证书部署工作流已改为按目标服务器逐台执行 HTTPS 基线、部署后证书指纹和恢复校验。
- [x] 生产应用已接入目标服务器地址拨号，域名仍作为 TLS SNI 和证书校验名称。
- [x] 已移除远端 HTTPS 校验的跳过证书验证配置，改用受信任根证书进行严格 TLS 校验。
- [x] `go test ./...`、`go vet ./...`、`git diff --check` 通过；新增指定地址与域名 SNI 回归测试。
- [~] 尚未重新部署本轮最新候选包；因此本节代码结果不能替代两台服务器的实机业务验收。
- [!] 第一台服务器仍需确认并通过真实 Nginx 站点配置、证书加载和独立 HTTPS 指纹校验；完整生产门禁继续未通过。

## 2026-10-01 部署锁获取失败事务终态修复记录

### 本轮结果

- 修复 `deployOne` 在创建部署事务后、锁获取阶段提前返回而未写入最终状态的问题。
- 锁获取失败、锁归属写入失败、锁阶段持久化失败现在都会执行锁清理并写入 `rolled_back`；若锁释放无法证明或发生释放失败，则写入 `recovery_required` 并保留人工核查现场。
- 新增两类真实代码回归测试：无锁持有时的锁冲突、部分锁持有时的锁冲突。
- `go test ./...`：通过。
- 代码提交：`1fad8db`；测试提交：`d315179`。

### 当前限制

- 本轮尚未使用最新提交重新构建并部署两台 Linux 服务器，因此本轮本地测试不能替代实机验收。
- `8.137.23.95` 的 `recovery_required` 现场仍需重启候选服务后复核；`45.136.13.241` 的历史空锁目录只有在再次确认无 owner、无活动进程且属于历史测试现场后才允许清理。
- 双服务器真实业务部署、失败恢复、重试幂等、三数据库、Windows、公网 DNS/ACME、SMTP、备份恢复和最终生产门禁仍未完成。

### 下一步

1. 构建包含本轮修复的最新 Linux amd64 候选包。
2. 使用密码 SSH 部署到 `8.137.23.95` 和 `45.136.13.241`。
3. 重启服务并复核恢复事务、锁 owner、Nginx 校验、reload 和 HTTPS。
4. 通过正式页面重新执行一域名多服务器、多域名单服务器部署验证。
5. 更新实机证据后再决定是否推进总体验收位置。

本轮未创建辅助验收脚本，未以辅助脚本作为验收条件。

## 最新实机复核（2026-10-01，最新候选包双服务器业务部署）

本轮使用密码 SSH 和正式管理接口复核，未创建辅助验收脚本，未修改远端数据库内容，未关闭 TLS 校验。Windows 环境按当前上线范围暂不处理。

- [x] 最新候选包已安装到 `8.137.23.95` 和 `45.136.13.241`；两台服务器的 `Scert`、`Scert-storage.socket`、Nginx 均为 `active`。
- [x] 两台服务器的 `https://127.0.0.1:8443/readyz` 均返回 HTTP `200`。
- [x] 已验证域名 `scert-multi-20260930.apmmk.cn` 同时绑定两台服务器；服务器端反向查询也能看到该域名。
- [x] 已验证服务器 `45.136.13.241` 同时托管 `scert-20260930.apmmk.cn` 和 `scert-multi-20260930.apmmk.cn`；满足一台服务器多个域名的关系模型要求。
- [x] 使用最新候选服务重新执行证书部署，返回 `total=2`、`success=2`、`failed=0`，两台服务器均报告“证书部署、配置校验并 reload 成功”，`verification_status=success`。
- [x] 两台服务器目标证书文件均已写入 `/ssl/scert-multi-20260930.apmmk.cn/single/`，证书 SHA-256 指纹均为 `43:06:3C:07:26:D3:43:62:B2:54:CE:28:B6:6D:7E:B6:37:E0:0B:16:5F:68:EC:0D:07:FC:ED:0D:F2:C6:4A:D2` 的同一证书（记录中保留冒号格式时以 OpenSSL 输出为准）。
- [x] 两台服务器均执行 `nginx -t` 成功，并通过本机 SNI HTTPS 请求返回 HTTP `200`。
- [x] 已确认历史失败部署任务的失败结果持久化了各服务器状态和备份标识；最新候选服务启动后未再产生新的空归属 `acquiring` 事务。
- [!] 现有历史 `recovery_required` 事务仍缺少可信恢复证据，未直接改库伪造成功；相关现场保留人工核查状态。
- [~] 失败回滚的真实双服务器故障注入、任务级重试幂等、真实公网 DNS/ACME、Smail 系统账户 SMTP、三数据库矩阵和备份恢复仍需继续。

### 当前任务看板

- [x] T1 主清单、执行清单和 Linux 实机记录已同步。
- [x] T2 部署锁获取失败、空锁恢复和事务终态修复已完成并部署复核。
- [x] T3 最新 Linux amd64 候选包双服务器安装及基础健康检查完成。
- [x] T4 一域名多服务器、单服务器多域名关系和最新候选证书部署成功。
- [~] T5 真实失败回滚、重试幂等和历史恢复证据统一复审。
- [~] T6 公网 DNS/ACME、Smail SMTP、数据库生产矩阵和备份恢复。
- [ ] T7 最终生产门禁和上线结论。

总体验收位置仍为 **P0-03：完成，待复审**，本轮不得标记全量生产验收通过。

## 2026-10-01 反向代理端口收敛记录

本节补充当前部署要求，不覆盖历史记录。当前仅处理 Linux；本轮是保留数据的端口配置复核，不代表清库首次初始化验收。

- [x] 已完成，等待复审：两台服务器通过密码 SSH 核验，Scert 均仅监听 `127.0.0.1:8443`；没有 `0.0.0.0:8443` 或 `[::]:8443` 监听。
- [x] 已完成，等待复审：Nginx 对外监听 `80/443`，两台服务器的 `nginx -t` 均通过；未修改邮件及其他既有站点。
- [x] 已完成，等待复审：第二台管理入口 `odacm.cn` 的 HTTP 请求返回 301 并跳转 HTTPS，反代 `/readyz` 和回环上游 `/readyz` 均返回 200；上游启用 SNI 和证书校验。
- [x] 已完成，等待复审：从第二台服务器按正常 DNS 访问管理域名，连接地址为 `45.136.13.241`，HTTPS 就绪检查返回 200；该结果不是独立外部客户端的完整生产验收。
- [x] 已完成，等待复审：静态 systemd 模板已提交并推送 `48d10fe`；动态生成的 Linux unit 同步回环监听约束，并补充模板与生成器回归断言。
- [x] 已完成，等待复审：本地后端 `go test ./...`、`go vet ./...` 和 `git diff --check` 通过；本机 Windows 测试不代替 Linux 专属用例执行。
- [~] 进行中：使用干净提交构建 Linux 候选包，核对安装后的默认 unit 和动态生成配置，并补充 Linux 专属用例证据。
- [!] 暂缓或存在阻塞：历史恢复事务、双服务器真实失败回滚与重试幂等、Smail SMTP、数据库生产矩阵、备份恢复及最终生产门禁仍未完成。

端口边界：不新增公网业务端口，不为内部 `8443` 添加防火墙或安全组放行规则。SSH 运维端口与其他已有服务不属于本次端口收敛的修改范围。总体验收保持 **P0-03：完成，待复审**，不新增全量验收通过结论。

### 反向代理端口收敛复核结果（2026-10-01 16:35，北京时间）

- [x] 已完成，等待复审：构建提交 `51f7f3e55cee16d3b747bea30e0382fca4b25dd1`，发布清单 `source_dirty=false`；Linux 包位于 `build/release-20261001-loopback/Scert-20261001-V0.1.0-linux-amd64.tar.gz`。
- [x] 已完成，等待复审：包 SHA-256 为 `2953f52d252194fda7865e1ee06e548204fdfdb818dab28ce10611087ba94443`；两台服务器上传后校验一致，包内 `SHA256SUMS` 全部通过。
- [x] 已完成，等待复审：两台服务器均经官方安装器安装成功并启动，安装退出码均为 0；主服务、存储 socket 和 Nginx 均正常，主服务重启计数为 0。
- [x] 已完成，等待复审：两台服务器 `/scert/scert` 的 SHA-256 均为 `beeade4c38e1be7f8af29b42d08896ca4f9efcb782c3f0a278f58fd0abc9baae`，与本地候选二进制一致；已安装的主 unit 自身包含回环监听设置，不再仅依赖 drop-in。
- [x] 已完成，等待复审：在第二台 Linux 上执行仓库 `internal/servicehost` 的编译测试程序，9 项全部通过，覆盖动态 unit 和发布模板回环约束；没有创建辅助验收脚本，此结果仅作为代码回归证据。
- [x] 已完成，等待复审：本地前端 241 项测试通过，正式前端及 Linux 后端构建通过。
- [x] 已完成，等待复审：升级后两台 Scert 均监听 `127.0.0.1:8443`，Nginx 仍对外监听 `80/443`，`nginx -t` 通过；未改动已有站点或新增公网放行规则。
- [x] 已完成，等待复审：第二台回环上游按管理域名 SNI 和系统 CA 严格验证返回 200；第一台作为独立客户端按正常 DNS 访问 `https://odacm.cn/readyz` 返回 200，连接地址为 `45.136.13.241`。
- [x] 已完成，等待复审：两台服务器交叉探测对方公网 `8443` 均超时；结合实际监听地址确认该内部端口未对外监听，未据此声称其他已有服务端口全部关闭。
- [!] 边界说明：第一台内部管理服务沿用原有 IP 自签证书，使用测试域名和系统 CA 直连会失败；通过密码 SSH 核查证书及 IP SAN 后，以该证书作为显式信任文件访问回环上游返回 200，未关闭 TLS 校验。这仅是该机内部健康核验，不是第一台管理入口的公网受信 TLS 验收。
- [!] 边界说明：本轮保留数据库，没有执行清库首次流程；未额外制作数据备份，官方安装器自身保留升级前发布文件以支持失败回退。完整生产验收仍为 **P0-03：完成，待复审**。

### 部署重试阻塞修复进度（2026-10-01 16:51，北京时间）

本节为本轮当前状态说明，前文保留为历史记录。P0-03 的局部代码复审状态不代表整个项目的生产验收状态；本轮上线范围仅 Linux，不将 Windows 列为阻塞。

- [✓] 复审通过：双机服务保持回环监听 127.0.0.1:8443，由 Nginx 80/443 对外提供服务；本轮未新增公网业务端口。
- [x] 已完成，等待复审：通过正式部署接口制造第一台 Nginx 校验命令失败，两台部署报告整体失败，恢复报告为 success；随后已恢复正常校验命令，双机 nginx -t 通过。该验证使用同一证书重部署，不代表不同新旧证书切换验收。
- [x] 已完成，等待复审：定位部署重试自冲突。HTTP 重试已创建尝试并标记原任务运行，但应用装配再次调用创建任务的工作流，将自身判为同类活动任务。新增真实应用登录与路由装配回归，修复前稳定返回 502。
- [x] 已完成，等待复审：拆分普通部署与已有任务执行回调，普通部署继续创建任务，重试只执行原始证书部署流程，保留权限、审计、尝试记录与事务关联。
- [x] 已完成，等待复审：定向回归验证唯一父任务、失败后成功的两次尝试、原任务事务关联、并发重试 409、成功后重复重试 409；后端 go test ./... 与 go vet ./... 通过。
- [~] 进行中：竞态测试、干净提交构建、双机升级和真实失败任务重试复核。当前尚未取得修复后的实机成功证据。
- [!] 暂缓或存在阻塞：历史恢复事务、Smail SMTP、数据库生产矩阵、备份恢复与最终清库首次部署门禁仍未闭环，不宣称生产验收通过，不给缺乏依据的完成百分比。

现场失败任务标识：683ef23a-c912-4f46-b2a6-3ec11908808e。本轮保留其数据用于复现与修复验证，不属于清库首次流程；没有直接改库伪造状态，也没有创建辅助验收脚本。

本轮补充回归：应用装配测试同时核对双服务器结果归属于第二次尝试，以及开始和成功审计的操作者关联；缺少已有任务执行器时返回 501，不回退到创建任务入口，不改变原任务状态。后端全量测试与 vet 再次通过；app/httpapi/repository 竞态测试通过，补充用例的定向竞态测试通过；前端 241 项通过。实机复测尚待新包部署。

### 部署重试双机实测复核结果（2026-10-01 17:02，北京时间）

以下为本轮最新结果，更新前述“尚待实机复测”的状态；只关闭部署重试自冲突这一子项，不等同于完成全部生产门禁。

- [✓] 复审通过：修复提交 302111c8affd6c31c6d616eed45a2a8cee42843c 已推送，发布清单 source_dirty=false；使用正式构建流程生成 Linux 包，并通过官方安装器升级两台测试服务器，安装及启动退出码均为 0。
- [✓] 复审通过：发布包 build/release-20261001-deploy-retry/Scert-20261001-V0.1.0-linux-amd64.tar.gz，SHA-256 为 0ad94de03e625eb5fd6789e1fa84f8cad8298e2487876f55d34e36f14df7c878；双机上传和包内 SHA256SUMS 校验均通过。
- [✓] 复审通过：两台 /scert/scert 的 SHA-256 均为 393ae5a1cfaf8e4442ec30e47950f020e6ab47adb47af7fdca45a36eb9486ed2；Scert 与 Nginx active，NRestarts=0。
- [✓] 复审通过：复用真实失败任务 683ef23a-c912-4f46-b2a6-3ec11908808e，通过正式重试接口同时提交两次请求，一个返回 200、双服务器部署成功并通过 HTTPS 校验；另一个返回 409，没有执行第二次部署。成功后再重试返回 409。
- [✓] 复审通过：原任务保留，未产生嵌套父任务；尝试记录只从 3 增加到 4，第 4 次为 success，两台部署结果均关联该次尝试 b9ca3a51-30af-4d2c-9655-758509140f5e。旧失败记录保留用于追溯。
- [✓] 复审通过：只读核对数据库，首次失败的两个事务均为 rolled_back；本次两个事务 20261001-085936.117414575-5eecfbb86709、20261001-085940.320149826-09d1cf34f629 均为 committed；开始及成功审计均关联原任务和实际操作者。没有直接修改数据库状态。
- [✓] 复审通过：两台证书目录均无残留锁及 scert-tmp 文件；nginx -t 通过，证书和证书链文件哈希一致。第一台通过带 SNI 和主机名校验的 TLS 连接核对第二台站点证书指纹，仍与部署前同一正式证书一致。
- [✓] 复审通过：升级后 Scert 仍仅监听 127.0.0.1:8443，Nginx 通过 80/443 提供 Web 服务；管理入口前端与 readyz 返回 200，第二台回环上游仍使用系统 CA 严格校验。没有开放额外公网业务端口，未关闭 TLS 或 SSH 主机校验。
- [!] 仍需处理：历史事务 20260930-231310.231207974-5bf9d1e37542、20260930-231313.144123222-ce71b1dfe14e 的恢复证据字段为 null，服务保留 recovery_required 并拒绝自动接管；本轮没有伪造历史证据或强改终态。
- [ ] 未开始：本轮修复后的最终清库首次部署验收。Smail SMTP、数据库矩阵及备份恢复等其余上线门禁仍需按原任务清单验证。

本轮运维使用密码 SSH，没有新建辅助验收脚本；没有额外制作远端数据备份。安装器自行保留升级前发布文件，未擅自删除。以上域名、服务器及凭据仅用于测试，不作为产品验收规则。

## 真实进程崩溃恢复与原任务重试（2026-10-01 17:15，北京时间）

### 范围与操作边界

沿用已部署的源码提交 302111c8affd6c31c6d616eed45a2a8cee42843c 及正式候选包，本轮没有修改产品代码或重新构建。双机二进制版本沿用上一节证据，第二台再次核对 SHA-256 为 393ae5a1cfaf8e4442ec30e47950f020e6ab47adb47af7fdca45a36eb9486ed2。本轮保留数据连续复测，不代表最终清库首次流程。

使用已有正式证书重部署，未重新签发；测试域名 scert-multi-20260930.apmmk.cn。人工操作使用密码 SSH，产品部署仍使用其托管密钥并校验主机指纹。未创建辅助脚本、未直接写库、未手工删锁、未额外备份远端数据。

### 崩溃注入及自动恢复

- 正式域名部署接口创建任务 72f4222a-3706-419c-847c-0fcb55b436f2。为得到可控中断窗口，通过正式服务器 API 将第一台重载命令临时设置为 nginx 重载后延迟 18 秒，校验命令始终为 nginx -t。
- 只读确认两个事务均为 reload_attempted/pending 后，于 UTC 09:07:09 对第二台 Scert.service 主进程发送 SIGKILL；未停止 Nginx。
- 原进程 PID 112721，systemd 自动启动新进程 PID 113012，NRestarts=1。
- UTC 09:07:29 第二台事务已回滚，第一台仍恢复中；此时管理 readyz 暂时返回 502，第一台业务站点本机严格 HTTPS 仍为 200。此管理短暂不可用如实保留，不宣称零中断。
- UTC 09:07:42 两条事务均 rolled_back，消息为“服务启动已完成中断部署事务回滚”；原任务为 failed/service_restart，管理 readyz 已恢复 ready。
- 第一次尝试 a680e853-2d2b-40c6-a68f-2dea91b36a79 保留 failed。对应事务：20261001-090648.928412564-d8bedbb5d1d1、20261001-090653.896206069-a567d37db475，均为 rolled_back。
- 浏览器中原同步请求最终返回 Nginx 502，符合主进程被杀后的连接中断；没有将该响应伪报为成功。

### 配置恢复和正式重试

- 重新登录后，通过正式服务器 API 将第一台 reload_command 恢复为 ["nginx","-s","reload"]，PUT 返回 200，随后 GET 再次核对一致。
- 正式接口 POST /api/v1/tasks/72f4222a-3706-419c-847c-0fcb55b436f2/retry 返回 200，双机 total=2、success=2、failed=0，verification_status=success；结束时间 UTC 09:12:11。
- 原任务更新为 success，第二次尝试 e52b6717-46b8-4072-9931-eafc7f8fa817 为 success；双机部署结果均关联此次尝试，没有新建嵌套父任务。
- 第二次尝试的事务 20261001-091153.272958781-00baba19c6a9、20261001-091159.467779056-dc1f13c75a80 均为 committed。
- 只读审计依次存在 certificate_deploy/running、task_recovery/failed、certificate_deploy_retry/running、certificate_deploy_retry/success，均关联原任务和 primary-super-admin 操作者。
- 成功后再调用正式重试接口返回 409。

### 文件、服务与网络复核

- 双机 nginx -t 成功，证书目录使用不限制文件类型的 find 检查，无名称包含 lock 或 scert-tmp 的残留。
- 恢复后双机 cert.pem 和 fullchain.pem 的 SHA-256 均为 ecf815ca6ba91abb43985663f7ff2df4ee7987362131e068b6ff32e4657f90bc。
- 双机本机 SNI 严格 HTTPS 请求均为 200；第一台访问第二台公网严格 HTTPS 为 200。第一台本机和第二台公网证书指纹均为 43:06:3C:07:26:D3:43:62:B2:54:CE:28:B6:6D:7E:B6:37:E0:0B:16:5F:68:EC:0D:07:FC:ED:0D:F2:C6:4A:D2。
- 双机 Scert 均仅监听 127.0.0.1:8443，Nginx 对外监听 80/443；第二台管理 HTTP 返回 301，跳转 HTTPS，readyz 正常。没有开放额外公网业务端口。
- 单独发现的公网限制：第二台多次访问第一台公网测试域名 HTTPS 均为连接重置，本地 Windows 客户端同样握手失败。第一台抓包看到 TCP 建连后入站 RST；公网 HTTP 返回 Server: Beaver、403，正文标题为 Non-compliance ICP Filing，并指向阿里云拦截页面。第一台本机同域名 HTTPS 仍为 200；不把本机成功代替公网通过，也不通过开放其他端口或关闭证书校验绕过此限制。
- 无 SNI 的公网握手仅用于定位网络差异，返回其他默认站点证书；该诊断不计入测试域名 TLS 验收，未修改产品校验策略。

### 结论

- [✓] 该崩溃窗口的可信事务自动回滚、原任务正式重试和重试后状态关联复审通过。
- [!] 第一台该测试域名公网 HTTP 有明确拦截页面，公网 HTTPS 失败，公网访问验收不通过；第二台管理及测试站点正常。
- [~] 仅同证书重部署，其他负向窗口、新旧证书切换和历史证据缺失事务尚未全部闭环。
- [ ] 最终清库首次流程及其他原清单生产门禁未完成；不宣称整体生产部署验收通过。

## 数据库实机矩阵与新阻塞（2026-10-01 17:45，北京时间）

### 范围与环境

- 沿用第二台 Linux 测试机，正式运行主程序仍为 302111c 对应产物，SHA-256 为 393ae5a1cfaf8e4442ec30e47950f020e6ab47adb47af7fdca45a36eb9486ed2。
- 测试使用项目原有 Go 集成测试及正式 CLI，不使用新建辅助脚本。非 root 测试进程由 systemd 临时单元以 mysql 用户启动，隔离数据库仅监听回环地址。
- 测试根目录 /var/tmp/scert-db-matrix-20261001-1720；MySQL 数据根目录 /var/lib/mysql-files/scert-20261001。保留 AppArmor 策略，不放宽 /scert 权限。
- 未重建或清空业务库，未额外备份业务数据；当前不是最终清库首次部署验收。

### 已结束结果

| 测试 | 单元或日志 | 结果 |
|---|---|---|
| 原 MySQL 数据库矩阵 | scert-mysql-matrix-20261001-1722 / mysql-database.log | 退出 1；模式 15 中断后 1、2 条语句及模式 16、17 建表后中断，共四个边界因表已存在失败 |
| MySQL 8.0.46 仓储 | scert-mysql-repository-20261001-1732 / mysql-repository.log | 退出 0；9 个主测试、92 个子场景通过，无失败或跳过 |
| PostgreSQL 14.24 首次尝试 | scert-postgres-matrix-20261001-1723 / postgres-cli.log | 七场景均因备份工具版本不满足要求失败；不计通过 |
| PostgreSQL 16.15 | scert-postgres16-matrix-20261001-1727 / postgres16-cli.log | 退出 0；七场景全部通过，无跳过；总耗时 14.52 秒 |
| SQLite 旧断言 | scert-sqlite-matrix-20261001-1726 / sqlite-cli.log | 失败，测试将当前迁移版本写死为 14，实际已为 18 |
| SQLite 修正断言及历史样本 | scert-sqlite-history-20261001-1740 / sqlite-history-cli.log | 退出 0；historical_backup、upgraded_backup 全部通过；总耗时 0.79 秒 |

PostgreSQL 的 success、certificate、permission、inventory、authentication、tls 使用独立正式 CLI；cancel 使用进程内确定性取消和真实工具回退，仅据实记录该范围。16.15 工具位于 /var/tmp/scert-pg16-tools-20261001/extracted，包来自经官方公钥签名验证的独立 PGDG 索引。测试后无残留 PostgreSQL 服务进程。

SQLite 新测试程序 SHA-256：4c81656fae63a2eebad999d58e6a79e63e6f441810e02ce29b2a9ce3706ae7f6；上传后核对一致。新增样本覆盖 task_idempotency、task_attempts、task_deployment_results、deployment_transactions 及 recovery_evidence_json；恢复前实际修改样本，恢复后全表快照核验。

### 当前修复与边界

- [~] 原 MySQL 15～17 迁移使用非事务建表，已落盘后再次执行报表已存在。不能简单忽略冲突：模式 15 的历史幂等回填还需避免重复写入，同时拒绝内容不一致的数据。
- [~] 正在实测可重入建表、字段和索引校验、回填与版本标记同事务提交，以及部分回填、冲突数据、异常结构负向场景。待后续结果更新，不能根据运行中的单元 Result=success 提前判定通过。
- [✓] 双机端口复核符合 Nginx 80/443 反代要求；Scert 与 MySQL 均未新增公网监听。
- [!] 第一台测试域名公网平台拦截、无可信证据的历史事务及此前剩余生产门禁继续保留，不以数据库子项通过替代。

## MySQL 中断修复、正式构建与双机升级复核（2026-10-01 18:02，北京时间）

### 修复和实库证据

- 修复提交：c6982ab827038c2cb16bc1432fb393f68a5296e2，已推送。首次推送遇到认证失败，重试成功；没有修改远程认证设置。
- 第一轮修复测试单元 scert-mysql-reentry-20261001-1743 退出 0，106 个子场景通过；此后补充默认空字符串、排序规则和大小写冲突保护，没有用第一轮结果替代最终代码验证。
- 最终单元 scert-mysql-reentry-guards-20261001-1751：inactive、Result=success、ExecMainStatus=0。日志 /var/tmp/scert-db-matrix-20261001-1720/mysql-reentry-guards.log。
- TestMySQLRealDeploymentMigrationRecovery：18 个子场景通过，92.25 秒；覆盖建表前后、部分及完整回填、重复历史任务、不同域名相同键、空键排除、错误任务及时间、孤立映射、大小写冲突、错误列类型或默认值、允许空值、缺失唯一约束、缺失或逆序索引、额外字段、排序规则和非 InnoDB 表。
- TestMySQLRealMigrations：91 个子场景通过，385.84 秒；覆盖 0～18 版升级、各语句中断边界、现有数据、作用域账户、非法迁移历史和重启持久化。总计 109 个子场景，无失败或跳过。
- 最终测试程序 SHA-256：243a42a3826d5e77547346bb95ed56394ca67bab7334568d3202b193a3c31019，上传后核对一致。

### 本地门禁与正式包

- go test -count=1 ./...、go vet ./... 退出 0；go test -race -count=1 ./internal/database ./internal/cli 退出 0；前端 241 项通过、0 失败、0 跳过。未启用的本地外部数据库场景不计实库通过。
- 正式入口 build/build-release.ps1 -Target linux-amd64，包目录 build/release-20261001-mysql-reentry，manifest 标记 source_commit=c6982ab827038c2cb16bc1432fb393f68a5296e2、source_dirty=false。23 项产物大小及哈希核验一致。
- 归档 SHA-256：c075297e9f8f76f4ebb8fa28d43a4099ca853a3619212d5e7f51e78d4b0e9e4d。
- 主程序 SHA-256：39b70a1a11148982baeb699bae749f7e2988a2fba02b001c0da740ba8b86f36d。
- 新正式程序用于 SQLite 再测：scert-sqlite-release-20261001-1754，两个子场景通过，1.34 秒，退出 0。
- 新正式程序用于 PostgreSQL 16.15 再测：scert-postgres16-release-20261001-1754，七场景通过，14.49 秒，退出 0；六场景独立正式 CLI，取消场景为进程内入口，不扩大证明范围。

### 双机升级与健康复核

- 运维通过密码 SSH 连接，保留严格主机指纹检查。发布包上传双机后，归档哈希及包内 21 项 SHA256SUMS 全部通过。
- 发布目录 /var/tmp/scert-release-mysql-reentry-20261001；安装日志 /var/tmp/scert-install-mysql-reentry-20261001.log。
- 两台首次运行安装器时主服务仍在运行，被保护检查拒绝；停止主服务后仍因 Scert-storage.socket 在监听再次被拒绝。随后完整停止 Scert、Scert-admin、Scert-storage.service、Scert-storage.socket，官方安装成功，再启动目录 socket 和主服务，最终退出 0。所有失败记录保留在同一追加日志中；未将拒绝事件计为成功安装。
- Nginx 未停止；管理上游在停服窗口不可用，本次不宣称零停机。安装器按自身事务流程保留发布文件，没有额外制作业务数据备份，也没有手工删除历史文件。
- 两台 /scert/scert 哈希均与上述新正式包一致；第一台 PID 2091132，第二台 PID 158279，均 active、NRestarts=0，目录 socket active。
- 双机 nginx -t 成功。第二台管理首页 HTTP 200，readyz 返回 ready，HTTP 入口 301；第一台测试域名本机带 SNI 且严格校验证书的 HTTPS 为 200，不能代替其公网访问通过。
- 双机 Scert 均仍监听 127.0.0.1:8443，MySQL 监听 127.0.0.1:3306；第二台 MySQL X 协议也仅监听 127.0.0.1:33060。Web 仍由 Nginx 80/443 对外，未添加公网规则。
- 只读核查双机迁移版本均为 18，升级前后任务及事务状态计数一致，没有活动任务被中断。第二台两条 recovery_required 历史事务保留，不伪造证据，不强改状态。

### 下一项正在运行

- 单元 scert-mysql-cli-20261001-1801，日志 mysql-cli-release.log，运行项目现有 TestMySQLReal 维护测试。测试程序 SHA-256：73cd220dfcb898d452ca9de752c2e19b40d5951c693d6fab28e2e6c00c9ea7b9。
- 官方 mysqld、mysql、mysqldump 在系统中位于不同目录；仅在隔离目录 mysql-tools 创建指向原程序的符号链接，以满足夹具同目录要求，没有编写包装或辅助验收脚本，没有修改系统程序。
- 当前运行中，不提前判定通过。该套测试包含进程内正式命令入口及实际工具互操作，不等同于所有独立发行 CLI、完整 Web 和最终生产流程通过。

### 最新接续状态与任务确认（2026-10-01 18:15，北京时间）

本节更新前文历史快照；旧章节中“当前有效”仅指其记录时点，不代表本轮状态。沿用原始四十四项需求，本次仅验收 Linux，Windows 不在本轮范围；不以历史 P0-03 局部状态代表全部生产门禁。

- [x] 已完成，等待复审：MySQL 8.0.46 备份恢复矩阵单元 `scert-mysql-cli-20261001-1801` 已真实结束，退出零；日志 `mysql-cli-release.log` 中二十一个主测试、一百三十六个子场景通过，零失败、零跳过。服务器日志开始时间为北京时间 18:01:26，结束为 18:06:49。
- [x] 已完成，等待复审：本批矩阵覆盖备份快照、官方导入导出互操作、视图、触发器、例程、事件、权限和失败回退。主要调用进程内正式实现，不冒充独立发行程序全量验收。
- [x] 已完成，等待复审：双机服务正常；Scert 仅监听回环 8443，MySQL 仅监听回环 3306；第二台的 33060 同样仅监听回环。Web 对外使用 Nginx 80/443，不新增公网业务端口，不修改第一台现有邮件端口。
- [!] 暂缓或存在阻塞：只读检查确认双机 Scert 均未启用 SMTP，主机、系统通知用户名、凭据引用均为空；缺少可用系统通知配置，不能宣称真实发信与邮件密码重置通过，不使用普通邮箱替代。
- [~] 进行中：复用仓库现有 MySQL 隔离实库测试，增加独立正式发行程序调用；保留严格 TLS、凭据加密存储、成功恢复及 SQL/证书失败回退断言，不创建辅助验收脚本，不接触业务数据库。
- [ ] 未开始：将新增测试编译到 Linux，在第二台隔离数据库中验证当前正式发行程序，再记录实际通过或失败结果。
- [!] 暂缓或存在阻塞：MySQL 5.7、完整三库 Web 流程、Linux 原生构建、其余部署负向场景、历史事务核查、系统发信与最终清库首次部署等仍未闭环；整体生产验收未放行。

### MySQL 独立发行程序验收结果（2026-10-01 18:22，北京时间）

- [x] 已完成，等待复审：新增仓库测试 `TestMySQLRealReleaseBackupRestoreCommands`，复用现有成功、SQL 失败、证书恢复失败场景；通过独立进程调用正式发行程序，不再依赖测试进程的 TLS 注册表。未创建辅助验收脚本。
- [x] 已完成，等待复审：第二台 Linux 上单元 `scert-mysql-release-cli-20261001-1818` 真实结束，退出零；三个子场景全部通过，耗时 33.85 秒，日志为 `/var/tmp/scert-db-matrix-20261001-1720/mysql-release-cli.log`。
- [x] 已完成，等待复审：验证对象为已部署候选同源的正式发行程序 `/var/tmp/scert-release-mysql-reentry-20261001/scert`，SHA-256 为 `39b70a1a11148982baeb699bae749f7e2988a2fba02b001c0da740ba8b86f36d`；测试程序 SHA-256 为 `88ff0fec3f2ab9a14f89dd52f384a677ecfb450f679b077ec47e83649644eb09`。
- [x] 已完成，等待复审：隔离 MySQL 8.0.46 仅监听回环，使用严格 TLS；只在子进程中信任隔离证书，数据库密码通过加密凭据引用读取，未更改系统信任库。断言覆盖业务数据、审计数据、配置、主密钥、加密凭据、证书及失败后恢复前快照。
- [x] 已完成，等待复审：本地后端全量测试、全量静态检查、CLI 竞态测试和差异检查通过。本地条件跳过不计实库通过；本批只修改测试，没有更换线上程序。
- [~] 进行中：准备在隔离目录使用固定工具链执行 Linux 原生正式构建，避免将 Windows 交叉编译包冒充 Linux 原生证据；不新增公网监听，不更改生产工具链或业务配置。
- [!] 暂缓或存在阻塞：上述三个独立 CLI 场景不代表全部数据库、Web 或生产流程验收通过；既有 SMTP 配置缺失、历史事务核查及其他未完成门禁继续保留。

### Linux 原生包双机升级与 Nginx 端口门禁（2026-10-01 19:27，北京时间）

- 构建单元：`scert-linux-native-release-20261001-1852`，结果为 success；原生工具链为 Go 1.26.6、Node.js 24.19.0、npm 11.17.0，目标为 `linux/amd64`。
- 发布清单：源码提交 `be6e2059f4ce949f7e1b6c0dacb3ff0bdea9ea52`，`source_dirty=false`；归档 `Scert-20261001-V0.1.0-linux-amd64.tar.gz` SHA-256 为 `08ba7769bf9225b88ea84f104720fb166b583a494737627523179c0527e83130`。
- 第二台 `45.136.13.241`：使用官方安装器完成停服升级后启动 `Scert.service` 和 `Scert-storage.socket`；Nginx 未停止；`https://odacm.cn/readyz` 返回 ready，首页和 HTTP 到 HTTPS 跳转正常。
- 第一台 `8.137.23.95`：使用同一归档和官方安装器完成停服升级后启动 `Scert.service` 和 `Scert-storage.socket`；Nginx 未停止；既有站点配置和证书保持不变。
- 端口检查：双机 Scert 仅绑定回环 `8443`；MySQL 仅绑定回环 `3306`；第二台 `33060` 仅绑定回环。绕过本机代理直连公网时，三个内部端口均超时；对外项目入口仅为 Nginx `80/443`。未关闭或修改第一台现有邮件业务端口。
- 结果：双机原生包部署和 Nginx 反代端口门禁完成，等待复审；不代表整体生产部署验收通过。SMTP、密码重置、完整 Web 流程、历史事务、DNS/ACME 和最终清库首次部署仍未闭环。

## 2026-10-01 SSH 私钥登录能力安全复核

当前总体状态仍为：**P0-03：完成，等待复审**。本轮安全整改不代表生产部署整体验收通过。

- [x] 后端服务器模型、SSH 连接工厂、服务器 API 和服务器仓储已收敛为密码认证，未保留 SSH 登录私钥字段或部署路径。
- [x] 新增数据库迁移 19，清理历史服务器记录中的 SSH 私钥引用和 SSH 密钥状态；历史表结构迁移文件仅用于兼容既有数据库，不代表当前能力重新启用。
- [x] 敏感信息存储测试已改为 DNS 凭据场景，不再创建或读取 `secret://ssh/key-*` 测试引用。
- [x] TLS 证书业务的 `private_key_path`、证书上传、部署和回滚逻辑保持不变；该字段只用于证书私钥，不用于 SSH 登录。
- [x] `go test ./...`、三个 Go 命令构建和 `git diff --check` 已通过。
- [!] 仍未完成清库后的三数据库 Web 首次初始化、Smail 系统账户真实发信、验证码重置闭环、P0-10 负向矩阵、双机失败重试和最终生产门禁，因此不得标记为整体验收通过。
## 2026-10-01 SSH 密码认证候选包实机复核补充

### 本轮范围

本轮只验证安全整改后的正式 Linux amd64 候选包在 `45.136.13.241` 的安装和入口安全边界。使用密码 SSH，未使用密钥，未创建辅助验收脚本；未清空业务数据库，未执行真实发信或密码重置流程。

### 结果

- 候选包 `Scert-20261002-V0.1.0-linux-amd64.tar.gz` 的安装包清单校验通过。
- 安装器在发现 `Scert-storage.socket` 仍监听时拒绝覆盖；停止相关服务后重新安装成功，未强制覆盖运行中的存储服务。
- `Scert`、`Scert-storage.socket`、Nginx 均为 `active`，`nginx -t` 通过。
- Scert 监听 `127.0.0.1:8443`，MySQL 监听 `127.0.0.1:3306` 和 `127.0.0.1:33060`，公网服务入口仍为 Nginx `80/443`；既有邮件服务端口未在本轮修改。
- SSH 服务端复核为 `pubkeyauthentication no`、`passwordauthentication yes`、`authenticationmethods password`，满足密码唯一认证要求。
- 从本机按 `odacm.cn` SNI 严格访问 `https://odacm.cn/readyz` 返回 `{"service":"scert","status":"ready"}`；从服务器回环按同一域名严格访问也返回 ready；没有使用 `-k` 或关闭 TLS 校验。
- HTTP `odacm.cn` 返回 `301` 并跳转到 `https://odacm.cn/`。

### 尚未完成的门禁

本轮证据不能替代 SQLite、MySQL、PostgreSQL 清库后的 Web 首次初始化、Smail 系统账户真实 SMTP 发信、超级管理员验证码重置密码、修改密码后重新登录、P0-10 负向矩阵、双服务器失败重试与历史 `recovery_required` 证据核查。当前总体状态仍为 **P0-03：完成，等待复审**，不能标记为生产整体验收通过。

### 服务重启后的恢复窗口复核

在 `45.136.13.241` 使用密码 SSH 执行 `systemctl restart Scert`。重启后 `Scert`、`Scert-storage.socket`、Nginx 均保持 `active`；约等待 8 秒后，回环和 `odacm.cn` 的严格 HTTPS `/readyz` 均返回 `{"service":"scert","status":"ready"}`。

重启后的首个约 2 秒请求返回 HTTP `502`，随后 Scert 完成 `127.0.0.1:8443` HTTPS 监听并恢复正常。该短暂恢复窗口已记录为生产门禁观察项，不能宣称无感重启；本轮没有开放内部端口、关闭 TLS 校验或修改 Nginx 为不安全转发。

同次启动日志继续报告两条历史 `recovery_required` 事务因缺少可信恢复证据而拒绝自动接管。系统保留现场等待人工核查，本轮未删除锁、伪造恢复证据或修改历史事务。

## 2026-10-01 双机密码认证候选包部署实测

### 测试范围

- 目标服务器：`8.137.23.95`（`ikun1`）和 `45.136.13.241`（`yc`）。
- 部署方式：仅使用 SSH 密码认证和官方 Linux 安装器；未使用 SSH 私钥、公钥、`authorized_keys` 或辅助验收脚本。
- 候选包：`Scert-20261002-V0.1.0-linux-amd64.tar.gz`。
- 包 SHA-256：`4090D6D614C359A4A6C7FF92634189AA136099324DD53B2D4557038954DCD33E`。

### 实测结果

- [x] 两台均完成归档校验和官方安装器停服升级，升级前发布文件由安装器保留；未额外制作远端备份。
- [x] 两台 `Scert`、`Scert-storage.socket`、Nginx 均为 active，`nginx -t` 通过。
- [x] 两台 Scert 均仅监听 `127.0.0.1:8443`，数据库仅监听回环地址；公网项目入口仍为 Nginx `80/443`。
- [x] 两台 SSH 服务端均为 `pubkeyauthentication no`、`passwordauthentication yes`、`authenticationmethods password`。
- [x] 第二台 `https://odacm.cn/readyz` 在正常 TLS 校验下返回 `{"service":"scert","status":"ready"}`。
- [!] 第一台不是 `odacm.cn` 当前证书域名，使用该域名探测发生证书名称不匹配；该结果不作为第一台公网域名验收通过依据。
- [!] 第二台启动日志仍报告两条历史 `recovery_required` 事务缺少可信恢复证据；本轮保留现场，未删除锁、伪造证据或自动接管。
- [!] 第一台 `php-fpm-82.service` 的既有 failed 状态与 Scert 无关，单独记录，不作为 Scert 服务失败依据。

### 结论

本轮通过了双机候选包安装、密码唯一 SSH、Scert 回环监听、数据库回环绑定和 Nginx 入口边界复核；没有通过生产整体验收。清库后的三数据库 Web 首次初始化、Smail 系统账户真实发信、密码重置完整闭环、P0-10 负向矩阵、历史恢复证据核查以及重启瞬间短暂 502 观察项仍需继续处理。当前总体状态保持 **P0-03：完成，等待复审**。

## 2026-10-01 当前日期候选包部署和安全边界复核

### 构建与部署

- 候选包：`Scert-20261001-V0.1.0-linux-amd64.tar.gz`。
- SHA-256：`56426C1BBCECA68B32D122E813A950D2EE345349F89C3178080BD313F6F77E8E`。
- 来源提交：`79bae3bf2b52720591279f78f5cbc340f82c4f36`，`source_dirty=false`。
- 双机均通过远端归档校验、包内 `SHA256SUMS` 和官方安装器升级；未使用 SSH 私钥、公钥、`authorized_keys` 或辅助验收脚本。
- 双机运行中的 `/scert/scert` 摘要均为 `3422abf0a5fb2c0254fb382118424733534fd00548eba4930a0e90857cd874b8`；`/scert/scert-admin` 摘要均为 `ee52d429a88b3942147e1acf67da3653f61e076c11d67bf3dd20b254b4321722`。

### 安全回归

- [x] `TestSSHKeyAPIIsUnavailable` 通过，`GET /api/v1/ssh-keys` 返回 404。
- [x] 前端 241 项测试全部通过；测试确认服务器认证提示仅显示密码配置状态，SSH 私钥入口未挂载。
- [x] 两台 SSH 服务端均为 `pubkeyauthentication no`、`passwordauthentication yes`、`authenticationmethods password`。
- [x] 两台 Scert 仅监听 `127.0.0.1:8443`，数据库仅监听回环地址，Nginx 仅承担公网 `80/443` 入口。

### 当前候选包重启复核

在第二台对当前日期候选包执行 `systemctl restart Scert`，第 0、1 秒通过 Nginx 访问 `https://odacm.cn/readyz` 返回 502，第 2 秒恢复 200；随后服务保持 active，Scert 仍为回环监听，严格 TLS 校验没有关闭。该短暂恢复窗口继续作为生产门禁阻塞项记录。

### 结论

当前日期候选包部署和安全边界复核完成，但生产整体验收仍未通过。历史 `recovery_required` 事务、清库后的三数据库 Web 流程、Smail 系统账户真实发信、密码重置闭环和 P0-10 负向矩阵仍需继续处理。总体状态保持 **P0-03：完成，等待复审**。

## 2026年10月1日 Linux上线加速复核（当前执行记录）

本节是本轮目标的当前状态入口，只记录非敏感证据，不记录服务器口令、RAM 密钥、数据库密码、授权码、令牌或其他认证信息。Windows 环境不在本轮范围；公网邮件服务端口属于既有 Smail 服务，不作为 Scert 管理入口处理。

### 本轮需求清单

- [x] 仅验证 Linux 双机：第一台 `8.137.23.95`，第二台 `45.136.13.241`。
- [x] Scert 服务器连接只使用 SSH 密码认证，不使用或新增 SSH 私钥、公钥、`authorized_keys` 和密钥登录流程。
- [x] Scert 公网入口只经 Nginx `80/443`；Scert `8443` 和数据库端口保持回环监听。
- [x] 不创建辅助验收脚本，不把辅助脚本作为验收依据。
- [~] 继续完成 P0-10 负向恢复矩阵和历史 `recovery_required` 事务的可信证据核查。
- [~] 继续完成 SQLite、MySQL、PostgreSQL 三数据库的清库后完整 Web 首次流程。
- [!] Smail 系统通知 SMTP 当前未启用且未配置系统账户凭据，真实发信、验证码密码重置和改密登录闭环暂不能完成；不得猜测 SMTP 主机、账号或授权码。
- [~] 完成最终 Linux 候选包部署、浏览器缓存清理和生产门禁复审。
- [!] Windows 环境明确排除，不纳入本轮上线验收。

### 当前任务状态

- [✓] T0：双机 Scert、存储通道、Nginx 均为 active；Scert 仅监听 `127.0.0.1:8443`，数据库仅监听回环地址，公网入口为 Nginx `80/443`。
- [x] T1：MySQL 5.7 隔离实例和正式发行程序 CLI 实库测试已有通过证据，等待整体验收复审；不能替代 Web 首次流程。
- [~] T2：P0-10 剩余负向窗口、锁归属不匹配、文件摘要不匹配和历史恢复证据仍在核查。
- [~] T3：三数据库完整 Web 流程尚未取得全部直接实机证据。
- [!] T4：Smail 系统账户发信凭据尚未配置，密码重置邮件和新密码登录闭环阻塞。
- [~] T5：候选包已在双机运行并通过健康检查；最终清库后的首次流程、缓存清理和统一门禁复审仍未完成。
- [!] T6：Windows 不在本轮范围。

### 本轮直接证据

- [✓] 两台服务器均通过密码 SSH 复核：Scert、存储 socket、Nginx active。
- [✓] 两台服务器均保持 Scert `127.0.0.1:8443`、MySQL 回环监听；`odacm.cn` 的 `/readyz` 返回 200 ready，HTTP 入口跳转 HTTPS。
- [✓] 生产页面 DOM 未出现 SSH 私钥、公钥或 `authorized_keys`，服务器配置只提交密码和主机指纹；`/api/v1/ssh-keys` 返回 404。
- [✓] 本地 Go 全量测试、前端 241 项测试和 Vite 生产构建通过；工作区无未提交代码修改。
- [!] 当前 `/api/v1/settings` 显示通知 SMTP 未启用且系统通知凭据未配置；该项需要从 Smail 系统通知页面取得真实授权配置后才能继续。

### 当前总体验收结论

总体仍为 **P0-03：完成，等待复审**，不能因为 T0、T1 和 SSH 安全整改通过而宣布生产验收通过。上线前必须补齐 T2、T3、T4、T5 的直接证据；其中 T4 受 Smail 系统账户授权配置阻塞。

## 2026年10月2日最新候选包实机部署记录

本节只记录非敏感结果，不记录口令、RAM 密钥、数据库密码、令牌、私钥、公钥或 `authorized_keys`。本轮按照生产安装器执行，未创建辅助验收脚本，未开放额外外部端口。

### 构建与部署

- [x] 最新候选包来源提交 `cc3ac06`，构建目录标记 `source_dirty=false`；归档 `Scert-20261002-V0.1.0-linux-amd64.tar.gz` SHA-256：`e4623636c5021a99998ce04f4680ebd3d107c02fb4a118a17875fc84c7faeefa`。
- [x] 双机均通过密码 SSH 上传和正式安装器部署，未使用密钥登录；安装后程序 SHA-256 均为 `f6fcad3a8d6d0cd47e90e0e0cc6b2ff9c9657082a63835aae0e1d72ab3500fb9`。
- [x] 两台 Scert、存储 socket、Nginx active，Nginx `-t` 通过；Scert 监听回环 8443，数据库监听回环 3306/33060，公网入口保持 Nginx 80/443。

### 入口与安全复核

- [x] 第二台 `odacm.cn` 严格 HTTPS `/readyz` 返回 200 ready，HTTP 请求跳转 HTTPS。
- [x] 第一台使用 `odacm.cn` 严格 TLS 校验时证书 SAN 不匹配，客户端拒绝连接；未关闭 TLS 校验，需更换为包含目标域名的正式证书后才能放行。
- [x] 第一台跨机探测第二台内部端口 8443、3306、33060 均超时；本机代理网卡导致的 TCP 探测结果不采纳。
- [x] `/api/v1/ssh-keys` 返回 404，生产 Web 包不存在 SSH 密钥管理入口；本轮未生成、读取、上传或部署 SSH 密钥材料。
- [x] 启动后早期出现短暂 502，等待服务就绪后恢复 200；该项仍保留为生产门禁观察项。

### 质量门禁

- [x] 后端全量 Go 测试和 `go vet` 通过。
- [x] 前端 241 项测试通过。
- [~] P0-10、三数据库清库后的 Web 流程、Smail 系统账户发信、验证码密码重置和最终清库首次验收仍未完成。

总体生产部署验收仍未放行，当前状态保持 **P0-03：完成，等待复审**。

## 2026年10月2日 SSH 登录密钥入口移除复核

本轮按安全要求收紧产品边界：SSH 登录只允许密码认证，不提供私钥、公钥、密钥生成、授权文件写入或密钥登录管理能力。HTTPS 证书部署所需的证书私钥路径仍保留，二者用途不同。

- [x] 删除未注册的 SSH 密钥 CLI 入口文件，正式 CLI 不再包含密钥管理命令。
- [x] 删除未注册的 SSH 密钥 HTTP 处理文件，生产路由继续不注册相关接口，`/api/v1/ssh-keys` 保持 404。
- [x] 删除未引用的前端密钥弹窗文件和遗留样式，生产页面不提供密钥管理入口、命令或授权文件提示。
- [x] 保留服务器主机指纹校验、SSH 密码字段和 HTTPS 证书私钥路径；没有把证书私钥误当作 SSH 登录凭据。
- [x] 后端 `go test ./...` 全部通过；前端 241 项测试全部通过；Vite 生产构建通过；`git diff --check` 通过。
- [~] 需要将最新候选包按正式安装流程部署到双机，并复核页面和 API 后再进入最终生产验收。

本轮不删除历史数据库迁移文件和禁用兼容测试；它们只用于既有数据库升级和安全回归，不会启用 SSH 密钥能力。总体生产验收仍未通过，既有证书 SAN、启动早期 502、Smail 系统账户发信和三数据库完整 Web 流程仍是门禁项。
## 2026年10月2日 SSH 登录凭据边界复审补充

本次仅记录非敏感结果。部署和验证继续使用 SSH 密码认证，不使用、不生成、不上传 SSH 私钥或公钥，不写入远程授权文件，也不创建辅助验收脚本。HTTPS 证书私钥只用于 TLS 证书部署。

- [x] SSH 客户端新增连接前密码认证硬校验，非密码认证不能进入连接流程。
- [x] 前端部署页面和 CLI 帮助文字明确区分服务器指纹、SSH 密码与 HTTPS 证书私钥。
- [x] 未新增公网端口，Scert 仍只经 Nginx 80/443 反代，服务自身保持回环监听。
- [~] 生产整体验收仍受第二台正式域名证书、短暂启动 502、三数据库 Web 首次流程、Smail 发信和密码重置闭环阻塞。

当前总体状态保持 **P0-03：完成，等待复审**。
# 2026年10月2日12:35 回环监听安全复核

本节为只读现场核查及代码整改记录，不是清库首次生产验收；没有重启线上服务、修改 Nginx、开放端口或触碰其他业务。

- [x] 第一台与第二台实际监听均为 Scert 127.0.0.1:8443、MySQL 127.0.0.1:3306 和 127.0.0.1:33060；第一台到第二台上述三个端口的四秒限时 TCP 探测均未连接成功。
- [x] 第二台 Nginx 配置检查成功，管理上游为回环 HTTPS，开启 proxy_ssl_verify、SNI 和系统 CA 校验。
- [x] 发现两台磁盘配置仍保存通配地址，但 systemd 的 SCERT_SERVER_HOST 将实际监听覆盖为回环地址。新增校验必须针对环境覆盖后的有效配置，不误伤当前安全运行方式。
- [~] 补齐程序独立启动防护：Linux 默认及空值回退改为回环地址，CLI 配置入口、初始化入口、正式应用入口在建立资源前拒绝非回环监听；新增 Linux 回归用例待执行。
- [!] 12:26 正式构建因 GOFLAGS 覆盖被拒绝；12:30 重试移除覆盖后，12:31 被 install-linux.sh 的发布路径扫描拦截。两次均判失败，不以进程启动时的 Result=success 冒充构建成功，不绕过路径检查。
- [ ] 最新代码正式候选包、双机升级及严格 HTTPS 复测待完成。
## 2026年10月3日17:22 只读恢复核查原生 Linux 回归（未部署）

- [x] 修改前检查点 `80b6c0c` 已推送。第一台密码 SSH 使用既有固定主机密钥严格校验，未启用密钥登录，未绕过 TLS。
- [x] 基线源码归档 `scert-inspection-80b6c0c.tar` 两端 SHA256 一致：`0032a7f3bbfc4d6de0258dddff50679490fd7cc11b5603c956f851407a476cf0`。归档仅包含已跟踪后端源码，不包含运行时数据库或秘密。
- [x] 测试目录为 `/var/tmp/scert-inspection-20261003-1714/backend`；Go 为既有 1.26.6 linux/amd64；以 nobody 身份运行隔离文件测试，生产数据库未清理或参与测试。
- [x] 基线 `TestRecoveryInspection` 通过。后续上传四份源文件修复和新增测试后，专项命令 `go test -count=1 -timeout=180s -v ./internal/deployment -run 'TestRecoveryInspection|TestRecoveryPreflightOverPasswordSFTP'` 退出码 0。
- [x] 真实密码 SSH/SFTP 的 9 个核查场景全部通过，包括可信、无历史证据、目标符号链接、目录、不可读、超大文件、超大归属文件、归属文件符号链接和锁目录符号链接。
- [x] `go test -race -count=1 -timeout=240s ./internal/deployment` 退出码 0，`go vet ./internal/deployment` 退出码 0。
- [~] `go test -p 2 -count=1 -timeout=8m ./...` 已启动，另行记录终态；不得将上述专项扩称为后端全量通过。

日志在测试目录上一级：`inspection.log`、`deployment-race.log`、`deployment-vet.log`、`backend-full.log`。本轮没有创建辅助验收脚本，没有远端备份，没有修改业务事务终态，也未部署新增功能。只读采样不构成原子快照、Nginx/HTTPS 或历史提交/回滚证据。

### 17:35 全量测试环境修复与后续执行

- [x] 外部依赖代理连通性探测失败，curl 退出码 28；原联网下载进程已中止，并确认相关 PID 消失，未将该次测试记为通过。
- [x] 复用第一台既有离线代理，保留项目 go.sum 校验；首次离线全量编译期间 SSH 响应变慢且新连接握手超时，已中止该次全量执行并确认测试 PID 消失，不能据此认定是代码错误或 OOM。
- [x] 17:33 再次核实 Scert 与 nginx 均为 active；本机严格 TLS 校验的管理域名 `/readyz` 返回 200。此为服务器回环路径检查，不代表公共入口验收。核查时约 889 MiB 可用内存，1 GiB swap 未使用，指定时段内未检出内核 OOM 记录。
- [x] 四份增量 Go 文件的服务器 SHA256 与本地逐一一致。
- [~] 改为受限单元 `scert-inspection-full-20261003-1735.service` 执行同一份源码的后端全量测试：nobody、单编译并发、CPU 50%、MemoryHigh 500 MiB、MemoryMax 650 MiB、20 分钟运行上限，且 Go 内存软限制 350 MiB。日志为 `backend-full-bounded.log`，终态尚待获取。

这只是隔离测试进程的资源限制，没有调整生产服务配置、重启生产服务或清理业务数据。
## 2026年10月3日18:06 本机核查入口专项

本节优先于后续历史状态。以下是隔离的项目 Go 测试，不是生产业务验收，也不是辅助验收脚本。

- 上一轮 `scert-inspection-full-20261003-1735.service` 已因运行上限终止：运行二十分钟，退出码一，`Result=timeout`，`MainPID=0`。没有全量通过结论。
- 第一台新隔离源码目录：`/var/tmp/scert-local-recovery-20261003-1757/backend`。
- 首批增量归档 SHA-256：`0a14755c3057978a3f84bd8a3bf92be47d4bd570a31e5c37e9a826364daddd76`，上传后核对一致。
- `go test -p 1 -count=1 -timeout=180s ./internal/deployment ./internal/localreset ./internal/adminconsole` 原生 Linux 通过，退出码零；以 nobody 运行，使用既有离线依赖和独立测试数据。
- HTTP 本机会话专项仍在运行。新增 Unix socket 菜单集成与异常响应等测试尚未上传复跑，不能算入首批通过范围。
- 17:57 第一台 Scert/nginx 均为 active；18:02 再查均为 active。编译资源有内存、CPU 和运行时间限制；未因测试重启业务服务。
- 未清理生产数据库、未修改证书及事务锁、未新增公网端口；双机正式候选包和公共 HTTPS 验收仍未完成。


## 2026年10月4日最新执行进度：邮件配置确认与第一台候选部署

本轮仅操作第一台 `8.137.23.95`，第二台 `45.136.13.241` 未操作；没有清理业务数据库，没有创建辅助验收脚本，没有远端备份，没有开放新的公网端口，也没有使用或生成 SSH 密钥材料。

- [x] 旧系统 DNS 账户启动导入已完成编码，固定标识为 `legacy-system-dns`，重复启动幂等，保留现有账户修改并拒绝固定标识冲突；仓储、应用、HTTP API、通知定向测试及 `go vet` 通过。
- [x] 提交 `920b318` 已提交并推送；使用干净 worktree 生成 Linux amd64 候选包，`source_dirty=false`、`trimpath=true`、`local_path_check=passed`，归档 SHA-256 为 `0d2f05892f223c96c305e792a424ccc84905dcb57c0179be3732ff83b668bdc4`。
- [x] 最新候选包已通过密码 SSH 部署到第一台；归档 `SHA256SUMS` 全部通过，服务器 `/scert/scert` 摘要与候选包一致，`Scert.service` 和 `Scert-storage.socket` active。
- [x] 第一台运行边界复核通过：公网仅由 Nginx 提供 `80/443`，Scert 监听 `127.0.0.1:8443`，MySQL 仍为回环监听；没有新增外部业务端口。
- [x] 通过域名页面登录并只读核对超级管理员邮件配置：SMTP 已启用，`smtp.cdis.cn:587`、STARTTLS、系统通知用户名和收件人已保存，凭据已配置且未回显；未读取、记录或覆盖邮件密码。
- [!] 真实系统发信及收件箱送达尚未通过：网页公开密码恢复流程按设计不发验证码；本轮本机 `Scert-admin` 重置会话未形成有效请求并退出，未修改密码，也没有把失败结果误判为发信成功。
- [!] 服务启动日志仍保留历史中断部署事务缺少可信站点恢复基线，继续等待人工核查；未自动接管、删除锁或改写事务终态。
- [~] 待从真正保持前台终端的本机管理员会话重新验证验证码发送与密码重置闭环；随后继续三数据库首次 Web、DNS/ACME 正负向、真实证书部署回滚和公共域名严格 TLS 验收。

当前结论：第一台最新候选已运行，邮件配置已确认保存，但生产整体验收仍未通过；第二台仍只保留给后续证书推送、重载和回滚测试。

## 2026年10月4日15:27 第一台人工核查与验收门禁复核

### 实机证据

- 使用已核对主机指纹和密码 SSH 登录第一台 `8.137.23.95`，未使用或生成 SSH 密钥。`Scert`、`Scert-storage.socket`、`nginx` 均为 active。
- 第一台监听复核：Scert 为 `127.0.0.1:8443`，MySQL 为 `127.0.0.1:3306/33060`，公网监听仅 Nginx `80/443`。
- 第一台 `/ssl/odacm.cn/single/fullchain.pem` 证书为 `CN=odacm.cn`，SAN 包含 `DNS:odacm.cn`，有效期 `2026-10-02` 至 `2026-12-31`；严格 HTTPS `/readyz` 返回服务 ready。
- 通过正式 `systemctl start Scert-admin` 菜单 2 对两条事务分别完成只读核查：`20261003-142919.192475924-417b9bcc5b09` 和 `20261003-142924.300953335-fac63bc92b1f`。两条事务的三个目标文件、临时文件和备份文件均不存在，三个归属锁均存在且匹配。
- 只读核查未写入文件、未执行远端命令、未删除锁、未改变事务状态；两条事务仍为 `recovery_required`，不能据此宣称历史部署成功。

### 当前门禁

- [✓] 第一台服务、回环监听、Nginx 公网边界、正式证书和只读核查证据已具备。
- [!] 第一台 `/scert/conf/scert.json` 实际 `smtp_enabled=false`，`smtp_host` 和 `smtp_username` 为空；当前域名浏览器响应为第二台 Nginx，第二台页面配置不能替代第一台证据。
- [!] 因第一台 SMTP 未启用，`Scert-admin` 本机验证码发送、超级管理员改密和新密码 Web 登录尚未通过；没有修改现有密码。
- [~] 三数据库首次 Web、DNS/ACME、第二台证书推送/重载/不同版本回滚和最终公共域名矩阵待执行。

### 处理边界

不清理业务数据库，不删除历史锁，不伪造恢复基线，不重复第二台主程序测试，不创建辅助验收脚本，不使用 `-k` 或其他 TLS 绕过方式。整体生产验收保持未通过。

## 2026年10月4日15:36 两条历史事务安全处置实机结果

- 第一条事务 `20261003-142919.192475924-417b9bcc5b09` 通过正式本机菜单 3 选择“仅记录核查”，处置编号为 `f23507a439fb63e2c829ac9c01020af560ce46dd96b248da851991516195212d`。
- 第二条事务 `20261003-142924.300953335-fac63bc92b1f` 通过正式本机菜单 3 选择“仅记录核查”，处置编号为 `0f65a3d3c4fe8cb881e61e6e39f86747767a8e21421b9d6ca02ea46bf7ad208b`。
- 两次确认均返回“核查结论已记录；未改变原事务终态，不代表历史部署成功”。随后使用菜单 4 查询两个处置编号，结果一致。
- 本次未删除锁、未清理证书目录、未执行提交、回滚、服务重载或远端写操作；两条事务继续保持缺少可信恢复基线的历史待恢复状态。

## 2026年10月4日15:45 最新源码回归

- 后端 `go test ./...` 全部通过；后端 `go vet ./...` 退出零。
- 前端 `npm.cmd test -- --run` 共 338 项，全部通过，0 失败、0 跳过；`npm.cmd run build` 生产构建通过，生成 `index-CFcXnkqC.js` 与 `index-CfVlMv6G.css`。
- 本轮没有修改源码、没有清理业务数据库、没有改变线上配置；这些结果仅作为源码回归证据，不替代第一台实机 SMTP、密码重置和最终生产矩阵。

## 2026年10月4日15:52 两台 SMTP 配置落点与链路复核

- 第二台 `/scert/conf/scert.json` 的非敏感通知状态为 `smtp_enabled=true`、`smtp.cdis.cn:587`、STARTTLS、`notify:xuans@cdis.cn`；凭据引用存在但内容未读取。
- 第一台 `/scert/conf/scert.json` 的非敏感通知状态为 `smtp_enabled=false`，`smtp_host` 和 `smtp_username` 均为空。
- 第一台执行 `openssl s_client -starttls smtp -connect smtp.cdis.cn:587 -servername smtp.cdis.cn`，TLS 1.3 握手成功，服务端证书校验为 OK；未执行 AUTH，不能据此宣称真实邮件通过。
- 结论：之前浏览器页面显示的配置命中第二台 `nginx/1.18.0`，第一台 `nginx/1.30.5` 尚未保存 SMTP 配置。整体邮件门禁仍未关闭。

## 2026年10月4日 第二台部署包清理实机记录

- 通过已登记主机指纹和密码 SSH 登录第二台 `45.136.13.241`，先确认无安装器、解包或发布进程运行。
- 删除本轮残留事务目录 `/scert/.install-VSIoJ4Cj`，随后在 `/scert`、`/var/tmp`、`/tmp` 限定范围内复核，未发现 `Scert-20261004` 候选归档、`production-candidate` 解包目录、`.install-*` 或 `.restore-from-*` 残留。
- `/scert/data/scert.db`、`/scert/backups`、`/ssl/scert`、`/etc/nginx` 均仍存在；本次清理没有删除业务数据库、证书、备份或 Nginx 配置。
- 清理前后 Nginx 配置语法检查通过；Nginx 和 `Scert-storage.socket` 保持 active。旧版 `Scert.service` 仍因数据库版本 22 高于程序支持的版本 21 而失败。
- 结论：本次仅完成部署包和事务残留清理，未完成 SSL 推送验收，也未改变第二台仅作为推送目标的边界。

## 2026年10月4日 接手加速整改复核

### 当前任务状态

- [✓] 已阅读整改合并清单和任务执行清单，当前范围固定为 Linux/Web；第一台用于部署测试，第二台仅用于 SSL 推送测试。
- [✓] 当前源码回归通过：后端全量测试、`go vet`、前端 338 项测试、前端生产构建和差异检查均通过。
- [✓] 第一台真实服务复核通过：Scert、存储 Socket、Nginx active，Scert 和 MySQL 仅回环监听，`/readyz` 返回 ready。
- [✓] 第二台 Scert 自有数据、备份和日志已清空；第二台未安装当前候选包，部署包扫描为空。
- [!] 第一台 SMTP 仍关闭，验证码、邮件送达和本机重置闭环未通过。
- [ ] 第二台正式 SSL 推送、Nginx 校验与重载、故障回滚、恢复指纹和幂等重试未完成。
- [ ] 三数据库首次 Web、DNS/ACME 正负向和公共 HTTPS/SNI 未完成。
- [!] 历史恢复事务继续等待正式人工处置，未自动接管、删锁或伪造成功。
- [!] 当前记录不构成生产上线通过证明。

### 下一步

1. `[~]` 准备正式证书部署入口并选择普通业务域名。
2. `[ ]` 通过第一台正式 Web/API 流程执行双机 SSL 推送和结果记录。
3. `[ ]` 在第二台执行可控失败、回滚恢复和幂等重试验证。
4. `[!]` 用户在指向第一台的管理入口输入系统通知密码后，继续验证码和本机密码重置闭环。
5. `[ ]` 完成三数据库、DNS/ACME、公共 HTTPS 和最终复审。

## 2026年10月4日 19:22 第二台清理后的实时复核

- [✓] 实时 SSH 复核主机 `45.136.13.241`，主机身份沿用已登记指纹。
- [✓] `/scert`、`/var/tmp`、`/tmp` 未发现 `Scert-20261004*`、`Scert-*.tar.gz`、`production-candidate*`、`.install-*` 或 `.restore-from-*`。
- [✓] `/scert/data`、`/scert/backups`、`/scert/log` 仅保留目录；`/ssl/scert` 和 `/etc/nginx` 保留业务现场。
- [✓] `nginx -t` 通过；第二台没有监听 `8443`、`3306`、`33060` 的公网地址。
- [!] 第二台历史 `Scert.service` 仍为 failed，原因是数据库版本高于当前程序支持版本；本次未重新安装候选包，不将其写成恢复成功。

## 2026年10月4日 19:35 第一台候选包与事务复核

- [✓] 第一台 `/scert/scert` 主程序 SHA-256 与本地 `production-candidate-20261004-f5c425f` 包内文件一致。
- [✓] `Scert-admin` 前台菜单执行只读事务核查，未提交人工处置；两条历史事务仍保留需要恢复状态及锁现场。
- [!] 未执行自动接管、删锁、数据库终态改写或绕过 SMTP 的密码重置。

## 2026年10月4日 第二台项目现场清理实机记录

### 清理范围

- 目标主机：第二台 `45.136.13.241`，通过已登记主机身份完成 root SSH 操作。
- 移除内容：`/scert` 项目安装目录、Scert/`scert-nginx` systemd 单元、项目辅助程序、项目专用 Nginx 测试配置、项目证书版本和事务现场。
- 保留内容：PMail、共享 MySQL、Nginx、`/ssl/vcer.cn` 业务证书；重建空的 `/ssl/scert` 目录作为后续 SSL 推送目标。
- 未执行：数据库服务清库、PMail 数据修改、共享 MySQL 删除、非项目 Nginx 配置删除、第二台重新安装 Scert。

### 清理后证据

- `/scert`、`/etc/scert-nginx`、`/usr/local/libexec/scert-nginx`、`/var/lib/scert-nginx` 均不存在；Scert 相关 systemd 单元和进程均不存在。
- `/tmp`、`/var/tmp`、`/root`、`/home`、`/opt`、`/ssl` 范围未发现 `Scert-*.tar.gz`、`production-candidate*`、`.install-*`、`.restore-from-*` 或发布解包目录。
- `nginx -t` 通过并已 reload；Nginx、PMail、MySQL 均为 active。
- 无 `8443` 监听；MySQL `3306`、`33060` 仍只监听 `127.0.0.1`；`/ssl/vcer.cn` 文件保持存在，`/ssl/scert` 为空。

本节证明的是第二台项目现场清理完成，不证明正式 SSL 推送、失败回滚、恢复指纹、幂等重试或总体生产上线已通过。
## 2026年10月4日 第二台项目数据及部署包清理实时记录

### 清理范围

- 目标主机：第二台 `45.136.13.241`。
- 清理对象：Scert 项目证书数据、安装日志、项目专用 Nginx 测试配置、测试证书目录和项目部署残留。
- 保留对象：PMail、共享 MySQL、现有 Nginx、`/ssl/vcer.cn` 和 SSH 登录安全配置。

### 复核结果

- [✓] `/var/lib/scert`、`/var/tmp/scert-install-mysql-reentry-20261001.log`、`/etc/nginx/conf.d/scert-ssl-push-test.conf` 和 `/ssl/scert-multi-20260930.apmmk.cn` 已移除。
- [✓] 在 `/root`、`/tmp`、`/var/tmp`、`/opt`、`/srv`、`/etc` 和 `/usr/local` 的限定范围扫描中，未发现项目命名文件；部署包扫描为空。
- [✓] 无 Scert 进程、systemd 单元、`8443`/`8080`/`9000` 监听；`nginx -t` 成功并已 reload。
- [✓] PMail 25/465/587/993/995、MySQL 3306/33060 和 Nginx 80/443 仍在运行，未清理业务数据库。
- [ ] 正式 SSL 推送和失败回滚矩阵尚未执行，不能把本次清理记录写成生产上线验收。

本次为第二台现场清理证据，后续推送测试必须通过正式 Web/API 流程重新建立最小目标。

## 2026年10月4日 22:18 清理结果更正

- [✓] 第二台全限定范围发布物扫描未发现 `.tar`、`.tar.gz`、`.tgz`、`.zip` 等部署包；`/ssl/scert` 为空。
- [✓] Scert systemd 单元、Scert 进程和 `8443`/`8080`/`9000` 监听不存在；`nginx -t` 成功，Nginx、MySQL、PMail 为 active。
- [!] 失败事务的 3 个 `.scert-lock` 恢复锁目录仍在 `/ssl/acceptance-0928.odacm.cn/single/` 下，按产品安全边界保留，不能直接删除或改写。
- [!] 两份带有 Scert 标识的 SSH 配置仍在使用范围内，本轮未修改，以避免改变第二台的登录安全边界。

更正结论：第二台“部署包清理”已完成；受保护恢复锁和 SSH 安全配置不作为部署包处理，也不能据此宣称所有项目命名数据均已删除。

## 2026年10月4日 22:24 第一台实时服务复核

- [✓] 通过已登记 SSH 主机身份核对第一台 `8.137.23.95`，主机为 `ikun1`；`Scert.service`、`Scert-storage.socket`、Nginx、MySQL active。
- [✓] 端口边界符合预期：Scert `127.0.0.1:8443`，MySQL `127.0.0.1:3306/33060`，公网仅 Nginx `80/443`。
- [✓] 浏览器不强制 IP、不关闭证书校验，访问 `https://odacm.cn/readyz` 返回 `service=scert,status=ready`；Nginx SNI 证书为 `odacm.cn`，Let's Encrypt，有效期至 2026年12月31日。
- [!] `/scert/conf/scert.json` 显示第一台 `smtp_enabled=false`，`smtp_host` 和 `smtp_username` 为空；不将此前第二台的邮件配置当作第一台证据。
- [ ] 邮件、验证码、本机重置、新密码登录和正式证书推送仍未通过正式 Web 会话完成。

## 2026年10月4日 22:55 接手加速复核实机记录

### 当前证据

- [✓] 第一台 `8.137.23.95` 通过已登记 SSH 主机身份完成只读核对：主机为 `ikun1`，`Scert.service`、`Scert-storage.socket`、Nginx、MySQL 均为 active。
- [✓] 第一台配置非敏感状态核对为 `smtp_enabled=false`、`smtp_host` 为空、`smtp_username` 为空；未读取 SMTP 密码、秘密仓库或数据库凭据。
- [✓] 第一台公共登录页仍可访问；使用现有候选账号进行的错误密码尝试返回“账号或密码错误”，未修改密码、会话、数据库或服务配置。
- [✓] 前端 `npm.cmd test -- --run` 共 338 项通过，`npm.cmd run build` 生产构建通过；后端 `go test -count=1 ./...` 和 `go vet ./...` 通过。
- [✓] 遗留第一台只读 SSH 会话已关闭，未留下后台交互会话。

### 任务状态

- [!] 第一台真实系统通知、验证码、本机密码重置和新密码登录受有效超级管理员 Web 会话及 SMTP 凭据阻塞；不能用第二台配置、健康检查或数据库直改替代。
- [~] 第二台清理已完成，下一步仍需通过正式 Web/API 流程重新建立最小 SSL 推送目标。
- [ ] 正式证书推送、Nginx `-t`/reload、受控失败回滚、恢复指纹、幂等重试。
- [ ] 三数据库清库后首次 Web、DNS/ACME 正负向、公网 HTTPS/SNI 和最终生产复审。

本节不宣称总体生产上线通过；Windows 环境不在本轮范围。

## 2026年10月4日 23:36 第一台候选升级及第二台清理复核实机记录

### 第一台 `8.137.23.95`

- 本地候选归档 `Scert-20261004-V0.1.0-linux-amd64.tar.gz` SHA-256 为 `40e3fcf4f7ca1140f5c93ac9efb2c89a8ac414142a0ed8d74925b59bfac8e882`；上传第一台后远端归档哈希一致，包内校验清单全部通过。
- 第一次执行官方安装器因 `Scert-storage.socket` 仍为 active/listening 被安装器拒绝；随后仅停止 `Scert-storage.socket` 和其服务单元，第二次执行 `install-linux.sh --source-dir .` 成功。
- 升级后服务状态：`Scert.service`、`Scert-storage.socket`、`nginx`、`mysql` 均为 active。
- 升级后二进制摘要：`/scert/scert` 为 `366cefe24b785256871d1b0e3fe5f64c065dd2c3cddc1c40d4a25fa30c3dae93`；`/scert/scert-admin` 为 `242a497053d70581a9de2aff779d83f36b2e369425534b0a409ede057f4751d2`；`/scert/scert-storage` 为 `0aaef6f34110a986212c26ea421ea1464fc315076130e350dd2be145d52ced28`，均与候选包清单一致。
- 严格域名 HTTPS `https://odacm.cn/readyz` 返回 `{"service":"scert","status":"ready"}`；Scert 仅监听 `127.0.0.1:8443`，MySQL 仅监听 `127.0.0.1:3306/33060`，公网监听仍为 Nginx `80/443`。
- 本轮上传的 `/var/tmp/Scert-20261004-8c40a01-linux-amd64.tar.gz` 和 `/var/tmp/scert-linux-amd64-8c40a01` 已在核对后删除；`/scert` 下安装器历史 `.install-*` 回滚目录保留，未擅自删除。
- 当前 `/scert/conf/scert.json` 的非敏感通知状态仍为 `notification.smtp_enabled=false`、SMTP 主机和用户名为空；未读取 SMTP 密码，真实发信与重置闭环仍未通过。

### 第二台 `45.136.13.241`

- 只读 SSH 复核主机为 `yc`；`/scert`、`/etc/scert-nginx`、`/usr/local/libexec/scert-nginx`、`/var/lib/scert-nginx` 均不存在。
- `/ssl/scert` 为空；限定范围扫描 `/root`、`/tmp`、`/var/tmp`、`/opt`、`/srv`、`/etc`、`/usr/local` 未发现本项目归档、解包目录、安装临时目录或 `production-candidate*`。
- Scert 服务、辅助 Socket、Scert 进程和 `8443`/`8080`/`9000` 均不存在；`nginx -t` 成功，Nginx、MySQL、PMail 保持运行，邮件端口和 MySQL 回环端口未改变。
- `/ssl/acceptance-0928.odacm.cn/single/` 下 3 个 `.scert-lock` 恢复锁仍保留，按安全边界不删除、不改名、不伪造终态；它们不是部署包。

### 当前门禁

- [✓] 第一台当前候选升级、哈希、服务、监听和严格 HTTPS 就绪检查。
- [✓] 第二台项目部署包清理和共享业务保护复核。
- [!] 第一台 SMTP、真实系统发信、验证码、本机重置和新密码登录。
- [ ] 正式 SSL 推送、Nginx 重载、受控失败回滚、恢复指纹、幂等重试、三数据库首次 Web、DNS/ACME 和最终生产复审。

本节仅证明当前候选升级和清理复核证据，不宣称总体生产上线通过；Windows 环境不在本轮范围。

## 2026年10月4日 23:53 Smail 系统通知授权实机记录

- 通过已打开并保持登录的 `https://web.cdis.cn/` 超级管理员页面进入“设置-系统通知发信”。
- 账号授权表显示 `xuans`（admin）原为“未开启”；通过该页面开启后显示“已开启”。每分钟和每日收件人限额保持页面原有值，未扩大配额。
- 页面显示正式 SMTP 参数：服务器 `smtp.cdis.cn`，STARTTLS 端口 `587`，用户名 `notify:xuans@cdis.cn`，发件地址由系统固定为 `notification+system@cdis.cn`。
- 通过“重置发信凭据”确认流程生成新的独立通知凭据；凭据只用于后续第一台正式配置，未写入本地文件、仓库、文档、日志或截图，生成后已从页面隐藏。
- 第一台 `odacm.cn` 当前仍停留在登录页，没有有效 Scert 超级管理员会话，因此未把凭据通过非正式方式写入第一台；真实发信和密码重置仍未执行。

### 本节门禁

- [✓] Smail 系统通知账号授权和参数确认。
- [~] 第一台 Scert 正式 SMTP 配置，等待有效管理会话。
- [ ] 真实系统发信、验证码、本机重置、新密码登录、正式 SSL 推送及最终生产复审。

本节不记录任何通知凭据内容，不宣称第一台邮件或总体生产上线已通过。

## 2026年10月4日 Linux 加速整改实测记录

### 本地回归

- [✓] `backend` 执行 `go test -count=1 ./...`，全量通过。
- [✓] `backend` 执行 `go vet ./...`，通过。
- [✓] `frontend` 执行 `npm.cmd test`，338 项通过、0 失败、0 跳过。
- [✓] `frontend` 执行 `npm.cmd run build`，Vite 生产构建通过。

### 第二台 `45.136.13.241`

- [✓] 通过已核对主机身份的 root SSH 会话复核：`/scert`、`/etc/scert-nginx`、`/usr/local/libexec/scert-nginx`、`/var/lib/scert-nginx` 和 `/ssl/scert` 均不存在；空的 `/ssl/scert` 已按本轮清理授权移除。
- [✓] `/root`、`/tmp`、`/var/tmp`、`/opt`、`/srv`、`/etc`、`/usr/local` 限定范围未发现本项目部署包、解包目录或归档；Scert 进程、systemd 单元和 `8443/8080/9000` 监听均不存在。
- [✓] 删除两个项目专用 SSH 测试配置后 `sshd -t` 通过；没有重载 SSH。
- [✓] `nginx -t` 通过；PMail、共享 MySQL、现有 Nginx 和业务证书未修改。
- [✓] `/ssl/acceptance-0928.odacm.cn/single/` 下 3 个受保护 `.scert-lock` 目录及其 `owner` 文件仍存在，本轮未删除、改名或改写。

### 第一台 `8.137.23.95`

- [✓] `Scert.service`、`Scert-storage.socket`、Nginx、MySQL active；Scert 仅监听 `127.0.0.1:8443`，MySQL 仅监听回环地址。
- [!] 通过 `systemctl start Scert-admin` 进入正式本机管理菜单并选择密码重置后，服务报告“本机重置请求无效或已失效”；只读配置核对为 `smtp_enabled=false`、SMTP 主机和用户名为空。
- [!] 浏览器登录仍返回“账号或密码错误”；没有修改登录凭据，密码框已清空。
- [ ] 第一台正式 SMTP 配置、真实系统通知、验证码、本机重置和新密码登录。

### 本节结论

本轮 Linux 源码、前端和生产构建检查通过，第二台已清理项目部署痕迹且保留受保护恢复锁；第一台通知配置和正式管理会话仍未完成，不能宣称总体生产上线通过。Windows 环境不在本轮范围。
## 2026年10月4日 当前实机复核：第一台 SMTP 参数与 Smail 502

### 第一台 `8.137.23.95`

- [✓] 通过 `/scert/scert config set` 写入 SMTP 非敏感配置：主机 `smtp.cdis.cn`、端口 `587`、模式 `starttls`、系统通知用户名；明文认证保持关闭。
- [✓] 通过 `/scert/scert config validate` 复核配置结构通过。
- [!] 当前脱敏配置显示 `smtp_enabled=false` 且 SMTP 加密凭据引用为空；本次未读取或写入任何密码、授权码或秘密存储内容。

### Smail `web.cdis.cn`

- [!] 严格 HTTPS 探针返回 `502 Bad Gateway`；浏览器页面和系统通知相关接口同样不可用。
- [~] 待上游恢复后，使用正式页面获取授权凭据并通过受支持流程完成第一台 SMTP 配置；不以 502、TLS 握手或页面历史状态替代真实投递证据。

### 本节门禁

- [✓] 第一台非敏感 SMTP 参数写入与配置校验。
- [!] Smail 可用性和第一台加密凭据配置。
- [ ] 真实系统发信、验证码、本机重置、新密码登录、正式 SSL 推送及最终生产复审。

本节不记录任何通知凭据内容，不宣称第一台邮件或总体生产上线已通过。
## 2026年10月5日 Smail 恢复与上线门禁重排

### Smail 实时页面复核

- [✓] 清理浏览器缓存并重新加载 `https://web.cdis.cn/#/` 后，页面显示 Smail 收件箱，不再是历史 `502 Bad Gateway`。
- [✓] 当前页面显示账号为 `xuans`、角色为超级管理员；收件箱和设置入口可用。
- [✓] “设置-系统通知发信”页面加载成功，`xuans` 状态显示“已开启”；系统通知授权页面可继续进行正式配置核对。
- [✓] 历史 502 记录保留，不用新的 200 页面结果覆盖历史故障证据。

### 第一台 `8.137.23.95`

- [✓] 非敏感 SMTP 参数继续保持为 `smtp.cdis.cn:587`、STARTTLS、系统通知用户名，明文认证关闭。
- [!] 当前脱敏配置仍显示 `smtp_enabled=false`，加密凭据引用为空；本次浏览器复核没有读取或记录任何密码、授权码或令牌。
- [~] 待在第一台 root 受控终端交互录入敏感凭据后，执行配置校验、真实通知投递、验证码、本机重置和新密码登录。

### 第二台 `45.136.13.241`

- [✓] 继续保持无本项目部署包、无 Scert 运行实例和无专用监听，仅作为 SSL 推送测试目标。
- [ ] 正式 SSL 推送、`nginx -t`、reload、受控失败回滚、恢复指纹及幂等重试尚未执行。

### 当前门禁

- [✓] Smail 上游恢复和系统通知授权页面可用。
- [!] 第一台 SMTP 敏感凭据和有效 Scert 管理会话仍未完成。
- [ ] 真实系统发信、验证码、本机重置、新密码登录。
- [ ] 正式 SSL 推送与回滚矩阵。
- [ ] 三数据库首次 Web、DNS/ACME、公共 HTTPS/SNI 和最终生产复审。

本节仅更新当前实测进度，不宣称 Linux 项目已经上线通过；Windows 环境继续不纳入本轮。
## 2026年10月4日第一台实时复核补充

### 第一台 `8.137.23.95`

- `/scert/scert config validate`：通过。
- `https://odacm.cn/readyz`：严格 HTTPS 返回 `{"service":"scert","status":"ready"}`。
- 监听：Scert 为 `127.0.0.1:8443`，Nginx 为公网 `80/443`；未发现 `8080` 或 `9000` 专用监听。
- `nginx -t`：通过，仅有 `listen ... http2` 弃用警告，不影响语法通过。
- SMTP 非敏感配置：`smtp.cdis.cn:587`、STARTTLS、`notify:xuans@cdis.cn`，明文认证关闭。
- SMTP 网络复核：`smtp.cdis.cn` 解析到 `47.108.78.12`；从第一台连接 `25/465/587` 均被拒绝，STARTTLS 未建立。
- SMTP 结论：网络层服务不可达，尚未录入敏感凭据，未执行真实投递；本记录不包含任何密码、授权码或令牌。

### Smail 与管理会话

- Smail `https://web.cdis.cn/` 当前可用，`xuans` 系统通知授权显示“已开启”；授权码未查看、未复制。
- `https://odacm.cn/` 当前仍为登录页，未形成有效管理会话；未猜测密码、未直改数据库、未开放公网重置。

### 当前验收状态

- [✓] 第一台服务、HTTPS、Nginx 和 SMTP 网络层复核完成。
- [!] SMTP 提交服务可达性待恢复，真实通知和重置闭环不能开始。
- [✓] 第二台项目部署包清理完成。
- [ ] 第二台正式 SSL 推送、失败回滚和恢复指纹验证。
- [!] Linux 总体生产验收未通过。
## 2026年10月4日 Smail 接口与 SMTP 关联证据

- Smail 系统通知状态接口返回成功，非敏感配置确认 `smtp.cdis.cn`、提交端口 `587/465`、`xuans` 已启用。
- 同一会话对邮件列表、分组接口出现多次 `502 Bad Gateway`，浏览器控制台出现连接关闭错误。
- 第一台从 DNS 和 TCP 层复核到 `smtp.cdis.cn -> 47.108.78.12`，`25/465/587` 均连接被拒绝；因此不能把授权状态或页面可打开当作真实 SMTP 投递证据。
## 2026年10月4日 最新候选升级与 SMTP 网络测试记录

### 测试对象

- 第一台：`8.137.23.95`，部署测试。
- 第二台：`45.136.13.241`，仅 SSL 推送目标，不安装 Scert。
- 候选提交：`9cd8ce3`。
- 候选归档：`Scert-20261004-V0.1.0-linux-amd64.tar.gz`，SHA-256：`e7ad6fd9308c832aeb92763885ef5130cef5b5977c25f628a3d821b094fd7b5e`。

### 测试结果

- [✓] 包内 `SHA256SUMS` 全部通过。
- [✓] 第一台安装器在服务 active 时拒绝升级；停止服务与存储 Socket 后按正式安装流程重试成功。
- [✓] 第一台运行主程序摘要与候选包一致，`config validate` 通过。
- [✓] Nginx 配置检查通过，`Scert.service` 和 `Scert-storage.socket` active，严格 HTTPS `/readyz` 返回 ready。
- [✓] `smtp.cdis.cn` 解析到 `47.108.78.12`；587 STARTTLS 与 465 TLS 握手及证书校验通过。
- [!] 25 端口关闭；第一台 SMTP 凭据引用为空、`smtp_enabled=false`，认证发信和收件箱到达尚未验证。
- [✓] 第二台没有重新安装或上传 Scert，既有项目数据清理和受保护恢复锁边界不变。

### 验收结论

本次仅完成最新 Linux 候选升级和 SMTP 网络层复核，不把构建、TLS 握手、页面可访问或 readyz 当作生产上线通过。真实通知、验证码、本机重置、第二台正式证书推送回滚、三数据库首次 Web 和公共 HTTPS/SNI 仍待完成。
## 2026年10月4日 前端全量回归补充

- [✓] `npm.cmd test`：338 项通过，0 失败、0 跳过、0 todo。
- [✓] 本次候选包 Web 静态资源由同一提交构建并纳入 Linux amd64 归档。
- [!] 本地前端测试不等同于公网页面、SMTP 投递或证书推送生产验收。
## 2026年10月4日 当前现场实测补充：双机清理与第一台门禁

### 第二台 `45.136.13.241`

- [✓] 严格 SSH 主机校验后复核：无 Scert 进程、systemd 单元、专用监听、项目目录、安装临时物、部署包或归档。
- [✓] 根文件系统限定范围未发现 Scert、Certimate、Certd 项目文件；`nginx -t` 通过。
- [✓] `/ssl/vcer.cn` 既有业务证书和 `/ssl/acceptance-0928.odacm.cn/single/` 下受保护 `.scert-lock` 恢复目录保留；未修改 PMail、共享 MySQL 或现有 Nginx 业务配置。

### 第一台 `8.137.23.95`

- [✓] `Scert.service`、`Scert-storage.socket`、Nginx active；Scert 监听 `127.0.0.1:8443`，HTTPS `/readyz` 返回 `{"service":"scert","status":"ready"}`。
- [✓] `/scert/scert config validate` 通过；非敏感通知参数为 `smtp.cdis.cn:587`、STARTTLS，系统通知用户名已配置。
- [!] 脱敏状态为 `smtp_enabled=false`，SMTP 凭据引用为空；本次未读取、记录或猜测 SMTP 密码。
- [!] `Scert-admin` 最近一次受保护操作因请求无效或已失效退出，未产生密码、数据库或配置变更。

### Smail 浏览器证据

- [!] 当前打开 `https://web.cdis.cn/` 显示登录页，当前浏览器没有有效 Smail 管理会话；历史“页面可用”记录不作为当前授权凭据证据。
- [ ] 真实 SMTP 投递、收件箱到达、验证码、本机重置、新密码登录、第二台正式 SSL 推送与回滚仍待完成。

本节仅记录当前实测证据，不宣称整体 Linux 项目已经上线通过；Windows 环境不在本轮范围。
## 2026年10月5日 Linux整改复核记录

### 本轮验证结果

- [✓] 提交 `0788b6e` 已推送，Linux CLI 敏感输入改为交互式终端隐藏输入，拒绝命令行参数和管道输入。
- [✓] `go test -count=1 ./...`：后端全部通过。
- [✓] `go vet ./...`：通过。
- [✓] `npm.cmd test -- --run`：338 项通过，0 失败、0 跳过、0 todo。
- [✓] `npm.cmd run build`：Vite 生产构建通过。
- [✓] Linux amd64 `scert`、`scert-admin` 构建通过，CLI 测试编译通过。

### 双机任务状态

- [✓] 第一台 `8.137.23.95` 的现有服务边界、配置校验和候选部署证据继续保留；本轮未用本地构建结果替代实机验收。
- [✓] 第二台 `45.136.13.241` 无本项目部署包、解包目录、运行实例或专用监听；既有业务证书、PMail、共享 MySQL、现有 Nginx 配置和受保护 SSL 恢复锁未修改。
- [~] 第一台待交互式录入 SMTP 敏感凭据；录入后必须重新执行配置校验和真实通知投递。
- [ ] 第一台验证码、本机受保护重置、新密码登录和旧会话失效。
- [ ] 第二台正式 SSL 推送、`nginx -t`、reload、受控失败回滚、恢复指纹和幂等重试。
- [!] Linux 总体生产验收未通过。

### 验收边界

本轮结果只证明代码回归、构建和第二台清理状态；不证明第一台 SMTP 认证、真实邮件到达、验证码、密码重置、第二台 SSL 推送回滚或公网最终验收。敏感值未写入本记录。
## 2026年10月5日 实时凭据与第一台访问复核

### 当前实测

- [✓] `https://web.cdis.cn/` 登录页可访问，页面中文显示正常。
- [!] 使用当前任务记录中的账号进行一次正式登录尝试，接口返回 `errorNo=100`、`账号或密码错误`；未建立管理会话。
- [!] 第一台 `8.137.23.95` 的已登记 SSH 主机指纹匹配，但密钥认证被服务器拒绝；随后仅进行一次密码认证尝试并失败，已停止继续尝试。
- [✓] 未猜测密码、未直改数据库、未开放公网重置、未使用未核验主机指纹或不安全 TLS 绕过。

### 对上线任务的影响

- [!] 尚不能在第一台受控终端录入 SMTP 敏感凭据，因此真实通知、验证码和密码重置闭环不能开始。
- [ ] 需要由有权限人员提供正确的 Smail 管理凭据或第一台 SSH 受控登录方式；凭据不得发送到任务文档、日志、参数或仓库。
- [ ] 获得有效管理会话后，继续第一台通知投递、受保护重置和第二台 SSL 推送回滚矩阵。
- [!] Linux 总体生产上线门禁继续未通过。

本节仅记录当前实测阻塞，不记录任何密码、授权码、Cookie、令牌或私钥内容。
## 2026年10月5日 第二台实时清理复核记录

### 需求与任务状态

- [✓] 仅处理 Linux；第一台承担部署测试，第二台仅承担 SSL 推送测试，不安装本项目。
- [✓] 删除第二台残留的 `/run/scert-nginx`、`/run/scert-storage` 运行时目录及对应 systemd 传播目录。
- [✓] 清理后无 Scert 安装/加载单元、无专用监听、无本项目部署包或归档；限定目录的 `.tar`、`.tar.gz`、`.tgz`、`.zip` 扫描为空。
- [✓] `nginx -t` 通过，PMail、MySQL、Nginx 仍为 active；业务证书和三个受保护 `.scert-lock` 目录未修改。
- [ ] 第一台真实 SMTP、验证码、本机重置、新密码登录及旧会话失效。
- [ ] 第二台正式 SSL 推送、Nginx reload、受控失败回滚、恢复指纹和幂等重试。
- [!] Linux 总体生产验收未通过。

### 验收边界

本节证明第二台项目残留和部署包清理已完成，不证明 SSL 推送、失败回滚或总体上线已通过。Windows 环境不在本轮范围，敏感凭据未写入记录。

## 2026年10月5日 14:08 公网入口与第一台访问复核补充

### 本轮任务状态

- [✓] 第二台 `45.136.13.241` 的项目残留、运行时目录和部署包清理状态继续保持；未再次上传或安装本项目。
- [✓] `https://web.cdis.cn/` 严格 HTTPS 页面可访问，返回 HTTP 200；该结果仅证明 Smail 页面可访问，不替代 Scert 第一台管理会话和真实发信验收。
- [!] `https://odacm.cn/readyz` 当前严格 TLS 握手失败；当前解析环境通过 `1.1.1.1` 和 `8.8.8.8` 得到 `198.18.0.137`，不能作为正常公网管理入口验收证据。
- [!] 第一台 `8.137.23.95` 当前没有可用的受控 SSH 密钥会话；未继续猜测密码、未关闭 TLS 校验、未通过数据库或公网接口绕过管理流程。
- [~] 待获得第一台有效 root 受控登录或有效 Scert 管理会话后，继续 SMTP 敏感凭据录入、真实通知、受保护重置、第二台 SSL 推送与回滚。
- [!] Linux 总体生产上线门禁继续未通过。

### 验收边界

本节只记录当前公网和访问阻塞，不把页面 HTTP 200、构建结果或第二台清理结果扩大解释为整体上线通过；Windows 环境不在本轮范围，敏感凭据未写入文档。
## 2026年10月5日 Linux 本地回归复核补充

### 本轮验证结果

- [✓] 后端 `go test -p 1 -count=1 -timeout=12m ./...`：全部通过。
- [✓] 后端 `go vet ./...`：通过。
- [✓] 前端 `npm.cmd test -- --run`：338 项通过，0 失败、0 取消、0 跳过、0 todo。
- [✓] 前端 `npm.cmd run build`：Vite 生产构建通过。
- [✓] 本轮没有修改源码，没有重新上传或安装第二台服务器，不以本地回归结果替代生产实机验收。
- [~] 第一台受控访问、SMTP 真实投递、受保护重置和第二台正式 SSL 推送仍待外部访问条件满足。
- [!] Linux 总体生产上线门禁继续未通过。

### 验收边界

本节只证明当前工作区的后端、前端回归和生产构建状态；不证明第一台 SMTP、验证码、密码重置、第二台 SSL 推送回滚或公网 HTTPS/SNI 已完成。
## 2026年10月5日 第一台入口诊断补充

### 本轮实测

- [✓] 通过 Cloudflare 和 Google 公共 DoH 查询，确认 `odacm.cn` 公网 A 记录为 `8.137.23.95`。
- [✓] 第一台 `8.137.23.95:80` 可达，返回 `nginx/1.30.5` 和 `301`，将 `/readyz` 重定向到 HTTPS。
- [!] 第一台 `8.137.23.95:443` 的严格 TLS 握手失败，尚未取得证书或 HTTP 响应。
- [!] 对 443 端口发送普通 HTTP 请求返回 `Server: Beaver`、`403 Forbidden`，说明前置网关/TLS 路由仍未形成可验收的 `odacm.cn` HTTPS 入口。
- [!] SSH 主机指纹匹配且 22 端口可达；服务器当前仅继续提供密码认证，公钥认证被拒绝，未重复猜测或记录密码。

### 任务状态

- [✓] 公网 DNS 指向第一台已复核。
- [~] 第一台 HTTPS/SNI 前置路由和受控 SSH 登录仍待修复或提供有效授权。
- [ ] 第一台 SMTP 真实通知、验证码、受保护重置、新密码登录和旧会话失效闭环。
- [ ] 第二台正式 SSL 推送、Nginx reload、失败回滚和幂等验证。
- [!] Linux 总体生产上线验收继续未通过。

本节不记录密码、授权码、Cookie、令牌或私钥；不使用不安全 TLS 绕过方式，Windows 环境不在本轮范围。
## 2026年10月5日 当前接续实测记录

### 本轮结果

- [✓] `go test -p 1 -count=1 -timeout=12m ./...` 全部通过。
- [✓] `go vet` 之前已通过，本轮未修改源码；前端 `npm.cmd test -- --run` 338 项通过。
- [✓] 前端 `npm.cmd run build` 通过，`git diff --check` 通过。
- [✓] 第二台 `45.136.13.241` 清理状态保持通过，没有重新上传或安装本项目部署包。
- [!] 第一台 `8.137.23.95` 的 SSH 公钥认证仍被拒绝；单次密码认证返回 `Permission denied (password)`，已停止继续尝试。
- [!] 浏览器邮件通知页仍显示 SMTP 凭据尚未配置；未读取或记录密码，未发送测试邮件。
- [ ] 真实 SMTP 投递、收件箱到达、验证码、本机重置、第二台 SSL 推送和回滚仍待完成。
- [!] Linux 总体生产验收继续未通过。

### 证据边界

本节只记录本轮实际复核结果。构建、测试、页面可访问、端口可达和第二台清理结果均不能替代真实邮件投递、受保护重置、SSL 推送回滚或公网严格 HTTPS/SNI 验收。Windows 环境不在本轮范围，敏感凭据未写入记录。