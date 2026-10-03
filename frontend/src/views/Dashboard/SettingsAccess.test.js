import assert from "node:assert/strict";
import test from "node:test";
import { useDashboard } from "./Dashboard.js";
import { deferred, mountComposable } from "../../test-support/mount.js";

for (const role of ["admin", "user", "super_admin"]) {
  test(`工作台按实时会话加载系统设置：${role}`, async (t) => {
    const requests = [];
    const { state } = await mountComposable(t, useDashboard, async (path) => {
      requests.push(path);
      if (path.endsWith("/auth/session")) return Response.json({ role });
      if (path.endsWith("/settings")) {
        if (role !== "super_admin") return Response.json({ error: "无系统权限" }, { status: 403 });
        return Response.json({ notification: { smtp_username: "system-account" } });
      }
      return Response.json({ items: [] });
    });
    assert.equal(state.error.value, "");
    assert.equal(requests.includes("/api/v1/settings"), role === "super_admin");
    assert.equal(state.settings.value?.notification?.smtp_username || "", role === "super_admin" ? "system-account" : "");
  });
}

test("工作台重载在确认角色之前清除旧系统配置", async (t) => {
  let session = { role: "super_admin" };
  let pending;
  let reads = 0;
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/auth/session")) return pending ? pending.promise : Response.json(session);
    if (path.endsWith("/settings")) {
      reads += 1;
      return Response.json({ notification: { smtp_username: "system-account" } });
    }
    return Response.json({ items: [] });
  });
  state.showSettings.value = true;
  state.settingsForm.value.notification.smtp_password = "unsaved-secret";
  pending = deferred();
  const reload = state.loadData();
  assert.equal(state.currentUser.value, null);
  assert.equal(state.settings.value, null);
  assert.equal(state.showSettings.value, false);
  assert.equal(state.settingsForm.value.notification.smtp_password, "");
  session = { role: "admin" };
  pending.resolve(Response.json(session));
  await reload;
  assert.equal(reads, 1);
  assert.equal(state.error.value, "");
  assert.equal(state.canManageSystemSettings.value, false);
});

test("会话查询失败不能继续获取系统设置或使用旧角色", async (t) => {
  let fail = false;
  let reads = 0;
  const { state } = await mountComposable(t, useDashboard, async (path) => {
    if (path.endsWith("/auth/session")) {
      return fail ? Response.json({ error: "会话服务异常" }, { status: 503 }) : Response.json({ role: "super_admin" });
    }
    if (path.endsWith("/settings")) {
      reads += 1;
      return Response.json({ dns: { project_id: "system-project" } });
    }
    return Response.json({ items: [] });
  });
  fail = true;
  await state.loadData();
  assert.equal(reads, 1);
  assert.equal(state.settings.value, null);
  assert.equal(state.currentUser.value, null);
  assert.match(state.error.value, /会话服务异常/);
});
