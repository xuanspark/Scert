import assert from "node:assert/strict";
import "./DeploymentResult.test.js";
import "./TaskMonitor.test.js";
import "./RenewalAction.test.js";
import "./DashboardLogout.test.js";
import "./SettingsAccess.test.js";
import "./DomainModal.test.js";
import "./DomainForm.test.js";
import "./DomainDNSAccount.test.js";
import "../DNSAccounts/DNSAccounts.test.js";
import test from "node:test";
import { nextTick, ref } from "vue";
import { useDashboard } from "./Dashboard.js";
import { useDashboardNavigation } from "./DashboardNavigation.js";
import { summarizeRuntime, runtimeTime, useRuntimeOverview } from "./RuntimeOverview.js";
import { aliyunProvider, deferred, mountComposable, verifiedResponse } from "../../test-support/mount.js";

test("管理页面使用拆分后的域名表单与同项目服务器候选", async (t) => {
  const requests = [];
  const item = { id: "domain-form", name: "example.test", project_id: "default" };
  const server = { id: "server-form", name: "共享服务器", project_id: "default" };
  const { state } = await mountComposable(t, useDashboard, async (path, options = {}) => {
    if (options.method) {
      requests.push({ path, ...options });
      return Response.json(path.endsWith("/servers") ? { items: [server] } : item);
    }
    if (path === "/api/v1/servers") return Response.json({ items: [server, { id: "other", project_id: "other" }] });
    return Response.json({ items: [] });
  });
  await state.openCreate();
  assert.deepEqual(state.domainServerOptions.value, [server]);
  state.form.value.name = item.name;
  state.form.value.server_ids = [server.id];
  await state.saveDomain();
  assert.deepEqual(requests.map(({ method }) => method), ["POST", "PUT"]);
  assert.equal(requests[1].path, `/api/v1/domains/${item.id}/servers`);
  assert.deepEqual(JSON.parse(requests[1].body), { server_ids: [server.id] });
  assert.equal(state.showForm.value, false);
});

test("板块导航默认只选中基本信息，超级管理员可切换全部板块", () => {
  const navigation = useDashboardNavigation(ref({ role: "super_admin" }));
  assert.equal(navigation.activeSection.value, "overview");
   assert.equal(navigation.sections.value.length, 7);
  for (const section of navigation.sections.value) {
    navigation.selectSection(section.id);
    assert.equal(navigation.activeSection.value, section.id);
    assert.equal(navigation.activeLabel.value, section.label);
  }
  navigation.selectSection("invalid");
  assert.equal(navigation.activeSection.value, "users");
});

test("用户管理导航按权限显示，权限变化后回到基本信息", () => {
  const user = ref(null);
  const navigation = useDashboardNavigation(user);
  assert.equal(navigation.sections.value.some((section) => section.id === "users"), false);
  navigation.selectSection("users");
  assert.equal(navigation.activeSection.value, "overview");
  user.value = { role: "super_admin" };
  navigation.selectSection("users");
  assert.equal(navigation.activeSection.value, "users");
  user.value = { role: "admin" };
  assert.equal(navigation.activeSection.value, "users");
   assert.equal(navigation.sections.value.length, 7);
  user.value = null;
  assert.equal(navigation.activeSection.value, "overview");
    assert.equal(navigation.sections.value.length, 5);
});

test("DNS 账户入口允许项目角色访问，数据范围由后端实时授权", () => {
  const user = ref({ role: "user" });
  const navigation = useDashboardNavigation(user);
  assert.equal(navigation.sections.value.some((section) => section.id === "dns-accounts"), true);
  assert.equal(navigation.sections.value.some((section) => section.id === "users"), false);
  navigation.selectSection("dns-accounts");
  assert.equal(navigation.activeSection.value, "dns-accounts");
  user.value = null;
  assert.equal(navigation.activeSection.value, "overview");
  assert.equal(navigation.sections.value.some((section) => section.id === "dns-accounts"), false);
});

test("单板块布局使用互斥条件，切换重置滚动且保留弹窗入口", async () => {
  const { readFile } = await import("node:fs/promises");
  const template = await readFile(new URL("./Dashboard.vue", import.meta.url), "utf8");
  const stylesheet = await readFile(new URL("./DashboardLayout.css", import.meta.url), "utf8");
  for (const id of ["overview", "activity", "projects", "domains", "servers", "dns-accounts", "users"]) {
    assert.ok(template.includes(`v-if="activeSection === '${id}'"`));
  }
  assert.match(template, /:key="activeSection"/);
  assert.match(template, /role="region"\s+:aria-label="activeLabel"\s+tabindex="0"/);
  for (const modal of ["SettingsModal", "DomainModal", "ServerModal"]) {
    assert.ok(template.includes(`<${modal} v-if=`));
  }
  assert.match(stylesheet, /height: 100dvh/);
  assert.match(stylesheet, /\.dashboard-content\s*\{[^}]*overflow: auto/);
});

test("侧栏支持键盘焦点、当前项标记和悬浮说明", async () => {
  const { readFile } = await import("node:fs/promises");
  const template = await readFile(new URL("./DashboardNavigation.vue", import.meta.url), "utf8");
  const stylesheet = await readFile(new URL("./DashboardNavigation.css", import.meta.url), "utf8");
  assert.match(template, /<button/);
  assert.match(template, /:aria-current=/);
  assert.match(template, /:title="section.hint"/);
  assert.match(stylesheet, /\.navigation-item:focus-visible/);
});

test("证书导入使用组合式状态导出，避免点击时缺失处理函数", async () => {
  const { readFile } = await import("node:fs/promises");
  const template = await readFile(new URL("./Dashboard.vue", import.meta.url), "utf8");
  const script = template.split("</script>")[0];
  for (const name of ["showCertificateImport", "openCertificateImport", "closeCertificateImport", "importCertificate", "selectCertificateImportFile"]) {
    assert.ok(script.includes(`  ${name},`));
  }
});

test("证书历史版本使用稳定标识并提供独立恢复弹窗样式", async () => {
  const { readFile } = await import("node:fs/promises");
  const template = await readFile(new URL("./Dashboard.vue", import.meta.url), "utf8");
  const script = await readFile(new URL("./Dashboard.js", import.meta.url), "utf8");
  const stylesheet = await readFile(new URL("./Dashboard.css", import.meta.url), "utf8");
  for (const name of [
    "showCertificateVersions",
    "openCertificateVersions",
    "closeCertificateVersions",
    "restoreCertificateVersion",
  ]) {
    assert.ok(template.includes(`  ${name},`));
  }
  assert.match(script, /certificate\/versions/);
  assert.match(template, /version\.history_id/);
  assert.match(template, /certificate-history-modal/);
  for (const selector of [
    ".certificate-history-modal",
    ".certificate-history-list",
    ".certificate-history-row",
  ]) {
    assert.match(stylesheet, new RegExp(`${selector.replaceAll(".", "\\.")}\\s*\\{`));
  }
});

test("证书历史版本读取列表并阻止恢复当前版本", async (t) => {
  const requests = [];
  const domain = { id: "domain-history", name: "example.test" };
  const versions = [
    { history_id: "history-current", current: true, version: 2 },
    { history_id: "history-old", current: false, version: 1 },
  ];
  const { state } = await mountComposable(t, useDashboard, async (path, options = {}) => {
    requests.push({ path, options });
    if (path === `/api/v1/domains/${domain.id}/certificate/versions`) {
      return Response.json({ items: versions });
    }
    return Response.json({ items: [] });
  });
  await state.openCertificateVersions(domain);
  assert.equal(state.showCertificateVersions.value, true);
  assert.deepEqual(state.certificateVersions.value, versions);
  assert.equal(
    requests.some(({ path }) => path === `/api/v1/domains/${domain.id}/certificate/versions`),
    true,
  );
  window.confirm = () => assert.fail("当前版本不应触发恢复确认");
  await state.restoreCertificateVersion(versions[0]);
  assert.equal(
    requests.some(({ path }) => path.includes("/restore")),
    false,
  );
});

test("证书历史版本恢复使用 history_id、幂等键并打开任务详情", async (t) => {
  const requests = [];
  const domain = { id: "domain-history", name: "example.test" };
  const task = { id: "task-history", task_type: "certificate_restore", status: "queued" };
  const version = { history_id: "history-old", current: false, version: 1 };
  const { state } = await mountComposable(t, useDashboard, async (path, options = {}) => {
    requests.push({ path, options });
    if (path.endsWith("/certificate/versions")) return Response.json({ items: [version] });
    if (path.endsWith("/restore")) return Response.json({ accepted: true, task }, { status: 202 });
    if (path === `/api/v1/tasks/${task.id}`) return Response.json(task);
    if (path.endsWith(`/api/v1/tasks/${task.id}/attempts`)) return Response.json({ items: [] });
    if (path.endsWith(`/api/v1/tasks/${task.id}/deployment-results`)) return Response.json({ items: [] });
    return Response.json({ items: [] });
  });
  window.confirm = () => true;
  await state.openCertificateVersions(domain);
  await state.restoreCertificateVersion(version);
  const restoreRequest = requests.find(({ path }) => path.endsWith("/restore"));
  assert.ok(restoreRequest);
  assert.equal(
    restoreRequest.path,
    `/api/v1/domains/${domain.id}/certificate/versions/${version.history_id}/restore`,
  );
  assert.match(restoreRequest.options.headers["Idempotency-Key"], /^certificate-restore-domain-history-/);
  assert.equal(state.showCertificateVersions.value, false);
  assert.equal(state.showTaskDetails.value, true);
  assert.equal(state.taskDetails.value.id, task.id);
  assert.match(state.notice.value, /恢复完成后请另行确认远端部署状态/);
});

test("完整页面逐个渲染六个板块，每次只有一个内容标题", async (t) => {
  const { readFile } = await import("node:fs/promises");
  const { parse, compileTemplate } = await import("@vue/compiler-sfc");
  const { renderToString } = await import("@vue/server-renderer");
  const Vue = await import("vue");
  const source = await readFile(new URL("./Dashboard.vue", import.meta.url), "utf8");
  const { descriptor, errors } = parse(source);
  assert.deepEqual(errors, []);
  const compiled = compileTemplate({
    source: descriptor.template.content,
    filename: "Dashboard.vue",
    id: "dashboard-panels",
    compilerOptions: { mode: "function" },
  });
  assert.deepEqual(compiled.errors, []);
  const render = new Function("Vue", compiled.code)(Vue);
  const { state } = await setup(t, () => verifiedResponse());
  state.closeSettings();
  state.currentUser.value = { account: "test-admin", role: "super_admin" };
  const navigation = useDashboardNavigation(state.currentUser);
  const headings = {
    overview: "基本信息",
    activity: "运行概览",
    projects: "项目与成员",
    domains: "托管域名",
    servers: "部署服务器",
    "dns-accounts": "DNS 账户管理",
    users: "用户管理",
  };
  for (const section of navigation.sections.value) {
    navigation.selectSection(section.id);
    const app = Vue.createSSRApp({
      render,
      setup: () => ({ ...state, ...navigation, dashboard: state }),
    });
    for (const name of ["ChangePassword", "DashboardNavigation", "SettingsModal", "DomainModal", "ServerModal", "RuntimeOverview"]) {
      app.component(name, { render: () => null });
    }
    app.component("UserManagement", { render: () => Vue.h("h2", headings.users) });
    app.component("ProjectManagement", { render: () => Vue.h("h2", headings.projects) });
    app.component("DNSAccounts", { render: () => Vue.h("h2", headings["dns-accounts"]) });
    app.component("DeploymentResult", { render: () => null });
    app.component("TaskDeploymentResults", { render: () => null });
    app.component("TaskValidationResult", { render: () => null });
    const warnings = [];
    app.config.warnHandler = (message) => warnings.push(message);
    const html = await renderToString(app);
    assert.deepEqual([...html.matchAll(/<h2>(.*?)<\/h2>/g)].map((match) => match[1]), [headings[section.id]]);
    assert.deepEqual(warnings, []);
    if (section.id === "overview") {
      assert.match(html, /test-admin/);
      assert.doesNotMatch(html, /立即检查续期/);
    }
  }
});

test("certificateImport.preservesRequiredFileInputSelection", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  state.openCertificateImport({ id: "domain-import", name: "example.test" });
  for (const kind of ["certificate", "private_key", "issuer_bundle"]) {
    const file = new File([kind], `${kind}.pem`);
    const target = { files: [file], value: `C:\\fakepath\\${file.name}` };
    state.selectCertificateImportFile(kind, { target });
    assert.equal(target.value, `C:\\fakepath\\${file.name}`);
    assert.equal(target.files[0], file);
  }
});

test("certificateImport.submitsSelectedFilesAndResetsOnReopen", async (t) => {
  const imports = [];
  const { state } = await mountComposable(t, useDashboard, async (path, options) => {
    if (path.endsWith("/certificate/import")) {
      imports.push({ path, body: options.body });
      return Response.json({ imported: true });
    }
    return Response.json({ items: [] });
  });
  state.openCertificateImport({ id: "domain-import", name: "example.test" });
  for (const kind of ["certificate", "private_key", "issuer_bundle"]) {
    const file = new File([kind], `${kind}.pem`);
    state.selectCertificateImportFile(kind, { target: { files: [file], value: file.name } });
  }
  await state.importCertificate();
  assert.equal(imports.length, 1);
  assert.equal(imports[0].path, "/api/v1/domains/domain-import/certificate/import");
  for (const kind of ["certificate", "private_key", "issuer_bundle"]) {
    assert.equal(await imports[0].body.get(kind).text(), kind);
  }
  state.closeCertificateImport();
  state.openCertificateImport({ id: "another-domain", name: "another.test" });
  await state.importCertificate();
  assert.equal(imports.length, 1);
  assert.ok(state.importError.value);
});

test("导入异步接收只展示任务并等待结果，不提前宣布保存成功", async (t) => {
  let imports = 0;
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/certificate/import")) {
      imports++;
      return Response.json({ accepted: true, task: { id: "import-task", status: "queued" } }, { status: 202 });
    }
    return Response.json({ items: [] });
  });
  state.openCertificateImport({ id: "domain-import", name: "example.test" });
  for (const kind of ["certificate", "private_key"]) {
    state.selectCertificateImportFile(kind, { target: { files: [new File([kind], `${kind}.pem`)] } });
  }
  await state.importCertificate();
  assert.equal(state.deploymentResult.value.taskID, "import-task");
  assert.equal(state.deploymentResult.value.pending, true);
  assert.equal(state.deploymentResult.value.report, null);
  assert.notEqual(state.deploymentResult.value.certificateImported, true);
  assert.equal(state.showCertificateImport.value, false);
  assert.match(state.notice.value, /导入任务已提交/);
  assert.doesNotMatch(state.notice.value, /证书已导入/);
  await state.importCertificate();
  assert.equal(imports, 1);
});

test("导入响应断开后使用原幂等键找回任务，不重复执行导入", async (t) => {
  const keys = [];
  const { state } = await mountComposable(t, useDashboard, async (path, options) => {
    if (path.endsWith("/certificate/import")) {
      keys.push(options.headers["Idempotency-Key"]);
      if (keys.length === 1) throw new Error("连接断开");
      return Response.json({ replayed: true, task: { id: "existing-import", status: "running" } }, { status: 409 });
    }
    return Response.json({ items: [] });
  });
  state.openCertificateImport({ id: "domain-import", name: "example.test" });
  for (const kind of ["certificate", "private_key"]) {
    state.selectCertificateImportFile(kind, { target: { files: [new File([kind], `${kind}.pem`)] } });
  }
  await state.importCertificate();
  assert.match(state.importError.value, /连接断开/);
  await state.importCertificate();
  assert.equal(keys.length, 2);
  assert.match(keys[0], /^import-domain-import-/);
  assert.equal(keys[0], keys[1]);
  assert.equal(state.importError.value, "");
  assert.equal(state.deploymentResult.value.taskID, "existing-import");
  assert.equal(state.deploymentResult.value.pending, true);
});

test("异步导入缺少任务编号时不能显示导入成功", async (t) => {
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/certificate/import")) return Response.json({ accepted: true }, { status: 202 });
    return Response.json({ items: [] });
  });
  state.openCertificateImport({ id: "domain-import", name: "example.test" });
  for (const kind of ["certificate", "private_key"]) {
    state.selectCertificateImportFile(kind, { target: { files: [new File([kind], `${kind}.pem`)] } });
  }
  await state.importCertificate();
  assert.match(state.importError.value, /未返回任务编号/);
  assert.equal(state.importResult.value, null);
  assert.doesNotMatch(state.notice.value, /证书已导入/);
});

test("证书已导入但部署失败时展示服务器结果并刷新状态，不能重复导入", async (t) => {
  let imports = 0;
  let refreshes = 0;
  const report = {
    total: 1, success: 0, failed: 1, status: "failed",
    results: [{ server_id: "server-1", server: "验收服务器", status: "failed", message: "连接服务器失败" }],
  };
  const message = "证书已导入，但部署未完成。请修正服务器配置后重试部署，无需重新导入。";
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/certificate/import")) {
      imports++;
      return Response.json({ imported: true, deployment: report, error: message }, { status: 502 });
    }
    if (path === "/api/v1/domains") refreshes++;
    return Response.json({ items: [] });
  });
  const before = refreshes;
  state.openCertificateImport({ id: "domain-import", name: "example.test" });
  for (const kind of ["certificate", "private_key"]) {
    state.selectCertificateImportFile(kind, { target: { files: [new File([kind], `${kind}.pem`)] } });
  }
  await state.importCertificate();
  assert.equal(state.importResult.value?.imported, true);
  assert.equal(state.importError.value, "");
  assert.equal(state.showCertificateImport.value, false);
  assert.equal(state.deploymentResult.value.certificateImported, true);
  assert.equal(state.deploymentResult.value.error, message);
  assert.deepEqual(state.deploymentResult.value.report, report);
  assert.ok(refreshes > before);
  assert.match(state.notice.value, /证书已导入/);
  await state.importCertificate();
  assert.equal(imports, 1);
});

test("证书未保存时保留导入表单并显示真正的导入失败", async (t) => {
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/certificate/import")) {
      return Response.json({ imported: false, error: "证书导入失败" }, { status: 502 });
    }
    return Response.json({ items: [] });
  });
  state.openCertificateImport({ id: "domain-import", name: "example.test" });
  for (const kind of ["certificate", "private_key"]) {
    state.selectCertificateImportFile(kind, { target: { files: [new File([kind], `${kind}.pem`)] } });
  }
  await state.importCertificate();
  assert.equal(state.importResult.value, null);
  assert.equal(state.deploymentResult.value, null);
  assert.equal(state.showCertificateImport.value, true);
  assert.equal(state.importError.value, "证书导入失败");
});

test("导入执行中阻止重复提交，成功后展示实际部署结果", async (t) => {
  const pending = deferred();
  let imports = 0;
  const report = { total: 1, success: 1, failed: 0, status: "success", results: [{ server_id: "server-1", status: "success" }] };
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/certificate/import")) {
      imports++;
      return pending.promise;
    }
    return Response.json({ items: [] });
  });
  state.openCertificateImport({ id: "domain-import", name: "example.test" });
  for (const kind of ["certificate", "private_key"]) {
    state.selectCertificateImportFile(kind, { target: { files: [new File([kind], `${kind}.pem`)] } });
  }
  const action = state.importCertificate();
  const duplicate = state.importCertificate();
  assert.equal(imports, 1);
  pending.resolve(Response.json({ imported: true, deployment: report }));
  await Promise.all([action, duplicate]);
  assert.equal(state.showCertificateImport.value, false);
  assert.equal(state.deploymentResult.value.error, "");
  assert.deepEqual(state.deploymentResult.value.report, report);
});

async function setup(t, request) {
  const mounted = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/auth/session")) return Response.json({ account: "test-admin", role: "super_admin" });
    if (path === "/api/v1/dns/test") return request();
    if (path.endsWith("/dns/providers")) return Response.json({ items: [aliyunProvider] });
    if (path.endsWith("/settings")) return Response.json({ dns: { provider: "aliyun", credential_configured: true, verification_state: "verified" } });
    return Response.json({ items: [] });
  });
  const { state } = mounted;
  state.currentUser.value = { account: "test-admin", role: "super_admin" };
  state.openSettings();
  await nextTick();
  return mounted;
}

test("普通管理员不能在前端执行全局续期、DNS验证或保存系统设置", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  state.currentUser.value = { account: "operator", role: "admin" };
  state.showSettings.value = true;

  await state.testDNS();
  assert.equal(state.dnsTestError.value, "只有超级管理员可以进行 DNS 在线测试");
  await state.saveSettings();
  assert.equal(state.settingsError.value, "只有超级管理员可以保存系统设置");
  await state.runRenewalNow();
  assert.equal(state.error.value, "只有超级管理员可以执行续期检查");
});

test("编辑服务器正确保留已有密码状态，新建和未配置密码仍要求输入", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  state.openServerEdit({ id: "configured", auth_method: "password", password_configured: true });
  assert.equal(state.serverForm.value.passwordConfigured, true);
  assert.equal(state.serverForm.value.password, "");
  for (const password_configured of [false, undefined]) {
    state.openServerEdit({ id: "missing", auth_method: "password", password_configured });
    assert.equal(state.serverForm.value.passwordConfigured, false);
  }
  state.openServerCreate();
  assert.equal(state.serverForm.value.passwordConfigured, false);
  assert.equal(state.serverEditingId.value, "");
});

test("部署服务器仅使用密码认证且不暴露私钥管理入口", async (t) => {
  const requests = [];
  const { state } = await mountComposable(t, useDashboard, async (path, options = {}) => {
    if (options.method) {
      requests.push({ path, ...options });
      return Response.json({});
    }
    return verifiedResponse();
  });
  state.openServerCreate();
  assert.equal(state.serverForm.value.auth_method, "password");
  assert.equal("openSSHKeyCreate" in state, false);
  assert.equal("sshKeys" in state, false);
  state.serverForm.value.name = "生产服务器";
  state.serverForm.value.address = "example.test";
  state.serverForm.value.username = "deploy";
  state.serverForm.value.password = "test-password";
  state.serverForm.value.host_key_fingerprint = "SHA256:test";
  await state.saveServer();
  const payload = JSON.parse(requests.at(-1).body);
  assert.equal(payload.password, "test-password");
  assert.equal("auth_method" in payload, false);
  assert.equal("private_key_ref" in payload, false);
});

async function fill(state) {
  state.settingsForm.value.dns.credentials = { access_key_id: "fake-key" };
  state.settingsForm.value.dns.test_domain = "example.test";
  await nextTick();
}

test("管理续期的任务类型、审计和阶段使用中文", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  assert.equal(state.taskTypeLabel("management_renewal"), "管理证书续期");
  assert.equal(state.auditActionLabel("management_renewal"), "管理证书续期");
  assert.equal(state.taskStageLabel("activate"), "激活管理证书");
  assert.equal(state.taskStageLabel("retry_wait"), "等待续期重试");
  assert.equal(state.taskStatusLabels.partial_success, "\u90e8\u5206\u6210\u529f");
  assert.equal(state.attemptStatusLabels.retry_wait, "\u7b49\u5f85\u91cd\u8bd5");
});

test("已保存凭据不变可保存，输入新凭据必须重新验证", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  assert.equal(state.dnsReadyToSave.value, true);
  assert.equal(state.settingsForm.value.acme.propagation_timeout_secs, 300);
  assert.equal(state.settingsForm.value.acme.propagation_polling_interval_secs, 5);
  await fill(state);
  assert.equal(state.dnsReadyToSave.value, false);
  await state.testDNS();
  assert.equal(state.dnsReadyToSave.value, true);
  state.settingsForm.value.dns.credentials.access_key_id = "changed-key";
  assert.equal(state.dnsReadyToSave.value, false);
});

test("管理设置不接受旧输入的在途 DNS 响应", async (t) => {
  const pending = deferred();
  const { state } = await setup(t, () => pending.promise);
  await fill(state);
  const task = state.testDNS();
  state.settingsForm.value.dns.test_domain = "other.test";
  await nextTick();
  pending.resolve(verifiedResponse());
  await task;
  assert.equal(state.dnsReadyToSave.value, false);
  assert.equal(state.settingsForm.value.dns.verification_token, "");
});

test("管理设置拒绝过期验证令牌", async (t) => {
  const { state } = await setup(t, () => Response.json({ credential_ref: "secret://test/only", verification_token: "expired", expires_at: "2000-01-01T00:00:00Z" }));
  await fill(state);
  await state.testDNS();
  assert.equal(state.dnsReadyToSave.value, false);
});

test("管理设置变更阿里云凭据不能复用验证令牌", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  await fill(state);
  await state.testDNS();
  assert.equal(state.dnsReadyToSave.value, true);
  state.settingsForm.value.dns.credentials.access_key_id = "changed-key";
  assert.equal(state.dnsReadyToSave.value, false);
  assert.equal(state.settingsForm.value.dns.verification_token, "");
});

test("关闭设置后不显示旧 DNS 请求的成功提示", async (t) => {
  const pending = deferred();
  const { state } = await setup(t, () => pending.promise);
  await fill(state);
  const task = state.testDNS();
  state.closeSettings();
  pending.resolve(verifiedResponse());
  await task;
  assert.equal(state.settingsForm.value.dns.verification_token, "");
  assert.equal(state.notice.value, "");
});

test("管理设置新请求成功后旧失败不能删除令牌", async (t) => {
  const first = deferred();
  const second = deferred();
  let count = 0;
  const { state } = await setup(t, () => (++count === 1 ? first.promise : second.promise));
  await fill(state);
  const oldTask = state.testDNS();
  const newTask = state.testDNS();
  second.resolve(verifiedResponse("new-token"));
  await newTask;
  first.reject(new Error("旧请求失败"));
  await oldTask;
  assert.equal(state.settingsForm.value.dns.verification_token, "new-token");
  assert.equal(state.dnsTestError.value, "");
  assert.equal(state.dnsReadyToSave.value, true);
});

test("管理设置 DNS 验证显示等待进度，并在结束后停止计时", async (t) => {
  const pending = deferred();
  const { state, runIntervals, advanceTime } = await setup(t, () => pending.promise);
  await fill(state);
  const task = state.testDNS();

  assert.equal(state.dnsTesting.value, true);
  assert.equal(state.dnsVerificationElapsedSeconds.value, 0);
  assert.equal(state.dnsVerificationTimeoutSeconds.value, 6);
  advanceTime(3200);
  runIntervals();
  assert.equal(state.dnsVerificationElapsedSeconds.value, 3);

  pending.resolve(Response.json({
    verified: true,
    credential_ref: "secret://test/only",
    verification_token: "test-token",
    expires_at: new Date(Date.now() + 60000).toISOString(),
    verification_timeout_secs: 6,
  }));
  await task;
  assert.equal(state.dnsTesting.value, false);
  assert.equal(state.dnsVerificationElapsedSeconds.value, 0);
  assert.equal(state.dnsVerificationTimeoutSeconds.value, 6);
  advanceTime(1000);
  runIntervals();
  assert.equal(state.dnsVerificationElapsedSeconds.value, 0);
});

test("关闭管理设置会停止 DNS 等待计时器", async (t) => {
  const pending = deferred();
  const { state, runIntervals, advanceTime } = await setup(t, () => pending.promise);
  await fill(state);
  const task = state.testDNS();
  advanceTime(1000);
  runIntervals();
  assert.equal(state.dnsVerificationElapsedSeconds.value, 1);

  state.closeSettings();
  assert.equal(state.dnsTesting.value, false);
  assert.equal(state.dnsVerificationElapsedSeconds.value, 0);
  advanceTime(1000);
  runIntervals();
  assert.equal(state.dnsVerificationElapsedSeconds.value, 0);

  pending.resolve(verifiedResponse());
  await task;
});

test("auditActionLabel", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  const localized = state.auditActionLabels.settings_update;
  assert.equal(state.auditActionLabel(localized), localized);
  assert.equal(state.auditActionLabel(` ${localized} `), localized);
  assert.equal(state.auditActionLabel("settings_update"), localized);
  assert.equal(state.auditActionLabel("unmapped_action"), state.taskTypeLabel("unmapped_action"));
  assert.equal(state.auditActionLabel(null), state.taskTypeLabel(null));
  assert.equal(state.auditActionLabel(undefined), state.taskTypeLabel(undefined));
  assert.equal(state.auditActionLabel(""), state.taskTypeLabel(""));
});

test("settings.savedDnsCredentialsRemainOptionalUntilChanged", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  assert.equal(state.dnsVerificationRequired.value, false);
  state.settingsForm.value.acme.renewal_threshold_days = 91;
  assert.equal(state.dnsVerificationRequired.value, false);
  assert.equal(state.dnsReadyToSave.value, true);
  await fill(state);
  assert.equal(state.dnsVerificationRequired.value, true);
  assert.equal(state.dnsReadyToSave.value, false);
  await state.testDNS();
  assert.equal(state.dnsReadyToSave.value, true);
});


test("settingsPayloadContract", async (t) => {
  const { settingsPayload } = await import("./SettingsPayload.js");
  const { readFile } = await import("node:fs/promises");
  const contract = await readFile(
    new URL("../../../../backend/internal/httpapi/settings_api.go", import.meta.url),
    "utf8",
  );
  const { state } = await setup(t, () => verifiedResponse());
  const form = JSON.parse(JSON.stringify(state.settingsForm.value));
  form.notification.http_credential_configured = true;
  form.notification.smtp_credential_configured = true;
  form.notification.recipients = ["owner@example.test"];
  form.notification.retry_count = 0;
  form.dns.credentials = { access_key_id: "not-for-settings" };
  form.dns.credential_ref = "secret://test/new";
  form.dns.verification_token = "verified-token";
  form.dns.test_domain = "example.test";
  const payload = settingsPayload(form);
  for (const [section, type] of Object.entries({
    server: "Server", web: "Web", paths: "Paths", acme: "ACME",
    dns: "DNS", notification: "Notification",
  })) {
    const body = contract.split(`type settings${type}Request struct {`)[1].split("\n}")[0];
    const allowed = [...body.matchAll(/json:"([^"]+)"/g)].map((match) => match[1]);
    for (const key of Object.keys(payload[section])) {
      assert.ok(allowed.includes(key), `${section}.${key}`);
    }
  }
  assert.equal("smtp_credential_configured" in payload.notification, false);
  assert.equal("http_credential_configured" in payload.notification, false);
  assert.equal("credentials" in payload.dns, false);
  assert.equal(payload.dns.verification_token, "verified-token");
  assert.equal("http_credentials" in payload.notification, false);
  assert.deepEqual(payload.notification.recipients, ["owner@example.test"]);
  assert.equal(payload.notification.retry_count, 0);
  assert.equal(payload.web.enabled, true);
  assert.equal(payload.notification.smtp_enabled, false);
  assert.equal(form.notification.smtp_credential_configured, true);
});

test("settingsReadEditSaveContract", async (t) => {
  const saved = {
    server: { host: "127.0.0.1", port: 8443, https_mode: "proxy", proxy_https_port: 443 },
    web: { enabled: true, hostname: "example.test", public_ip: "" },
    paths: { certificate: "/var/lib/scert/certificates" },
    acme: { renewal_threshold_days: 6 },
    dns: { provider: "aliyun", credential_configured: true, verification_state: "verified" },
    notification: {
      smtp_credential_configured: true, http_credential_configured: true,
      recipients: ["owner@example.test"], retry_intervals_secs: [30, 120, 600],
    },
    response_only: true,
  };
  let submitted;
  const { state } = await mountComposable(t, useDashboard, async (path, options = {}) => {
    if (path.endsWith("/auth/session")) {
      return Response.json({ account: "test-admin", role: "super_admin" });
    }
    if (path.endsWith("/settings")) {
      if (options.method === "PUT") {
        submitted = JSON.parse(options.body);
        return Response.json({ settings: saved });
      }
      return Response.json(saved);
    }
    return Response.json({ items: [] });
  });
  state.currentUser.value = { account: "test-admin", role: "super_admin" };
  state.openSettings();
  state.settingsForm.value.acme.renewal_threshold_days = 91;
  assert.equal(state.dnsReadyToSave.value, true);
  await state.saveSettings();
  assert.equal(state.settingsError.value, "");
  assert.equal(state.showSettings.value, false);
  assert.equal(submitted.acme.renewal_threshold_days, 91);
  assert.deepEqual(submitted.dns, { provider: "aliyun" });
  assert.equal("smtp_credential_configured" in submitted.notification, false);
  assert.equal("http_credential_configured" in submitted.notification, false);
  assert.equal("response_only" in submitted, false);
  assert.deepEqual(submitted.notification.recipients, ["owner@example.test"]);
  assert.deepEqual(submitted.notification.retry_intervals_secs, [30, 120, 600]);
});


test("statusClass", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  for (const status of ["active", "success"]) {
    assert.equal(state.statusClass(status), "success");
  }
  for (const status of ["error", "failed"]) {
    assert.equal(state.statusClass(status), "danger");
  }
  for (const status of ["running", "queued", "cancelled", "unknown", null]) {
    assert.equal(state.statusClass(status), "warning");
  }
});

test("auditStatusRendering", async () => {
  const { readFile } = await import("node:fs/promises");
  const { compileTemplate } = await import("@vue/compiler-sfc");
  const { renderToString } = await import("@vue/server-renderer");
  const { createSSRApp } = await import("vue");
  const source = await readFile(new URL("./RuntimeOverview.vue", import.meta.url), "utf8");
  const start = source.indexOf('<article v-for="item in auditLogs"');
  assert.notEqual(start, -1);
  const end = source.indexOf("</article>", start) + "</article>".length;
  const compiled = compileTemplate({
    source: source.slice(start, end),
    filename: "AuditStatus.vue",
    id: "audit-status-regression",
    compilerOptions: { mode: "function" },
  });
  assert.deepEqual(compiled.errors, []);
  const Vue = await import("vue");
  const render = new Function("Vue", compiled.code)(Vue);
  for (const status of ["running", "queued", "cancelled", "unknown"]) {
    const html = await renderToString(createSSRApp({
      render,
      setup: () => ({
        auditLogs: [{ id: status, action: "test", status, message: "test" }],
        auditActionLabel: (value) => value,
        statusClass: () => "warning",
        taskStatusLabels: { [status]: `label-${status}` },
        displayLabel: (labels, value) => labels[value],
      }),
    }));
    assert.match(html, new RegExp(`label-${status}`));
    assert.doesNotMatch(html, /danger/);
  }
});


test("domainModal.compilesAndRendersSanFieldsWithoutLiteralNewline", async () => {
  const { readFile } = await import("node:fs/promises");
  const { parse, compileScript, compileTemplate } = await import("@vue/compiler-sfc");
  const { renderToString } = await import("@vue/server-renderer");
  const Vue = await import("vue");
  const source = await readFile(new URL("./DomainModal.vue", import.meta.url), "utf8");
  const { descriptor, errors } = parse(source);
  assert.deepEqual(errors, []);
  assert.ok(compileScript(descriptor, { id: "domain-modal" }));
  const compiled = compileTemplate({
    source: descriptor.template.content,
    filename: "DomainModal.vue",
    id: "domain-modal",
    compilerOptions: { mode: "function" },
  });
  assert.deepEqual(compiled.errors, []);
  const render = new Function("Vue", compiled.code)(Vue);
  for (const certificateType of ["single", "wildcard", "san"]) {
    const app = Vue.createSSRApp({
      render,
      setup: () => ({
        showForm: true,
        formTitle: "test-domain",
        closeForm: () => {},
        saveDomain: () => {},
        form: {
          name: "example.test",
          certificate_type: certificateType,
          sans: "",
          acme_environment: "staging",
          dns_provider: "aliyun",
          auto_renew: true,
          server_ids: [],
        },
        dnsProviders: [],
        domainServerOptions: [],
        domainBindingsLoading: false,
        domainBindingsReady: true,
        domainSaveUncertain: false,
        reloadDomainBindings() {},
        displayLabel: (_, value) => value,
        webServerLabels: {},
        formError: "",
        saving: false,
      }),
    });
    app.component("HelpTip", {
      setup: (_, { slots }) => () => Vue.h("span", { class: "test-help-tip" }, slots.default?.()),
    });
    const html = await renderToString(app);
    assert.match(html, /class="modal-card domain-modal"/);
    assert.match(html, /type="submit"/);
    assert.match(html, /class="test-help-tip"/);
    assert.doesNotMatch(html, /www\.example\.com\\napi/);
    assert.equal(html.includes('id="domain-sans"'), certificateType === "san");
    if (certificateType === "san") {
      assert.match(html, /rows="2"/);
      assert.ok(html.includes("www.example.com\napi.example.com"));
    }
  }
});

test("serverModal.compilesAndPreservesDeploymentFields", async () => {
  const { readFile } = await import("node:fs/promises");
  const { parse, compileScript, compileTemplate } = await import("@vue/compiler-sfc");
  const { renderToString } = await import("@vue/server-renderer");
  const Vue = await import("vue");
  const source = await readFile(new URL("./ServerModal.vue", import.meta.url), "utf8");
  const { descriptor, errors } = parse(source);
  assert.deepEqual(errors, []);
  assert.ok(compileScript(descriptor, { id: "server-modal" }));
  const compiled = compileTemplate({
    source: descriptor.template.content,
    filename: "ServerModal.vue",
    id: "server-modal",
    compilerOptions: { mode: "function" },
  });
  assert.deepEqual(compiled.errors, []);
  const render = new Function("Vue", compiled.code)(Vue);
  for (const webServer of ["nginx", "caddy", "apache", "custom"]) {
    const app = Vue.createSSRApp({
      render,
      setup: () => ({
        showServerForm: true,
        closeServerForm: () => {},
        serverEditingId: null,
        serverForm: {
          name: "test-server",
          web_server: webServer,
          address: "example.test",
          port: 22,
          username: "deploy",
          host_key_fingerprint: "SHA256:test-fingerprint",
          certificate_path: "/test/cert.pem",
          certificate_chain_path: "/test/fullchain.pem",
          private_key_path: "/test/key.pem",
          validation_command: "nginx\n-t",
          reload_command: "nginx\n-s\nreload",
        },
        defaultCertificatePath: "",
        defaultCertificateChainPath: "",
        defaultPrivateKeyPath: "",
        serverFormError: "test-validation-error",
        serverSaving: false,
        saveServer: () => {},
      }),
    });
    app.component("HelpTip", {
      setup: (_, { slots }) => () => Vue.h("span", { class: "test-help-tip" }, slots.default?.()),
    });
    const html = await renderToString(app);
    assert.match(html, /class="modal-card server-modal"/);
    assert.match(html, /class="server-paths"/);
    assert.match(html, /type="submit"/);
    assert.match(html, /SHA256:test-fingerprint/);
    assert.match(html, /test-validation-error/);
    assert.match(html, /value="22"/);
    assert.match(html, /min="1"/);
    assert.match(html, /max="65535"/);
    assert.equal((html.match(/class="test-help-tip"/g) || []).length, 4);
    for (const path of ["/test/cert.pem", "/test/fullchain.pem", "/test/key.pem"]) {
      assert.ok(html.includes(path));
    }
    assert.ok(html.includes("nginx\n-t"));
    assert.ok(html.includes("nginx\n-s\nreload"));
  }
});

test("changePassword.loadsScopedBaseStylesAndCompiles", async () => {
  const { readFile } = await import("node:fs/promises");
  const { parse, compileScript, compileTemplate } = await import("@vue/compiler-sfc");
  const source = await readFile(new URL("../components/ChangePassword/ChangePassword.vue", import.meta.url), "utf8");
  const { descriptor, errors } = parse(source);
  assert.deepEqual(errors, []);
  assert.ok(compileScript(descriptor, { id: "change-password" }));
  const compiled = compileTemplate({
    source: descriptor.template.content,
    filename: "ChangePassword.vue",
    id: "change-password",
  });
  assert.deepEqual(compiled.errors, []);
  assert.ok(descriptor.styles.some(style => style.scoped && style.src === "../../Dashboard/Dashboard.css"));
  assert.ok(descriptor.styles.some(style => style.scoped && style.src === "./ChangePassword.css"));
  assert.match(source, /<HelpTip>/);
  const base = await readFile(new URL("./Dashboard.css", import.meta.url), "utf8");
  assert.match(base, /\.modal-backdrop\s*\{[^}]*position:\s*fixed;/);
  assert.match(base, /\.field\s*\{/);
});

test("domainAction.refreshesPersistedFailureWithoutLosingError", async (t) => {
  let issued = false;
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/issue")) {
      issued = true;
      return Response.json({ error: "test-issue-failure" }, { status: 502 });
    }
    if (path.startsWith("/api/v1/tasks?")) {
      return Response.json({ items: issued ? [{ id: "task-1", status: "failed" }] : [] });
    }
    return Response.json({ items: [] });
  });
  await state.issueDomain({ id: "domain-1", name: "example.test" });
  assert.equal(state.error.value, "test-issue-failure");
  assert.equal(state.notice.value, "");
  assert.deepEqual(state.tasks.value, [{ id: "task-1", status: "failed" }]);
  assert.equal(state.displayLabel(state.taskStatusLabels, "error"), state.taskStatusLabels.failed);
});

test("domainAction.preservesOriginalErrorWhenRefreshAlsoFails", async (t) => {
  let issued = false;
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/issue")) {
      issued = true;
      return Response.json({ error: "test-original-failure" }, { status: 502 });
    }
    if (issued) return Response.json({ error: "test-refresh-failure" }, { status: 503 });
    return Response.json({ items: [] });
  });
  await state.issueDomain({ id: "domain-1", name: "example.test" });
  assert.equal(state.error.value, "test-original-failure");
  assert.equal(state.loading.value, false);
});

test("domainAction.refreshesSuccessfulResultAndRetainsNotice", async (t) => {
  let completed = false;
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/certificate/validate")) {
      completed = true;
      return Response.json({ valid: true });
    }
    if (path.startsWith("/api/v1/tasks?")) {
      return Response.json({ items: completed ? [{ id: "task-2", status: "success" }] : [] });
    }
    return Response.json({ items: [] });
  });
  await state.validateDomain({ id: "domain-1", name: "example.test" });
  assert.equal(state.error.value, "");
  assert.match(state.notice.value, /example\.test/);
  assert.deepEqual(state.tasks.value, [{ id: "task-2", status: "success" }]);
});

test("dashboardLayout.keepsActivityListsBoundedAndKeyboardAccessible", async () => {
  const { readFile } = await import("node:fs/promises");
  const template = await readFile(new URL("./RuntimeOverview.vue", import.meta.url), "utf8");
  const stylesheet = await readFile(new URL("./RuntimeOverview.css", import.meta.url), "utf8");
  assert.match(template, /class="runtime-table-scroll" tabindex="0"/);
  assert.match(template, /class="runtime-audit-list" tabindex="0"/);
  assert.match(stylesheet, /\.runtime-audit-list\s*\{[^}]*max-height:[^;]+;[^}]*overflow:\s*auto;/);
  assert.match(stylesheet, /\.runtime-table-scroll:focus-visible/);
  assert.match(stylesheet, /\.runtime-audit-list:focus-visible/);
});

test("runtimeOverview.summarizesCertificateBoundariesAndConfiguration", () => {
  const now = Date.parse("2026-09-25T12:00:00Z");
  const day = 86400000;
  const domains = ["valid", "near", "expired", "missing", "invalid", "future"].map((id) => ({ id, name: `${id}.test`, auto_renew: id === "valid" }));
  const certificate = (id, end, start = now - day) => ({ domain: { id }, certificate: { saved: { not_after: end, not_before: new Date(start).toISOString() } } });
  const result = summarizeRuntime({
    domains,
    certificates: [
      certificate("valid", new Date(now + 11 * day).toISOString()),
      certificate("near", new Date(now + 10 * day).toISOString()),
      certificate("expired", new Date(now).toISOString()),
      certificate("invalid", "invalid"),
      certificate("future", new Date(now + 30 * day).toISOString(), now + day),
    ],
    settings: { acme: { renewal_near_expiry_days: 10 } },
    servers: [{ id: "server" }],
    domainServers: { valid: [{ id: "server" }], near: [] },
  }, now);
  assert.equal(result.total, 6);
  assert.equal(result.valid, 2);
  assert.equal(result.expiring, 1);
  assert.equal(result.expired, 1);
  assert.equal(result.missing, 3);
  assert.equal(result.alerts.length, 5);
  assert.equal(result.alerts[0].id, "expired");
  assert.equal(result.autoRenew, 1);
  assert.equal(result.configuredServers, 1);
  assert.equal(result.boundDomains, 1);
});

test("runtimeOverview.handlesEmptyDataThresholdsAndTaskOrdering", () => {
  const empty = summarizeRuntime({});
  for (const key of ["total", "valid", "expiring", "expired", "missing", "autoRenew", "configuredServers", "boundDomains", "activeTasks", "failedTasks"]) assert.equal(empty[key], 0);
  for (const value of [0, -1, "invalid"]) assert.equal(summarizeRuntime({ settings: { acme: { renewal_near_expiry_days: value } } }).threshold, 10);
  assert.equal(summarizeRuntime({ settings: { acme: { renewal_near_expiry_days: 30 } } }).threshold, 30);
  const tasks = ["queued", "running", "retry_wait", "cancelling", "failed", "error", "success", "cancelled"].map((status, index) => ({ id: index, status, created_at: `2026-09-25T12:0${index}:00Z` }));
  const original = structuredClone(tasks);
  const result = summarizeRuntime({ tasks });
  assert.equal(result.activeTasks, 4);
  assert.equal(result.failedTasks, 2);
  assert.deepEqual(result.tasks.map((item) => item.id), [7, 6, 5, 4, 3, 2, 1, 0]);
  assert.deepEqual(tasks, original);
  assert.equal(runtimeTime(null), runtimeTime("invalid"));
  assert.doesNotMatch(runtimeTime("2026-09-25T12:00:00Z"), /Invalid/);
});

test("runtimeOverview.defaultsToSummaryAndLimitsRecentTasks", async (t) => {
  const data = {
    domains: ref([{ id: "domain", name: "example.test" }]), certificates: ref([]),
    servers: ref([]), domainServers: ref({}), settings: ref({}),
    tasks: ref(Array.from({ length: 12 }, (_, id) => ({ id, status: "success" }))),
  };
  const { state } = await mountComposable(t, () => useRuntimeOverview(data), async () => Response.json({}));
  assert.equal(state.detailView.value, "");
  assert.equal(state.visibleTasks.value.length, 5);
  state.detailView.value = "tasks";
  assert.equal(state.visibleTasks.value.length, 12);
  state.detailView.value = "";
  assert.equal(state.visibleTasks.value.length, 5);
  assert.equal(state.taskDomain({ domain_id: "domain" }), "example.test");
  assert.notEqual(state.taskDomain({ task_type: "management_renewal" }), state.taskDomain({}));
});

test("runtimeOverview.compilesAndKeepsAuditOutOfDefaultView", async () => {
  const { readFile } = await import("node:fs/promises");
  const { parse, compileScript, compileTemplate } = await import("@vue/compiler-sfc");
  const { renderToString } = await import("@vue/server-renderer");
  const Vue = await import("vue");
  const source = await readFile(new URL("./RuntimeOverview.vue", import.meta.url), "utf8");
  const { descriptor, errors } = parse(source);
  assert.deepEqual(errors, []);
  assert.ok(compileScript(descriptor, { id: "runtime-overview" }));
  const compiled = compileTemplate({ source: descriptor.template.content, filename: "RuntimeOverview.vue", id: "runtime-overview", compilerOptions: { mode: "function" } });
  assert.deepEqual(compiled.errors, []);
  const render = new Function("Vue", compiled.code)(Vue);
  for (const detailView of ["", "tasks", "audit"]) {
    const app = Vue.createSSRApp({ render, setup: () => ({
      loading: false, error: "", detailView, summary: summarizeRuntime({}),
      visibleTasks: [], auditLogs: [{ id: "audit", action: "login", status: "success", message: "audit-only-marker" }],
      auditActionLabel: (value) => value, statusClass: () => "success",
    }) });
    app.component("HelpTip", { render: () => null });
    const warnings = [];
    app.config.warnHandler = (message) => warnings.push(message);
    const html = await renderToString(app);
    assert.equal(html.includes('class="runtime-metrics"'), detailView === "");
    assert.equal(html.includes("audit-only-marker"), detailView === "audit");
    assert.deepEqual(warnings, []);
  }
});

test("dashboardLayout.replacesHeroWithBasicInformationSummary", async () => {
  const { readFile } = await import("node:fs/promises");
  const template = await readFile(new URL("./Dashboard.vue", import.meta.url), "utf8");
  const stylesheet = await readFile(new URL("./Dashboard.css", import.meta.url), "utf8");
  assert.doesNotMatch(template, /class="hero-card"/);
  assert.match(template, /class="panel overview-panel"/);
  assert.match(template, /当前用户/);
  assert.match(template, /到期时间/);
  assert.match(template, /部署服务器/);
  assert.match(stylesheet, /\.overview-summary-grid\s*\{[^}]*grid-template-columns: repeat\(3/);
  assert.match(stylesheet, /\.overview-domain-row\s*\{[^}]*grid-template-columns:/);
});

test("dashboardLayout.paginatesHostedDomainsWithConfiguredPageSizes", async (t) => {
  const { state } = await mountComposable(t, useDashboard, async () => Response.json({ items: [] }));
  state.domains.value = Array.from({ length: 16 }, (_, index) => ({
    id: `domain-${index + 1}`,
    name: `domain-${index + 1}.example.test`,
  }));
  await nextTick();

  assert.deepEqual(state.domainPageSizeOptions, [5, 10, 15, 30]);
  assert.equal(state.domainPageCount.value, 4);
  assert.equal(state.paginatedDomains.value.length, 5);
  assert.equal(state.domainPageStart.value, 1);
  assert.equal(state.domainPageEnd.value, 5);

  state.nextDomainPage();
  assert.equal(state.domainPage.value, 2);
  assert.equal(state.paginatedDomains.value[0].id, "domain-6");

  state.domainPageSize.value = 10;
  await nextTick();
  assert.equal(state.domainPageCount.value, 2);
  assert.equal(state.paginatedDomains.value.length, 10);

  state.domainPage.value = 2;
  state.domainPageSize.value = 5;
  await nextTick();
  assert.equal(state.domainPage.value, 1);
  state.domainPage.value = 2;
  state.nextDomainPage();
  state.nextDomainPage();
  assert.equal(state.domainPage.value, 4);
  state.nextDomainPage();
  assert.equal(state.domainPage.value, 4);
  state.previousDomainPage();
  assert.equal(state.domainPage.value, 3);
  assert.equal(state.statusClass("valid"), "success");
  assert.equal(state.displayLabel(state.domainStatusLabels, "valid"), "正常");
});

test("dashboardLayout.movesOverviewDetailsAboveHostedDomainList", async () => {
  const { readFile } = await import("node:fs/promises");
  const template = await readFile(new URL("./Dashboard.vue", import.meta.url), "utf8");
  const detailsIndex = template.indexOf('class="overview-details-grid"');
  const listHeadingIndex = template.indexOf('class="overview-domain-heading"');
  assert.ok(detailsIndex >= 0);
  assert.ok(listHeadingIndex >= 0);
  assert.ok(detailsIndex < listHeadingIndex);
  assert.match(template, /v-model\.number="domainPageSize"/);
  assert.match(template, /上一页/);
  assert.match(template, /下一页/);
});

test("dashboardLayout.usesSharedTooltipForUserManagementDescription", async () => {
  const { readFile } = await import("node:fs/promises");
  const template = await readFile(new URL("../UserManagement/UserManagement.vue", import.meta.url), "utf8");
  assert.match(template, /import HelpTip from "\.\.\/components\/HelpTip\/HelpTip.vue";/);
  assert.match(template, /<HelpTip>[\s\S]*?<\/HelpTip>/);
  assert.doesNotMatch(template, /<p class="user-management-description">/);
});


test("workflowTasks.renderOnlySupportedActions", async () => {
  const { readFile } = await import("node:fs/promises");
  const { compileTemplate } = await import("@vue/compiler-sfc");
  const { renderToString } = await import("@vue/server-renderer");
  const Vue = await import("vue");
  const source = await readFile(new URL("./RuntimeOverview.vue", import.meta.url), "utf8");
  const start = source.indexOf('<tr v-for="item in visibleTasks"');
  assert.notEqual(start, -1);
  const end = source.indexOf("</tr>", start) + "</tr>".length;
  const compiled = compileTemplate({
    source: `<table><tbody>${source.slice(start, end)}</tbody></table>`,
    filename: "RuntimeTaskActions.vue",
    id: "runtime-task-actions",
    compilerOptions: { mode: "function" },
  });
  assert.deepEqual(compiled.errors, []);
  const render = new Function("Vue", compiled.code)(Vue);
  for (const taskType of ["certificate_issue", "certificate_import", "certificate_validate", "renewal_check", "renewal_run", "certificate_deploy", "management_renewal", "unknown"]) {
    for (const status of ["queued", "running", "success", "failed", "cancelled", "partial_success"]) {
      const html = await renderToString(Vue.createSSRApp({
        render,
        setup: () => ({
          visibleTasks: [{ id: "task", task_type: taskType, status }],
          taskTypeLabel: (value) => value,
          taskDomain: () => "example.test",
          statusClass: () => "warning",
          displayLabel: (_, value) => value,
          taskStatusLabels: {},
          runtimeTime: () => "2026-09-26",
          openTaskDetails: () => {},
          cancelTask: () => {},
          retryTask: () => {},
        }),
      }));
      const canCancel = ["certificate_issue", "certificate_deploy", "certificate_import", "certificate_validate", "renewal_check"].includes(taskType) && ["queued", "running"].includes(status);
      const canRetry = (taskType === "certificate_issue" && ["failed", "cancelled"].includes(status)) || (taskType === "certificate_deploy" && ["failed", "cancelled", "partial_success"].includes(status));
      const buttonCount = (html.match(/<button\b/g) || []).length;
      assert.equal(buttonCount, canCancel || canRetry ? 2 : 1, `${taskType}:${status}`);
      assert.equal(canCancel && canRetry, false, `${taskType}:${status} must not show cancel and retry together`);
    }
  }
});
