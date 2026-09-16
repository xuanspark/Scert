import assert from "node:assert/strict";
import test from "node:test";
import { ref } from "vue";
import { useDatabaseSetup } from "./DatabaseSetup.js";
import { deferred, mountComposable } from "../../test-support/mount.js";

function verified(driver = "sqlite", token = "test-database-token") {
  return Response.json({
    driver,
    verified: true,
    verification_token: token,
    expires_at: new Date(Date.now() + 60000).toISOString(),
  });
}

for (const driver of ["sqlite", "mysql", "postgres"]) {
  test(`数据库 ${driver} 使用结构化参数与严格 TLS`, async (t) => {
    const requests = [];
    const { state } = await mountComposable(t, () => useDatabaseSetup(ref(Date.now())), async (path, options) => {
      requests.push({ path, payload: JSON.parse(options.body), credentials: options.credentials });
      return verified(driver);
    });
    state.databaseForm.value.driver = driver;
    Object.assign(state.databaseForm.value, {
      host: "db.example.test", username: "test-user", password: "test-password",
    });
    await state.testDatabase();
    assert.equal(state.databaseVerified.value, true);
    assert.equal(requests[0].path, "/api/v1/bootstrap/database/test");
    assert.equal(requests[0].credentials, "same-origin");
    if (driver === "sqlite") {
      assert.deepEqual(requests[0].payload, { driver: "sqlite" });
    } else {
      assert.equal(requests[0].payload.port, driver === "mysql" ? 3306 : 5432);
      assert.equal(requests[0].payload.tls_mode, driver === "mysql" ? "true" : "verify-full");
    }
  });
}

test("数据库输入变化立即作废令牌并忽略旧响应", async (t) => {
  const pending = deferred();
  const { state } = await mountComposable(t, () => useDatabaseSetup(ref(Date.now())), () => pending.promise);
  const task = state.testDatabase();
  state.databaseForm.value.password = "changed-test-password";
  pending.resolve(verified());
  await task;
  assert.equal(state.databaseVerified.value, false);
  assert.equal(state.databaseVerification.value, null);
  assert.equal(state.databaseTesting.value, false);
});

test("数据库旧失败不覆盖新成功，卸载后不接受在途结果", async (t) => {
  const first = deferred();
  const second = deferred();
  const third = deferred();
  const pending = [first, second, third];
  const { state, unmount } = await mountComposable(t, () => useDatabaseSetup(ref(Date.now())), () => pending.shift().promise);
  const oldTask = state.testDatabase();
  const newTask = state.testDatabase();
  second.resolve(verified());
  await newTask;
  first.reject(new Error("旧请求失败"));
  await oldTask;
  assert.equal(state.databaseVerified.value, true);
  assert.equal(state.databaseError.value, "");
  const closingTask = state.testDatabase();
  unmount();
  third.resolve(verified());
  await closingTask;
  assert.equal(state.databaseVerification.value, null);
});

test("数据库令牌到期后不可继续提交", async (t) => {
  const clock = ref(Date.now());
  const { state, advanceTime } = await mountComposable(t, () => useDatabaseSetup(clock), () => verified());
  await state.testDatabase();
  assert.equal(state.databaseVerified.value, true);
  advanceTime(60001);
  clock.value = Date.now();
  assert.equal(state.databaseVerified.value, false);
});

test("拒绝数据库类型不匹配、非法有效期与畸形响应", async (t) => {
  const responses = [
    () => verified("mysql"),
    () => Response.json({ verified: true, driver: "sqlite", verification_token: "test", expires_at: "invalid" }),
    () => Response.json({ verified: "true", driver: "sqlite", verification_token: "test" }),
    () => new Response("not-json", { status: 502 }),
  ];
  const { state } = await mountComposable(t, () => useDatabaseSetup(ref(Date.now())), () => responses.shift()());
  for (let index = 0; index < 4; index += 1) {
    await state.testDatabase();
    assert.equal(state.databaseVerified.value, false);
    assert.notEqual(state.databaseError.value, "");
  }
});
