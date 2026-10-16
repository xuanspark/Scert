import assert from "node:assert/strict";
import "./UserWorkspace.test.js";
import test from "node:test";
import { groupUsers, useUserManagement } from "./UserManagement.js";
import { useUserGroupEditor } from "./UserGroupEditor.js";
import { mountComposable } from "../../test-support/mount.js";

async function setup(t) {
  const calls = [];
  const mounted = await mountComposable(t, useUserManagement, async (path, options) => {
    calls.push({ path, options });
    return Response.json({ items: [], current_role: "super_admin" });
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

test("默认分组置顶，多组管理员同时出现在所属分组且不重复", () => {
  const members = [
    { id: "multi", groups: ["乙组", "甲组", "甲组"] },
    { id: "single", groups: ["甲组"] },
    { id: "default" },
  ];
  const result = groupUsers(members);
  assert.equal(result[0].isDefault, true);
  assert.deepEqual(result[0].users.map((user) => user.id), ["default"]);
  assert.equal(result.find((group) => group.name === "甲组").users.length, 2);
  assert.equal(result.find((group) => group.name === "乙组").users.length, 1);
});

test("管理员只能读取成员，普通用户不显示分组，未知权限默认关闭写入", async (t) => {
  for (const role of ["admin", "user", ""]) {
    const writes = [];
    const { state } = await mountComposable(t, useUserManagement, async (path, options) => {
      if (options.method) writes.push(path);
      return Response.json({ items: [], current_role: role });
    });
    assert.equal(state.canManage.value, false);
    assert.equal(state.showGroups.value, role === "admin");
    state.openCreate();
    state.openEdit({ id: "member", role: "user" });
    assert.equal(state.showForm.value, false);
    await state.saveUser();
    await state.toggleStatus({ id: "member", status: "active" });
    await state.deleteUser({ id: "member" });
    assert.deepEqual(writes, []);
  }
});

test("普通用户不允许多组，降级时保留选择供用户修正", async (t) => {
  const { state, calls } = await setup(t);
  state.openEdit({ id: "leader", username: "leader", role: "admin", groups: ["甲组", "乙组"] });
  state.form.value.role = "user";
  await state.saveUser();
  assert.match(state.formError.value, /只能属于一个分组/);
  assert.deepEqual(state.form.value.groups, ["甲组", "乙组"]);
  assert.equal(calls.length, 0);
  state.form.value.groups = ["乙组"];
  await state.saveUser();
  assert.deepEqual(JSON.parse(calls.find((call) => call.options.method === "PUT").options.body).groups, ["乙组"]);
});

test("分组编辑器支持管理员多组并拒绝普通用户多组及保留名称", () => {
  const props = { modelValue: [], role: "admin" };
  const editor = useUserGroupEditor(props, (_, value) => { props.modelValue = value; });
  for (const name of [" 甲组 ", "乙组", "甲组"]) {
    editor.groupName.value = name;
    editor.addGroup();
  }
  assert.deepEqual(props.modelValue, ["甲组", "乙组"]);
  props.role = "user";
  editor.groupName.value = "丙组";
  editor.addGroup();
  assert.match(editor.groupError.value, /只能属于一个分组/);
  editor.removeGroup("乙组");
  assert.deepEqual(props.modelValue, ["甲组"]);
  editor.groupName.value = "默认分组";
  editor.addGroup();
  assert.match(editor.groupError.value, /默认分组无需添加/);
});

test("分组页面结构可编译，默认组不折叠，命名组默认关闭，密码入口仅在用户管理", async () => {
  const { readFile } = await import("node:fs/promises");
  const { parse, compileScript, compileTemplate } = await import("@vue/compiler-sfc");
  for (const file of ["UserManagement.vue", "UserTable.vue", "UserGroupEditor.vue", "UserWorkspace.vue"]) {
    const source = await readFile(new URL(file, import.meta.url), "utf8");
    const { descriptor, errors } = parse(source);
    assert.deepEqual(errors, []);
    assert.ok(compileScript(descriptor, { id: file }));
    assert.deepEqual(compileTemplate({ source: descriptor.template.content, filename: file, id: file }).errors, []);
  }
  const source = await readFile(new URL("UserManagement.vue", import.meta.url), "utf8");
  assert.match(source, /section v-if="group.isDefault"/);
  assert.match(source, /<details v-else class="user-group">/);
  assert.match(source, /v-else-if="showGroups"/);
  assert.match(source, /<ChangePassword/);
  const dashboard = await readFile(new URL("../Dashboard/Dashboard.vue", import.meta.url), "utf8");
  const navigation = await readFile(new URL("../Dashboard/DashboardNavigation.vue", import.meta.url), "utf8");
  assert.doesNotMatch(dashboard, /<ChangePassword/);
  assert.doesNotMatch(dashboard, /@click="openSettings"/);
  assert.match(navigation, /\$emit\('settings'\)/);
});

test("账户列表自增ID分配及超管管理员排序规则", async () => {
  const { sortAndAssignDisplayIds } = await import("./UserManagement.js");
  const users = [
    { id: "user-2", username: "bob", role: "user", created_at: "2026-01-03T00:00:00Z" },
    { id: "admin-1", username: "alice", role: "admin", created_at: "2026-01-02T00:00:00Z", updated_at: "2026-02-01T00:00:00Z" },
    { id: "primary-super-admin", username: "admin", role: "super_admin", created_at: "2026-01-01T00:00:00Z" },
    { id: "admin-2", username: "charlie", role: "admin", created_at: "2026-01-04T00:00:00Z", updated_at: "2026-01-15T00:00:00Z" },
  ];
  const processed = sortAndAssignDisplayIds(users);
  
  // 超管排在第一，分配账户 ID 1
  assert.equal(processed[0].id, "primary-super-admin");
  assert.equal(processed[0].account_number, 1);
  
  // 管理员排在超管后面，按更新/设置时间升序排序：admin-2 (01-15) 在 admin-1 (02-01) 前面
  assert.equal(processed[1].id, "admin-2");
  assert.equal(processed[1].role, "admin");
  assert.equal(processed[2].id, "admin-1");
  assert.equal(processed[2].role, "admin");
  
  // 普通用户排在最后
  assert.equal(processed[3].id, "user-2");
  assert.equal(processed[3].role, "user");
  
  // 账户ID序号检查：admin-1(created: 01-02 => 2), user-2(created: 01-03 => 3), admin-2(created: 01-04 => 4)
  assert.equal(processed.find(u => u.id === "admin-1").account_number, 2);
  assert.equal(processed.find(u => u.id === "user-2").account_number, 3);
  assert.equal(processed.find(u => u.id === "admin-2").account_number, 4);
});
