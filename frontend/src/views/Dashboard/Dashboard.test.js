import assert from "node:assert/strict";
import test from "node:test";
import { nextTick } from "vue";
import { useDashboard } from "./Dashboard.js";
import { aliyunProvider, deferred, mountComposable, verifiedResponse } from "../../test-support/mount.js";

async function setup(t, request) {
  const mounted = await mountComposable(t, useDashboard, async (path) => {
    if (path === "/api/v1/dns/test") return request();
    if (path.endsWith("/dns/providers")) return Response.json({ items: [aliyunProvider] });
    if (path.endsWith("/settings")) return Response.json({ dns: { provider: "aliyun", credential_configured: true, verification_state: "verified" } });
    return Response.json({ items: [] });
  });
  const { state } = mounted;
  state.openSettings();
  await nextTick();
  return mounted;
}

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
  form.notification.http_credentials = { token: "test-only" };
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
  assert.deepEqual(payload.notification.http_credentials, { token: "test-only" });
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
    if (path.endsWith("/settings")) {
      if (options.method === "PUT") {
        submitted = JSON.parse(options.body);
        return Response.json({ settings: saved });
      }
      return Response.json(saved);
    }
    return Response.json({ items: [] });
  });
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
  const source = await readFile(new URL("./Dashboard.vue", import.meta.url), "utf8");
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
        servers: [],
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
          private_key_ref: "key-ref",
          host_key_fingerprint: "SHA256:test-fingerprint",
          certificate_path: "/test/cert.pem",
          certificate_chain_path: "/test/fullchain.pem",
          private_key_path: "/test/key.pem",
          validation_command: "nginx\n-t",
          reload_command: "nginx\n-s\nreload",
        },
        sshKeys: [],
        openSSHKeyCreate: () => {},
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

test("sshKeyModal.compilesAndPreservesCredentialFields", async () => {
  const { readFile } = await import("node:fs/promises");
  const { parse, compileScript, compileTemplate } = await import("@vue/compiler-sfc");
  const { renderToString } = await import("@vue/server-renderer");
  const Vue = await import("vue");
  const source = await readFile(new URL("./SSHKeyModal.vue", import.meta.url), "utf8");
  const { descriptor, errors } = parse(source);
  assert.deepEqual(errors, []);
  assert.ok(compileScript(descriptor, { id: "ssh-key-modal" }));
  const compiled = compileTemplate({
    source: descriptor.template.content,
    filename: "SSHKeyModal.vue",
    id: "ssh-key-modal",
    compilerOptions: { mode: "function" },
  });
  assert.deepEqual(compiled.errors, []);
  assert.ok(descriptor.styles.some(style => style.scoped && style.src === "./Dashboard.css"));
  assert.ok(descriptor.styles.some(style => style.scoped && style.src === "./SSHKeyModal.css"));
  assert.doesNotMatch(source, /<HelpTip\s+class=/);
  const render = new Function("Vue", compiled.code)(Vue);
  for (const platform of ["windows", "linux"]) {
    const app = Vue.createSSRApp({
      render,
      setup: () => ({
        showSSHKeyForm: true,
        closeSSHKeyForm: () => {},
        saveSSHKey: () => {},
        sshKeyPlatform: platform,
        sshKeyFileName: "test-key",
        sshKeyPrivatePath: "/test/private",
        sshKeyPublicPath: "/test/public",
        sshKeyCommandUsername: "deploy",
        sshKeyCommandHost: "example.test",
        copySSHKeyCommand: () => {},
        sshKeyCommands: { windows: "generate-windows", linux: "generate-linux" },
        sshPublicKeyCommands: { windows: "install-windows", linux: "install-linux" },
        sshKeyForm: { name: "", private_key: "", passphrase: "" },
        importSSHKeyFile: () => {},
        sshKeyPassphraseVisible: false,
        sshKeyFormError: "test-validation-error",
        sshKeySaving: false,
      }),
    });
    app.component("HelpTip", {
      setup: (_, { slots }) => () => Vue.h("span", { class: "test-help-tip" }, slots.default?.()),
    });
    const html = await renderToString(app);
    assert.match(html, /class="modal-card ssh-key-modal"/);
    assert.match(html, /type="submit"/);
    assert.match(html, /type="file"/);
    assert.match(html, /type="password"/);
    assert.match(html, /rows="5"/);
    assert.match(html, /test-validation-error/);
    assert.equal((html.match(/ required/g) || []).length, 2);
    assert.ok(html.includes(`generate-${platform}`));
    assert.ok(html.includes(`install-${platform}`));
    assert.ok(html.includes("deploy@example.test"));
  }
  const css = await readFile(new URL("./SSHKeyModal.css", import.meta.url), "utf8");
  assert.match(css, /\.ssh-key-modal\s*\{[^}]*overflow:\s*auto;/);
});test("domainAction.refreshesPersistedFailureWithoutLosingError", async (t) => {
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
  const template = await readFile(new URL("./Dashboard.vue", import.meta.url), "utf8");
  const stylesheet = await readFile(new URL("./Dashboard.css", import.meta.url), "utf8");
  assert.equal((template.match(/class="record-list" tabindex="0"/g) || []).length, 3);
  assert.match(stylesheet, /\.record-list\s*\{[^}]*max-height:\s*280px;[^}]*overflow-y:\s*auto;/);
  assert.match(stylesheet, /\.record-list:focus-visible/);
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

test("dashboardLayout.usesSharedTooltipForUserManagementDescription", async () => {
  const { readFile } = await import("node:fs/promises");
  const template = await readFile(new URL("../UserManagement/UserManagement.vue", import.meta.url), "utf8");
  assert.match(template, /import HelpTip from "\.\.\/components\/HelpTip\/HelpTip.vue";/);
  assert.match(template, /<HelpTip>[\s\S]*?<\/HelpTip>/);
  assert.doesNotMatch(template, /<p class="user-management-description">/);
});
