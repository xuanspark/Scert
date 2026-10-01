import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { parse, compileTemplate } from "@vue/compiler-sfc";
import { useDashboard } from "./Dashboard.js";
import { serverAuthLabel, deploymentSummary } from "./DeploymentFeedback.js";
import { mountComposable, deferred } from "../../test-support/mount.js";

const report = {
  total: 1,
  success: 1,
  failed: 0,
  status: "success",
  results: [{ server_id: "server-1", server: "验收服务器", status: "success", message: "部署完成" }],
};

test("服务器认证提示只显示密码配置状态", () => {
  assert.equal(serverAuthLabel({ auth_method: "password", password_configured: true }), "SSH 密码已配置");
  assert.equal(serverAuthLabel({ auth_method: "password", password_configured: false }), "SSH 密码未配置");
  assert.equal(serverAuthLabel({ auth_method: "private_key", password_configured: false }), "SSH 密码未配置");
  assert.equal(serverAuthLabel({}), "SSH 密码未配置");
});

test("部署汇总不把空目标、缺失结果或部分失败称为成功", () => {
  assert.equal(deploymentSummary(report), "证书部署完成：1 台成功");
  assert.equal(deploymentSummary(null), "未收到有效部署结果");
  assert.equal(deploymentSummary({ total: 0, results: [] }), "未配置部署目标，未执行推送");
  assert.match(deploymentSummary({ ...report, results: [] }), /未全部成功/);
  assert.match(deploymentSummary({ ...report, status: "partial", failed: 1 }), /未全部成功/);
});

test("站点生效校验与单台推送分开展示，不把旧证书或校验失败宣称为部署成功", () => {
  const failed = { ...report, status: "failed", verification_status: "failed" };
  assert.match(deploymentSummary(failed), /HTTPS 校验失败/);
  assert.match(deploymentSummary(failed), /未自动回滚/);
  assert.doesNotMatch(deploymentSummary(failed), /部署完成/);
  assert.match(deploymentSummary({ ...report, verification_status: "failed" }), /尚未确认/);
  assert.equal(
    deploymentSummary({ ...report, verification_status: "success" }),
    "证书部署完成：1 台推送成功，站点 HTTPS 校验通过",
  );
});

test("手动部署展示真实返回结果并阻止执行期间重复提交", async (t) => {
  const pending = deferred();
  let requests = 0;
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/deploy")) {
      requests++;
      return pending.promise;
    }
    return Response.json({ items: [] });
  });
  const action = state.deployDomain({ id: "domain-1", name: "example.test" });
  assert.equal(state.deploymentResult.value.running, true);
  await state.deployDomain({ id: "domain-1", name: "example.test" });
  assert.equal(requests, 1);
  pending.resolve(Response.json({ deployment: report }));
  await action;
  assert.equal(state.deploymentResult.value.running, false);
  assert.deepEqual(state.deploymentResult.value.report, report);
  assert.equal(state.notice.value, "证书部署完成：1 台成功");
});

test("失败部署区分已恢复旧站点与恢复未确认，不再统一显示未自动回滚", () => {
  const failed = { ...report, status: "failed", success: 0, failed: 1, verification_status: "failed" };
  const restored = deploymentSummary({ ...failed, recovery_status: "success" });
  assert.match(restored, /本次部署失败/);
  assert.match(restored, /站点已恢复部署前证书/);
  assert.doesNotMatch(restored, /未自动回滚|部署完成/);
  for (const recovery_status of ["failed", "unconfirmed"]) {
    const message = deploymentSummary({ ...failed, recovery_status });
    assert.match(message, /勿直接重试/);
    assert.doesNotMatch(message, /站点已恢复部署前证书|部署完成/);
  }
});

test("部署失败仍保留各服务器结果，网络失败不会残留执行中状态", async (t) => {
  let failNetwork = false;
  const failed = { ...report, status: "failed", success: 0, failed: 1, results: [{ ...report.results[0], status: "failed" }] };
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/deploy")) {
      if (failNetwork) throw new Error("network-test-failure");
      return Response.json({ error: "部署失败", deployment: failed }, { status: 502 });
    }
    return Response.json({ items: [] });
  });
  await state.deployDomain({ id: "domain-1", name: "example.test" });
  assert.deepEqual(state.deploymentResult.value.report, failed);
  assert.equal(state.deploymentResult.value.error, "部署失败");
  assert.equal(state.notice.value, "");
  failNetwork = true;
  await state.deployDomain({ id: "domain-1", name: "example.test" });
  assert.equal(state.deploymentResult.value.running, false);
  assert.equal(state.deploymentResult.value.report, null);
  assert.equal(state.deploymentResult.value.error, "network-test-failure");
});

test("HTTPS 校验失败响应保留推送明细，但不弹出成功通知", async (t) => {
  const failed = { ...report, status: "failed", verification_status: "failed" };
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/deploy")) {
      return Response.json({ error: "证书部署失败", deployment: failed }, { status: 502 });
    }
    return Response.json({ items: [] });
  });
  await state.deployDomain({ id: "domain-1", name: "example.test" });
  assert.equal(state.deploymentResult.value.running, false);
  assert.equal(state.deploymentResult.value.error, "证书部署失败");
  assert.deepEqual(state.deploymentResult.value.report, failed);
  assert.match(deploymentSummary(state.deploymentResult.value.report), /尚未确认新证书生效/);
  assert.equal(state.notice.value, "");
});

test("遗留锁的安全诊断保留在失败明细中，不提示部署成功", async (t) => {
  const message = "无法取得部署锁，可能有任务正在执行或存在中断遗留；请管理员核查远端文件、部署锁及服务状态后再重试，勿直接删除锁";
  const failed = {
    total: 1,
    success: 0,
    failed: 1,
    status: "failed",
    results: [{ server_id: "server-1", server: "测试服务器", status: "failed", failure_code: "lock_unavailable", message }],
  };
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/deploy")) {
      return Response.json({ error: "证书部署失败", deployment: failed }, { status: 502 });
    }
    return Response.json({ items: [] });
  });
  await state.deployDomain({ id: "domain-1", name: "example.test" });
  assert.equal(state.deploymentResult.value.report.results[0].message, message);
  assert.equal(state.deploymentResult.value.report.results[0].failure_code, "lock_unavailable");
  assert.equal(state.notice.value, "");
  assert.equal(state.deploymentResult.value.running, false);
});

test("部署结果组件可编译且认证提示绑定真实配置", async () => {
  const source = await readFile(new URL("./DeploymentResult.vue", import.meta.url), "utf8");
  const { descriptor, errors } = parse(source);
  assert.deepEqual(errors, []);
  const compiled = compileTemplate({ source: descriptor.template.content, filename: "DeploymentResult.vue", id: "deployment-result" });
  assert.deepEqual(compiled.errors, []);
  assert.match(source, /:disabled="result.running"/);
  assert.match(source, /role="dialog"/);
  const dashboard = await readFile(new URL("./Dashboard.vue", import.meta.url), "utf8");
  assert.match(dashboard, /serverAuthLabel\(item\)/);
  assert.doesNotMatch(dashboard, /serverSSHKeyStatusLabel|serverSSHCommand|SSHKeyModal/);
});
