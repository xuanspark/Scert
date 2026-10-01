import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { baseParse } from "@vue/compiler-dom";
import { parse } from "@vue/compiler-sfc";
import { usePasswordReset } from "./PasswordReset.js";
import { mountComposable } from "../../test-support/mount.js";

function template(path) {
  const source = readFileSync(new URL(path, import.meta.url), "utf8");
  return baseParse(parse(source).descriptor.template.content);
}

function elements(node, predicate, matches = []) {
  if (predicate(node)) matches.push(node);
  for (const child of node.children || []) elements(child, predicate, matches);
  return matches;
}

test("超级管理员查询保留本机处理标识，不生成网页重置链接", async (t) => {
  const payload = {
    account_type: "super_admin",
    action: "contact_administrator",
    title: "超级管理员密码重置",
    description: "请在部署主机操作。",
  };
  const { state } = await mountComposable(t, usePasswordReset, async (url, options) => {
    assert.equal(url, "/api/v1/password-reset/resolve");
    assert.equal(options.credentials, "same-origin");
    assert.deepEqual(JSON.parse(options.body), { identifier: "owner" });
    return Response.json(payload);
  });
  state.identifier.value = " owner ";
  await state.submit();
  assert.equal(state.resolution.value.action, "contact_administrator");
  assert.equal("reset_path" in state.resolution.value, false);
  assert.doesNotMatch(JSON.stringify(state.resolution.value), /local_admin_command|systemctl|scert-admin/iu);
  assert.equal(state.error.value, "");
  assert.equal(state.submitting.value, false);
  state.reset();
  assert.equal(state.resolution.value, null);
});

test("密码重置页面不显示本机管理命令", () => {
  const root = template("./PasswordReset.vue");
  const guides = elements(root, (node) => node.tag === "LocalResetGuide");
  assert.equal(guides.length, 0);
  const source = readFileSync(new URL("./PasswordReset.vue", import.meta.url), "utf8");
  assert.doesNotMatch(source, /systemctl|scert-admin|本机管理命令/iu);
  const message = elements(root, (node) => node.tag === "p"
    && node.props.some((prop) => prop.name === "if"
      && prop.exp?.content === "message && !resolution"));
  assert.equal(message.length, 1);
});

test("本机指引不包含可执行命令", () => {
  const root = template("../components/LocalResetGuide/LocalResetGuide.vue");
  assert.equal(elements(root, (node) => ["a", "input", "form"].includes(node.tag)).length, 0);
  assert.equal(elements(root, (node) => node.tag === "HelpTip").length, 1);
  assert.equal(elements(root, (node) => node.tag === "code").length, 0);
  const source = readFileSync(new URL("../components/LocalResetGuide/LocalResetGuide.vue", import.meta.url), "utf8");
  assert.doesNotMatch(source, /systemctl|scert-admin|<code/iu);
});
