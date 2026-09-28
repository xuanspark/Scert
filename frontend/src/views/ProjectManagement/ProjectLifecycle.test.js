import assert from "node:assert/strict";
import test from "node:test";
import { ref } from "vue";
import { mountComposable, deferred } from "../../test-support/mount.js";
import { useProjectLifecycle } from "./ProjectLifecycle.js";
import { useProjectManagement } from "./ProjectManagement.js";

const projectA = { id: "project-a", name: "项目甲", description: "说明", can_manage_project: true, can_delete_project: true, can_manage_members: false };
const projectB = { ...projectA, id: "project-b", name: "项目乙" };

async function setup(t, fetcher, refresh = async () => {}) {
  const project = ref({ ...projectA });
  const canCreate = ref(true);
  const busy = ref(false);
  const mounted = await mountComposable(t, () => useProjectLifecycle(project, canCreate, busy, refresh), fetcher);
  return { ...mounted, project, canCreate, busy };
}

test("创建项目仅发送资料，编辑及删除使用固定项目标识", async (t) => {
  const calls = [];
  const refreshed = [];
  const { state } = await setup(t, async (path, options) => {
    calls.push([path, options]);
    return options.method === "DELETE" ? new Response(null, { status: 204 }) : Response.json({ id: "created" });
  }, async (action) => refreshed.push(action));
  state.openProjectEditor(true);
  state.projectEditor.value.name = "  新项目  ";
  state.projectEditor.value.description = " 新说明 ";
  await state.saveProject();
  assert.deepEqual(JSON.parse(calls[0][1].body), { name: "新项目", description: "新说明" });
  assert.equal(calls[0][0], "/api/v1/projects");
  assert.equal(calls[0][1].method, "POST");
  assert.match(state.lifecycleNotice.value, /created/);
  state.openProjectEditor(false);
  state.projectEditor.value.name = "修改项目";
  await state.saveProject();
  assert.equal(calls[1][0], "/api/v1/projects/project-a");
  assert.equal(calls[1][1].method, "PUT");
  state.requestDeleteProject();
  assert.equal(calls.length, 2);
  await state.confirmDeleteProject();
  assert.equal(calls[2][1].method, "DELETE");
  assert.deepEqual(JSON.parse(calls[2][1].body), { confirmation: "project-a" });
  assert.deepEqual(refreshed, ["create", "update", "delete"]);
  assert.ok(calls.every(([, options]) => options.credentials === "include" && options.cache === "no-store"));
});

test("生命周期权限和成员保存状态阻止发送变更", async (t) => {
  const { state, project, canCreate, busy } = await setup(t, () => assert.fail("不应发送请求"));
  canCreate.value = false;
  state.openProjectEditor(true);
  assert.equal(state.projectEditor.value, null);
  project.value = { ...projectA, can_manage_project: false, can_delete_project: false };
  state.openProjectEditor(false);
  state.requestDeleteProject();
  assert.equal(state.projectEditor.value, null);
  assert.equal(state.pendingDeleteProject.value, null);
  project.value = { ...projectA };
  busy.value = true;
  state.openProjectEditor(false);
  assert.equal(state.projectEditor.value, null);
});

test("项目名称校验、二次确认取消和切换项目清理", async (t) => {
  const { state, project } = await setup(t, () => assert.fail("不应发送请求"));
  state.openProjectEditor(true);
  state.projectEditor.value.name = " ";
  await state.saveProject();
  assert.match(state.lifecycleError.value, /项目名称/);
  state.cancelProjectAction();
  assert.equal(state.projectEditor.value, null);
  state.requestDeleteProject();
  state.cancelProjectAction();
  await state.confirmDeleteProject();
  state.requestDeleteProject();
  project.value = { ...projectB };
  assert.equal(state.pendingDeleteProject.value, null);
  await state.confirmDeleteProject();
});

test("重复提交、切换及卸载后的迟到结果不修改新页面", async (t) => {
  for (const change of ["switch", "unmount"]) {
    await t.test(change, async (t) => {
      const pending = deferred();
      let calls = 0;
      let refreshes = 0;
      const { state, project, unmount } = await setup(t, () => { calls += 1; return pending.promise; }, async () => { refreshes += 1; });
      state.requestDeleteProject();
      const first = state.confirmDeleteProject();
      await state.confirmDeleteProject();
      assert.equal(calls, 1);
      if (change === "switch") project.value = { ...projectB };
      else unmount();
      pending.resolve(new Response(null, { status: 204 }));
      await first;
      assert.equal(refreshes, 0);
      assert.equal(state.lifecycleNotice.value, "");
      assert.equal(state.savingProject.value, false);
    });
  }
});

test("拒绝及引用冲突刷新权限且不显示删除成功", async (t) => {
  for (const status of [403, 409]) {
    await t.test(String(status), async (t) => {
      let refreshes = 0;
      const { state, project } = await setup(t, async () => Response.json({ error: "存在引用或权限失效", request_id: "request-test" }, { status }), async () => {
        refreshes += 1;
        project.value = { ...projectA, can_manage_project: false, can_delete_project: false };
      });
      state.requestDeleteProject();
      await state.confirmDeleteProject();
      assert.equal(refreshes, 1);
      assert.equal(state.lifecycleNotice.value, "");
      assert.match(state.lifecycleError.value, /request-test/);
      assert.equal(state.canDeleteProject.value, false);
      assert.equal(state.pendingDeleteProject.value, null);
    });
  }
});

test("会话过期跳登录，网络中断明确结果未确认", async (t) => {
  let mode = "auth";
  const { state } = await setup(t, async () => {
    if (mode === "network") throw new TypeError("network");
    return Response.json({ error: "登录失效" }, { status: 401 });
  });
  const redirects = [];
  window.location.assign = (path) => redirects.push(path);
  state.requestDeleteProject();
  await state.confirmDeleteProject();
  assert.deepEqual(redirects, ["/login"]);
  mode = "network";
  await state.confirmDeleteProject();
  assert.match(state.lifecycleError.value, /结果未确认/);
  assert.equal(state.lifecycleNotice.value, "");
});

test("删除末页项目后回到首页，刷新期间不可切换或重复操作", async (t) => {
  let deleted = false;
  const pending = deferred();
  const { state } = await mountComposable(t, useProjectManagement, async (path, options) => {
    if (options.method === "DELETE") return pending.promise;
    const second = new URL(path, "http://local").searchParams.get("after");
    return Response.json({ items: deleted || !second ? [projectA] : [projectB], can_create_project: true, has_more: !deleted && !second, next_cursor: "project-a" });
  });
  await state.nextProjectPage();
  assert.equal(state.selectedProject.value.id, "project-b");
  state.requestDeleteProject();
  const saving = state.confirmDeleteProject();
  state.selectProject("project-a");
  await state.previousProjectPage();
  assert.equal(state.projectPage.value, 1);
  deleted = true;
  pending.resolve(new Response(null, { status: 204 }));
  await saving;
  assert.equal(state.projectPage.value, 0);
  assert.equal(state.selectedProject.value.id, "project-a");
  assert.match(state.lifecycleNotice.value, /已删除/);
  assert.equal(state.savingProject.value, false);
});

test("新建项目未在首页出现时保留明确成功标识，不伪造目录条目", async (t) => {
  const { state } = await mountComposable(t, useProjectManagement, async (_path, options) => options.method === "POST"
    ? Response.json({ id: "outside-first-page" }, { status: 201 })
    : Response.json({ items: [projectA], can_create_project: true }));
  state.openProjectEditor(true);
  state.projectEditor.value.name = "末页项目";
  await state.saveProject();
  assert.deepEqual(state.projects.value.map((item) => item.id), ["project-a"]);
  assert.match(state.lifecycleNotice.value, /outside-first-page/);
  assert.match(state.lifecycleNotice.value, /翻页/);
});
