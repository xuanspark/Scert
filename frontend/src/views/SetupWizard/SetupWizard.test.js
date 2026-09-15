import assert from "node:assert/strict";
import test from "node:test";
import { nextTick } from "vue";
import { useSetupWizard } from "./SetupWizard.js";
import { aliyunProvider, deferred, mountComposable, verifiedResponse } from "../../test-support/mount.js";

test("全新初始化默认选择阿里云", async (t) => {
  const { state } = await mountComposable(t, () => useSetupWizard({ status: {} }, () => {}),
    async () => Response.json({ items: [aliyunProvider] }));
  assert.equal(state.form.value.dns_provider, "aliyun");
});

test("已有初始化服务商选择保持兼容", async (t) => {
  const provider = { ...aliyunProvider, id: "cloudflare" };
  const { state } = await mountComposable(t, () => useSetupWizard({ status: { dns_provider: "cloudflare" } }, () => {}),
    async () => Response.json({ items: [provider, aliyunProvider] }));
  assert.equal(state.form.value.dns_provider, "cloudflare");
});

async function setup(t, request) {
  const { state, unmount } = await mountComposable(t, () => useSetupWizard({ status: { dns_provider: "aliyun" } }, () => {}),
    async (path) => path.endsWith("/providers") ? Response.json({ items: [aliyunProvider] }) : request());
  state.dnsCredentials.value = { access_key_id: "fake-key" };
  state.dnsTestDomain.value = "example.test";
  await nextTick();
  return { state, unmount };
}

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

test("初始化修改显式授权范围立即作废旧验证", async (t) => {
  const { state } = await setup(t, () => verifiedResponse());
  state.dnsCredentials.value.managed_zones = "example.test";
  state.dnsCredentials.value.allowed_domains = "example.test";
  await state.testDNS();
  assert.equal(state.dnsVerificationValid.value, true);
  state.dnsCredentials.value.allowed_domains = "*.example.test";
  assert.equal(state.dnsVerificationValid.value, false);
  assert.equal(state.dnsVerification.value, null);
});

test("初始化卸载后不恢复旧 DNS 验证结果", async (t) => {
  const pending = deferred();
  const { state, unmount } = await setup(t, () => pending.promise);
  const task = state.testDNS();
  unmount();
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
