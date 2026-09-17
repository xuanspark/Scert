import assert from "node:assert/strict";
import test from "node:test";
import { useUserManagement } from "./UserManagement.js";
import { mountComposable } from "../../test-support/mount.js";

async function setup(t) {
  const calls = [];
  const mounted = await mountComposable(t, useUserManagement, async (path, options) => {
    calls.push({ path, options });
    return Response.json({ items: [] });
  });
  calls.length = 0;
  return { ...mounted, calls };
}

test("主超级管理员不开放用户管理修改操作", async (t) => {
  const { state, calls } = await setup(t);
  const item = { id: "primary-super-admin", role: "super_admin", status: "active" };
  assert.equal(state.isPrimaryAccount(item), true);
  state.openEdit(item);
  assert.equal(state.showForm.value, false);
  await state.toggleStatus(item);
  await state.deleteUser(item);
  assert.equal(calls.length, 0);
});

test("禁止新增或提升为超级管理员，包括非标准大小写", async (t) => {
  const { state, calls } = await setup(t);
  for (const editing of [false, true]) {
    for (const role of ["super_admin", " SUPER_ADMIN "]) {
      if (editing) state.openEdit({ id: "operator", username: "operator", role: "admin" });
      else state.openCreate();
      Object.assign(state.form.value, { username: "operator", password: "OperPass123", role });
      await state.saveUser();
      assert.equal(state.formError.value, "不能新增或提升为超级管理员");
    }
  }
  assert.equal(calls.length, 0);
});

test("管理员和普通用户仍能正常创建及编辑", async (t) => {
  const { state, calls } = await setup(t);
  for (const role of ["admin", "user"]) {
    state.openCreate();
    Object.assign(state.form.value, { username: "operator", password: "OperPass123", role });
    await state.saveUser();
    state.openEdit({ id: "operator", username: "operator", role, status: "active" });
    await state.saveUser();
  }
  const writes = calls.filter(({ options }) => options.method);
  assert.deepEqual(writes.map(({ options }) => options.method), ["POST", "PUT", "POST", "PUT"]);
  assert.equal(state.formError.value, "");
});
