import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { baseParse } from "@vue/compiler-dom";
import { parse } from "@vue/compiler-sfc";
function template(path) {
  const source = readFileSync(new URL(path, import.meta.url), "utf8");
  return baseParse(parse(source).descriptor.template.content);
}

function elements(node, predicate, matches = []) {
  if (predicate(node)) matches.push(node);
  for (const child of node.children || []) elements(child, predicate, matches);
  return matches;
}

test("公开密码重置页面只显示安全阻断提示", () => {
  const root = template("./PasswordReset.vue");
  const source = readFileSync(new URL("./PasswordReset.vue", import.meta.url), "utf8");
  assert.match(source, /当前方式无法使用/);
  assert.match(source, /请联系运维人员辅助解决/);
  assert.doesNotMatch(source, /fetch\(|password-reset\/resolve|systemctl|scert-admin/iu);
  assert.equal(elements(root, (node) => node.tag === "form").length, 0);
  assert.equal(elements(root, (node) => node.tag === "input").length, 0);
  assert.equal(elements(root, (node) => node.tag === "button").length, 0);
});
