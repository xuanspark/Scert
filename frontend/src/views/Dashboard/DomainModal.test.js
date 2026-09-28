import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import * as Vue from "vue";
import { parse, compileTemplate } from "@vue/compiler-sfc";
import { renderToString } from "@vue/server-renderer";

async function renderModal(overrides = {}) {
  const source = await readFile(new URL("./DomainModal.vue", import.meta.url), "utf8");
  const { descriptor, errors } = parse(source);
  assert.deepEqual(errors, []);
  const compiled = compileTemplate({
    source: descriptor.template.content,
    filename: "DomainModal.vue",
    id: "domain-many-to-many",
    compilerOptions: { mode: "function" },
  });
  assert.deepEqual(compiled.errors, []);
  const render = new Function("Vue", compiled.code)(Vue);
  const warnings = [];
  const app = Vue.createSSRApp({
    render,
    setup: () => ({
      showForm: true,
      formTitle: "编辑域名",
      closeForm() {},
      saveDomain() {},
      form: {
        name: "example.test",
        certificate_type: "single",
        sans: "",
        auto_renew: false,
        acme_environment: "staging",
        dns_provider: "",
        server_ids: ["server-one", "server-two"],
      },
      dnsProviders: [],
      domainServerOptions: [
        { id: "server-one", name: "共享服务器一", address: "192.0.2.1", web_server: "nginx" },
        { id: "server-two", name: "共享服务器二", address: "192.0.2.2", web_server: "nginx" },
      ],
      displayLabel: (_, value) => value,
      webServerLabels: {},
      formError: "",
      saving: false,
      domainBindingsLoading: false,
      domainBindingsReady: true,
      domainSaveUncertain: false,
      reloadDomainBindings() {},
      ...overrides,
    }),
  });
  app.component("HelpTip", {
    setup: (_, { slots }) => () => Vue.h("span", { class: "help-tip" }, slots.default?.()),
  });
  app.config.warnHandler = (message) => warnings.push(message);
  const html = await renderToString(app);
  assert.deepEqual(warnings, []);
  return html;
}

test("域名表单使用复选框同时显示两台已关联服务器并提供复用提示", async () => {
  const html = await renderModal();
  const inputs = html.match(/<input\b[^>]*>/g) || [];
  for (const id of ["server-one", "server-two"]) {
    const input = inputs.find((value) => value.includes(`value="${id}"`));
    assert.ok(input);
    assert.match(input, /type="checkbox"/);
    assert.match(input, /\bchecked(?:[\s=>])/);
  }
  assert.match(html, /同一服务器可关联多个域名/);
  assert.match(html, /不影响其他域名/);
  assert.match(html, /当前域名所属项目/);
});

test("关联读取失败或结果未确认时禁用提交，读取失败提供重试入口", async () => {
  for (const overrides of [
    { domainBindingsReady: false },
    { domainBindingsLoading: true },
    { domainSaveUncertain: true },
    { saving: true },
  ]) {
    const html = await renderModal(overrides);
    const submit = (html.match(/<button\b[^>]*>/g) || []).find((tag) => tag.includes('type="submit"'));
    assert.ok(submit);
    assert.match(submit, /\bdisabled(?:[\s=>])/);
    const picker = (html.match(/<fieldset\b[^>]*>/g) || [])[0];
    assert.match(picker, /\bdisabled(?:[\s=>])/);
    if (overrides.domainBindingsReady === false) assert.match(html, /重新读取关联/);
    if (overrides.domainBindingsLoading) assert.match(html, /正在读取服务器关联/);
  }
});
