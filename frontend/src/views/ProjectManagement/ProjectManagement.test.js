import assert from "node:assert/strict";
import test from "node:test";
import { nextTick, ref } from "vue";
import { readFile } from "node:fs/promises";
import { mountComposable, deferred } from "../../test-support/mount.js";
import { useProjectManagement } from "./ProjectManagement.js";
import { useProjectMembers } from "./ProjectMembers.js";
import { useUserWorkspace } from "../UserManagement/UserWorkspace.js";

test("完整成员页面可渲染中文、多个角色、权限拒绝和确认状态且转义账户文字", async (t) => {
  const Vue = await import("vue");
  const { parse, compileTemplate } = await import("@vue/compiler-sfc");
  const { renderToString } = await import("@vue/server-renderer");
  const source = await readFile(new URL("./ProjectManagement.vue", import.meta.url), "utf8");
  const { descriptor } = parse(source);
  const compiled = compileTemplate({ source: descriptor.template.content, filename: "ProjectManagement.vue", id: "project-members", compilerOptions: { mode: "function" } });
  assert.deepEqual(compiled.errors, []);
  const render = new Function("Vue", compiled.code)(Vue);
  const { state } = await mountComposable(t, useProjectManagement, async (path) => {
    if (path.startsWith("/api/v1/projects?")) return page([projectA]);
    if (path === "/api/v1/users") return page([]);
    return page([{ ...memberA, username: "<script>unexpected</script>" }]);
  });
  const renderPage = async () => {
    const warnings = [];
    const app = Vue.createSSRApp({ render, setup: () => state });
    app.config.warnHandler = (message) => warnings.push(message);
    const html = await renderToString(app);
    assert.deepEqual(warnings, []);
    return html;
  };
  let html = await renderPage();
  assert.match(html, /项目与成员/);
  assert.match(html, /DNS 管理员/);
  assert.match(html, /&lt;script&gt;unexpected&lt;\/script&gt;/);
  assert.doesNotMatch(html, /<script>unexpected/);
  state.requestRevoke(state.members.value[0], "dns_admin");
  html = await renderPage();
  assert.match(html, /确认撤销/);
  state.selectedProject.value = { ...projectA, can_manage_members: false };
  html = await renderPage();
  assert.match(html, /当前没有成员管理权限/);
  assert.doesNotMatch(html, /project-grant-form/);
  assert.doesNotMatch(html, /unexpected/);

  state.canCreateProject.value = true;
  state.openProjectEditor(true);
  state.projectEditor.value.name = '<img src=x onerror="unexpected">';
  state.projectEditor.value.description = "第一行\n第二行";
  html = await renderPage();
  assert.match(html, /aria-label="项目资料"/);
  assert.match(html, /maxlength="128"/);
  assert.match(html, /maxlength="4096"/);
  assert.match(html, /第一行\n第二行/);
  assert.match(html, /&lt;img/);
  assert.doesNotMatch(html, /<img src=x/);
  state.savingProject.value = true;
  html = await renderPage();
  assert.match(html, /正在保存……/);
  assert.match(html, /<input[^>]*disabled/);
  assert.match(html, /<textarea[^>]*disabled/);
  state.savingProject.value = false;
  state.cancelProjectAction();
  state.selectedProject.value = { ...projectA, can_manage_project: true, can_delete_project: true };
  state.requestDeleteProject();
  html = await renderPage();
  assert.match(html, /确认删除项目/);
  assert.match(html, /默认项目不能删除/);
  assert.doesNotMatch(html, /project-editor/);
  state.selectedProject.value = { ...projectA, can_manage_project: false, can_delete_project: false };
  state.openProjectEditor();
  state.requestDeleteProject();
  html = await renderPage();
  assert.doesNotMatch(html, /确认删除项目|aria-label="项目资料"/);
});

test("账户页提供本人可分享的账户 ID，不要求用户查看数据库", async () => {
  const source = await readFile(new URL("../UserManagement/UserTable.vue", import.meta.url), "utf8");
  assert.ok(source.includes("账户 ID：{{ item.id }}"));
});

test("目录卸载后响应不再修改页面状态", async (t) => {
  const pending = deferred();
  const { state, unmount } = await mountComposable(t, useProjectManagement, () => pending.promise);
  unmount();
  pending.resolve(page([projectA]));
  await flush();
  assert.deepEqual(state.projects.value, []);
  assert.equal(state.selectedProject.value, null);
});

test("网络错误显示中文，服务端错误保留安全的请求编号", async (t) => {
  let mode = "network";
  const { state } = await setupMembers(t, (path) => {
    if (!path.includes("/members?")) return;
    if (mode === "network") throw new TypeError("Failed to fetch");
    return Response.json({ error: "读取失败", request_id: "scert-test-123" }, { status: 500 });
  });
  assert.equal(state.memberError.value, "项目请求失败，请检查网络后重试");
  mode = "server";
  await state.loadMembers();
  assert.match(state.memberError.value, /请求编号：scert-test-123/);
});

test("变更请求在卸载后成功，不显示成功提示也不再读取成员", async (t) => {
  const pending = deferred();
  const { state, calls, unmount } = await setupMembers(t, (_path, options) => options.method === "POST" ? pending.promise : undefined);
  state.memberForm.value = { userID: "target", role: "read_only" };
  const mutation = state.grantMember();
  const count = calls.length;
  unmount();
  pending.resolve(Response.json({}, { status: 201 }));
  await mutation;
  assert.equal(calls.length, count);
  assert.equal(state.memberNotice.value, "");
});

test("停用成员仍可撤销角色，但不在授予角色的建议列表中", async (t) => {
  const { state } = await setupMembers(t, (path) => path.includes("/members?") ? page([{ ...memberA, status: "disabled" }]) : undefined);
  assert.equal(state.members.value.length, 1);
  assert.equal(state.accountChoices.value.some((item) => item.id === memberA.user_id), false);
  state.requestRevoke(state.members.value[0], "dns_admin");
  assert.equal(state.pendingRevoke.value.userID, memberA.user_id);
});

test("撤销必须确认且 DELETE 不带正文，撤销自身管理权限后清空敏感页面", async (t) => {
  let revoked = false;
  const { state, calls } = await setupMembers(t, (path, options) => {
    if (options.method === "DELETE") { revoked = true; return new Response(null, { status: 204 }); }
    if (revoked && path.includes("/members?")) return Response.json({ error: "无权管理成员" }, { status: 403 });
  });
  state.requestRevoke(memberA, "project_admin");
  assert.equal(calls.some(({ options }) => options.method === "DELETE"), false);
  assert.equal(state.pendingRevoke.value.userID, "user-a");
  await state.confirmRevoke();
  const request = calls.find(({ options }) => options.method === "DELETE");
  assert.equal(request.path, "/api/v1/projects/project-a/members/user-a/roles/project_admin");
  assert.equal(Object.hasOwn(request.options, "body"), false);
  assert.equal(state.canManageMembers.value, false);
  assert.deepEqual(state.members.value, []);
  assert.deepEqual(state.accountChoices.value, []);
  assert.equal(state.pendingRevoke.value, null);
});

test("切换项目后旧成员响应和账户建议不得污染新项目", async (t) => {
  const oldMembers = deferred();
  const oldAccounts = deferred();
  let accounts = 0;
  const { state, project } = await setupMembers(t, (path) => {
    if (path.includes("project-a/members")) return oldMembers.promise;
    if (path === "/api/v1/users" && ++accounts === 1) return oldAccounts.promise;
    if (path.includes("project-b/members")) return page([{ ...memberA, user_id: "new-user" }]);
  });
  project.value = projectB;
  await flush();
  oldMembers.resolve(page([{ ...memberA, user_id: "old-user" }]));
  oldAccounts.resolve(page([{ id: "old-account", status: "active" }]));
  await flush();
  assert.deepEqual(state.members.value.map((item) => item.user_id), ["new-user"]);
  assert.equal(state.accountChoices.value.some((item) => item.id === "old-account"), false);
});

test("旧分页请求后返回时不能覆盖新的页长和内容", async (t) => {
  const old = deferred();
  const { state } = await setupMembers(t, (path) => {
    if (path.includes("/members?page_size=10")) return old.promise;
    if (path.includes("/members?page_size=5")) return page([{ ...memberA, user_id: "five" }]);
  });
  await state.changeMemberPageSize(5);
  old.resolve(page([memberA], "user-a"));
  await flush();
  assert.equal(state.members.value[0].user_id, "five");
  assert.equal(state.memberHasMore.value, false);
});

test("切换项目后旧变更成功不能清空新表单或显示错误成功消息", async (t) => {
  const pending = deferred();
  const { state, project } = await setupMembers(t, (_path, options) => options.method === "POST" ? pending.promise : undefined);
  state.memberForm.value = { userID: "target-a", role: "dns_admin" };
  const mutation = state.grantMember();
  project.value = projectB;
  await flush();
  state.memberForm.value = { userID: "target-b", role: "read_only" };
  pending.resolve(Response.json({}, { status: 201 }));
  await mutation;
  assert.equal(state.memberForm.value.userID, "target-b");
  assert.equal(state.memberNotice.value, "");
});

test("卸载后成员请求失败不得回写或触发登录跳转", async (t) => {
  const pending = deferred();
  const { state, unmount } = await setupMembers(t, (path) => path.includes("/members?") ? pending.promise : undefined);
  let redirects = 0;
  window.location.assign = () => { redirects += 1; };
  unmount();
  pending.resolve(Response.json({ error: "会话过期" }, { status: 401 }));
  await flush();
  assert.deepEqual(state.members.value, []);
  assert.equal(redirects, 0);
});

test("有效页面收到 401 跳转登录并清空成员与账户建议", async (t) => {
  const pending = deferred();
  const { state } = await setupMembers(t, (path) => path.includes("/members?") ? pending.promise : undefined);
  const redirects = [];
  window.location.assign = (path) => redirects.push(path);
  pending.resolve(Response.json({ error: "请登录" }, { status: 401 }));
  await flush();
  assert.deepEqual(redirects, ["/login"]);
  assert.equal(state.canManageMembers.value, false);
  assert.deepEqual(state.accountChoices.value, []);
});

test("账户建议失败不妨碍通过明确账户 ID 授予角色", async (t) => {
  const { state, calls } = await setupMembers(t, (path) => path === "/api/v1/users" ? Response.json({}, { status: 500 }) : undefined);
  assert.match(state.accountError.value, /账户 ID/);
  state.memberForm.value = { userID: "known-user", role: "read_only" };
  await state.grantMember();
  assert.equal(calls.filter(({ options }) => options.method === "POST").length, 1);
});

test("普通工作台采用单板块导航，不挂载管理员控制台", async (t) => {
  const { state } = await mountComposable(t, useUserWorkspace, async () => page([]));
  assert.equal(state.section.value, "account");
  state.section.value = "projects";
  assert.equal(state.section.value, "projects");
  const template = await readFile(new URL("../UserManagement/UserWorkspace.vue", import.meta.url), "utf8");
  assert.ok(template.includes(`v-if="section === 'account'"`));
  assert.ok(template.includes(`v-else-if="section === 'projects'"`));
  assert.equal(template.includes("<Dashboard"), false);
});

const projectA = { id: "project-a", name: "项目甲", can_manage_members: true };
const projectB = { id: "project-b", name: "项目乙", can_manage_members: true };
const memberA = { user_id: "user-a", username: "成员甲", status: "active", roles: ["project_admin", "dns_admin"] };
const page = (items, next = "") => Response.json({ items, has_more: Boolean(next), next_cursor: next, page_size: 10 });
const flush = async () => { await new Promise((resolve) => setImmediate(resolve)); await nextTick(); };

async function setupMembers(t, handler = () => undefined, project = ref(projectA)) {
  const calls = [];
  const mounted = await mountComposable(t, () => useProjectMembers(project), async (path, options) => {
    calls.push({ path, options });
    const override = handler(path, options);
    if (override !== undefined) return override;
    if (path === "/api/v1/users") return page([{ id: "self", username: "本人", status: "active" }, { id: "disabled", status: "disabled" }]);
    return page([memberA]);
  });
  return { ...mounted, calls, project };
}

test("项目入口使用服务端项目且默认选择实际返回项，不写死默认项目", async (t) => {
  const calls = [];
  const { state } = await mountComposable(t, useProjectManagement, async (path, options) => {
    calls.push({ path, options });
    if (path.startsWith("/api/v1/projects?")) return page([projectA, projectB]);
    if (path === "/api/v1/users") return page([]);
    return page([memberA]);
  });
  assert.equal(state.selectedProject.value.id, "project-a");
  assert.equal(state.members.value.length, 1);
  assert.ok(calls.every(({ options }) => options.credentials === "include" && options.cache === "no-store"));
  assert.equal(calls.some(({ path }) => path.includes("default")), false);
  state.selectProject("project-b");
  await flush();
  assert.equal(state.selectedProject.value.id, "project-b");
  assert.ok(calls.some(({ path }) => path.includes("project-b/members")));
});

test("项目分页保留游标并支持四种页长，未知项目不能被选中", async (t) => {
  const paths = [];
  const { state } = await mountComposable(t, useProjectManagement, async (path) => {
    paths.push(path);
    if (path.startsWith("/api/v1/projects?")) {
      const query = new URL(path, "http://localhost").searchParams;
      return query.get("after") ? page([projectB]) : page([projectA], "project-a");
    }
    return page([]);
  });
  await state.nextProjectPage();
  assert.equal(state.projectPage.value, 1);
  assert.equal(state.selectedProject.value.id, "project-b");
  assert.ok(paths.includes("/api/v1/projects?page_size=5&after=project-a"));
  await state.previousProjectPage();
  assert.equal(state.projectPage.value, 0);
  for (const size of [5, 10, 15, 30]) {
    await state.changeProjectPageSize(size);
    assert.equal(state.projectPageSize.value, size);
    assert.equal(state.projectPage.value, 0);
  }
  const count = paths.length;
  await state.changeProjectPageSize(100);
  assert.equal(paths.length, count);
  state.selectProject("hidden-project");
  assert.equal(state.selectedProject.value, null);
});

test("没有项目时显示空列表，不尝试请求默认项目或全站用户", async (t) => {
  const paths = [];
  const { state } = await mountComposable(t, useProjectManagement, async (path) => { paths.push(path); return page([]); });
  assert.deepEqual(state.projects.value, []);
  assert.equal(state.selectedProject.value, null);
  assert.equal(paths.length, 1);
});

test("项目查询刷新失败清空旧项目和成员，旧响应不能覆盖新分页", async (t) => {
  const old = deferred();
  let attempt = 0;
  const { state } = await mountComposable(t, useProjectManagement, (path) => {
    if (!path.startsWith("/api/v1/projects?")) return page([]);
    attempt += 1;
    if (attempt === 1) return old.promise;
    if (attempt === 2) return page([projectB]);
    return Response.json({ error: "无法读取项目" }, { status: 500 });
  });
  await state.changeProjectPageSize(15);
  old.resolve(page([projectA]));
  await flush();
  assert.equal(state.selectedProject.value.id, "project-b");
  await state.loadProjects();
  assert.equal(state.selectedProject.value, null);
  assert.deepEqual(state.projects.value, []);
  assert.deepEqual(state.members.value, []);
  assert.equal(state.projectError.value, "无法读取项目");
});

test("只读成员不请求成员目录或账户建议，也不能伪造授予操作", async (t) => {
  const { state, calls } = await setupMembers(t, undefined, ref({ ...projectA, can_manage_members: false }));
  assert.equal(state.canManageMembers.value, false);
  state.memberForm.value = { userID: "target", role: "project_admin" };
  await state.grantMember();
  assert.equal(calls.length, 0);
});

test("账户建议只使用已有可见账户接口和本页成员，不使用邮箱或全站查询", async (t) => {
  const { state, calls } = await setupMembers(t);
  assert.deepEqual(state.accountChoices.value, [{ id: "self", username: "本人" }, { id: "user-a", username: "成员甲" }]);
  assert.equal(calls.filter(({ path }) => path === "/api/v1/users").length, 1);
});

test("成员分页使用账户游标且多个角色不拆分，切换页长重置页码", async (t) => {
  const { state, calls } = await setupMembers(t, (path) => {
    if (path.includes("/members?")) return page([memberA], "user-a");
  });
  assert.equal(state.members.value[0].roles.length, 2);
  await state.nextMemberPage();
  assert.equal(state.memberPage.value, 1);
  assert.ok(calls.at(-1).path.endsWith("page_size=10&after=user-a"));
  await state.previousMemberPage();
  assert.equal(state.memberPage.value, 0);
  for (const size of [5, 10, 15, 30]) {
    await state.changeMemberPageSize(size);
    assert.equal(state.memberPageSize.value, size);
    assert.equal(state.memberPage.value, 0);
    assert.ok(calls.at(-1).path.endsWith(`page_size=${size}&after=`));
  }
  const count = calls.length;
  await state.changeMemberPageSize(100);
  assert.equal(calls.length, count);
});

test("授予角色只提交账户 ID 与角色，去重点击并在成功后刷新", async (t) => {
  const pending = deferred();
  const { state, calls } = await setupMembers(t, (_path, options) => options.method === "POST" ? pending.promise : undefined);
  state.memberForm.value = { userID: " target ", role: "dns_admin" };
  const first = state.grantMember();
  await state.grantMember();
  const posts = calls.filter(({ options }) => options.method === "POST");
  assert.equal(posts.length, 1);
  assert.deepEqual(JSON.parse(posts[0].options.body), { user_id: "target", role: "dns_admin" });
  assert.equal(posts[0].options.headers["Content-Type"], "application/json");
  pending.resolve(Response.json({ message: "成功" }, { status: 201 }));
  await first;
  assert.equal(state.memberNotice.value, "项目角色已授予");
  assert.equal(state.memberForm.value.userID, "");
  assert.equal(state.savingMember.value, false);
});

test("非法账户和角色不提交请求，重复授权冲突保留表单供修正", async (t) => {
  const { state, calls } = await setupMembers(t, (_path, options) => options.method === "POST" ? Response.json({ error: "角色已存在" }, { status: 409 }) : undefined);
  const initial = calls.length;
  for (const form of [{ userID: "../other", role: "dns_admin" }, { userID: "target", role: "super_admin" }]) {
    state.memberForm.value = form;
    await state.grantMember();
  }
  assert.equal(calls.length, initial);
  state.memberForm.value = { userID: "target", role: "dns_admin" };
  await state.grantMember();
  assert.equal(state.memberError.value, "角色已存在");
  assert.equal(state.memberForm.value.userID, "target");
  assert.equal(state.canManageMembers.value, true);
});
