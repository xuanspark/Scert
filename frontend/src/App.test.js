import assert from "node:assert/strict";
import test from "node:test";
import { useAppShell } from "./App.js";
import { deferred, mountComposable } from "./test-support/mount.js";

function dispatchPageEvent(type, persisted = true) {
  const event = new Event(type);
  Object.defineProperty(event, "persisted", { value: persisted });
  window.dispatchEvent(event);
}

test("用户中心身份只取已认证会话白名单，重新验证期间立即清除", async (t) => {
  let session = { authenticated: true, account: "普通用户", role: "user", extra: "not-for-view" };
  const { state } = await mountComposable(t, useAppShell, async (path) => {
    if (path.endsWith("/status")) return Response.json({ initialized: true });
    return Response.json(session);
  });
  assert.deepEqual(state.currentUser.value, { account: "普通用户", role: "user" });
  session = { authenticated: true, account: "新用户", role: "user" };
  const reload = state.bootstrap();
  assert.equal(state.currentUser.value, null);
  await reload;
  assert.deepEqual(state.currentUser.value, { account: "新用户", role: "user" });
  dispatchPageEvent("pagehide");
  assert.equal(state.currentUser.value, null);
  session = { authenticated: false, account: "不得显示", role: "super_admin" };
  dispatchPageEvent("pageshow");
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(state.currentUser.value, null);
});

for (const failure of ["unauthorized", "network", "uninitialized"]) {
  test(`身份核验失败或回到初始化不能保留旧账号：${failure}`, async (t) => {
    let fail = false;
    const { state } = await mountComposable(t, useAppShell, async (path) => {
      if (fail && failure === "network") throw new Error("连接失败");
      if (path.endsWith("/status")) {
        return Response.json({ initialized: !(fail && failure === "uninitialized") });
      }
      if (fail) return new Response(null, { status: 401 });
      return Response.json({ authenticated: true, account: "普通用户", role: "user" });
    });
    assert.equal(state.currentUser.value.account, "普通用户");
    fail = true;
    await state.bootstrap();
    assert.equal(state.currentUser.value, null);
  });
}

test("旧会话请求晚到不能覆盖新版用户身份", async (t) => {
  const pending = deferred();
  let count = 0;
  const { state } = await mountComposable(t, useAppShell, async (path) => {
    if (path.endsWith("/status")) return Response.json({ initialized: true });
    count += 1;
    return count === 1 ? pending.promise : Response.json({ authenticated: true, account: "新用户", role: "user" });
  });
  await state.bootstrap();
  pending.resolve(Response.json({ authenticated: true, account: "旧用户", role: "super_admin" }));
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(state.currentUser.value, { account: "新用户", role: "user" });
});

test("历史缓存恢复先隐藏旧控制台，再按真实会话显示登录页", async (t) => {
  let active = true;
  const sessionOptions = [];
  const { state } = await mountComposable(t, useAppShell, async (path, options) => {
    if (path.endsWith("/status")) return Response.json({ initialized: true });
    sessionOptions.push(options);
    return active ? Response.json({ authenticated: true, is_admin: true }) : new Response(null, { status: 401 });
  });
  assert.equal(state.authenticated.value, true);
  dispatchPageEvent("pagehide");
  assert.equal(state.authenticated.value, null);
  assert.equal(state.bootstrapping.value, true);
  active = false;
  dispatchPageEvent("pageshow");
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(state.authenticated.value, false);
  assert.equal(state.isAdmin.value, false);
  assert.equal(state.bootstrapping.value, false);
  assert.equal(sessionOptions.length, 2);
  assert.ok(sessionOptions.every((options) => options.cache === "no-store" && options.credentials === "include"));
});

test("非缓存页面显示不重复检查，卸载后移除恢复监听", async (t) => {
  let calls = 0;
  const { unmount } = await mountComposable(t, useAppShell, async (path) => {
    calls += 1;
    return Response.json(path.endsWith("/status") ? { initialized: true } : { authenticated: true });
  });
  assert.equal(calls, 2);
  dispatchPageEvent("pageshow", false);
  assert.equal(calls, 2);
  unmount();
  dispatchPageEvent("pageshow");
  assert.equal(calls, 2);
});

test("恢复期间网络失败不恢复旧权限或业务界面", async (t) => {
  let failed = false;
  const { state } = await mountComposable(t, useAppShell, async (path) => {
    if (failed) throw new Error("连接失败");
    return Response.json(path.endsWith("/status") ? { initialized: true } : { authenticated: true, is_admin: true });
  });
  failed = true;
  dispatchPageEvent("pageshow");
  assert.equal(state.authenticated.value, null);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(state.setupState.value, null);
  assert.equal(state.isAdmin.value, null);
  assert.equal(state.error.value, "连接失败");
});

test("离开页面前的在途会话响应不能覆盖恢复后的未登录状态", async (t) => {
  const pending = deferred();
  let sessionCalls = 0;
  const { state } = await mountComposable(t, useAppShell, async (path) => {
    if (path.endsWith("/status")) return Response.json({ initialized: true });
    sessionCalls += 1;
    return sessionCalls === 1 ? pending.promise : new Response(null, { status: 401 });
  });
  dispatchPageEvent("pagehide");
  dispatchPageEvent("pageshow");
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(state.authenticated.value, false);
  pending.resolve(Response.json({ authenticated: true, is_admin: true }));
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(state.authenticated.value, false);
  assert.equal(state.isAdmin.value, false);
});

test("初始化令牌兑换前清除地址片段，后续请求只用同源会话", async (t) => {
  const calls = [];
  const { state } = await mountComposable(t, () => {
    window.location.hash = "#token=test-bootstrap-token";
    window.history.replaceState = (_, __, url) => calls.push({ replace: url });
    return useAppShell();
  }, async (path, options) => {
    calls.push({ path, options });
    if (path.endsWith("/status")) return Response.json({ mode: "bootstrap" });
    if (path.endsWith("/session")) return Response.json({ authenticated: true });
    return Response.json({ mode: "bootstrap", initialized: false });
  });
  assert.equal(state.error.value, "");
  assert.equal(state.setupState.value.mode, "bootstrap");
  assert.equal(calls[1].replace, "/");
  assert.equal(calls[2].path, "/api/v1/bootstrap/session");
  assert.equal(calls[2].options.credentials, "same-origin");
  assert.deepEqual(JSON.parse(calls[2].options.body), { token: "test-bootstrap-token" });
  assert.equal(calls[3].path, "/api/v1/bootstrap/settings");
});

test("没有令牌和有效会话时不暴露初始化表单", async (t) => {
  const { state } = await mountComposable(t, useAppShell, async (path) => path.endsWith("/status")
    ? Response.json({ mode: "bootstrap" }) : new Response(null, { status: 401 }));
  assert.equal(state.setupState.value, null);
  assert.match(state.error.value, /完整一次性地址/);
});

test("正式服务回退状态接口并检查登录态", async (t) => {
  const calls = [];
  const { state } = await mountComposable(t, useAppShell, async (path) => {
    calls.push(path);
    if (path.includes("bootstrap")) return new Response(null, { status: 404 });
    if (path.includes("setup")) return Response.json({ initialized: true });
    return new Response(null, { status: 401 });
  });
  assert.deepEqual(calls, ["/api/v1/bootstrap/status", "/api/v1/setup/status", "/api/v1/auth/session"]);
  assert.equal(state.authenticated.value, false);
});

test("完成跳转拒绝畸形地址、非 HTTPS 和带密码地址", async (t) => {
  const { state } = await mountComposable(t, () => {
    window.location.pathname = "/login";
    return useAppShell();
  }, () => { throw new Error("不应发送请求"); });
  for (const address of ["invalid", "http://example.test", "https://owner:password@example.test"]) {
    assert.doesNotThrow(() => state.handleInitialized({ completed: true, management_url: address }));
    assert.match(state.error.value, /正式管理地址无效/);
  }
  let destination;
  window.setTimeout = (callback) => callback();
  window.location.assign = (url) => { destination = url; };
  state.handleInitialized({ completed: true, management_url: "https://127.0.0.1:8443/" });
  assert.equal(destination, "https://127.0.0.1:8443/");
});
