# PostgreSQL 备份恢复说明

## 支持范围

Scert 的 `backup` 命令现已接入 PostgreSQL 官方工具，可在同一备份目录保存：

- `scert.pgdump`：自定义格式的数据库一致性归档，包含数据库对象、数据、序列、对象所有者与授权。
- `scert.json`：不含数据库账号密码的配置。
- `master.key`、`secrets.json`：主密钥和加密凭据。
- `certificates/`：托管证书文件。
- `manifest.json`：文件大小、权限与 SHA-256 校验清单。

自动 `restore` 目前只支持**原目标数据库、对象清单及所有者一致**的恢复。跨数据库、跨结构迁移版本、对象增删、角色变更或空库灾难恢复不在自动流程支持范围；检查不符时明确拒绝，不能把拒绝保护当作灾难恢复已完成。

PostgreSQL 集群级角色、角色密码、表空间定义、服务端配置，以及数据库连接所用的外置 CA/客户端证书文件不由此归档创建或恢复。请由数据库管理员单独保护这些资源。数据库账户轮换后，旧备份中的加密密码可能已经失效；恢复旧凭据前应确认它们仍然有效。

## 运行前提

1. 在运行 Scert CLI 的机器安装官方 `pg_dump`、`pg_restore`，将可信工具目录加入 `PATH`。
2. 两个工具的主版本一致，至少为 PostgreSQL 16；`pg_dump` 的主版本不得低于数据库服务端主版本。工具缺失、版本检查失败或服务端不兼容时停止，不发布不完整备份。
3. 数据库连接保持 `sslmode=verify-full`。允许显式 CA、客户端证书/私钥、连接超时和应用名称参数；没有显式 CA 时使用工具的系统信任库，不跳过证书验证。
4. 使用 Scert 专属数据库，并停止所有连接该数据库的 Scert 实例及外部结构变更任务。CLI 的本机进程锁不能阻止另一台机器上的进程写入。
5. 账号应有读取完整数据及恢复对应对象、所有者和授权的权限。需要的角色、扩展、表空间必须预先存在；权限不足时单事务恢复失败，不忽略错误或静默丢弃 ACL。
6. 为新备份及恢复前完整快照预留足够空间。成功恢复后也会保留恢复前快照，需由管理员根据保留策略管理；Scert 不自动删除历史备份。

以下命令中的路径仅为示例，应替换为本机实际配置与备份路径：

```bash
pg_dump --version
pg_restore --version
scert --config /scert/config/scert.json backup --output /scert/backup/manual-20260916
scert --config /scert/config/scert.json restore --input /scert/backup/manual-20260916
```

## 恢复安全流程

1. 校验备份清单、配置和加密凭据引用。
2. 检查 PostgreSQL 自定义归档头，并通过 `pg_restore` 完整解码到丢弃流；此时不连接目标库。
3. 验证配置路径、数据目录、数据库连接目标与当前配置一致，取得本机进程锁。
4. 在内存中固定当前账号，用它创建恢复前快照；替换配置或主密钥不能改变本次导入及回滚账号。
5. 比较两份归档对象清单。采用严格相等检查，避免 `pg_restore --clean` 只清理归档列出对象而残留额外数据，也保证反向恢复不会留下新对象。
6. 保存当前配置，使用 `--clean --if-exists --single-transaction --exit-on-error` 导入，然后恢复凭据及证书。
7. 任一步失败时尝试恢复原数据库及配置。请求取消后，数据库回滚仍使用独立的五分钟超时；回滚失败明确要求管理员停止服务并使用保留的快照人工恢复。

普通工具操作最长三十分钟，版本检查最长十秒，对象清单读取最长三十秒。超时不会被报告为成功。独立回滚不保证抵抗进程被强制杀死、主机断电、磁盘故障或数据库不可达。

## 敏感信息与信任边界

- 账号密码只在内存中解密，通过专属子进程环境传递，不写命令行、普通配置、清单或错误日志。继承的 `PG*` 环境变量会清除，避免其改变目标或降低 TLS。
- 子进程环境不是对同一操作系统账号或管理员的保密边界。维护操作应在可信主机、专用账号下执行，不能与不可信程序共享账号。
- 不透传 `pg_dump`、`pg_restore` 的原始错误输出，避免其回显业务数据或连接信息；失败时检查工具、网络、TLS、空间和数据库权限，不要将完整连接串粘贴到日志。
- 归档中有业务数据，备份同时包含主密钥及证书私钥。目录及文件采用受限权限，但**整个备份不是加密容器**；异地存储时须加密并限制访问。
- SHA-256 清单只能发现文件损坏或与既有清单不符，不能证明备份来源可信。PostgreSQL 归档可能携带可执行 SQL，只能恢复可信来源的备份，不能导入第三方提供的未知归档。

## 当前验证状态

- 已有工具协议替身测试，覆盖完整备份、配置/凭据/证书联动、导入与证书失败、取消、回滚失败、对象差异、归档解码失败、既有文件保护等分支。
- 实际测试子进程验证了执行失败脱敏和超时终止；该子进程不是 PostgreSQL 官方工具。
- 2026-09-17 已在 Windows 隔离 PostgreSQL 16.15 集群完成真实演练：最新编译的 Scert、官方工具、全部数据库迁移、完整备份恢复、权限不足、证书恢复失败、取消后的回滚、对象清单差异、错误密码及不可信 CA 场景通过。连接使用回环地址、SCRAM 和严格 TLS，未连接生产库。
- 成功与回滚场景核对了业务数据、自增序列、对象所有者、授权、配置、主密钥及加密凭据；成功恢复还逐字节核对证书文件。权限不足时原数据保持不变，但正向和反向导入都缺权限，仍明确提示人工处理，不误报自动回滚成功。
- 七个真实场景分别完成一轮普通演练及三轮 `-race` 重复演练，全部通过；结束后确认无残留数据库进程。竞态检测覆盖 Go 测试及进程内取消路径，不能检测 PostgreSQL 或非竞态编译 CLI 二进制的内部竞态。
- 尚未完成 Linux 实机、浏览器及远程部署验收，也未完成空库灾难恢复开发；Windows 隔离演练不能替代这些验收。

## 隔离实机测试入口

真实集成测试为 `TestPostgresRealBackupRestore`，默认跳过。必须显式指定官方工具目录、最新编译的 Scert 可执行文件与隔离产物根目录；不接受业务数据库连接串，不安装系统服务，不修改系统 PATH。

Windows PowerShell 示例（在 `backend` 目录执行，工具路径需按实际安装位置调整）：

```powershell
go build -p 1 -o ../.runtime/scert-postgres-integration.exe ./cmd/scert
$env:SCERT_POSTGRES_TEST_BIN = 'D:\Desktop\SSL\.runtime\postgres-tools\pgsql\bin'
$env:SCERT_POSTGRES_TEST_CLI = 'D:\Desktop\SSL\.runtime\scert-postgres-integration.exe'
$env:SCERT_POSTGRES_TEST_ROOT = 'D:\Desktop\SSL\.runtime\postgres-integration'
go test -p 1 -count=1 -timeout=5m -run '^TestPostgresRealBackupRestore$' -v ./internal/cli
```

- 每轮创建独立集群，各场景使用全新数据库并运行 Scert 全部迁移，避免复用残留数据。不会清空任何既有业务库。
- 仅监听 `127.0.0.1` 动态端口，启用 SCRAM 密码认证和 `sslmode=verify-full`，生成仅用于本轮测试的证书。
- 普通备份及恢复场景执行指定的 Scert 可执行文件；取消场景在进程内确定性注入取消时机，但仍执行真实 `pg_dump`、`pg_restore` 和数据库操作。
- 覆盖成功恢复、证书恢复失败回滚、导入后取消回滚、权限不足、对象清单不一致、密码错误和不可信 CA。
- 检查配置、主密钥、加密凭据、证书文件、业务数据、自增序列、所有者、授权及恢复前快照，而不仅检查命令退出码。
- 正常完成或断言失败后停止自己的集群，保留集群与演练产物。整个测试进程被强制终止时，清理回调可能无法运行，须检查该轮目录中的 `postmaster.pid` 和进程状态。
- 产物包含测试密码、私钥及数据库备份，只能放在可信账号控制的隔离目录，不得提交到 Git；Windows 需由目录 ACL 控制访问，文件模式不是独立的 ACL 保证。
- 该入口不等于 Linux、浏览器、远程部署、空库灾难恢复或跨版本恢复已经验收。

## 官方语义参考

- [PostgreSQL 16 libpq 连接参数](https://www.postgresql.org/docs/16/libpq-connect.html)：`sslrootcert=system` 与严格 TLS。
- [PostgreSQL 16 pg_restore](https://www.postgresql.org/docs/16/app-pgrestore.html)：标准输出、单事务恢复、所有者、ACL 及可信归档要求。
- [PostgreSQL 16 pg_dump](https://www.postgresql.org/docs/16/app-pgdump.html)：自定义格式、一致性归档与服务端版本兼容。
