import assert from "node:assert/strict";
import test from "node:test";
import { ref } from "vue";
import { useDomainForm } from "./DomainForm.js";
import { deferred, mountComposable } from "../../test-support/mount.js";

const firstServer = { id: "server-one", project_id: "default", name: "共享服务器一" };
const secondServer = { id: "server-two", project_id: "default", name: "共享服务器二" };
const otherServer = { id: "server-other", project_id: "other", name: "其他项目服务器" };
const firstDomain = {
  id: "domain-one", project_id: "default", name: "one.example.test",
  certificate_type: "single", sans: [], auto_renew: true,
  acme_environment: "staging", dns_provider: "",
};

async function mountForm(t, fetcher) {
  const deps = {
    fetchAdmin: (...args) => fetch(...args),
    domains: ref([]),
    servers: ref([firstServer, secondServer, otherServer]),
    domainServers: ref({ "domain-two": [firstServer, secondServer] }),
    notice: ref(""),
    loadData: t.mock.fn(async () => {}),
  };
  const mounted = await mountComposable(t, () => useDomainForm(deps), (path, options) => {
    if (path === "/api/v1/dns/accounts") return Promise.resolve(Response.json({ items: [] }));
    if (path.endsWith("/dns-account")) return Promise.resolve(Response.json({
      id: "", dns_account_id: "", domain_id: path.split("/")[4], project_id: "default",
    }));
    return fetcher(path, options);
  });
  return { ...mounted, deps };
}

async function startCreate(state) {
  await state.openCreate();
  state.form.value.name = firstDomain.name;
  state.form.value.server_ids = [firstServer.id, secondServer.id];
}

test("HTTP 验证保存方式且不携带 DNS 服务商", async (t) => {
  const requests = [];
  const { state } = await mountForm(t, async (path, options) => {
    requests.push({ path, ...options });
    if (path.endsWith("/servers")) return Response.json({ items: [] });
    return Response.json({ ...firstDomain, ...JSON.parse(options.body) });
  });
  await startCreate(state);
  state.form.value.challenge_type = "http-01";
  state.form.value.dns_provider = "aliyun";
  await state.saveDomain();
  assert.equal(JSON.parse(requests[0].body).challenge_type, "http-01");
  assert.equal(JSON.parse(requests[0].body).dns_provider, "");
});

test("HTTP 验证拒绝主泛域名和附加泛域名，不提交请求", async (t) => {
  let calls = 0;
  const { state } = await mountForm(t, async () => { calls += 1; return Response.json({}); });
  for (const type of ["wildcard", "san"]) {
    await startCreate(state);
    state.form.value.challenge_type = "http-01";
    state.form.value.certificate_type = type;
    state.form.value.sans = "*.example.com";
    await state.saveDomain();
    assert.match(state.formError.value, /不支持泛域名/);
  }
  assert.equal(calls, 0);
});

test("编辑时保留已有 HTTP 验证方式", async (t) => {
  const { state } = await mountForm(t, async () => Response.json({ items: [] }));
  await state.openEdit({ ...firstDomain, challenge_type: "http-01" });
  assert.equal(state.form.value.challenge_type, "http-01");
  await state.openEdit(firstDomain);
  assert.equal(state.form.value.challenge_type, "dns-01");
});

test("创建成功但关联冲突后只重试关联，不重复创建且不改变另一域名", async (t) => {
  const requests = [];
  let attempts = 0;
  const { state, deps } = await mountForm(t, async (path, options) => {
    requests.push({ path, ...options });
    if (path === "/api/v1/domains") return Response.json(firstDomain, { status: 201 });
    attempts += 1;
    return attempts === 1
      ? Response.json({ error: "共享路径冲突" }, { status: 409 })
      : Response.json({ items: [firstServer, secondServer] });
  });
  await startCreate(state);
  await state.saveDomain();
  assert.equal(state.editingId.value, firstDomain.id);
  assert.equal(state.showForm.value, true);
  assert.equal(state.saving.value, false);
  assert.deepEqual(state.form.value.server_ids, [firstServer.id, secondServer.id]);
  assert.match(state.formError.value, /域名资料已保存.*共享路径冲突/);
  assert.deepEqual(deps.domains.value, [firstDomain]);
  await state.saveDomain();
  assert.deepEqual(requests.map(({ method }) => method), ["POST", "PUT", "PUT"]);
  assert.equal(requests[1].path, `/api/v1/domains/${firstDomain.id}/servers`);
  assert.equal(requests[2].body, requests[1].body);
  assert.deepEqual(deps.domainServers.value["domain-two"], [firstServer, secondServer]);
  assert.deepEqual(deps.domainServers.value[firstDomain.id], [firstServer, secondServer]);
  assert.equal(state.showForm.value, false);
  assert.match(deps.notice.value, /域名已添加/);
  assert.equal(deps.loadData.mock.callCount(), 1);
});

test("部分成功后修改资料使用已有标识更新，取消也保留已创建列表项", async (t) => {
  const requests = [];
  const { state, deps } = await mountForm(t, async (path, options) => {
    requests.push({ path, ...options });
    if (path.endsWith("/servers")) return Response.json({ error: "关联未保存" }, { status: 403 });
    return Response.json({ ...firstDomain, ...JSON.parse(options.body) });
  });
  await startCreate(state);
  await state.saveDomain();
  state.form.value.auto_renew = false;
  await state.saveDomain();
  assert.deepEqual(requests.map(({ method }) => method), ["POST", "PUT", "PUT", "PUT"]);
  assert.equal(requests[2].path, `/api/v1/domains/${firstDomain.id}`);
  assert.equal(JSON.parse(requests[2].body).auto_renew, false);
  state.closeForm();
  assert.equal(deps.domains.value.length, 1);
  assert.equal(deps.domains.value[0].auto_renew, false);
  state.openCreate();
  assert.equal(state.editingId.value, "");
  assert.deepEqual(state.form.value.server_ids, []);
});

test("编辑先读取真实关联，明确清空只提交当前域名", async (t) => {
  const requests = [];
  const { state, deps } = await mountForm(t, async (path, options = {}) => {
    requests.push({ path, ...options });
    if (!options.method) return Response.json({ items: [firstServer, secondServer] });
    if (path.endsWith("/servers")) return Response.json({ items: [] });
    return Response.json(firstDomain);
  });
  await state.openEdit(firstDomain);
  assert.deepEqual(state.form.value.server_ids, [firstServer.id, secondServer.id]);
  state.form.value.server_ids = [];
  await state.saveDomain();
  assert.equal(requests.length, 3);
  assert.deepEqual(JSON.parse(requests[2].body), { server_ids: [] });
  assert.deepEqual(deps.domainServers.value[firstDomain.id], []);
  assert.deepEqual(deps.domainServers.value["domain-two"], [firstServer, secondServer]);
});

test("服务器按域名项目过滤，不限制其他域名共享，缺归属不默认为默认项目", async (t) => {
  const { state, deps } = await mountForm(t, async () => Response.json({ items: [] }));
  deps.servers.value.push({ id: "unknown" });
  await startCreate(state);
  assert.deepEqual(state.domainServerOptions.value.map((server) => server.id), [firstServer.id, secondServer.id]);
  await state.openEdit({ ...firstDomain, project_id: "other" });
  assert.deepEqual(state.domainServerOptions.value, [otherServer]);
  state.form.value.server_ids = [firstServer.id];
  await state.saveDomain();
  assert.match(state.formError.value, /当前项目/);
  await state.openEdit({ ...firstDomain, project_id: "" });
  assert.deepEqual(state.domainServerOptions.value, []);
  await state.saveDomain();
  assert.match(state.formError.value, /项目归属未确认/);
});

for (const failure of ["forbidden", "network", "malformed"]) {
  test(`关联读取失败不作为空数组保存，可重新读取：${failure}`, async (t) => {
    let reads = 0;
    const { state, deps } = await mountForm(t, async (_, options = {}) => {
      assert.equal(options.method, undefined);
      reads += 1;
      if (reads > 1) return Response.json({ items: [firstServer] });
      if (failure === "network") throw new Error("offline");
      return failure === "forbidden"
        ? Response.json({ error: "无权限" }, { status: 403 })
        : Response.json({});
    });
    deps.domainServers.value[firstDomain.id] = [firstServer];
    await state.openEdit(firstDomain);
    assert.equal(state.domainBindingsReady.value, false);
    await state.saveDomain();
    assert.equal(reads, 1);
    assert.deepEqual(deps.domainServers.value[firstDomain.id], [firstServer]);
    await state.reloadDomainBindings();
    assert.equal(state.domainBindingsReady.value, true);
    assert.deepEqual(state.form.value.server_ids, [firstServer.id]);
  });
}

test("切换表单时旧关联读取结果不能污染新域名", async (t) => {
  const pending = deferred();
  const { state, deps } = await mountForm(t, async () => pending.promise);
  const reading = state.openEdit(firstDomain);
  assert.equal(state.domainBindingsLoading.value, true);
  state.closeForm();
  state.openCreate();
  pending.resolve(Response.json({ items: [firstServer] }));
  await reading;
  assert.equal(state.editingId.value, "");
  assert.deepEqual(state.form.value.server_ids, []);
  assert.equal(state.domainBindingsLoading.value, false);
  assert.equal(deps.domainServers.value[firstDomain.id], undefined);
});

test("保存期间重复点击、关闭和切换表单不创建额外请求", async (t) => {
  const pending = deferred();
  let calls = 0;
  const { state } = await mountForm(t, async (path) => {
    calls += 1;
    return path.endsWith("/servers") ? Response.json({ items: [firstServer, secondServer] }) : pending.promise;
  });
  await startCreate(state);
  const saving = state.saveDomain();
  await state.saveDomain();
  state.openCreate();
  await state.openEdit({ ...firstDomain, id: "other-domain" });
  state.closeForm();
  assert.equal(calls, 1);
  assert.equal(state.showForm.value, true);
  assert.equal(state.form.value.name, firstDomain.name);
  pending.resolve(Response.json(firstDomain));
  await saving;
  assert.equal(calls, 2);
});

for (const failure of ["network", "server", "malformed", "missing-project"]) {
  test(`资料保存结果不明确时禁止盲目重新创建：${failure}`, async (t) => {
    let calls = 0;
    const { state } = await mountForm(t, async () => {
      calls += 1;
      if (failure === "network") throw new Error("offline");
      if (failure === "server") return Response.json({}, { status: 500 });
      return Response.json(failure === "missing-project" ? { id: firstDomain.id } : {});
    });
    await startCreate(state);
    await state.saveDomain();
    assert.equal(state.domainSaveUncertain.value, true);
    assert.match(state.formError.value, /刷新列表确认/);
    await state.saveDomain();
    assert.equal(calls, 1);
  });
}

test("卸载后不继续保存关联，不改写列表", async (t) => {
  const pending = deferred();
  let calls = 0;
  const { state, deps, unmount } = await mountForm(t, async () => {
    calls += 1;
    return pending.promise;
  });
  await startCreate(state);
  const saving = state.saveDomain();
  unmount();
  pending.resolve(Response.json(firstDomain));
  await saving;
  assert.equal(calls, 1);
  assert.deepEqual(deps.domains.value, []);
});

test("关联网络失败可重试，但刷新失败不能被误报为保存失败", async (t) => {
  let bindings = 0;
  const { state, deps } = await mountForm(t, async (path) => {
    if (!path.endsWith("/servers")) return Response.json(firstDomain);
    bindings += 1;
    if (bindings === 1) throw new Error("offline");
    return Response.json({ items: [firstServer, secondServer] });
  });
  deps.loadData.mock.mockImplementation(async () => { throw new Error("offline"); });
  await startCreate(state);
  await state.saveDomain();
  assert.match(state.formError.value, /关联保存结果未确认/);
  assert.equal(state.domainSaveUncertain.value, false);
  await state.saveDomain();
  assert.equal(state.showForm.value, false);
  assert.equal(state.formError.value, "");
  assert.match(deps.notice.value, /配置已保存.*列表刷新失败/);
});
