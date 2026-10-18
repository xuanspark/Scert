import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import * as Vue from "vue";
import { compileScript, compileTemplate, parse } from "@vue/compiler-sfc";
import { renderToString } from "@vue/server-renderer";
import { workspaceRoleLabels } from "./UserWorkspace.js";

async function renderComponent(file, state) {
  const source = await readFile(new URL(file, import.meta.url), "utf8");
  const { descriptor, errors } = parse(source);
  assert.deepEqual(errors, []);
  assert.ok(compileScript(descriptor, { id: "account-display" }));
  const compiled = compileTemplate({
    source: descriptor.template.content,
    filename: file,
    id: "account-display",
    compilerOptions: { mode: "function" },
  });
  assert.deepEqual(compiled.errors, []);
  const render = new Function("Vue", compiled.code)(Vue);
  const app = Vue.createSSRApp({ setup: () => state, render });
  app.component("UserManagement", { render: () => null });
  app.component("ProjectManagement", { render: () => null });
  return renderToString(app);
}

test("普通用户切换板块时右上角仍显示会话账号与中文权限", async () => {
  for (const section of ["account", "projects"]) {
    const html = await renderComponent("./UserWorkspace.vue", {
      currentUser: { account: "用户<测试>", role: "user" },
      workspaceRoleLabels,
      loggingOut: false,
      error: "",
      logout() {},
      section,
    });
    assert.match(html, /<header>[\s\S]*aria-label="当前登录账号"[\s\S]*<\/header>/);
    assert.match(html, /用户&lt;测试&gt;/);
    assert.match(html, /权限：普通用户/);
    assert.doesNotMatch(html, /用户<测试>/);
  }
});

test("未取得会话不显示旧身份，未知权限不伪装成管理员", async () => {
  for (const currentUser of [null, { account: "", role: "unknown" }]) {
    const html = await renderComponent("./UserWorkspace.vue", {
      currentUser, workspaceRoleLabels, loggingOut: false, error: "", logout() {}, section: "account",
    });
    if (!currentUser) assert.doesNotMatch(html, /aria-label="当前登录账号"/);
    else {
      assert.match(html, /未命名账号/);
      assert.match(html, /权限：未知权限/);
    }
  }
});

test("系统设置导航仅对当前已确认的超级管理员显示", async () => {
  for (const role of [null, "user", "admin", "super_admin", "unknown"]) {
    const html = await renderComponent("../Dashboard/DashboardNavigation.vue", {
      activeSection: "overview", sections: [],
      currentUser: role ? { account: "测试", role } : null,
      version: "1.52.2",
      roleLabels: workspaceRoleLabels,
      loggingOut: false, error: "", logout() {},
    });
    assert.equal(html.includes("系统设置"), role === "super_admin");
    assert.match(html, /退出登录/);
  }
});

test("应用把已验证会话传给独立用户中心", async () => {
  const source = await readFile(new URL("../../App.vue", import.meta.url), "utf8");
  assert.match(source, /<UserWorkspace\s[^>]*:current-user="currentUser"/);
});
