import assert from "node:assert/strict";
import test from "node:test";
import { ref } from "vue";
import { useRenewalAction } from "./RenewalAction.js";
import { useDashboard } from "./Dashboard.js";
import { deferred, mountComposable } from "../../test-support/mount.js";

const task = { id: "renewal-check-task", task_type: "renewal_check", status: "queued" };

test("续期提交防重复点击，响应丢失后保留幂等键找回原任务", async () => {
  const pending = deferred();
  const keys = [];
  const opened = [];
  const error = ref("");
  const notice = ref("");
  const state = useRenewalAction({
    canRunRenewal: ref(true), error, notice,
    fetchAdmin: async (_, options) => {
      keys.push(options.headers["Idempotency-Key"]);
      if (keys.length === 1) return pending.promise;
      return Response.json({ task, replayed: true });
    },
    taskMonitor: { openTaskDetails: async (item) => opened.push(item) },
  });
  const request = state.runRenewalNow();
  assert.equal(state.renewalSubmitting.value, true);
  await state.runRenewalNow();
  assert.equal(keys.length, 1);
  pending.reject(new Error("连接中断"));
  await request;
  assert.equal(state.renewalSubmitting.value, false);
  assert.equal(notice.value, "");
  await state.runRenewalNow();
  assert.equal(keys[0], keys[1]);
  assert.match(keys[0], /^[A-Za-z0-9._-]{8,128}$/);
  assert.equal(opened.length, 1);
  assert.equal(error.value, "");
  assert.match(notice.value, /已提交/);
  assert.doesNotMatch(notice.value, /续期检查完成/);
  await state.runRenewalNow();
  assert.notEqual(keys[1], keys[2]);
});

for (const response of [
  { status: 202, body: {} },
  { status: 200, body: { report: { succeeded: 5 } } },
  { status: 202, body: { task: { ...task, task_type: "certificate_issue" } } },
  { status: 503, body: { error: "任务队列已满" } },
]) {
  test(`续期无有效受理结果不显示完成：${response.status} ${JSON.stringify(response.body)}`, async () => {
    const error = ref("");
    const notice = ref("");
    const state = useRenewalAction({
      canRunRenewal: ref(true), error, notice,
      fetchAdmin: async () => Response.json(response.body, { status: response.status }),
      taskMonitor: { openTaskDetails: () => assert.fail("无有效任务不得打开详情") },
    });
    await state.runRenewalNow();
    assert.ok(error.value);
    assert.equal(notice.value, "");
  });
}

test("真实续期入口跟踪已有任务，失败摘要持久化后可重新打开", async (t) => {
  let current = { ...task };
  let posts = 0;
  const { state } = await mountComposable(t, useDashboard, async (path, options = {}) => {
    if (path === "/api/v1/renewal/run") {
      posts++;
      assert.match(options.headers["Idempotency-Key"], /^[A-Za-z0-9._-]{8,128}$/);
      return Response.json({ task: current, replayed: true }, { status: 409 });
    }
    if (path === `/api/v1/tasks/${task.id}`) return Response.json(current);
    if (path.endsWith("/attempts")) return Response.json({ items: [{ attempt_number: 1, status: current.status }] });
    if (path.startsWith("/api/v1/tasks?")) return Response.json({ items: [current] });
    return Response.json({ items: [] });
  });
  state.currentUser.value = { role: "super_admin" };
  await state.runRenewalNow();
  assert.equal(posts, 1);
  assert.equal(state.error.value, "");
  assert.equal(state.showTaskDetails.value, true);
  assert.equal(state.taskDetails.value.status, "queued");
  current = { ...current, status: "partial_success", message: "续期检查未全部完成：成功 1 项，失败 1 项" };
  await state.refreshTaskStatus();
  assert.equal(state.taskDetails.value.message, current.message);
  assert.equal(state.taskDeploymentReport.value, null);
  state.closeTaskDetails();
  await state.openTaskDetails(current);
  assert.equal(state.taskDetails.value.status, "partial_success");
  assert.equal(state.taskTypeLabel("renewal_check"), "全局续期检查");
  assert.equal(posts, 1);
});
