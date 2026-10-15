import assert from "node:assert/strict";
import test from "node:test";
import { deferred, mountComposable } from "../../test-support/mount.js";
import { dnsVerificationLabel, useDNSAccounts } from "./DNSAccounts.js";
import { loadDNSProjects, requestDNS } from "./DNSAccountRequest.js";

const account = {
  id: "account-a", name: "测试账户", project_id: "project-a", managed: true,
  provider: "cloudflare", status: "active", revision: 7, verification_state: "verified",
};
const provider = {
  id: "cloudflare", name: "Cloudflare", available: true,
  credential_fields: [{ name: "api_token", label: "访问令牌", required: true, secret: true }],
};

async function setup(t, mutate = () => Response.json({}), options = {}) {
  const calls = [];
  const { state, unmount } = await mountComposable(t, useDNSAccounts, async (path, init = {}) => {
    calls.push({ path, ...init });
    if (init.method) return mutate(path, init);
    if (path.startsWith("/api/v1/projects?")) {
      return Response.json({ items: [{ id: "project-a", name: "项目甲", can_manage_dns: options.manage !== false }] });
    }
    if (path.endsWith("/providers")) return Response.json({ items: [provider] });
    return Response.json({ items: options.accounts || [account] });
  });
  return { state, calls, unmount };
}

test("DNS 列表显示项目归属、分页及真实账户验证状态", async (t) => {
  const { state, calls } = await setup(t, undefined, { accounts: Array.from({ length: 31 }, (_, index) => ({ ...account, id: `a-${index}` })) });
  assert.equal(state.accounts.value.length, 31);
  assert.equal(state.visibleAccounts.value.length, 10);
  assert.equal(state.projectLabel(account), "项目甲");
  for (const size of [5, 10, 15, 30]) {
    state.page.value = 2;
    state.pageSize.value = size;
    state.changePageSize();
    assert.equal(state.page.value, 1);
    assert.equal(state.visibleAccounts.value.length, size);
  }
  assert.equal(dnsVerificationLabel({ ...account, status: "disabled" }), "已停用");
  assert.equal(dnsVerificationLabel({ ...account, verification_state: "failed" }), "验证失败");
  assert.equal(dnsVerificationLabel({ ...account, credential_state: "stored" }), "已验证");
  assert.ok(calls.every((item) => item.credentials === "include" && item.cache === "no-store"));
});

test("DNS 新建只发送白名单字段并清空凭据", async (t) => {
  const { state, calls } = await setup(t);
  state.openEditor("create");
  Object.assign(state.editor.value, { name: " 新账户 ", owner_user_id: "forged", revision: 900 });
  state.editor.value.credentials = { api_token: "fixture-token", unknown: "must-not-send" };
  await state.saveEditor();
  const write = calls.find((item) => item.method);
  assert.equal(write.method, "POST");
  assert.equal(write.path, "/api/v1/dns/accounts");
  assert.deepEqual(JSON.parse(write.body), {
    name: "新账户", project_id: "project-a", provider: "cloudflare", credentials: { api_token: "fixture-token" },
  });
  assert.equal(state.editor.value, null);
  assert.match(state.notice.value, /在线验证/);
});

for (const [action, suffix, body, method] of [
  ["edit", "", { revision: 7, name: "新名称", credentials: { api_token: "replacement-token" } }, "PUT"],
  ["rename", "", { revision: 7, name: "新名称" }, "PUT"],
  ["rotate", "/credentials", { revision: 7, credentials: { api_token: "replacement-token" } }, "PUT"],
  ["status", "/status", { revision: 7, enabled: false }, "PUT"],
  ["verify", "/verify", { revision: 7, test_domain: "example.test" }, "POST"],
]) {
  test(`DNS ${action} 使用选中账户的固定身份与版本`, async (t) => {
    const { state, calls } = await setup(t);
    state.openEditor(action, account);
    Object.assign(state.editor.value, {
      id: "forged", revision: 999, name: "新名称", test_domain: " EXAMPLE.TEST ",
      credentials: { api_token: "replacement-token" },
    });
    await state.saveEditor();
    const write = calls.find((item) => item.method);
    assert.equal(write.path, `/api/v1/dns/accounts/account-a${suffix}`);
    assert.equal(write.method, method);
    assert.deepEqual(JSON.parse(write.body), body);
  });
}

test("编辑账户时凭据留空表示保留原配置", async (t) => {
  const { state, calls } = await setup(t);
  state.openEditor("edit", account);
  state.editor.value.name = "仅修改名称";
  await state.saveEditor();
  const write = calls.find((item) => item.method);
  assert.deepEqual(JSON.parse(write.body), { revision: 7, name: "仅修改名称" });
});

test("只读项目和历史配置不能发起账户写操作", async (t) => {
  const { state, calls } = await setup(t, undefined, { manage: false });
  for (const action of ["create", "edit", "rename", "rotate", "status", "verify"]) {
    state.openEditor(action, account);
    assert.equal(state.editor.value, null);
  }
  assert.equal(calls.filter((item) => item.method).length, 0);
  assert.equal(state.canManage({ ...account, managed: false }), false);
});

test("停用账户不能验证，轮换和重新启用仍可用", async (t) => {
  const { state, calls } = await setup(t);
  const disabled = { ...account, status: "disabled" };
  state.openEditor("verify", disabled);
  assert.equal(state.editor.value, null);
  state.openEditor("status", disabled);
  await state.saveEditor();
  assert.equal(JSON.parse(calls.find((item) => item.method).body).enabled, true);
});

test("切换服务商、取消和刷新清空未提交凭据", async (t) => {
  const { state } = await setup(t);
  state.openEditor("create");
  state.editor.value.credentials = { api_token: "discard-me" };
  state.changeProvider();
  assert.deepEqual(state.editor.value.credentials, {});
  const form = state.editor.value;
  form.credentials.api_token = "discard-me";
  state.closeEditor();
  assert.deepEqual(form.credentials, {});
  state.openEditor("rotate", account);
  const nextForm = state.editor.value;
  nextForm.credentials.api_token = "discard-me";
  await state.loadAccounts();
  assert.deepEqual(nextForm.credentials, {});
  assert.equal(state.editor.value, null);
});

test("无效名称、缺失凭据和无效域名不发出写请求", async (t) => {
  const { state, calls } = await setup(t);
  state.openEditor("create");
  state.editor.value.name = "坏\u0000名称";
  await state.saveEditor();
  assert.match(state.mutationError.value, /名称/);
  state.editor.value.name = "有效名称";
  await state.saveEditor();
  assert.match(state.mutationError.value, /访问令牌/);
  state.openEditor("verify", account);
  state.editor.value.test_domain = "https://example.test/path";
  await state.saveEditor();
  assert.match(state.mutationError.value, /域名/);
  assert.equal(calls.filter((item) => item.method).length, 0);
});

for (const status of [401, 403, 409, 422, 503]) {
  test(`DNS 写入 ${status} 清空输入、刷新状态且不自动重试或显示敏感响应`, async (t) => {
    const { state, calls } = await setup(t, () => Response.json({ error: "secret://sensitive" }, { status }));
    state.openEditor("rotate", account);
    state.editor.value.credentials.api_token = "fixture-token";
    await state.saveEditor();
    assert.equal(state.editor.value, null);
    assert.equal(state.saving.value, false);
    assert.ok(state.mutationError.value);
    assert.doesNotMatch(state.mutationError.value, /sensitive|fixture-token/);
    assert.equal(calls.filter((item) => item.method).length, 1);
    assert.equal(calls.filter((item) => item.path === "/api/v1/dns/accounts" && !item.method).length, 2);
  });
}

test("DNS 写入中阻止双击、刷新、切换账户和关闭", async (t) => {
  const pending = deferred();
  const { state, calls } = await setup(t, () => pending.promise);
  state.openEditor("rotate", account);
  state.editor.value.credentials.api_token = "fixture-token";
  const request = state.saveEditor();
  assert.deepEqual(state.editor.value.credentials, {});
  const count = calls.length;
  await state.saveEditor();
  await state.loadAccounts();
  state.openEditor("rename", { ...account, id: "other" });
  state.closeEditor();
  assert.equal(calls.length, count);
  assert.equal(state.editor.value.action, "rotate");
  pending.resolve(Response.json({}));
  await request;
  assert.equal(state.editor.value, null);
});

test("DNS 页面卸载后迟到的写响应不会重新加载或恢复凭据", async (t) => {
  const pending = deferred();
  const { state, calls, unmount } = await setup(t, () => pending.promise);
  state.openEditor("rotate", account);
  state.editor.value.credentials.api_token = "fixture-token";
  const request = state.saveEditor();
  const count = calls.length;
  unmount();
  pending.resolve(Response.json({}));
  await request;
  assert.equal(calls.length, count);
  assert.equal(state.editor.value, null);
  assert.equal(state.notice.value, "");
});

test("DNS 项目选择遍历游标而非只保留第一页", async (t) => {
  let pages = 0;
  t.mock.method(globalThis, "fetch", async (path) => {
    pages += 1;
    return Response.json(path.includes("after=next")
      ? { items: [{ id: "second" }], has_more: false }
      : { items: [{ id: "first" }], has_more: true, next_cursor: "next" });
  });
  assert.deepEqual((await loadDNSProjects()).map((item) => item.id), ["first", "second"]);
  assert.equal(pages, 2);
});

test("DNS 项目列表拒绝重复游标避免循环", async (t) => {
  t.mock.method(globalThis, "fetch", async () => Response.json({ items: [], has_more: true, next_cursor: "repeat" }));
  await assert.rejects(loadDNSProjects(), /游标/);
});

test("DNS 旧读取不能覆盖刷新后的权限和账户", async (t) => {
  const { state } = await setup(t);
  const pending = deferred();
  let reads = 0;
  t.mock.method(globalThis, "fetch", async (path) => {
    if (path === "/api/v1/dns/accounts" && reads++ === 0) return pending.promise;
    return Response.json({ items: [] });
  });
  const old = state.loadAccounts();
  await state.loadAccounts();
  pending.resolve(Response.json({ items: [account] }));
  await old;
  assert.deepEqual(state.accounts.value, []);
  assert.equal(state.managedProjects.value.length, 0);
});

test("DNS 网络中断提示结果未确认而不重试写入", async (t) => {
  const { state, calls } = await setup(t, () => { throw new Error("sensitive-network-detail"); });
  state.openEditor("rename", account);
  await state.saveEditor();
  assert.match(state.mutationError.value, /未确认/);
  assert.doesNotMatch(state.mutationError.value, /sensitive/);
  assert.equal(calls.filter((item) => item.method).length, 1);
});

test("DNS 非 JSON 成功响应也不能暴露原始服务端文本", async (t) => {
  t.mock.method(globalThis, "fetch", async () => new Response("sensitive upstream content"));
  await assert.rejects(requestDNS("/api/v1/dns/accounts"), (error) => {
    assert.match(error.message, /响应格式/);
    assert.doesNotMatch(error.message, /sensitive/);
    return true;
  });
});

test("DNS 刷新失败立即移除旧账户和管理能力", async (t) => {
  const { state } = await setup(t);
  assert.equal(state.managedProjects.value.length, 1);
  t.mock.method(globalThis, "fetch", async () => Response.json({ error: "sensitive" }, { status: 403 }));
  await state.loadAccounts(null);
  assert.deepEqual(state.accounts.value, []);
  assert.deepEqual(state.managedProjects.value, []);
  state.openEditor("rename", account);
  assert.equal(state.editor.value, null);
  assert.match(state.error.value, /权限/);
});

test("DNS 页面与原生对话框可编译，字段分栏且提示不占用常驻空间", async () => {
  const { readFile } = await import("node:fs/promises");
  const { parse, compileScript, compileTemplate } = await import("@vue/compiler-sfc");
  for (const file of ["DNSAccounts.vue", "DNSAccountDialog.vue"]) {
    const source = await readFile(new URL(file, import.meta.url), "utf8");
    const { descriptor, errors } = parse(source);
    assert.deepEqual(errors, []);
    assert.ok(compileScript(descriptor, { id: file }));
    assert.deepEqual(compileTemplate({ source: descriptor.template.content, filename: file, id: file }).errors, []);
    assert.doesNotMatch(source, /&#x[0-9a-f]+;/i);
  }
  const dialog = await readFile(new URL("DNSAccountDialog.vue", import.meta.url), "utf8");
  assert.match(dialog, /showModal\(\)/);
  assert.match(dialog, /aria-labelledby="dns-editor-title"/);
  assert.match(dialog, /@cancel.prevent/);
  assert.match(dialog, /field.secret \? 'password'/);
  assert.match(dialog, /title="仅填写你有权管理的域名/);
});
