# Linux 生产流程测试记录

## 范围与任务状态

2026-09-17 在管理机 `47.108.78.12` 执行当前版本的受控测试。`8.137.23.95` 仅为推送测试机，本记录不包含对它的操作。不运行固定安装器，不切换正式服务，不清空既有数据库，不修改共享 Nginx 或业务证书。

- [✓] 密码 SSH 登录并通过已有主机密钥校验。
- [✓] 核对管理机服务、工具、空间和健康接口。
- [✓] 独立干净克隆构建 Linux 包，本地与远程归档、测试程序 SHA-256 一致。
- [!] PostgreSQL 18 第一轮测试超时，成功恢复及证书失败回滚检查已执行，完整七场景未通过。
- [ ] Linux Bootstrap 协议及 systemd 加固启动测试。
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
