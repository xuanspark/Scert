import assert from "node:assert/strict";
import test from "node:test";
import { useAppShell } from "./App.js";
import { mountComposable } from "./test-support/mount.js";

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
