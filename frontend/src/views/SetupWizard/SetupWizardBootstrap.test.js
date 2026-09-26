import assert from "node:assert/strict";
import test from "node:test";
import { useSetupWizard } from "./SetupWizard.js";
import { aliyunProvider, deferred, mountComposable } from "../../test-support/mount.js";

function completion() {
  return Response.json({ completed: true, management_url: "https://203.0.113.20:8443/" });
}

test("代理模式提交公开端口并跳转至对应入口", async (t) => {
  const address = "https://admin.example.test:8004/";
  const { state, submitted, emitted } = await setup(t, () => Response.json({ completed: true, management_url: address }));
  state.form.value.https_mode = "proxy";
  state.form.value.proxy_https_port = 8004;
  state.currentStep.value = 4;
  await state.handleSubmit();
  assert.equal(submitted[0].proxy_https_port, 8004);
  assert.equal(emitted.at(-1)[1].management_url, address);
});

for (const port of [0, -1, 65536, 8004.5, ""]) {
  test(`代理模式拒绝非法公开端口 ${port}`, async (t) => {
    const { state, submitted } = await setup(t);
    state.form.value.https_mode = "proxy";
    state.form.value.proxy_https_port = port;
    state.currentStep.value = 4;
    await state.handleSubmit();
    assert.equal(submitted.length, 0);
    assert.match(state.error.value, /代理公开 HTTPS 端口/);
  });
}

async function setup(t, complete = completion, driver = "sqlite") {
  const submitted = [];
  const emitted = [];
  const mounted = await mountComposable(t, () => useSetupWizard({ status: {
    mode: "bootstrap",
    web_public_ip: "203.0.113.20",
    server_port: 8443,
    certificate_root: "/srv/scert/ssl",
  } }, (...args) => emitted.push(args)), async (path, options) => {
    if (path.endsWith("/providers")) return Response.json({ items: [aliyunProvider] });
    if (path.endsWith("/test")) return Response.json({
      verified: true,
      driver,
      verification_token: path.includes("database") ? "database-token" : "dns-token",
      expires_at: new Date(Date.now() + 60000).toISOString(),
    });
    assert.equal(path, "/api/v1/bootstrap/complete");
    submitted.push(JSON.parse(options.body));
    return complete();
  });
  const { state } = mounted;
  Object.assign(state.form.value, {
    admin_username: "test-admin",
    admin_password: "TestPass123",
    confirm_password: "TestPass123",
  });
  state.handleEmailInput("admin@example.test");
  state.dnsCredentials.value = { access_key_id: "test-key" };
  state.dnsTestDomain.value = "authorized.example.test";
  state.databaseForm.value.driver = driver;
  await state.testDatabase();
  await state.testDNS();
  assert.equal(state.databaseVerified.value, true);
  assert.equal(state.dnsVerificationValid.value, true);
  return { ...mounted, submitted, emitted };
}

test("数据库验证成功清除过时的数据库步骤提示", async (t) => {
  const { state } = await setup(t);
  for (const message of [
    "请先验证所选数据库的连接和写权限。",
    "数据库验证结果无效或已过期，请重新验证。",
  ]) {
    state.invalidateDatabase();
    state.error.value = message;
    await state.testDatabase();
    assert.equal(state.databaseVerified.value, true);
    assert.equal(state.error.value, "");
  }
});

test("数据库验证成功不清除其他字段错误", async (t) => {
  const { state } = await setup(t);
  state.invalidateDatabase();
  state.error.value = "两次输入的超级管理员密码不一致。";
  await state.testDatabase();
  assert.equal(state.databaseVerified.value, true);
  assert.equal(state.error.value, "两次输入的超级管理员密码不一致。");
});

test("数据库验证失败不清除步骤提示", async (t) => {
  const { state } = await mountComposable(t,
    () => useSetupWizard({ status: { mode: "bootstrap" } }, () => {}),
    async (path) => path.endsWith("/providers")
      ? Response.json({ items: [aliyunProvider] })
      : Response.json({ error: "数据库连接失败。" }, { status: 400 }));
  state.error.value = "请先验证所选数据库的连接和写权限。";
  await state.testDatabase();
  assert.equal(state.databaseVerified.value, false);
  assert.equal(state.error.value, "请先验证所选数据库的连接和写权限。");
  assert.equal(state.databaseError.value, "数据库连接失败。");
});

for (const driver of ["sqlite", "mysql", "postgres"]) {
  test(`首次向导 ${driver} 完整流程只提交验证令牌并保留通知配置`, async (t) => {
    const { state, submitted, emitted } = await setup(t, completion, driver);
    Object.assign(state.form.value, {
      email_http_enabled: true,
      email_endpoint: "https://mail.example.test",
      email_http_auth_mode: "bearer",
      email_smtp_enabled: true,
      email_smtp_host: "smtp.example.test",
      email_smtp_username: "sender@example.test",
      email_smtp_password: "smtp-test-only",
      email_retry_count: 0,
      recipients: "first@example.test，second@example.test",
      email_http_credential_ref: "secret://client/not-trusted",
    });
    for (let step = 1; step < 4; step += 1) {
      assert.equal(state.currentStep.value, step);
      await state.handleSubmit();
      assert.equal(submitted.length, 0);
      assert.equal(state.currentStep.value, step + 1);
    }
    await state.handleSubmit();
    assert.equal(state.error.value, "");
    assert.equal(state.completed.value, true);
    assert.equal(submitted.length, 1);
    assert.equal(submitted[0].database_verification_token, "database-token");
    assert.equal(submitted[0].dns_verification_token, "dns-token");
    assert.equal(submitted[0].web_public_ip, "203.0.113.20");
    assert.equal(submitted[0].notification.http_enabled, false);
    assert.equal(submitted[0].notification.smtp_enabled, true);
    assert.equal(submitted[0].notification.smtp_host, "smtp.example.test");
    assert.equal(submitted[0].notification.smtp_username, "sender@example.test");
    assert.equal(submitted[0].notification.smtp_password, "smtp-test-only");
    assert.equal(submitted[0].notification.smtp_port, 587);
    assert.equal(submitted[0].notification.smtp_mode, "starttls");
    assert.equal(submitted[0].notification.retry_count, 0);
    assert.deepEqual(submitted[0].notification.recipients, ["first@example.test", "second@example.test"]);
    for (const key of ["endpoint", "http_auth_mode", "http_credentials", "http_path", "http_headers"]) {
      assert.equal(Object.hasOwn(submitted[0].notification, key), false);
    }
    for (const key of ["dns_credential_ref", "certificate_root", "database", "confirm_password"]) {
      assert.equal(Object.hasOwn(submitted[0], key), false);
    }
    assert.equal(Object.hasOwn(submitted[0].notification, "http_credential_ref"), false);
    assert.equal(emitted.length, 1);
    assert.equal(emitted[0][0], "initialized");
  });
}

test("首次向导提交中和完成后禁止重复提交", async (t) => {
  const pending = deferred();
  const { state, submitted, emitted } = await setup(t, () => pending.promise);
  state.currentStep.value = 4;
  const task = state.handleSubmit();
  assert.equal(state.submitting.value, true);
  await state.handleSubmit();
  assert.equal(submitted.length, 1);
  pending.resolve(completion());
  await task;
  await state.handleSubmit();
  assert.equal(submitted.length, 1);
  assert.equal(emitted.length, 1);
  assert.equal(state.submitting.value, false);
});

test("首次向导成功后显示外部防火墙提示且不输出畸形警告", async (t) => {
  const { state, emitted } = await setup(t, () => Response.json({
    completed: true,
    management_url: "https://203.0.113.20:8443/",
    warnings: ["防火墙由管理员管理，端口 8443 未检查或修改。", null, {}, ""],
  }));
  state.currentStep.value = 4;
  await state.handleSubmit();
  assert.equal(state.completed.value, true);
  assert.equal(emitted.length, 1);
  assert.match(state.resultMessage.value, /端口 8443 未检查或修改/);
  assert.doesNotMatch(state.resultMessage.value, /null|\[object Object\]/);
});

test("首次向导提交失败清除已领取令牌，重新验证后允许重试", async (t) => {
  let attempts = 0;
  const { state, submitted, emitted } = await setup(t, () => ++attempts === 1
    ? Response.json({ error: "证书签发失败" }, { status: 502 }) : completion());
  state.currentStep.value = 4;
  await state.handleSubmit();
  assert.match(state.error.value, /证书签发失败/);
  assert.equal(state.completed.value, false);
  assert.equal(state.databaseVerification.value, null);
  assert.equal(state.dnsVerification.value, null);
  assert.equal(emitted.length, 0);
  await state.handleSubmit();
  assert.equal(submitted.length, 1);
  await state.testDatabase();
  await state.testDNS();
  await state.handleSubmit();
  assert.equal(submitted.length, 2);
  assert.equal(state.completed.value, true);
});

test("首次向导验证到期即使未触发界面时钟也禁止提交", async (t) => {
  const { state, submitted, advanceTime } = await setup(t);
  state.currentStep.value = 4;
  advanceTime(60001);
  await state.handleSubmit();
  assert.equal(submitted.length, 0);
  assert.match(state.error.value, /过期/);
});

test("首次向导最终提交重新校验全部步骤", async (t) => {
  const { state, submitted } = await setup(t);
  for (const [field, value, step] of [
    ["server_port", 0, 1],
    ["email_http_headers", "[]", 2],
    ["propagation_polling_interval_secs", 99999, 3],
    ["certificate_root", "", 4],
  ]) {
    const original = state.form.value[field];
    state.form.value[field] = value;
    state.currentStep.value = 4;
    await state.handleSubmit();
    assert.equal(submitted.length, 0);
    assert.equal(state.currentStep.value, step);
    assert.notEqual(state.error.value, "");
    state.form.value[field] = original;
  }
});

test("首次向导拒绝畸形成功响应而不是误报完成", async (t) => {
  const responses = [
    {},
    null,
    { completed: false, management_url: "https://203.0.113.20/" },
    { completed: true, management_url: "invalid" },
    { completed: true, management_url: "http://203.0.113.20/" },
    { completed: true, management_url: "https://user:password@example.test/" },
  ];
  const { state, emitted } = await setup(t, () => Response.json(responses.shift()));
  while (responses.length) {
    await state.testDatabase();
    await state.testDNS();
    state.currentStep.value = 4;
    await state.handleSubmit();
    assert.equal(state.completed.value, false);
    assert.equal(emitted.length, 0);
    assert.match(state.error.value, /响应不完整/);
    assert.equal(state.databaseVerification.value, null);
    assert.equal(state.dnsVerification.value, null);
  }
});


test("bootstrap service port defaults to 80 independently of listener port", async (t) => {
  const { state, submitted } = await setup(t);
  assert.equal(state.form.value.server_port, 80);
  state.currentStep.value = 4;
  await state.handleSubmit();
  assert.equal(submitted[0].server_port, 80);
});

test("bootstrap service port accepts a custom port", async (t) => {
  const { state, submitted } = await setup(t);
  state.form.value.server_port = 8443;
  state.currentStep.value = 4;
  await state.handleSubmit();
  assert.equal(submitted[0].server_port, 8443);
});
