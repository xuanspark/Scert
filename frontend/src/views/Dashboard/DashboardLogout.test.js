import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import * as Vue from "vue";
import { compileScript, compileTemplate, parse } from "@vue/compiler-sfc";
import { renderToString } from "@vue/server-renderer";
import { useAccessDenied } from "../AccessDenied/AccessDenied.js";
import { deferred, mountComposable } from "../../test-support/mount.js";

test("管理侧栏始终提供退出入口，并展示忙碌和失败状态", async () => {
  const source = await readFile(new URL("./DashboardNavigation.vue", import.meta.url), "utf8");
  const { descriptor, errors } = parse(source);
  assert.deepEqual(errors, []);
  assert.ok(compileScript(descriptor, { id: "dashboard-logout" }));
  assert.match(descriptor.scriptSetup.content, /const\s*\{\s*loggingOut,\s*error,\s*logout\s*\}\s*=\s*useAccessDenied\(\)/);
  assert.match(descriptor.template.content, /@click="logout"/);
  const compiled = compileTemplate({
    source: descriptor.template.content,
    filename: "DashboardNavigation.vue",
    id: "dashboard-logout",
    compilerOptions: { mode: "function" },
  });
  assert.deepEqual(compiled.errors, []);
  const render = new Function("Vue", compiled.code)(Vue);
  for (const state of [
    { loggingOut: false, error: "" },
    { loggingOut: true, error: "" },
    { loggingOut: false, error: "退出登录失败" },
  ]) {
    const html = await renderToString(Vue.createSSRApp({
      setup: () => ({ activeSection: "overview", sections: [], logout() {}, ...state }),
      render,
    }));
    assert.match(html, /class="navigation-session"/);
    assert.match(html, state.loggingOut ? /退出中/ : /退出登录/);
    const button = html.match(/<button[^>]*navigation-logout[^>]*>/)?.[0];
    assert.ok(button);
    assert.equal(/\sdisabled(?:[\s=>])/.test(button), state.loggingOut);
    if (state.error) assert.match(html, /role="alert"[^>]*>退出登录失败/);
    else assert.doesNotMatch(html, /role="alert"/);
  }
});

test("退出登录携带会话且成功后跳转登录页", async (t) => {
  const calls = [];
  const { state } = await mountComposable(t, useAccessDenied, async (...args) => {
    calls.push(args);
    return Response.json({ success: true });
  });
  const redirect = t.mock.method(window.location, "assign");
  await state.logout();
  assert.deepEqual(calls, [["/api/v1/auth/logout", { method: "POST", credentials: "include" }]]);
  assert.equal(redirect.mock.callCount(), 1);
  assert.deepEqual(redirect.mock.calls[0].arguments, ["/login"]);
  assert.equal(state.loggingOut.value, false);
  assert.equal(state.error.value, "");
});

test("退出登录请求期间阻止重复提交", async (t) => {
  const pending = deferred();
  let calls = 0;
  const { state } = await mountComposable(t, useAccessDenied, () => {
    calls += 1;
    return pending.promise;
  });
  const first = state.logout();
  assert.equal(state.loggingOut.value, true);
  await state.logout();
  assert.equal(calls, 1);
  pending.resolve(Response.json({ success: true }));
  await first;
  assert.equal(state.loggingOut.value, false);
});

test("退出失败不跳转，展示服务端错误并允许重试", async (t) => {
  let calls = 0;
  const { state } = await mountComposable(t, useAccessDenied, async () => {
    calls += 1;
    return calls === 1
      ? Response.json({ error: "会话服务暂不可用" }, { status: 503 })
      : Response.json({ success: true });
  });
  const redirect = t.mock.method(window.location, "assign");
  await state.logout();
  assert.equal(redirect.mock.callCount(), 0);
  assert.equal(state.error.value, "会话服务暂不可用");
  assert.equal(state.loggingOut.value, false);
  await state.logout();
  assert.equal(state.error.value, "");
  assert.equal(redirect.mock.callCount(), 1);
});

test("退出接口返回非 JSON 错误时保留中文提示和当前页面", async (t) => {
  const { state } = await mountComposable(t, useAccessDenied, async () => new Response("Bad Gateway", { status: 502 }));
  const redirect = t.mock.method(window.location, "assign");
  await state.logout();
  assert.equal(state.error.value, "退出登录失败");
  assert.equal(state.loggingOut.value, false);
  assert.equal(redirect.mock.callCount(), 0);
});
