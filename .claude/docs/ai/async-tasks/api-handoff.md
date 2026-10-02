# 证书异步任务接口接线说明

更新日期：2026年10月2日。依据当前源码；本批尚未部署，不代表生产验收通过。

## 业务范围

证书申请、当前证书部署及手动重试先持久化任务，再交给后台工作线程执行。浏览器断线不取消已接收工作，前端通过任务接口确认最终结果。提交成功、逐服务器推送成功、整体任务成功是不同层级，不能混用。

## 接口与权限

所有接口要求完成初始化、有效登录会话及来源校验；前端沿用现有带会话的 fetchAdmin，不在 URL 中携带凭据。任务与域名接口返回 private, no-store 和 Vary: Cookie，不使用共享缓存。

| 方法与路径 | 行为 | 权限及返回 |
|---|---|---|
| POST /api/v1/domains/:id/issue | 提交申请及关联部署，无请求体 | 可见域名、证书管理、部署执行、DNS 凭据使用权限；202 |
| POST /api/v1/domains/:id/deploy | 提交已有证书部署，无请求体 | 可见域名、部署执行权限；202 |
| POST /api/v1/tasks/:id/retry | 同一任务新增一次尝试，无请求体 | 重试、证书管理、部署执行权限；申请类还检查 DNS 凭据；202 |
| DELETE /api/v1/tasks/:id | 请求取消 | 任务可见及取消权限；后台任务返回 202，非异步历史任务可能返回 200 |
| GET /api/v1/tasks | 当前身份可见任务列表 | 支持 domain_id 和 limit；返回 200 与 items 数组 |
| GET /api/v1/tasks/:id | 单个任务最新状态 | 返回 200 与任务对象本身，不包 task 字段 |
| GET /api/v1/tasks/:id/attempts | 任务尝试历史 | 返回 200 与 items 数组 |
| GET /api/v1/tasks/:id/deployment-results | 逐尝试的逐服务器部署结果 | 返回 200 与 items 数组；前端按最近 attempt_number 筛选 |

列表 limit 默认 50，有效范围为 1～200，越界由仓储回退至 50。不存在和无权访问的任务都返回 403，不能根据接口泄露其他项目资源是否存在。

### 异步提交响应

返回 202 和 Location: /api/v1/tasks/任务编号。以下仅为字段示例，编号不对应真实环境：

```json
{
  "accepted": true,
  "task": {
    "id": "example-task-id",
    "domain_id": "example-domain-id",
    "scope_type": "project",
    "project_id": "example-project-id",
    "task_type": "certificate_deploy",
    "status": "queued",
    "stage": "queued",
    "message": "等待执行证书任务",
    "created_at": "2026-10-02T08:00:00Z",
    "updated_at": "2026-10-02T08:00:00Z"
  }
}
```

202 的 task 是提交时快照，不保证仍处于 queued。重试响应可能已经是 running。前端必须使用后续 GET 状态作为依据，不凭提交响应显示成功。

### 幂等与错误

- 首次申请和部署支持 Idempotency-Key：长度 8～128，只允许英文字母、数字、连字符、下划线及英文句点。前端当前为每次明确操作生成新键，网络重发若需要去重必须复用原键。
- 同一域名、同一键命中已结束任务返回 200、task 和 replayed: true；命中活动任务返回 409、task 和 replayed: true，不再次执行。跨操作复用同一键返回 409。
- 同类活动任务、仍在收尾、成功任务重试或不支持的任务类型返回 409。部署的 partial_success 允许重试；申请类只允许 failed 或 cancelled。
- 取消 202 只说明接收取消意图，状态为 cancelling；不能提前显示已取消或允许重试。已经完成的外部操作仍可最终成功，并提示取消未撤销该操作。
- 400：幂等键等输入无效；401：登录失效；403：无权访问或来源错误；409：初始化未完成或状态冲突；500：持久化等内部错误；501：工作流未配置；503：权限服务未就绪、服务停止或队列已满。
- 错误响应一般含 error，可能带 request_id 或 task；网关也可能返回非 JSON 页面。解析失败只能提示暂时无法确认，不能推断任务已失败或自动再次提交。

## 数据模型

| 模型 | 字段 |
|---|---|
| Task | id、domain_id、task_type、status、stage、message、scope_type、project_id、created_at、updated_at；可选 operator_id、certificate_version_id、idempotency_key、started_at、finished_at |
| TaskAttempt | id、task_id、attempt_number、stage、status、message、scope_type、project_id、created_at；可选 started_at、finished_at |
| TaskDeploymentResult | task_id、attempt_id、attempt_number、result_index、server_id、server、status、message、created_at；可选 backup_id |

编号和时间为字符串，时间使用带时区的 RFC3339 表示；attempt_number 与 result_index 为整数。Task 与 TaskAttempt 的状态含义如下：

| 状态 | 中文含义 | 是否终态 |
|---|---|---|
| queued | 排队中 | 否 |
| running | 执行中 | 否 |
| retry_wait | 等待重试 | 否 |
| cancelling | 正在取消并等待收尾 | 否 |
| success | 已完成 | 是 |
| failed | 执行失败或结果未可靠确认 | 是 |
| cancelled | 已停止；不保证外部副作用全部撤销 | 是 |
| partial_success | 部分完成 | 是 |

未知状态不得默认视为成功。逐服务器结果不包含完整的站点验证及恢复报告，不能据此推导 HTTPS 校验成功。

## 执行及前端交互边界

- 当前后台最多接收 32 个在途任务，4 个执行槽，总执行预算 30 分钟；这些是实现限制，不是保证耗时。
- 队列满或关闭时拒绝执行，并尝试保存失败终态；持久化失败保留中断状态，不伪造已完成。
- 服务关闭停止接收、发送取消信号并等待工作线程收尾。启动恢复将中断记录标为失败，不自动重放外部签发、部署。
- 任务和本次尝试的终态通过同一数据库事务保存；逐目标结果和完成审计目前仍为独立写入，不能描述成所有记录完全原子化。
- 前端每三秒查询一次，单次周期十秒超时且不重叠；卸载后停止轮询、取消请求。
- 切换任务时丢弃旧选择的迟到响应；撤权清除详情及部署结果。一般查询故障保留最后有效状态并提示未知，不改变任务终态。
- 三个详情 GET 不是一次原子快照；任务终态与最大 attempt_number 的状态不一致时暂不合并，下一周期重新查询。
- 任务从活动变终态后刷新域名、证书、运行状态和审计，但不覆盖正在编辑的设置表单。

## 回归与未完成项

已加入自动用例：断线后继续执行、排队取消、取消收尾禁止重试、队列满、异常脱敏、读取和保存失败、跨任务尝试拒绝、终态事务回滚、已完成与部分成功不被取消意图覆盖、前端迟到响应和网络恢复。

- [~] 当前 Linux 自动回归持续执行；竞争检测尚无可靠通过结果。
- [ ] 完整验证及恢复报告持久化与页面展示仍需补齐。
- [ ] 完成审计、逐服务器结果与任务终态之间的跨记录一致性继续复核。
- [ ] 导入、版本恢复、校验及手动续期等其他长操作尚未全部异步化。
- [ ] 正式构建部署后验证超过反代等待窗口的真实操作、断线查询、取消回滚和服务重启；本说明不替代生产验收。
