import assert from "node:assert/strict";
import test from "node:test";
import { nextTick } from "vue";
import { useSetupWizard } from "./SetupWizard.js";
import { aliyunProvider, deferred, mountComposable, verifiedResponse } from "../../test-support/mount.js";

test("全新初始化默认选择阿里云", async (t) => {
  const { state } = await mountComposable(t, () => useSetupWizard({ status: {} }, () => {}),
    async () => Response.json({ items: [aliyunProvider] }));
  assert.equal(state.form.value.dns_provider, "aliyun");
  assert.equal(state.form.value.propagation_timeout_secs, 300);
  assert.equal(state.form.value.propagation_polling_interval_secs, 5);
});

test("已有初始化服务商选择保持兼容", async (t) => {
  const provider = { ...aliyunProvider, id: "cloudflare" };
  const { state } = await mountComposable(t, () => useSetupWizard({ status: { dns_provider: "cloudflare" } }, () => {}),
    async () => Response.json({ items: [provider, aliyunProvider] }));
  assert.equal(state.form.value.dns_provider, "cloudflare");
});

test("邮箱定时同步不能清除步骤校验错误，手动编辑仍可清除", async (t) => {
  const { state, runIntervals } = await mountComposable(t,
    () => useSetupWizard({ status: { mode: "bootstrap" } }, () => {}),
    async () => Response.json({ items: [aliyunProvider] }));
  Object.assign(state.form.value, {
    admin_username: "test-admin",
    admin_password: "TestPass123",
    confirm_password: "TestPass123",
    web_hostname: "scert.example.test",
  });
  state.emailInputElement.value = { value: "admin@example.test" };
  state.emailAutofillElement.value = { value: "" };
  state.handleEmailInput("admin@example.test");
  state.handleSubmit();
  assert.equal(state.error.value, "请先验证所选数据库的连接和写权限。");
  for (let i = 0; i < 8; i += 1) runIntervals();
  assert.equal(state.error.value, "请先验证所选数据库的连接和写权限。");
  assert.equal(state.currentStep.value, 1);
  state.handleEmailInput("changed@example.test");
  assert.equal(state.error.value, "");
});

test("自动填充同步新邮箱时保留其他字段的错误", async (t) => {
  const { state, runIntervals } = await mountComposable(t,
    () => useSetupWizard({ status: {} }, () => {}),
    async () => Response.json({ items: [aliyunProvider] }));
  state.emailInputElement.value = { value: "" };
  state.emailAutofillElement.value = { value: "autofill@example.test" };
  state.error.value = "两次输入的超级管理员密码不一致。";
  runIntervals();
  assert.equal(state.form.value.admin_email, "autofill@example.test");
  assert.equal(state.error.value, "两次输入的超级管理员密码不一致。");
});

async function setup(t, request) {
  const mounted = await mountComposable(t, () => useSetupWizard({ status: { dns_provider: "aliyun" } }, () => {}),
    async (path) => path.endsWith("/providers") ? Response.json({ items: [aliyunProvider] }) : request());
  const { state } = mounted;
  state.dnsCredentials.value = { access_key_id: "fake-key" };
  state.dnsTestDomain.value = "example.test";
  await nextTick();
  return mounted;
}

test("初始化 DNS 验证显示等待进度，并采用服务端返回的最长等待时间", async (t) => {
  const pending = deferred();
  const { state, runIntervals, advanceTime } = await setup(t, () => pending.promise);
  const task = state.testDNS();

  assert.equal(state.dnsTesting.value, true);
  assert.equal(state.dnsVerificationElapsedSeconds.value, 0);
  assert.equal(state.dnsVerificationTimeoutSeconds.value, 6);

  advanceTime(2500);
  runIntervals();
  assert.equal(state.dnsVerificationElapsedSeconds.value, 2);

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

test("初始化不接受输入修改前的在途 DNS 响应", async (t) => {
  const pending = deferred();
  const { state } = await setup(t, () => pending.promise);
  const task = state.testDNS();
  state.dnsCredentials.value.access_key_id = "changed-key";
  await nextTick();
  pending.resolve(verifiedResponse());
  await task;
  assert.equal(state.dnsVerificationValid.value, false);
  assert.equal(state.dnsVerification.value, null);
});

test("初始化仅接受最新请求，旧失败不能覆盖新成功", async (t) => {
  const first = deferred();
  const second = deferred();
  let count = 0;
  const { state } = await setup(t, () => (++count === 1 ? first.promise : second.promise));
  const oldTask = state.testDNS();
  const newTask = state.testDNS();
  second.resolve(verifiedResponse("new-token"));
  await newTask;
  first.reject(new Error("旧请求失败"));
  await oldTask;
  assert.equal(state.dnsVerification.value.verification_token, "new-token");
  assert.equal(state.dnsTestError.value, "");
});

test("初始化当前 DNS 成功有效，修改域名立即失效", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  await state.testDNS();
  assert.equal(state.dnsVerificationValid.value, true);
  state.dnsTestDomain.value = "other.test";
  assert.equal(state.dnsVerificationValid.value, false);
});

test("初始化修改 DNS 输入后会停止等待计时器", async (t) => {
  const pending = deferred();
  const { state, runIntervals, advanceTime } = await setup(t, () => pending.promise);
  const task = state.testDNS();
  advanceTime(1000);
  runIntervals();
  assert.equal(state.dnsVerificationElapsedSeconds.value, 1);

  state.dnsTestDomain.value = "other.test";
  assert.equal(state.dnsTesting.value, false);
  assert.equal(state.dnsVerificationElapsedSeconds.value, 0);
  advanceTime(1000);
  runIntervals();
  assert.equal(state.dnsVerificationElapsedSeconds.value, 0);

  pending.resolve(verifiedResponse());
  await task;
});

test("初始化修改阿里云凭据立即作废旧验证", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  await state.testDNS();
  assert.equal(state.dnsVerificationValid.value, true);
  state.dnsCredentials.value.access_key_id = "changed-key";
  assert.equal(state.dnsVerificationValid.value, false);
  assert.equal(state.dnsVerification.value, null);
});

test("初始化卸载后不恢复旧 DNS 验证结果或继续等待计时", async (t) => {
  const pending = deferred();
  const { state, unmount, runIntervals, advanceTime } = await setup(t, () => pending.promise);
  const task = state.testDNS();
  advanceTime(1000);
  runIntervals();
  assert.equal(state.dnsVerificationElapsedSeconds.value, 1);
  unmount();
  assert.equal(state.dnsVerificationElapsedSeconds.value, 0);
  advanceTime(1000);
  runIntervals();
  assert.equal(state.dnsVerificationElapsedSeconds.value, 0);
  pending.resolve(verifiedResponse());
  await task;
  assert.equal(state.dnsVerification.value, null);
});

test("初始化旧请求结束不清除新请求的加载状态", async (t) => {
  const first = deferred();
  const second = deferred();
  let count = 0;
  const { state } = await setup(t, () => (++count === 1 ? first.promise : second.promise));
  const oldTask = state.testDNS();
  state.dnsTestDomain.value = "other.test";
  const newTask = state.testDNS();
  first.resolve(verifiedResponse());
  await oldTask;
  assert.equal(state.dnsTesting.value, true);
  assert.equal(state.dnsVerification.value, null);
  second.resolve(verifiedResponse("current"));
  await newTask;
  assert.equal(state.dnsTesting.value, false);
  assert.equal(state.dnsVerification.value.verification_token, "current");
});
