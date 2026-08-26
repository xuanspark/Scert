# SSL 服务程序技术架构设计文档

- 项目名：Scert
- 关联需求文档：`SSL服务程序需求确认文档.md`
- 文档状态：技术架构草案，已按用户最新决策切换为 `lego`、MySQL/SQLite 双数据库和双 HTTPS 模式，等待代码实现复审
- 创建日期：2026-08-25
- 适用版本：第一阶段

> 本文档根据用户提供的技术选型草案和已经确认的需求整理形成。保留 Go、Gin、Cobra、MySQL、SQLite、Vue、Wails 等总体方向，并对与已确认需求冲突的部分进行调整。
> 
> 本文档暂不替代需求确认文档。代码目录、依赖锁定和实际平台测试完成后，才能冻结为发布基线。
>
> **架构决策变更：**第一阶段使用 `go-acme/lego`；多域名 SAN 纳入第一阶段；管理后台采用双 HTTPS 模式；通知通过外部 HTTP API 和 SMTP 适配器发送；数据库支持 MySQL 和 SQLite。

---

## 一、架构目标和边界

### 1.1 架构目标

1. 使用同一套核心业务服务支撑 CLI、Web 管理界面和桌面图形界面。
2. 使用 Let’s Encrypt 和 `go-acme/lego` 完成证书申请、DNS-01 验证和自动续期。
3. 在 Windows 和 Linux 上使用同一套业务模型、任务模型和配置模型。
4. 证书申请、续期、部署、校验和通知全部通过可恢复的持久化任务执行。
5. 证书私钥、DNS 凭据、SSH 私钥和 SMTP 凭据采用统一的敏感信息保护机制。
6. 第一阶段保持单实例、单数据库、单申请任务串行执行，暂不引入高可用集群复杂度。

### 1.2 第一阶段边界

第一阶段包含：

- Go 核心服务
- Gin Web API
- Vue Web 管理界面
- Cobra CLI
- Wails 桌面封装
- MySQL 和 SQLite 双数据库模式
- Let’s Encrypt + `go-acme/lego`
- DNS-01 和 DNS 服务商适配器
- CNAME 委派自动创建及失败手动提示
- SSH/SFTP 多服务器部署
- Nginx 及主流 Web 服务器、反向代理适配接口
- HTTP 和 SMTP 通知
- 外部邮件/消息服务 HTTP API 通知
- 用户自有证书上传和手动部署
- Windows Service、Linux systemd
- 审计日志、任务重试、部署回滚和数据库备份

暂不作为第一阶段默认实现：

- 高可用、多实例和集群部署
- ARM64
- 自定义 ACME CA
- SMTP、IMAP、POP3 邮件服务器端实现不属于 Scert 范围；Scert 仅通过外部邮件服务 API 或 SMTP 出站客户端发送通知

> **边界说明：**SMTP、IMAP、POP3 协议表仅作为外部邮件服务兼容性参考。Scert 不实现邮件服务器端，通知通过外部 HTTP API 和 SMTP 客户端适配器完成。

---

## 二、总体架构

### 2.1 逻辑架构

```text
+----------------------+       +--------------------------+
| Web 管理界面         |       | 桌面客户端（Wails）      |
| Vue + Vite           |       | 启动/监控本地核心服务   |
+----------+-----------+       +-------------+------------+
           | HTTP API                         |
           +----------------+----------------+
                            v
                 +--------------------------+
                 | Scert 核心服务           |
                 | Go + Gin                 |
                 +------------+-------------+
                              |
       +----------------------+----------------------+
       |                      |                      |
       v                      v                      v
+-------------+      +---------------+      +---------------+
| 业务服务层  |      | 任务调度/队列  |      | 认证与权限层  |
| 域名/证书   |      | 持久化任务      |      | 用户/角色/会话 |
| 续期/部署   |      | 单申请任务锁    |      | 审计记录       |
+------+------+      +-------+-------+      +---------------+
       |                     |
       +----------+----------+
                  v
         +--------------------+
         | 基础设施适配层      |
          | MySQL/SQLite / lego |
         | DNS / SSH / 通知     |
         | 文件系统 / HTTPS     |
         +--------------------+
```

### 2.2 进程组成

#### 核心服务进程

建议使用一个 Go 二进制提供以下子命令：

```text
scert init       初始化基础配置
scert serve      启动核心服务和 Web API
scert status     通过 API 查询服务状态
scert task       通过 API 查询和等待任务
```

核心服务启动后负责：

- 提供 Web API 和前端静态资源
- 执行认证、权限检查和审计
- 调度续期、部署、校验和通知任务
- 访问数据库和加密配置
- 调用 `lego`、DNS API、SSH/SFTP 和通知适配器

CLI 在初始化完成后不直接操作数据库。`init` 负责交互式填写基础配置；`status` 和 `task wait` 只提供非交互式状态查询和任务等待能力，不承担域名、证书、DNS、服务器或通知配置的日常管理。初始化后的日常管理统一通过 Web 管理界面完成。

#### 桌面客户端进程

桌面版不是第二套业务服务，而是：

1. 启动或连接本机 Scert 核心服务。
2. 检查核心服务健康状态。
3. 使用 Wails 加载同一套 Vue 页面。
4. 提供系统托盘、后台任务状态和退出/重启服务操作。
5. 根据用户选择配置开机自启动。

---

## 三、技术选型及优化结果

### 3.1 后端

| 模块 | 推荐选型 | 调整说明 |
|---|---|---|
| 编程语言 | Go | 保留，适合单二进制、跨平台服务和并发任务 |
| Web 框架 | Gin | 保留，用于 REST API、中间件和路由 |
| CLI | Cobra | 保留，CLI 作为核心服务 API 客户端 |
| 数据库 | MySQL 5.7 / MySQL 8.0 / SQLite | MySQL 5.7 作为基础开发和最低兼容版本，MySQL 8.0 作为兼容性测试版本；SQLite 用于单机、桌面和开发模式 |
| 数据库驱动 | `go-sql-driver/mysql`、`modernc.org/sqlite` | 使用 `database/sql` 统一访问；MySQL 和 SQLite 分别维护迁移脚本与方言适配 |
| 数据访问 | `database/sql` + Repository | 不强制使用 GORM，避免隐式查询和迁移行为；复杂查询使用明确 SQL，并通过数据库方言适配层处理差异 |
| 配置管理 | 类型化配置服务 + MySQL/SQLite | 不把 Viper 热加载作为运行时配置源；Viper 如保留，仅用于初始化文件、环境变量或命令行参数解析；敏感配置统一由加密配置服务管理 |
| 任务调度 | 持久化任务队列 + 内部调度器 | 不使用单纯 cron 承担任务状态、重试和恢复 |
| ACME | `go-acme/lego` 原生 Go 适配器 | 按用户最新确认，第一阶段使用 `lego` 完成申请、DNS-01 验证、续期和撤销 |
| SSH | `golang.org/x/crypto/ssh` | 密钥认证、主机指纹校验和远程命令执行 |
| SFTP | SSH 子系统适配器 | 用于安全上传证书文件，具体库在依赖验证阶段锁定 |
| 邮件 | HTTP API + SMTP 客户端适配器 | HTTP 调用外部邮件/消息服务 API，SMTP 支持明文连接限制、STARTTLS 和隐式 TLS；Scert 不实现 SMTP/IMAP/POP3 服务端 |
| 日志 | `log/slog` | 结构化日志，统一脱敏和审计字段 |
| 静态资源 | `embed` | 将 Vue 构建产物嵌入核心服务二进制 |
| 数据库迁移 | 版本化 SQL Migration | 启动时执行可追踪、可回滚的数据库迁移 |
| 图形界面 | Wails | 作为 Web 管理界面封装，不重复实现业务逻辑 |

### 3.2 运行时依赖原则

- 核心服务本身尽量保持单个 Go 可执行文件交付，证书申请和 DNS API 调用不依赖外部 Shell 或脚本运行时。
- `lego`、DNS provider、证书解析、任务调度和部署逻辑均作为 Go 模块或内部适配器实现，减少 Linux 与 Windows 的行为差异。
- 初始化阶段仍需要预检公网连通性、DNS API 权限、证书存储目录、系统时间和文件权限；这些预检必须使用 Go 实现，不依赖平台特定 Shell 命令。
- DNS 凭据只以受控配置对象传递到 provider，禁止出现在命令行参数、日志、任务错误详情和 API 响应中。
- 证书链和私钥校验优先使用 Go 标准库 `crypto/x509`；只有确实需要调用外部 Web 服务器检查命令时，才通过平台命令执行适配器运行白名单命令。
- Windows 和 Linux 的服务注册、文件权限、路径规范化和系统托盘仍由平台适配层分别实现。
- 安装、升级和卸载不得删除用户证书、数据库或备份；所有删除操作必须经过 Web/CLI 明确确认并写入审计日志。

### 3.3 前端

| 模块 | 推荐选型 | 说明 |
|---|---|---|
| 框架 | Vue | Web 和桌面版共享 |
| 构建 | Vite | 前端构建和开发服务器 |
| 组件库 | Naive UI | 管理后台表单、表格和状态展示 |
| 状态管理 | Pinia | 用户、域名、任务和系统配置状态 |
| 路由 | Vue Router | 页面路由和权限控制 |
| HTTP 客户端 | Axios 或封装后的 `fetch` | 统一处理认证、错误、任务轮询和超时 |

### 3.4 关键调整

#### 调整一：采用 `lego` 原生集成 ACME

根据用户最新确认，第一阶段改用 `go-acme/lego`。相比外部进程方案，`lego` 可以直接嵌入 Go 服务，减少 Shell 运行时依赖，并便于统一处理超时、取消、错误分类、日志脱敏和跨平台行为。

建议抽象为：

```go
type CertificateIssuer interface {
    Issue(ctx context.Context, request IssueRequest) (IssueResult, error)
    Renew(ctx context.Context, request RenewRequest) (RenewResult, error)
    Revoke(ctx context.Context, request RevokeRequest) error
}
```

第一阶段实现：

```text
LegoIssuer -> lego ACME client -> DNS provider adapter
```

`CertificateIssuer` 仍需屏蔽 CA、账户、DNS provider 和证书文件布局差异，避免后续接入其他 ACME CA 时改动业务层。

#### 调整二：使用持久化任务调度替代单纯每日 cron

需求不是简单的“每天运行一次”，而是：

- 常规每 3 天检查
- 剩余有效期低于 10 天时每天检查
- 剩余有效期低于 6 天触发续期
- 剩余有效期低于 3 天进入紧急重试
- 默认重试 3 次，每次间隔 30 分钟
- 服务重启后恢复任务状态
- 同一时间只运行一个证书申请任务

因此采用：

```text
调度器
  -> 创建 RenewalTask
  -> 数据库持久化
  -> Worker 获取任务租约
  -> 执行申请/部署/校验
  -> 保存状态、重试时间和错误
```

`robfig/cron` 可以作为周期触发器，但不能作为任务状态存储、重试管理和崩溃恢复机制。

#### 调整三：服务器和域名使用多对多关系

原方案在 `servers` 表中直接使用 `domain_id`，只能表达“一台服务器属于一个域名”。实际需求允许：

- 一个域名部署到多台服务器
- 一台服务器部署多个域名
- 每个域名在每台服务器上使用不同证书路径和 reload 命令

因此需要使用：

```text
domains
servers
 domain_servers
```

部署参数放在 `domain_servers` 关联表中，而不是只放在服务器基础信息表中。

#### 调整四：不把敏感配置放入普通 settings 键值表

普通配置和敏感配置分离：

- 普通配置：数据库中的类型化配置表
- 敏感配置：加密密文、密钥版本和轮换信息
- 密钥材料：由主密码派生的加密密钥解密
- 日志：只记录配置编号、版本和操作结果，不记录明文

---

## 四、核心模块设计

### 4.1 配置和密钥模块

职责：

- 管理初始化状态
- 管理管理后台域名、存储路径和通知配置
- 保存 DNS API、SSH 私钥、SMTP 密码等敏感信息
- 使用主密码解锁加密配置
- 支持敏感信息轮换
- 支持加密密钥备份和恢复校验

禁止：

- 在日志中输出明文密钥
- 在 API 响应中返回完整密钥
- 使用普通 settings 表保存明文凭据

### 4.2 证书生命周期模块

职责：

- 域名和证书类型校验
- 生产/开发环境分类
- 单域名和泛域名证书处理
- 证书和私钥匹配校验
- 证书链完整性校验
- 到期时间和域名匹配校验
- 证书版本保存、当前版本切换和历史版本保留
- 失败时保留原证书

第一阶段正式实现 SAN，证书以主域名作为管理归属，并保存完整的证书标识集合快照。

### 4.3 `lego` 适配模块

```text
CertificateService
        |
        v
LegoIssuer
        |
        +-- ACME directory adapter
        +-- DNS provider adapter
        +-- Account and certificate store
```

要求：

- 默认支持 Let’s Encrypt Production 和 Staging 两套 ACME Directory
- ACME 账户密钥、订单状态和证书版本分离保存，任务失败不能污染当前版本
- 每次执行使用独立任务上下文和证书版本目录
- 保存 CA 响应摘要、步骤状态和执行耗时
- 禁止把 DNS 密钥、账户私钥和挑战值写入普通日志
- 申请任务全局串行
- 支持超时、取消、重试和失败清理
- 申请成功后由 Go 代码完成证书解析、私钥匹配、版本保存和目录切换
- 不直接把 `lego` 的内部状态当作 Scert 对外证书目录
- DNS provider 通过显式接口注入，默认一个 provider 配置可以服务多个域名，支持域名级覆盖
- 将 ACME 错误分类为 DNS、限流、账户、网络、验证、证书解析和未知错误，供任务重试策略使用
- Windows 与 Linux 共用同一套 ACME 业务流程，仅在路径、权限和服务生命周期适配层区分

### 4.4 DNS 服务商模块

使用适配器：

```go
type DnsProvider interface {
    Validate(ctx context.Context, config ProviderConfig) error
    DetectZone(ctx context.Context, fqdn string) (ZoneInfo, error)
    CheckPermission(ctx context.Context, zone ZoneInfo) error
    UpsertTxt(ctx context.Context, record TxtRecord) error
    DeleteTxt(ctx context.Context, record TxtRecord) error
    CreateDelegation(ctx context.Context, delegation DelegationRecord) error
}
```

流程：

1. 检测 DNS Zone。
2. 检测 API 权限。
3. 检测是否支持 CNAME 委派自动创建。
4. 自动创建委派；失败时显示完整记录并提示手动创建。
5. 写入 TXT 记录。
6. 检测 DNS 传播。
7. 由 `lego` 完成 DNS-01 验证。
8. 任务结束后清理临时记录。

### 4.5 续期调度和任务模块

建议使用数据库任务表实现：

```text
Scheduler
  -> RenewalTask
       -> CertificateIssueStep
       -> DeploymentStep
       -> ValidationStep
       -> NotificationStep
```

任务必须包含：

- 任务编号
- 任务类型
- 当前状态
- 域名和证书版本
- 尝试次数
- 最大重试次数
- 下次重试时间
- 租约时间
- 当前步骤
- 错误分类
- 可展示错误信息
- 创建时间和完成时间

申请任务采用全局互斥锁；同一时间只能有一个证书申请任务运行。一个申请任务完成后，才能开始下一个申请任务。

### 4.6 部署执行器

部署采用“临时版本目录 + 校验 + 原子切换 + reload + 远程校验”的流程：

```text
1. 本地校验证书和私钥
2. 创建目标服务器临时目录
3. SFTP 上传证书、证书链和私钥
4. 设置目标文件权限
5. 执行 Web 服务器配置检查
6. 原子切换当前证书版本
7. 执行默认或自定义 reload 命令
8. 检查所有 A/AAAA 地址的 HTTPS 证书
9. 失败时回滚到旧版本
10. 保存部署节点结果
```

部署策略：

- 证书申请任务串行。
- 同一证书关联的多台服务器可以并行部署，但必须有并发上限。
- 每台服务器独立记录状态、错误、重试和回滚结果。
- 默认支持 Nginx，其他 Web 服务器通过适配器实现。
- 每台服务器独立配置 SSH 用户、端口、主机指纹、密钥、证书路径和 reload 命令。
- 远程 HTTPS 校验默认超时 2 分钟；按域名解析出的全部 A 和 AAAA 地址执行校验，并分别保存地址级结果。
- 远程部署路径必须在目标服务器上经过规范化和白名单校验，禁止使用未经校验的用户输入拼接 Shell 命令。

### 4.7 通知模块

通知模块只负责出站通知，不实现邮件接收服务：

```go
type Notifier interface {
    Send(ctx context.Context, message NotificationMessage) error
}
```

第一阶段保留：

- HTTP 通知适配器
- SMTP 出站客户端适配器

HTTP 通知适配器必须支持：

- 用户自定义请求地址
- Bearer Token、HMAC-SHA256 和无认证模式
- 详细事件和统一事件两种事件模式，默认使用详细事件
- 完整服务器部署明细、证书序列号、管理后台链接的可选发送
- 用户自定义请求头
- HTTP 2xx 成功判定
- 默认 10 秒超时和可配置重试
- `request_id` 幂等键

无认证模式仅用于开发、测试或受控内网场景；Token、签名密钥和密码禁止写入日志。

SMTP 连接策略按需求确认单执行：

- SMTP 接收连接信息可包含 25、465 端口及对应连接模式。
- SMTP Submission 使用 587 端口和 STARTTLS，不允许明文认证。
- Scert 不实现 SMTP、IMAP、POP3 服务端。
- IMAP/POP3 仅在确认需要作为外部服务检测功能时，再单独设计客户端适配器。
- 通知发送必须独立于证书部署任务，可以在证书任务完成后创建通知子任务；通知失败只重试通知，不回滚已成功的证书部署。
- 通知规则需要支持全局开关、按域名开关、事件类型开关、收件人覆盖和默认账户邮箱回退。

### 4.8 Web API 和认证模块

API 统一使用版本前缀：

```text
/api/v1/auth
/api/v1/domains
/api/v1/certificates
/api/v1/servers
/api/v1/tasks
/api/v1/settings
/api/v1/logs
/api/v1/backups
```

任务型接口使用异步返回：

```text
POST /api/v1/domains/{id}/renew
-> 202 Accepted
-> { "task_id": "..." }
```

前端通过任务查询接口轮询状态，不保持长时间 HTTP 请求。

认证建议：

- Web 使用 HttpOnly、Secure、SameSite 会话 Cookie。
- CLI 使用短期访问令牌或本地受保护的 API 凭据。
- 所有会话支持撤销、过期和强制退出。
- 登录失败锁定和角色权限在服务端统一执行。

### 4.9 域名配置页面设计

域名配置页面参考用户提供的表格布局设计，采用“域名列表汇总 + 服务器关联明细”的方式。这样既保留表格查看效率，又能正确支持一个域名部署到多台服务器、每台服务器使用不同路径和 SSH 配置的需求。

#### 4.9.1 页面主列表

页面标题建议为：

```text
域名配置
```

顶部操作区包含：

- 添加域名
- 搜索域名
- 按证书类型筛选
- 按部署环境筛选
- 按证书状态筛选
- 刷新状态

主列表字段参考用户图片：

| 页面显示字段 | 页面含义 | 展示规则 |
|---|---|---|
| 域名 | 托管域名 | 显示规范化后的域名；支持点击进入详情 |
| 证书类型 | 单域名、泛域名或后续 SAN | 使用标签显示；泛域名显示 `*.example.com`，不包含根域名 |
| 部署环境 | 生产或开发 | 使用颜色标签区分生产和开发，生产环境需要二次确认危险操作 |
| 部署服务器 IP | 关联的目标服务器 | 一行显示服务器数量和 IP 摘要；多台服务器使用标签或“共 N 台”显示，点击展开明细 |
| SSH 密钥 | 服务器 SSH 密钥引用 | 只显示密钥名称、指纹末尾和校验状态，不显示私钥内容 |
| 部署位置 | 远程证书部署路径 | 显示默认路径或“多个路径”；点击后查看每台服务器的证书、私钥和链文件路径 |
| 续签时间 | 下一次计划检查/续期时间 | 显示计划时间；详情中同时显示自动续期阈值，默认剩余有效期低于 6 天 |
| 紧急续签时间 | 紧急续期计划和触发条件 | 显示紧急任务时间或待触发状态；详情中显示紧急阈值，默认剩余有效期低于 3 天 |
| 状态 | 证书、部署和任务综合状态 | 显示有效、即将到期、续期中、部署失败、部分成功等状态 |
| 操作 | 页面操作 | 详情、编辑、手动续期、手动部署、查看日志、删除 |

> **时间字段说明：**现有需求使用“剩余有效期阈值”定义续期策略，而参考表格使用“续签时间”列。页面建议同时展示“下一次计划时间”和“阈值”，例如：`2026-09-01 02:00（剩余 6 天）`，避免用户误以为续期只在固定日期执行。

#### 4.9.2 多服务器展开明细

域名列表不把多个服务器强行拼接成一条长文本。点击服务器信息后，使用展开行或右侧抽屉显示：

| 明细字段 | 说明 |
|---|---|
| 服务器名称 | 用户自定义名称，便于区分不同节点 |
| 服务器 IP/主机名 | SSH 连接地址 |
| SSH 端口 | 默认 22，可单独覆盖 |
| SSH 用户 | 远程登录用户 |
| SSH 密钥 | 密钥引用、指纹和验证状态 |
| 主机指纹 | 已确认、待确认或指纹变化 |
| Web 服务类型 | 默认 Nginx，也可选择其他适配器或自定义 |
| 证书部署目录 | 远程证书目录 |
| 证书文件路径 | 远程证书文件 |
| 证书链文件路径 | 远程链文件 |
| 私钥文件路径 | 远程私钥文件 |
| reload 命令 | 默认命令或用户自定义命令 |
| 部署状态 | 成功、失败、回滚中、未部署 |
| 最近部署时间 | 最近一次部署任务完成时间 |

每台服务器独立保存部署状态、错误、重试次数和回滚结果，不能因为同一域名的其他服务器部署成功就覆盖失败节点的状态。

#### 4.9.3 添加/编辑域名表单

表单建议分为四个区域，避免把所有配置堆在一个页面中：

**一、域名基本信息**

- 域名：必填，校验 FQDN 格式和重复配置
- 证书类型：第一阶段支持单域名、泛域名和多域名 SAN 证书；SAN 证书保存完整域名集合，并按证书标识和版本管理
- 部署环境：生产、开发
- 是否启用自动续期：默认启用 ACME 证书，用户上传证书默认关闭
- ACME 环境：Production 或 Staging

**二、DNS 验证配置**

- DNS 服务商配置：选择已保存的 DNS 配置
- 允许域名级覆盖：需要时覆盖全局 DNS 配置
- CNAME 委派：自动创建、失败手动提示
- DNS 传播检测超时
- 测试 DNS 权限和 Zone

**三、部署服务器配置**

- 添加一台或多台部署服务器
- 选择已有服务器或创建服务器配置
- 选择 SSH 密钥引用
- 配置远程证书、证书链和私钥路径
- 选择 Web 服务类型
- 配置默认或自定义 reload 命令
- 测试 SSH 连接、主机指纹和远程目录权限
- 设置部署顺序或并发组

**四、续期和通知配置**

- 常规检查间隔：默认每 3 天
- 临近到期检查：剩余有效期低于 10 天时每天检查
- 自动续期阈值：默认剩余有效期低于 6 天
- 紧急重试阈值：默认剩余有效期低于 3 天
- 最大重试次数：默认 3 次
- 重试间隔：默认 30 分钟
- 通知类型：续期、部署、校验、错误、通知发送失败
- 收件人：默认账户绑定邮箱，可按域名覆盖

保存域名时先执行前端格式校验，再由后端执行域名、证书类型、DNS 配置、部署服务器和路径安全校验；保存成功后不立即执行续期或部署，用户需通过明确操作触发。

#### 4.9.4 页面操作规则

| 操作 | 行为 |
|---|---|
| 添加域名 | 打开域名配置表单，保存后返回列表 |
| 编辑 | 修改普通配置和服务器关联，敏感信息只允许重新录入或更换引用 |
| 手动续期 | 创建异步续期任务，返回 `task_id`，不阻塞页面 |
| 手动部署 | 选择证书版本和目标服务器，创建异步部署任务 |
| 查看详情 | 显示证书、DNS、服务器、任务、日志和通知配置 |
| 查看日志 | 按域名、服务器和任务过滤审计日志 |
| 删除 | 先停止关联任务，再二次确认删除数据库记录和历史证书文件 |

生产环境的手动续期、手动部署、删除和覆盖当前证书操作必须显示风险确认；失败时保留原证书并在页面展示回滚结果。

#### 4.9.5 页面响应式设计

- 桌面端使用表格布局，保持用户参考图中的横向字段组织方式。
- 低宽度窗口将“部署服务器 IP、SSH 密钥、部署位置”合并为“部署配置”列，通过展开行查看详细信息。
- 移动端使用卡片布局，每张卡片显示域名、证书类型、环境、到期状态和操作按钮，服务器明细使用抽屉展示。
- 表格不显示私钥、DNS API Token、SMTP 密码或完整 SSH 密钥内容。

#### 4.9.6 页面与 API 映射

域名配置页面只通过 Web API 访问业务服务，不直接操作 MySQL 或 SQLite：

```text
GET    /api/v1/domains
POST   /api/v1/domains
GET    /api/v1/domains/{id}
PATCH  /api/v1/domains/{id}
DELETE /api/v1/domains/{id}

GET    /api/v1/domains/{id}/servers
POST   /api/v1/domains/{id}/servers
PATCH  /api/v1/domains/{id}/servers/{serverId}
DELETE /api/v1/domains/{id}/servers/{serverId}

POST   /api/v1/domains/{id}/renew
POST   /api/v1/domains/{id}/deploy
POST   /api/v1/domains/{id}/test-dns
POST   /api/v1/domains/{id}/test-servers
```

`GET /api/v1/domains` 返回列表汇总字段，至少包含域名、证书类型、部署环境、服务器数量、服务器 IP 摘要、证书到期时间、下一次检查时间、自动续期阈值、紧急重试阈值和综合状态。服务器密码、SSH 私钥、DNS 凭据和 SMTP 密码不得出现在响应中。

续期、部署、DNS 测试和服务器测试均采用异步任务返回；配置保存接口只保存配置，不隐式触发证书申请或部署。

---

## 五、数据库设计调整

数据库支持 MySQL 和 SQLite 两种运行模式。MySQL 5.7 作为基础开发和最低兼容版本，MySQL 8.0 作为兼容性测试版本；SQLite 用于单机、桌面和开发模式。所有时间统一使用 UTC 存储，界面按用户时区显示。

数据库实现要求：

- 使用 `database/sql` 和 Repository 层统一业务访问。
- MySQL 与 SQLite 分别维护迁移脚本，禁止把 SQLite 专用语句直接用于 MySQL。
- 事务、分页、时间、布尔值、JSON 和自增主键等差异通过数据库方言适配层处理。
- SQLite 模式可以启用 WAL、busy timeout 和外键约束；MySQL 模式使用连接池、事务隔离和外键约束。
- 本机存在两套 MySQL 安装，开发配置必须通过明确的实例地址、端口和 DSN 区分 MySQL 5.7 与 MySQL 8.0，不根据 PATH 中的单个命令推断数据库版本。

本机开发环境的客户端版本已通过完整安装路径分别确认：

| 安装路径 | 检测结果 | 用途 |
|---|---|---|
| `E:\DB\5.7\bin\mysql.exe` | MySQL 5.7.44 | 基础开发和最低兼容性验证 |
| `E:\DB\8.0\bin\mysql.exe` | MySQL 8.0.45 | 兼容性测试 |

以上仅记录客户端版本确认结果；实际数据库连接必须继续使用明确的主机、端口、数据库名、账号和 DSN，禁止通过 PATH 中的 `mysql` 命令作为版本判断依据。

### 5.1 domains

| 字段 | 说明 |
|---|---|
| id | 主键 |
| domain_name | 托管域名，唯一 |
| environment | `production` 或 `development` |
| certificate_type | `single` 或 `wildcard` |
| status | 域名管理状态 |
| renewal_enabled | 是否启用自动续期 |
| check_interval_days | 常规检查间隔，默认 3 天 |
| near_expiry_check_days | 临近到期检查阈值，默认 10 天 |
| renewal_threshold_days | 自动续期阈值，默认 6 天 |
| emergency_threshold_days | 紧急重试阈值，默认 3 天 |
| next_check_at | 下一次计划检查时间 |
| next_renewal_at | 下一次计划续期时间，可为空 |
| notification_rule_id | 域名级通知规则引用 |
| created_at | 创建时间 |
| updated_at | 修改时间 |

### 5.2 certificates

| 字段 | 说明 |
|---|---|
| id | 证书逻辑主键 |
| domain_id | 关联域名 |
| source | `acme` 或 `uploaded` |
| current_version_id | 当前使用版本 |
| auto_renew | 是否参与自动续期 |
| created_at | 创建时间 |
| updated_at | 修改时间 |

### 5.3 certificate_versions

| 字段 | 说明 |
|---|---|
| id | 版本主键 |
| certificate_id | 关联证书 |
| version_name | 版本标识 |
| certificate_path | 证书文件路径 |
| chain_path | 证书链路径 |
| private_key_path | 私钥路径或加密存储引用 |
| fingerprint | 证书指纹 |
| issued_at | 签发时间 |
| expires_at | 到期时间 |
| status | `current`、`history`、`failed`、`rolled_back` |
| created_at | 创建时间 |

### 5.4 servers

保存服务器基础信息：

- 主机地址
- SSH 端口
- SSH 用户
- 主机指纹
- 密钥引用
- 启用状态

### 5.5 domain_servers

保存域名和服务器关联配置：

- domain_id
- server_id
- 远程证书目录
- 远程证书文件名
- 远程私钥文件名
- reload 命令
- Web 服务器类型
- 部署顺序或并发组
- 是否启用部署
- 最近部署状态、错误信息和最近部署时间

### 5.6 dns_credentials

保存 DNS 服务商和加密认证信息：

- provider_type
- account_name
- encrypted_secret_ref
- zone_scope
- enabled
- key_version
- created_at
- rotated_at

### 5.7 tasks、task_attempts

任务和任务尝试分离，支持：

- 续期任务
- 部署任务
- 校验任务
- 通知任务
- 重试次数
- 错误分类
- 下次执行时间
- 任务租约
- 节点级结果

### 5.8 users、sessions、audit_logs

用于支持：

- 超级管理员、管理员、普通用户
- 登录会话和强制退出
- 登录失败锁定
- 30 分钟无操作退出
- 重要操作审计
- 安全审计报告

### 5.9 app_settings、notification_channels、notification_recipients

用于保存普通运行配置和通知规则：

- `app_settings`：管理后台域名、HTTPS 模式、存储根路径、时区、检查间隔、重试次数、重试间隔和初始化状态等普通配置。
- `notification_channels`：HTTP 或 SMTP 通道、启用状态、地址、端口、连接模式、发件人显示名称、发件人地址和加密认证信息引用。
- `notification_recipients`：全局收件人、域名级收件人、启用状态和排序。
- `notification_rules`：续期、部署、校验、通知失败等事件的开关和通知级别。

SMTP 连接模式必须使用枚举值，不允许仅通过端口号推断：

```text
plain、starttls、implicit_tls
```

同时保存“是否允许明文认证”的显式策略，587 端口默认禁止明文认证。

### 5.10 backups、recovery_materials、acme_profiles

- `backups`：数据库备份、配置导出、加密备份版本、创建时间、校验值和恢复结果。
- `recovery_materials`：超级管理员专用恢复材料的版本、状态、使用时间和审计引用；不保存可直接登录的明文恢复凭据。
- `acme_profiles`：ACME CA、目录地址、生产/测试环境、账户引用和启用状态。第一阶段默认启用 Let’s Encrypt Production 和 Staging，其他 CA 只保留扩展字段。

### 5.11 SAN 扩展数据边界

SAN 第一阶段新增并启用：

- `certificate_identifiers`：证书关联的 DNS 名称、类型（精确域名或通配符）和排序。
- 申请任务保存完整标识集合快照，避免域名配置变更后无法追溯历史申请内容。
- 目录名称使用稳定的证书逻辑标识，不直接使用未经规范化的用户输入。

SAN 进入默认 UI、自动续期、证书版本保存、部署关联和验收路径。证书以主域名作为管理归属；域名集合变化时创建新的证书标识和证书版本，不覆盖原有版本。

---

## 六、文件和证书目录

逻辑目录：

```text
{storage_root}/{domain}/
├── production/
│   ├── wildcard/
│   │   ├── current/
│   │   └── versions/{version}/
│   └── single/
│       ├── current/
│       └── versions/{version}/
└── development/
    ├── wildcard/
    │   ├── current/
    │   └── versions/{version}/
    └── single/
        ├── current/
        └── versions/{version}/
```

每个版本目录至少包含：

```text
cert.pem
chain.pem
fullchain.pem
privkey.pem
metadata.json
```

安全要求：

- Linux 私钥文件使用 600 或更严格权限。
- Windows 使用仅服务账户和授权管理员可读的 ACL。
- `current` 可以使用目录切换或受控复制，不直接覆盖正在使用的版本。
- 删除域名时先停止相关任务，再删除数据库记录和证书文件。
- 用户上传证书默认保留当前版本和一个最近历史版本。

### 6.1 跨平台默认路径

需求中的 `/ssl` 作为 Linux 默认证书根路径保留，安装、配置、日志和备份目录分别固定；Windows 使用用户选择的安装根目录进行平台适配：

- Linux 默认：`/ssl`
- Linux 安装目录：`/scert`
- Linux 配置目录：`/scert/conf`
- Linux 日志目录：`/scert/log`
- Linux 备份目录：`/scert/backups`
- Windows 证书目录：`{安装目录}\Scert\ssl`
- Windows 数据库目录：`{安装目录}\Scert\data`
- Windows 配置目录：`{安装目录}\Scert\config`
- Windows 日志目录：`{安装目录}\Scert\logs`
- Windows 备份目录：`{安装目录}\Scert\backups`
- 用户可以在初始化或 Web 管理界面中修改
- 界面必须显示实际解析后的绝对路径

Linux 服务安装时创建专用 `Scert` 服务运行用户，仅用于运行核心服务；授予该用户专门管理 `/ssl` 或用户自定义证书目录的权限。私钥文件默认使用 600 或更严格权限，`Scert` 与 `root` 共同受控管理。Windows 使用等效 ACL 权限保护私钥。

---

## 七、HTTPS 和服务部署拓扑

### 7.1 当前明确的要求

- 管理后台正式访问使用 HTTPS。
- 初始化期间允许使用 HTTP 访问初始化界面。
- 前端和后端均需纳入 HTTPS 安全部署。
- HTTPS 采用双模式，默认模式为核心服务直接终止 HTTPS，高级模式为 Nginx/Caddy 终止公网 HTTPS、核心服务使用内部 HTTPS。

### 7.2 支持的部署模式

```text
浏览器 / 桌面 WebView
          |
       HTTPS 443
          v
+------------------------+
| HTTPS 入口层           |
| 反向代理或服务网关     |
+-----------+------------+
            |
       内部 HTTPS
            v
+------------------------+
| Scert 核心服务         |
| Gin API + Vue 静态资源 |
+------------------------+
```

架构需要同时兼容以下两种模式，默认使用模式 A，高级配置使用模式 B：

**模式 A：核心服务直接终止 HTTPS**

- 核心服务监听 443 并加载管理域名证书。
- 初始化期间监听 80，仅开放受控初始化入口或跳转到 HTTPS。
- 适合单机、Windows 和不额外部署反向代理的环境。

**模式 B：反向代理终止 HTTPS**

- 入口层监听 443，负责公网 TLS。
- 核心服务仍使用 HTTPS 监听内部端口，避免前后端之间退化为明文连接。
- 适合已有 Nginx、Caddy 或其他网关的服务器环境。

采用模式 B 时，公网 HTTPS 入口与核心服务分离可以：

- 管理证书自动更新不影响核心服务进程
- 核心服务不直接暴露公网监听
- 前后端统一受入口层保护
- 后续可扩展访问控制和安全策略

桌面版本地服务、CLI 初始化和没有额外反向代理的 Windows 环境默认使用模式 A；用户配置高级模式后使用模式 B。初始化流程必须明确当前模式、监听地址、证书来源和证书切换方式。

### 7.3 初始化特殊流程

```text
1. 启动核心服务
2. 检查初始化状态
3. 未初始化时只开放受控初始化入口
4. 完成管理员、DNS、存储和通知配置
5. 使用 Let’s Encrypt + `lego` 申请管理域名证书
6. 完成本地证书校验和部署
7. 启用正式 HTTPS 访问
8. 关闭或保护初始化入口
```

初始化入口必须防止重复初始化和未授权重新配置。

---

## 八、任务执行和可靠性

### 8.1 任务执行原则

- 所有耗时操作必须异步任务化。
- 所有任务必须持久化。
- 服务重启后根据租约和状态恢复任务。
- 申请任务全局串行。
- 部署节点可以有限并行。
- 每个外部调用必须设置超时。
- 所有重试必须记录原因和下一次执行时间。
- 任务操作必须具备幂等性，避免重复部署或重复通知。

### 8.2 失败处理

| 阶段 | 失败处理 |
|---|---|
| DNS 检测 | 标记 DNS 错误，保留原证书，不进入部署 |
| ACME 申请 | 按任务重试策略重试，超过次数后发送摘要通知 |
| 本地证书校验 | 丢弃新版本，保留当前证书 |
| 单台服务器部署 | 该节点回滚并记录失败，其他节点继续或按策略停止 |
| 远程 HTTPS 校验 | 回滚该节点，任务标记失败或部分成功 |
| 全部部署失败 | 保持原版本，发送失败通知 |
| 通知发送失败 | 通知任务独立重试，不回滚已成功的证书部署 |

---

## 九、安全架构

### 9.1 身份和权限

- 管理员密码使用安全密码哈希。
- Web 会话使用 HttpOnly、Secure、SameSite Cookie。
- 所有写操作执行角色权限检查。
- 普通用户不能查看其他用户的敏感配置。
- 续期、部署、导入、恢复和密钥备份操作全部记录审计日志。

### 9.2 敏感信息

需要加密保护：

- DNS API 凭据
- SSH 私钥
- SMTP 密码
- HTTP 通知认证信息
- 加密配置主密钥相关材料

敏感信息不允许：

- 出现在普通日志
- 出现在任务错误详情
- 出现在 API 列表响应
- 出现在 Git、脚本参数或命令回显中

### 9.3 远程主机安全

- 首次连接记录 SSH 主机指纹。
- 指纹变化时阻止自动部署并要求管理员确认。
- 不使用服务器登录密码。
- 远程命令和目标路径执行白名单或格式校验。
- 证书文件上传禁止路径穿越。

---

## 十、API 设计原则

### 10.1 API 版本和任务响应

- 使用 `/api/v1` 版本前缀。
- 修改和执行操作返回任务编号。
- 重复请求支持幂等键。
- 任务状态通过查询接口获取。
- 统一返回错误代码、用户可读信息和审计编号。

### 10.2 API 分组

```text
/api/v1/auth
/api/v1/users
/api/v1/domains
/api/v1/certificates
/api/v1/servers
/api/v1/dns-providers
/api/v1/tasks
/api/v1/notifications
/api/v1/settings
/api/v1/logs
/api/v1/backups
```

### 10.3 CLI 与 API

CLI 不直接访问数据库：

```text
scert init       本地初始化向导
scert serve      启动服务
scert status     API 查询状态
scert task wait  API 等待任务
```

如果后续保留域名增删、手动续期和部署 CLI 命令，也必须作为 API 客户端实现，不得复制业务逻辑。

---

## 十一、运行和打包

### 11.1 Linux

- 核心服务以 systemd 服务运行。
- 服务账户使用最小权限。
- 数据目录、证书目录、日志目录分离。
- 使用 systemd 重启策略和启动前权限检查。

### 11.2 Windows

- 核心服务以 Windows Service 运行。
- 服务账户使用最小权限。
- 敏感目录配置 ACL。
- 桌面版通过 Wails 启动或连接服务。
- 开机自启动必须由用户选择启用。

### 11.3 构建产物

建议拆分为：

```text
scert.exe / scert       CLI 和服务主程序
scert-desktop.exe       Wails 桌面程序
web assets               构建后嵌入核心服务
service installer       Windows/Linux 安装脚本或安装包
```

“单文件部署”只适用于核心服务二进制和前端静态资源；证书存储、数据库、日志、服务注册和系统权限仍属于运行时资源，不能简单视为全部包含在单文件中。

---

## 十二、开发阶段建议

### 阶段一：架构和基础设施

- [x] 已确认 Go 版本和构建矩阵：以当前开发电脑工具链为开发基线，目标构建架构为 amd64
- [x] 已确认数据库访问和迁移方式：使用 `database/sql`、Repository 和 MySQL/SQLite 独立迁移脚本
- [x] 已确认 HTTPS 终止拓扑：默认由核心服务直接终止 HTTPS，高级模式由 Nginx/Caddy 终止公网 HTTPS，核心服务使用内部 HTTPS
- [ ] 完成 Windows 与 Linux 的 `lego`、DNS API、文件权限和服务生命周期兼容性验证
- [x] 已确认 DNS 第一批适配器：Cloudflare、阿里云 DNS、DNSPod、CNAME 委派、华为云、腾讯云、AWS Route 53、Google Cloud DNS，并预留其他服务商扩展
- [x] 已确认 SMTP/IMAP/POP3 功能边界：协议表仅作外部邮件服务兼容性参考，Scert 不实现邮件服务器端
- [ ] 完成领域模型、任务状态机和 API 契约

### 阶段二：证书申请

- [ ] 实现 `lego` ACME Issuer 和 DNS provider 适配器
- [ ] 接入 Let’s Encrypt Staging
- [ ] 接入 Let’s Encrypt Production
- [ ] 建立 ACME CA 配置抽象；第一阶段仅启用 Let’s Encrypt，其他 CA 暂不纳入验收
- [ ] 实现 DNS 适配器和 CNAME 委派
- [ ] 实现证书解析、校验和版本保存
- [ ] 实现 SAN 标识集合快照和目录规则

### 阶段三：续期、部署和通知

- [ ] 实现持久化调度器
- [ ] 实现申请任务全局串行锁
- [ ] 实现 SSH/SFTP 和主机指纹校验
- [ ] 实现 Web 服务器部署适配器
- [ ] 实现回滚和远程 HTTPS 校验
- [ ] 实现 HTTP/SMTP 通知和独立重试

### 阶段四：管理端

- [ ] 实现认证、角色和会话
- [ ] 实现 Web API
- [ ] 实现域名配置页面：列表汇总、服务器展开明细、添加/编辑表单和危险操作确认
- [ ] 实现域名、证书和服务器页面
- [ ] 实现任务、日志和通知页面
- [ ] 实现初始化向导

### 阶段五：平台和桌面端

- [ ] 实现 Linux systemd
- [ ] 实现 Windows Service
- [ ] 实现 Wails 桌面封装
- [ ] 实现系统托盘和后台任务状态
- [ ] 完成基础平台测试：Windows 10/11、Ubuntu 22.04 + Nginx
- [ ] 完成扩展平台测试：Ubuntu 24.04 + Nginx、CentOS/Rocky Linux、Debian、Caddy、Apache 和其他主流反向代理

---

## 十三、已确认和仍需细化的架构决策

以下内容已经根据需求确认结果冻结，不再作为开发前置待确认项：

1. 第一阶段使用 Let’s Encrypt 和 `go-acme/lego`，支持 Production、Staging、DNS-01、单域名、泛域名和多域名 SAN。
2. HTTPS 采用双模式：默认由核心服务直接终止 HTTPS；高级模式由 Nginx/Caddy 终止公网 HTTPS，核心服务使用内部 HTTPS。
3. 数据访问使用 `database/sql` + Repository；支持 MySQL 5.7、MySQL 8.0 和 SQLite，分别维护迁移脚本与方言适配。
4. DNS 第一批适配器包括 Cloudflare、阿里云 DNS、DNSPod、CNAME 委派、华为云、腾讯云、AWS Route 53、Google Cloud DNS，并预留其他服务商扩展。
5. SMTP、IMAP、POP3 协议表仅作为外部邮件服务兼容性参考；Scert 不实现邮件服务器端，通知通过外部 HTTP API 和 SMTP 出站客户端完成。
6. HTTP 通知支持自定义地址、Bearer Token、HMAC-SHA256、无认证、详细/统一事件、自定义请求头、完整部署明细、证书序列号、管理后台链接、2xx 成功判定和可配置重试。
7. SAN 纳入第一阶段，保存完整域名集合，并按证书标识和版本管理，不覆盖历史证书版本。
8. Windows 和 Linux 默认目录、Linux `Scert` 专用运行用户、证书目录管理和私钥权限要求已经确认。
9. 基础测试范围为 Windows 10/11、Ubuntu 22.04 + Nginx；扩展测试范围为 Ubuntu 24.04 + Nginx、CentOS/Rocky Linux、Debian、Caddy、Apache 和其他主流反向代理。

仍需在编码和联调阶段细化，但不影响开始开发：

1. HTTP HMAC-SHA256 的签名原文、时间戳有效期、重放防护和幂等键过期策略。
2. HTTP 通知完整 JSON 请求体、详细事件与统一事件的具体事件名称和字段约束。
3. MySQL 连接配置、连接池默认值、SQLite 是否允许在 Web 服务器生产模式使用，以及数据库备份恢复流程。
4. 核心服务、Web 前端和桌面端的最终目录组织、构建脚本和安装包细节。

---

## 十四、与原技术方案相比的主要优化

| 原方案 | 优化后 |
|---|---|
| 使用 `lego` 申请证书 | 按用户最新确认保留并加强为原生 Go 集成，减少外部脚本运行时依赖 |
| 每日 cron 检查 | 持久化调度器，支持每 3 天、临近到期每天检查、重试和恢复 |
| `servers.domain_id` | 使用 `domains`、`servers`、`domain_servers` 多对多模型 |
| 证书直接放域名目录 | 增加环境、证书类型、当前版本和历史版本目录 |
| settings 键值表保存全部配置 | 普通配置与加密敏感配置分离 |
| 直接写死 Nginx 部署 | Web 服务器/反向代理适配器，默认 Nginx |
| API 返回同步执行结果 | 长任务统一返回 task_id 并轮询 |
| JWT 或 Session 二选一 | Web 使用受保护会话，CLI 使用受控 API 凭据，后续统一实现 |
| 单文件包含全部运行环境 | 明确区分核心二进制、证书、数据库、日志和系统服务资源 |
| SMTP/TLS 描述较宽泛 | 按端口、STARTTLS、隐式 TLS 和明文认证限制具体化 |
| 未说明 Windows 与 Linux 的 ACME 差异 | 增加 `lego` 原生适配、DNS provider 注入、平台权限适配和跨平台测试边界 |
| 未区分通知配置和任务结果 | 增加通知通道、收件人、规则和独立通知任务模型 |
| 只预留 SAN，未说明需求冲突 | SAN 已纳入第一阶段，保存完整域名集合并按证书标识和版本管理 |

---

## 十五、文档状态

- [x] 已完成：读取并优化用户提供的技术选型草案
- [x] 已完成：根据用户最新确认将 ACME 主实现调整为 `lego`
- [x] 已完成：补充持久化任务调度和失败恢复设计
- [x] 已完成：补充多对多服务器部署数据模型
- [x] 已完成：补充安全、API、跨平台和打包边界
- [~] 进行中：等待用户确认最终技术架构
- [ ] 未开始：根据最终架构创建项目代码结构

---

## 十六、状态标记

- `[ ]` 未开始
- `[~]` 进行中
- `[x]` 已完成，等待复审
- `[✓]` 复审通过
- `[!]` 暂缓或存在阻塞

## 十七、本轮安装部署架构补充（2026-08-26）

### 17.1 安装产物

安装产物统一包含以下组件：

```text
Scert/
├── scert                 # 核心服务和 CLI
├── scert-admin           # 超级管理员密码重置服务
├── config/               # 配置目录
├── data/                 # SQLite 数据库或本地数据
├── ssl/                  # Windows 默认证书根目录
├── logs/
└── backups/
```

Linux 安装目录固定为 `/scert`，证书根目录默认 `/ssl`；Windows 安装根目录由用户选择，默认目录为 `{安装目录}\Scert\ssl`、`data`、`config`、`logs` 和 `backups`。

### 17.2 初始化状态机

```text
未初始化
  -> 写入超级管理员和双端口配置
  -> 写入第三方邮箱配置
  -> 写入 DNS provider 和加密凭据引用
  -> DNS API 验证
       失败 -> 保持未初始化并记录错误
       成功 -> 申请 Web 证书
  -> 本地保存、部署和 HTTPS 校验
       失败 -> 保留原证书并保持未初始化
       成功 -> 写入 Web 域名/IP和证书路径
  -> 检查并放行 Scert 主服务本机防火墙端口
       防火墙未启用 -> 记录无需创建规则并继续
       权限或规则创建失败 -> 保持未初始化并返回明确错误
       配置保存失败 -> 只回滚本次新建的规则
  -> 启动或启用 Scert 核心服务
  -> Scert-admin 保持停止状态，仅在管理员明确执行密码重置操作时手动启动
  -> 提示云安全组放行要求
  -> 初始化完成
```

`setup_ready=true` 只能在超级管理员摘要存在、DNS 验证状态为 `verified`、Web 域名或公网 IP 已配置并完成证书流程后写入。当前代码已实现配置字段和前置校验，证书流程仍由 `go-acme/lego` 模块接入。

### 17.3 运行端口

| 服务 | 默认监听地址 | 默认端口 | 可配置 | 公网暴露策略 |
|---|---|---:|---|---|
| Scert 主服务 | `0.0.0.0` | 8080 | 是 | 按用户指定端口和 HTTPS 模式开放 |
| Scert-admin | `127.0.0.1` | 8091 | 是 | 默认不允许公网暴露 |

主服务和密码重置服务禁止使用相同监听地址与端口。云安全组属于外部资源，安装程序默认显示提示，不直接假设具有云厂商 API 权限。

#### 17.3.1 本机防火墙适配

- 初始化程序只自动开放 Scert 主服务的 TCP 端口，不为 `Scert-admin` 创建对外入站规则。
- Windows 10/11 通过 Windows Defender 防火墙 PowerShell cmdlet 检查状态、查询固定名称规则并创建入站允许规则。
- Ubuntu 优先使用 UFW；CentOS/Rocky Linux 及启用 firewalld 的发行版使用 `firewall-cmd` 永久规则并重新加载。
- 防火墙命令使用固定命令模板和参数数组执行，用户端口必须先通过 `1-65535` 校验，禁止将用户输入拼接为 Shell 命令。
- 已有规则保持不变，重复初始化不得重复创建；只有本次初始化创建的规则才登记为可回滚变更。
- 本机防火墙未启用时不主动启用，只返回“无需创建规则”状态；权限不足或执行失败时初始化失败并提示以管理员或 root 权限重新运行。
- 云安全组仍由用户手动放行，初始化结果必须同时显示主服务端口和安全组提示。

### 17.4 进程和服务管理

- Linux：后续提供 `scert.service` 和 `scert-admin.service` 两个 systemd 单元，服务运行用户为 `Scert`。
- Linux：`Scert-admin.service` 不提供 `[Install]` 段，不注册开机自启动；日常仅支持 `systemctl start Scert-admin` 和 `systemctl stop Scert-admin`，不配置自动重启或 `ExecReload`。安装或升级阶段仅允许使用一次 `systemctl daemon-reload` 加载 unit，不得自动启动或启用 `Scert-admin`。
- Windows：后续提供 `Scert` 和 `Scert-admin` 两个 Windows Service；桌面端负责显示服务状态和 Web 开关。
- 两个服务共享同一配置根目录，但 `Scert-admin` 不得绕过本地受保护通道直接授予普通管理员超级权限。
- 停止、升级和卸载不得删除用户证书、数据库和备份。

### 17.5 公网 IP 证书规则

- 有域名时支持单域名、泛域名和 SAN。
- 只有公网 IP 时按单 IP 标识申请；不支持泛 IP 证书。
- IPv4 和 IPv6 证书能力需分别完成 CA、`lego` 和目标系统验证后开放。
- 在 IP 证书能力未验证前，初始化向导必须给出明确失败原因，不得伪造申请成功。
