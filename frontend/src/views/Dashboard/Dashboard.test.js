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

test("已保存凭据不变可保存，输入新凭据必须重新验证", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  assert.equal(state.dnsReadyToSave.value, true);
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

test("管理设置变更托管区不能复用验证令牌", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  await fill(state);
  state.settingsForm.value.dns.credentials.managed_zones = "example.test";
  state.settingsForm.value.dns.credentials.allowed_domains = "example.test";
  await state.testDNS();
  assert.equal(state.dnsReadyToSave.value, true);
  state.settingsForm.value.dns.credentials.managed_zones = "other.test";
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
  assert.equal(state.dnsVerificationTimeoutSeconds.value, 15);
  advanceTime(3200);
  runIntervals();
  assert.equal(state.dnsVerificationElapsedSeconds.value, 3);

  pending.resolve(Response.json({
    verified: true,
    credential_ref: "secret://test/only",
    verification_token: "test-token",
    expires_at: new Date(Date.now() + 60000).toISOString(),
    verification_timeout_secs: 15,
  }));
  await task;
  assert.equal(state.dnsTesting.value, false);
  assert.equal(state.dnsVerificationElapsedSeconds.value, 0);
  assert.equal(state.dnsVerificationTimeoutSeconds.value, 15);
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
