import assert from "node:assert/strict";
import test from "node:test";
import { ref } from "vue";
import { useDomainForm } from "./DomainForm.js";
import { deferred, mountComposable } from "../../test-support/mount.js";

const account = {
  id: "account-one", project_id: "default", provider: "aliyun", provider_name: "阿里云 DNS",
  name: "生产账户", owner_username: "甲用户", managed: true, status: "active",
  verification_state: "verified", credential_state: "available", revision: 3,
};
const item = {
  id: "domain-one", project_id: "default", name: "one.example.test", sans: [],
  certificate_type: "single", auto_renew: true, acme_environment: "staging",
  challenge_type: "dns-01", dns_provider: "aliyun",
};
const emptyBinding = { id: "", domain_id: item.id, project_id: "default", dns_account_id: "" };
const originalBinding = { ...emptyBinding, id: "binding-old", dns_account_id: account.id };

async function mountForm(t, { accounts = [account], binding = emptyBinding, intercept = () => null } = {}) {
  const requests = [];
  let currentBinding = { ...binding };
  const deps = {
    fetchAdmin: (...args) => fetch(...args), domains: ref([]), servers: ref([]),
    domainServers: ref({}), notice: ref(""), loadData: async () => {},
  };
  const mounted = await mountComposable(t, () => useDomainForm(deps), async (path, options = {}) => {
    requests.push({ path, ...options });
    const intercepted = intercept(path, options);
    if (intercepted) return intercepted;
    if (path === "/api/v1/dns/accounts") return Response.json({ items: accounts });
    if (path.endsWith("/dns-account")) {
      if (options.method) {
        const body = JSON.parse(options.body);
        currentBinding = {
          ...emptyBinding, id: body.dns_account_id ? "binding-new" : "", dns_account_id: body.dns_account_id,
        };
      }
      return Response.json(currentBinding);
    }
    if (path.endsWith("/servers")) return Response.json({ items: [] });
    const body = JSON.parse(options.body);
    if (options.method === "POST" && body.dns_account_id) {
      currentBinding = { ...emptyBinding, id: "binding-new", dns_account_id: body.dns_account_id };
      return Response.json({ ...item, ...body, dns_binding_id: currentBinding.id });
    }
    return Response.json({ ...item, ...body });
  });
  return { ...mounted, deps, requests, writes: () => requests.filter((request) => request.method) };
}

async function create(state) {
  await state.openCreate();
  state.form.value.name = item.name;
  state.dnsAccountID.value = account.id;
}

test("新域名与初始 DNS 账户原子保存，提交真实账户版本且不重复绑定", async (t) => {
  const { state, deps, writes } = await mountForm(t);
  await create(state);
  await state.saveDomain();
  assert.deepEqual(writes().map((request) => request.path), [
    "/api/v1/domains", `/api/v1/domains/${item.id}/servers`,
  ]);
  assert.equal(JSON.parse(writes()[0].body).dns_provider, "aliyun");
  assert.equal(JSON.parse(writes()[0].body).dns_account_id, account.id);
  assert.equal(JSON.parse(writes()[0].body).dns_account_revision, 3);
  assert.equal(deps.domains.value[0].dns_binding_id, "binding-new");
  assert.equal(state.showForm.value, false);
});

test("编辑使用真实绑定令牌，更换服务商时先保留旧服务商再事务绑定", async (t) => {
  const second = { ...account, id: "account-two", provider: "cloudflare", revision: 8 };
  const { state, deps, writes } = await mountForm(t, { accounts: [account, second], binding: originalBinding });
  await state.openEdit({ ...item, dns_binding_id: "stale-list" });
  state.dnsAccountID.value = second.id;
  await state.saveDomain();
  assert.equal(JSON.parse(writes()[0].body).dns_provider, "aliyun");
  assert.deepEqual(JSON.parse(writes()[1].body), {
    dns_account_id: second.id, expected_binding_id: originalBinding.id, account_revision: 8,
  });
  assert.equal(deps.domains.value[0].dns_provider, "cloudflare");
});

test("账户按项目、状态、验证结果、凭据可用性及真实独立标识筛选", async (t) => {
  const invalid = [
    { project_id: "other" }, { managed: false }, { status: "disabled" },
    { verification_state: "unverified" }, { credential_state: "missing" },
    { revision: 0 }, { revision: 2147483648 }, { provider: "../bad" }, { id: "../bad" },
  ].map((overrides, index) => ({ ...account, id: `invalid-${index}`, ...overrides }));
  const { state, writes } = await mountForm(t, { accounts: [null, ...invalid, account] });
  await create(state);
  assert.deepEqual(state.domainDNSAccountOptions.value.map((entry) => entry.id), [account.id]);
  for (const entry of invalid) {
    state.dnsAccountID.value = entry.id;
    await state.saveDomain();
    assert.match(state.formError.value, /当前项目内/);
  }
  assert.equal(writes().length, 0);
});

test("停用的原账户保留绑定且不重新使用旧验证结果，可以显式解除", async (t) => {
  const { state, writes } = await mountForm(t, { accounts: [{ ...account, status: "disabled" }], binding: originalBinding });
  await state.openEdit(item);
  assert.equal(state.dnsAccountID.value, account.id);
  assert.equal(state.dnsUnavailableSelection.value, true);
  state.dnsAccountID.value = "";
  await state.saveDomain();
  assert.deepEqual(JSON.parse(writes()[1].body), {
    dns_account_id: "", expected_binding_id: originalBinding.id, account_revision: 0,
  });
});

test("已有绑定切换 HTTP 验证必须先解除，不能在同次保存中静默回退", async (t) => {
  const { state, writes } = await mountForm(t, { binding: originalBinding });
  await state.openEdit(item);
  state.dnsAccountID.value = "";
  state.form.value.challenge_type = "http-01";
  await state.saveDomain();
  assert.match(state.formError.value, /先.*解除账户绑定/);
  assert.equal(writes().length, 0);
});

test("无绑定 HTTP 验证保留兼容路径，不调用绑定写入", async (t) => {
  const { state, writes } = await mountForm(t);
  await state.openCreate();
  state.form.value.name = item.name;
  state.form.value.challenge_type = "http-01";
  await state.saveDomain();
  assert.equal(writes().length, 2);
  assert.equal(JSON.parse(writes()[0].body).dns_provider, "");
});

for (const failure of ["network", "invalid-json", "wrong-domain", "wrong-project", "half-binding", "forbidden"]) {
  test(`绑定读取失败不清空、不允许保存：${failure}`, async (t) => {
    const { state, writes } = await mountForm(t, { intercept: (path) => {
      if (!path.endsWith("/dns-account")) return null;
      if (failure === "network") return Promise.reject(new Error("secret-test-only"));
      if (failure === "invalid-json") return new Response("secret-test-only");
      if (failure === "forbidden") return Response.json({ error: "secret-test-only" }, { status: 403 });
      return Response.json({ ...originalBinding,
        ...(failure === "wrong-domain" ? { domain_id: "other-domain" } : {}),
        ...(failure === "wrong-project" ? { project_id: "other" } : {}),
        ...(failure === "half-binding" ? { dns_account_id: "" } : {}),
      });
    } });
    await state.openEdit(item);
    assert.equal(state.dnsBindingReady.value, false);
    assert.doesNotMatch(state.dnsBindingError.value, /secret-test-only/);
    await state.saveDomain();
    assert.equal(writes().length, 0);
  });
}

for (const failure of ["network", "conflict", "forbidden", "server", "invalid-json", "wrong-domain", "wrong-account"]) {
  test(`绑定写入失败停止后续关联且禁止盲目重放：${failure}`, async (t) => {
    const { state, writes } = await mountForm(t, { intercept: (path, options) => {
      if (!path.endsWith("/dns-account") || !options.method) return null;
      if (failure === "network") return Promise.reject(new Error("secret-test-only"));
      if (failure === "invalid-json") return new Response("secret-test-only");
      if (["conflict", "forbidden", "server"].includes(failure)) {
        return Response.json({ error: "secret-test-only" }, { status: { conflict: 409, forbidden: 403, server: 503 }[failure] });
      }
      return Response.json({ ...originalBinding,
        ...(failure === "wrong-domain" ? { domain_id: "other-domain" } : { dns_account_id: "other-account" }),
      });
    } });
    await state.openEdit(item);
    state.dnsAccountID.value = account.id;
    await state.saveDomain();
    assert.equal(writes().length, 2);
    assert.equal(state.dnsBindingReady.value, false);
    assert.match(state.formError.value, /域名资料已保存.*DNS 账户绑定/);
    assert.doesNotMatch(state.formError.value, /secret-test-only/);
    await state.saveDomain();
    assert.equal(writes().length, 2);
  });
}

test("绑定已成功而服务器关联失败时只重试服务器，不重新创建或绑定", async (t) => {
  let calls = 0;
  const { state, writes } = await mountForm(t, { intercept: (path, options) => {
    if (!path.endsWith("/servers") || !options.method) return null;
    calls += 1;
    return calls === 1 ? Response.json({ error: "关联冲突" }, { status: 409 }) : null;
  } });
  await create(state);
  await state.saveDomain();
  assert.equal(writes().length, 2);
  assert.equal(state.showForm.value, true);
  await state.saveDomain();
  assert.equal(writes().length, 3);
  assert.ok(writes()[2].path.endsWith("/servers"));
  assert.equal(state.showForm.value, false);
});

test("冲突后重新读取实际绑定，确认状态而不是自动重放旧选择", async (t) => {
  let conflict = true;
  let reads = 0;
  const { state, writes } = await mountForm(t, { intercept: (path, options) => {
    if (!path.endsWith("/dns-account")) return null;
    if (options.method && conflict) {
      conflict = false;
      return Response.json({}, { status: 409 });
    }
    if (!options.method) return Response.json(++reads === 1 ? emptyBinding : originalBinding);
    return null;
  } });
  await state.openEdit(item);
  state.dnsAccountID.value = account.id;
  await state.saveDomain();
  await state.reloadDomainDNS();
  assert.equal(state.dnsBindingReady.value, true);
  assert.equal(state.dnsAccountID.value, account.id);
  await state.saveDomain();
  assert.equal(writes().filter((request) => request.path.endsWith("/dns-account")).length, 1);
  assert.equal(writes().filter((request) => request.method === "POST").length, 0);
});

test("切换或卸载后迟到的账户响应不能恢复旧表单状态", async (t) => {
  const pending = deferred();
  let reads = 0;
  const { state, unmount, writes } = await mountForm(t, { intercept: (path) => {
    if (path !== "/api/v1/dns/accounts") return null;
    reads += 1;
    return reads === 1 ? pending.promise : null;
  } });
  const opening = state.openEdit(item);
  assert.equal(state.dnsBindingLoading.value, true);
  state.closeForm();
  await state.openCreate();
  pending.resolve(Response.json({ items: [{ ...account, id: "late-account" }] }));
  await opening;
  assert.deepEqual(state.domainDNSAccountOptions.value.map((entry) => entry.id), [account.id]);
  assert.equal(state.dnsAccountID.value, "");
  unmount();
  assert.equal(state.dnsBindingReady.value, false);
  assert.equal(writes().length, 0);
});

test("保存中固定目标账户和版本，拒绝双击、关闭、重读和切换", async (t) => {
  const pending = deferred();
  const { state, writes } = await mountForm(t, { intercept: (path) => path === "/api/v1/domains" ? pending.promise : null });
  await create(state);
  const saving = state.saveDomain();
  state.dnsAccountID.value = "forged-account";
  state.editingId.value = "forged-domain";
  state.closeForm();
  await state.openCreate();
  await state.reloadDomainDNS();
  await state.saveDomain();
  assert.equal(writes().length, 1);
  pending.resolve(Response.json({ ...item, dns_account_id: account.id, dns_binding_id: "binding-new" }));
  await saving;
  assert.equal(writes()[1].path, `/api/v1/domains/${item.id}/servers`);
  assert.equal(JSON.parse(writes()[0].body).dns_account_id, account.id);
  assert.equal(JSON.parse(writes()[0].body).dns_account_revision, 3);
});

test("卸载后返回的资料响应不继续写 DNS 绑定或服务器", async (t) => {
  const pending = deferred();
  const { state, unmount, writes } = await mountForm(t, { intercept: (path) => path === "/api/v1/domains" ? pending.promise : null });
  await create(state);
  const saving = state.saveDomain();
  unmount();
  pending.resolve(Response.json(item));
  await saving;
  assert.equal(writes().length, 1);
});

for (const mismatch of ["missing-binding", "wrong-account", "wrong-provider"]) {
  test(`初始绑定响应异常时禁止继续执行或重复创建：${mismatch}`, async (t) => {
    const result = { ...item, dns_account_id: account.id, dns_binding_id: "binding-new" };
    if (mismatch === "missing-binding") delete result.dns_binding_id;
    if (mismatch === "wrong-account") result.dns_account_id = "other";
    if (mismatch === "wrong-provider") result.dns_provider = "cloudflare";
    const { state, writes } = await mountForm(t, {
      intercept: (path) => path === "/api/v1/domains" ? Response.json(result) : null,
    });
    await create(state);
    await state.saveDomain();
    assert.equal(state.domainSaveUncertain.value, true);
    assert.match(state.formError.value, /初始 DNS 绑定/);
    await state.saveDomain();
    assert.equal(writes().length, 1);
  });
}
