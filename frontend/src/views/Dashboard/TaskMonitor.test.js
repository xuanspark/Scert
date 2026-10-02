import assert from "node:assert/strict";
import test from "node:test";
import { useDashboard } from "./Dashboard.js";
import { latestDeploymentReport, latestValidationResult } from "./TaskMonitor.js";
import { validationDate } from "./TaskValidationResult.js";
import { deploymentSummary } from "./DeploymentFeedback.js";
import { deferred, mountComposable } from "../../test-support/mount.js";

const initialTask = { id: "async-task", domain_id: "domain-1", task_type: "certificate_deploy", status: "running" };
const target = { server_id: "server-1", server: "部署目标", status: "success", attempt_number: 1 };

test("校验只展示当前任务最近一次成功尝试，不挪用旧结果", () => {
  const task = { ...initialTask, task_type: "certificate_validate", status: "success" };
  const attempts = [{ id: "latest", attempt_number: 2, status: "success" }];
  const validation = { domains: ["example.test"], fingerprint_sha256: "verified" };
  const stored = { task_id: task.id, attempt_id: "latest", attempt_number: 2, validation };
  assert.deepEqual(latestValidationResult(task, attempts, [stored]), validation);
  for (const record of [
    { ...stored, task_id: "another" },
    { ...stored, attempt_id: "old" },
    { ...stored, attempt_number: 1 },
    { ...stored, validation: {} },
  ]) assert.equal(latestValidationResult(task, attempts, [record]), null);
  for (const status of ["running", "failed", "cancelled"]) {
    assert.equal(latestValidationResult({ ...task, status }, attempts, [stored]), null);
  }
  assert.equal(latestValidationResult(initialTask, attempts, [stored]), null);
  assert.equal(latestValidationResult(task, [], [stored]), null);
  assert.equal(validationDate("invalid"), "未记录");
  assert.equal(validationDate(null), "未记录");
});

test("校验任务提交后读取独立详情，页面重开可恢复，撤权后清除", async (t) => {
  let task = { ...initialTask, task_type: "certificate_validate", status: "queued" };
  let denied = false;
  let submitted = 0;
  const validation = {
    domains: ["example.test"], serial_number: "42", fingerprint_sha256: "verified",
    not_before: "2026-01-01T00:00:00Z", not_after: "2026-12-31T00:00:00Z", chain_length: 2,
  };
  const { state } = await mountComposable(t, useDashboard, async (path, options = {}) => {
    if (path.endsWith("/certificate/validate")) {
      submitted++;
      assert.match(options.headers["Idempotency-Key"], /^[A-Za-z0-9._-]{8,128}$/);
      return Response.json({ task, accepted: true }, { status: 202 });
    }
    if (path === `/api/v1/tasks/${task.id}`) return Response.json(task);
    if (path.endsWith("/attempts")) return Response.json({
      items: [{ id: "validation-attempt", attempt_number: 1, status: task.status }],
    });
    if (path.endsWith("/validation-results")) {
      if (denied) return Response.json({ error: "禁止访问" }, { status: 403 });
      return Response.json({ items: task.status === "success" ? [{
        task_id: task.id, attempt_id: "validation-attempt", attempt_number: 1, validation,
      }] : [] });
    }
    if (path.startsWith("/api/v1/tasks?")) return Response.json({ items: [task] });
    return Response.json({ items: [] });
  });
  await state.validateDomain({ id: "domain-1", name: "example.test" });
  assert.equal(submitted, 1);
  assert.equal(state.error.value, "");
  assert.equal(state.showTaskDetails.value, true);
  assert.match(state.notice.value, /任务已提交/);
  assert.equal(state.taskValidationResult.value, null);
  task = { ...task, status: "success" };
  await state.refreshTaskStatus();
  assert.deepEqual(state.taskValidationResult.value, validation);
  assert.equal(state.taskDeploymentReport.value, null);
  state.closeTaskDetails();
  assert.equal(state.taskValidationResult.value, null);
  await state.openTaskDetails(task);
  assert.deepEqual(state.taskValidationResult.value, validation);
  denied = true;
  await state.refreshTaskStatus();
  assert.equal(state.taskValidationResult.value, null);
  assert.equal(state.taskDetails.value, null);
  assert.match(state.taskDetailsError.value, /无权/);
  assert.equal(submitted, 1);
});

test("校验详情拆分展示元数据和本地校验边界，不展示私钥路径", async () => {
  const { readFile } = await import("node:fs/promises");
  const source = await readFile(new URL("./TaskValidationResult.vue", import.meta.url), "utf8");
  for (const label of ["覆盖域名", "序列号", "生效时间", "到期时间", "证书链长度", "SHA-256 指纹", "不代表远端部署"]) {
    assert.ok(source.includes(label));
  }
  assert.match(source, /src="\.\/TaskValidationResult.css"/);
  assert.doesNotMatch(source, /private_key_path|certificate_path|&#x/);
  const { parse, compileTemplate } = await import("@vue/compiler-sfc");
  const { renderToString } = await import("@vue/server-renderer");
  const Vue = await import("vue");
  const { descriptor, errors } = parse(source);
  assert.deepEqual(errors, []);
  const compiled = compileTemplate({
    source: descriptor.template.content, filename: "TaskValidationResult.vue",
    id: "validation-result", compilerOptions: { mode: "function" },
  });
  assert.deepEqual(compiled.errors, []);
  const render = new Function("Vue", compiled.code)(Vue);
  const app = Vue.createSSRApp({
    render,
    setup: () => ({ validationDate, result: {
      domains: ["example.test", "*.example.test"], serial_number: "98765",
      fingerprint_sha256: "AB:CD:EF", chain_length: 2,
      not_before: "2026-01-01T00:00:00Z", not_after: "2026-12-31T00:00:00Z",
    } }),
  });
  const warnings = [];
  app.config.warnHandler = (message) => warnings.push(message);
  const html = await renderToString(app);
  assert.deepEqual(warnings, []);
  assert.match(html, /example.test、\*.example.test/);
  assert.match(html, /AB:CD:EF/);
  assert.match(html, /98765/);
});

test("完整报告恢复站点验证与回滚事实，不被逐目标结果覆盖", () => {
  const task = { ...initialTask, status: "failed" };
  const attempts = [{ id: "attempt-2", attempt_number: 2, status: "failed" }];
  const persisted = {
    task_id: task.id, attempt_id: "attempt-2", attempt_number: 2,
    report: {
      total: 1, success: 0, failed: 1, status: "failed",
      verification_status: "failed", recovery_status: "success",
      results: [{ ...target, status: "failed", failure_code: "recovery_restored" }],
    },
  };
  const report = latestDeploymentReport(task, attempts, [target], [persisted]);
  assert.equal(report.verification_status, "failed");
  assert.equal(report.recovery_status, "success");
  assert.equal(report.results[0].failure_code, "recovery_restored");
  assert.equal(report.success, 0);
  assert.match(deploymentSummary(report), /恢复/);
});

test("完整报告按任务和尝试精确匹配，不复用旧尝试或其他任务报告", () => {
  const task = { ...initialTask, status: "success" };
  const attempts = [{ id: "attempt-2", attempt_number: 2 }];
  for (const mismatch of [
    { task_id: "another-task", attempt_id: "attempt-2", attempt_number: 2 },
    { task_id: task.id, attempt_id: "attempt-1", attempt_number: 2 },
    { task_id: task.id, attempt_id: "attempt-2", attempt_number: 1 },
  ]) {
    assert.equal(latestDeploymentReport(task, attempts, [], [{
      ...mismatch, report: { status: "success", verification_status: "success", results: [] },
    }]), null);
  }
});

test("完整报告无逐目标结果仍可读取，执行成功不能覆盖任务收尾失败", () => {
  const task = { ...initialTask, status: "failed" };
  const attempts = [{ id: "attempt-1", attempt_number: 1 }];
  const reports = [{
    task_id: task.id, attempt_id: "attempt-1", attempt_number: 1,
    report: { status: "success", total: 0, success: 0, failed: 0, results: [] },
  }];
  assert.equal(latestDeploymentReport(task, attempts, [], reports).status, "failed");
  assert.equal(latestDeploymentReport(initialTask, attempts, [], reports), null);
});

for (const [method, type, prefix] of [
  ["issueDomain", "certificate_issue", "issue"],
  ["deployDomain", "certificate_deploy", "deploy"],
  ["validateDomain", "certificate_validate", "certificate-validate"],
]) {
  test(`域名操作 ${prefix} 生成符合后端契约且每次独立的幂等键`, async (t) => {
    const posts = [];
    const item = { id: "2fd1f484-0adc-4a14-a592-fd9342dc5720", name: "example.test" };
    const task = { ...initialTask, task_type: type, domain_id: item.id };
    const { state } = await mountComposable(t, useDashboard,
      taskFetcher(() => task, (options) => posts.push(options)));
    await state[method](item);
    await state[method](item);
    assert.equal(posts.length, 2);
    const keys = posts.map((options) => options.headers["Idempotency-Key"]);
    for (const key of keys) {
      assert.match(key, /^[A-Za-z0-9._-]{8,128}$/);
      assert.ok(key.startsWith(`${prefix}-${item.id}-`));
    }
    assert.notEqual(keys[0], keys[1]);
    assert.equal(state.error.value, "");
    assert.match(state.notice.value, /任务已提交/);
  });
}

function taskFetcher(readTask, onPost = () => {}) {
  return async (path, options = {}) => {
    const task = readTask();
    if (options.method === "POST") {
      onPost(options);
      return Response.json({ task, accepted: true }, { status: 202 });
    }
    if (path === `/api/v1/tasks/${task.id}`) return Response.json(task);
    if (path.endsWith("/attempts")) return Response.json({ items: [{ attempt_number: 1, status: task.status }] });
    if (path.endsWith("/deployment-results")) return Response.json({ items: task.status === "running" ? [] : [target] });
    if (path.startsWith("/api/v1/tasks?")) return Response.json({ items: [task] });
    return Response.json({ items: [] });
  };
}

test("页面刷新查询完整报告并展示恢复状态", async (t) => {
  const task = { ...initialTask, status: "failed" };
  const fetchTask = taskFetcher(() => task);
  const { state } = await mountComposable(t, useDashboard, (path, options) => {
    if (path.endsWith("/attempts")) {
      return Response.json({ items: [{ id: "stored-attempt", attempt_number: 1, status: "failed" }] });
    }
    if (path.endsWith("/deployment-results")) {
      return Response.json({ items: [target], reports: [{
        task_id: task.id, attempt_id: "stored-attempt", attempt_number: 1,
        report: {
          status: "failed", total: 1, success: 0, failed: 1,
          verification_status: "failed", recovery_status: "unconfirmed", results: [target],
        },
      }] });
    }
    return fetchTask(path, options);
  });
  await state.openTaskDetails(task);
  assert.equal(state.taskDeploymentReport.value.verification_status, "failed");
  assert.equal(state.taskDeploymentReport.value.recovery_status, "unconfirmed");
  assert.match(deploymentSummary(state.taskDeploymentReport.value), /恢复/);
});

test("异步导入从持久化报告恢复保存状态，不因部署失败诱导重复导入", async (t) => {
  const task = { ...initialTask, task_type: "certificate_import", status: "failed", message: "证书已导入，但部署未完成" };
  const fetchTask = taskFetcher(() => task);
  const { state } = await mountComposable(t, useDashboard, (path, options) => {
    if (path.endsWith("/attempts")) {
      return Response.json({ items: [{ id: "import-attempt", attempt_number: 1, status: "failed" }] });
    }
    if (path.endsWith("/deployment-results")) {
      return Response.json({ items: [], reports: [{
        task_id: task.id, attempt_id: "import-attempt", attempt_number: 1,
        report: { status: "failed", results: [], certificate_imported: true },
      }] });
    }
    return fetchTask(path, options);
  });
  state.deploymentResult.value = { taskID: task.id, pending: true };
  await state.refreshTaskStatus();
  assert.equal(state.deploymentResult.value.pending, false);
  assert.equal(state.deploymentResult.value.certificateImported, true);
  assert.equal(state.deploymentResult.value.taskStatus, "failed");
  await state.openTaskDetails(task);
  assert.equal(state.taskDeploymentReport.value.certificate_imported, true);
});

test("异步部署接收后不误报成功或缺失结果，最终失败不被逐目标成功覆盖", async (t) => {
  let current = { ...initialTask };
  const posts = [];
  const { state } = await mountComposable(t, useDashboard, taskFetcher(() => current, (options) => posts.push(options)));
  await state.deployDomain({ id: "domain-1", name: "example.test" });
  assert.equal(posts.length, 1);
  assert.match(posts[0].headers["Idempotency-Key"], /^deploy-domain-1-/);
  assert.equal(state.deploymentResult.value.pending, true);
  assert.equal(state.deploymentResult.value.running, false);
  assert.equal(state.deploymentResult.value.error, "");
  assert.equal(state.deploymentResult.value.report, null);
  assert.match(state.notice.value, /任务已提交/);
  current = { ...current, status: "failed", message: "站点校验未通过" };
  await state.refreshTaskStatus();
  assert.equal(state.deploymentResult.value.pending, false);
  assert.equal(state.deploymentResult.value.taskStatus, "failed");
  assert.equal(state.deploymentResult.value.report.results[0].status, "success");
  assert.doesNotMatch(deploymentSummary(state.deploymentResult.value.report), /部署完成/);
  assert.equal(posts.length, 1);
});

test("刷新页面从持久化任务恢复详情，关闭结果窗口不取消任务", async (t) => {
  let current = { ...initialTask };
  const { state } = await mountComposable(t, useDashboard, taskFetcher(() => current));
  assert.equal(state.tasks.value[0].id, current.id);
  await state.openTaskDetails(current);
  assert.equal(state.taskDetails.value.status, "running");
  state.deploymentResult.value = null;
  current = { ...current, status: "success", message: "证书部署成功" };
  await state.refreshTaskStatus();
  assert.equal(state.taskDetails.value.status, "success");
  assert.equal(state.taskDeploymentReport.value.results.length, 1);
  state.closeTaskDetails();
  assert.equal(state.taskDetails.value, null);
});

test("部署结果只汇总最新尝试，不混用上一次失败或伪造站点验证", () => {
  const task = { ...initialTask, status: "success" };
  const attempts = [{ attempt_number: 1 }, { attempt_number: 2 }];
  const report = latestDeploymentReport(task, attempts, [
    { ...target, status: "failed" }, { ...target, attempt_number: 2 },
  ]);
  assert.equal(report.total, 1);
  assert.equal(report.failed, 0);
  assert.equal(report.verification_status, undefined);
  assert.equal(latestDeploymentReport(task, attempts, [target]), null);
  assert.equal(latestDeploymentReport(initialTask, attempts, [target]), null);
});

test("状态查询断线保留后台任务，恢复后继续读取且不会再次提交", async (t) => {
  let unavailable = false;
  let posts = 0;
  const fetchTask = taskFetcher(() => initialTask, () => { posts += 1; });
  const { state } = await mountComposable(t, useDashboard, (path, options) => {
    if (unavailable && path.startsWith("/api/v1/tasks")) return Promise.reject(new Error("连接中断"));
    return fetchTask(path, options);
  });
  await state.deployDomain({ id: "domain-1", name: "example.test" });
  unavailable = true;
  await state.refreshTaskStatus();
  assert.equal(state.deploymentResult.value.pending, true);
  assert.equal(state.deploymentResult.value.taskStatus, "running");
  assert.match(state.deploymentResult.value.queryError, /勿重复提交/);
  unavailable = false;
  await state.refreshTaskStatus();
  assert.equal(state.deploymentResult.value.queryError, "");
  assert.equal(posts, 1);
});

test("任务轮询不重叠，卸载取消在途查询并停止计时器", async (t) => {
  let block = false;
  let requests = 0;
  let aborted = false;
  const fetchTask = taskFetcher(() => initialTask);
  const { state, unmount, runIntervals } = await mountComposable(t, useDashboard, (path, options) => {
    if (block && path.startsWith("/api/v1/tasks?")) {
      requests += 1;
      return new Promise((resolve, reject) => {
        options.signal.addEventListener("abort", () => { aborted = true; reject(new Error("已停止查询")); });
      });
    }
    return fetchTask(path, options);
  });
  block = true;
  const pending = state.refreshTaskStatus();
  runIntervals();
  runIntervals();
  assert.equal(requests, 1);
  unmount();
  await pending;
  runIntervals();
  assert.equal(aborted, true);
  assert.equal(requests, 1);
  assert.equal(state.taskQueryError.value, "");
});

test("切换详情时迟到请求不能覆盖新任务", async (t) => {
  const delayed = deferred();
  let blocked = true;
  const fetchTask = taskFetcher(() => initialTask);
  const { state } = await mountComposable(t, useDashboard, async (path, options) => {
    if (path === "/api/v1/tasks/first" && blocked) return delayed.promise;
    if (path === "/api/v1/tasks/second") return Response.json({ ...initialTask, id: "second" });
    return fetchTask(path, options);
  });
  const first = state.openTaskDetails({ id: "first" });
  await new Promise((resolve) => setImmediate(resolve));
  const second = state.openTaskDetails({ id: "second" });
  blocked = false;
  delayed.resolve(Response.json({ ...initialTask, id: "first" }));
  await Promise.all([first, second]);
  assert.equal(state.taskDetails.value.id, "second");
  assert.equal(state.taskDetailsLoading.value, false);
});

test("异步取消与重试只提示请求已接收，不提前宣布终态", async (t) => {
  const fetchTask = taskFetcher(() => initialTask);
  const { state } = await mountComposable(t, useDashboard, (path, options = {}) => {
    if (options.method === "DELETE") return Response.json({ task: { ...initialTask, status: "cancelling" } }, { status: 202 });
    return fetchTask(path, options);
  });
  window.confirm = () => true;
  await state.cancelTask(initialTask);
  assert.match(state.notice.value, /等待任务停止和部署收尾/);
  assert.doesNotMatch(state.notice.value, /任务已取消/);
  await state.retryTask({ ...initialTask, status: "failed" });
  assert.match(state.notice.value, /重试已提交/);
  assert.doesNotMatch(state.notice.value, /已重新执行/);
});

test("代理返回非 JSON 时保留原状态，不泄露 HTML 或误报完成", async (t) => {
  let invalid = false;
  const fetchTask = taskFetcher(() => initialTask);
  const { state } = await mountComposable(t, useDashboard, (path, options) => {
    if (invalid && path.startsWith("/api/v1/tasks/")) return Promise.resolve(new Response("<html>504</html>", { status: 504 }));
    return fetchTask(path, options);
  });
  await state.openTaskDetails(initialTask);
  invalid = true;
  await state.refreshTaskStatus();
  assert.equal(state.taskDetails.value.status, "running");
  assert.match(state.taskDetailsError.value, /不代表任务失败/);
  assert.doesNotMatch(state.taskDetailsError.value, /<html>|Unexpected/);
});

test("任务与尝试读取遇到并发重试时不混用旧终态和新结果", async (t) => {
  let retrying = false;
  const current = { ...initialTask, status: "success" };
  const fetchTask = taskFetcher(() => current);
  const { state } = await mountComposable(t, useDashboard, (path, options) => {
    if (retrying && path.endsWith("/attempts")) {
      return Promise.resolve(Response.json({ items: [{ attempt_number: 2, status: "running" }] }));
    }
    return fetchTask(path, options);
  });
  await state.openTaskDetails(current);
  retrying = true;
  await state.refreshTaskStatus();
  assert.match(state.taskDetailsError.value, /暂时无法查询/);
  assert.equal(state.taskAttempts.value[0].attempt_number, 1);
});

test("项目撤权后清除缓存的任务详情和部署结果", async (t) => {
  let denied = false;
  const current = { ...initialTask, status: "success" };
  const fetchTask = taskFetcher(() => current);
  const { state } = await mountComposable(t, useDashboard, (path, options) => {
    if (denied && path.startsWith("/api/v1/tasks/")) {
      return Promise.resolve(Response.json({ error: "无权访问此任务" }, { status: 403 }));
    }
    return fetchTask(path, options);
  });
  await state.openTaskDetails(current);
  assert.equal(state.taskDeploymentReport.value.results.length, 1);
  state.deploymentResult.value = { taskID: current.id, pending: true, certificateImported: true };
  denied = true;
  await state.refreshTaskStatus();
  assert.equal(state.taskDetails.value, null);
  assert.equal(state.taskDeploymentReport.value, null);
  assert.deepEqual(state.taskAttempts.value, []);
  assert.match(state.taskDetailsError.value, /无权读取/);
  assert.equal(state.deploymentResult.value.certificateImported, false);
});

test("任务完成后自动刷新域名证书数据，但不覆盖正在编辑的设置", async (t) => {
  let current = { ...initialTask };
  let resourceReads = 0;
  const fetchTask = taskFetcher(() => current);
  const { state } = await mountComposable(t, useDashboard, (path, options) => {
    if (path === "/api/v1/domains") {
      resourceReads += 1;
      return Promise.resolve(Response.json({ items: [{ id: "domain-1", status: current.status === "success" ? "active" : "pending" }] }));
    }
    return fetchTask(path, options);
  });
  state.settingsForm.value.notification.sender_name = "正在编辑的名称";
  assert.equal(state.domains.value[0].status, "pending");
  current = { ...current, status: "success" };
  await state.refreshTaskStatus();
  assert.equal(state.domains.value[0].status, "active");
  assert.equal(state.settingsForm.value.notification.sender_name, "正在编辑的名称");
  assert.equal(resourceReads, 2);
});
