import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import * as Vue from "vue";
import { parse, compileTemplate } from "@vue/compiler-sfc";
import { renderToString } from "@vue/server-renderer";

test("域名表单使用复选框同时显示两台已关联服务器并提供复用提示", async () => {
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
      servers: [
        { id: "server-one", name: "共享服务器一", address: "192.0.2.1", web_server: "nginx" },
        { id: "server-two", name: "共享服务器二", address: "192.0.2.2", web_server: "nginx" },
      ],
      displayLabel: (_, value) => value,
      webServerLabels: {},
      formError: "",
      saving: false,
    }),
  });
  app.component("HelpTip", {
    setup: (_, { slots }) => () => Vue.h("span", { class: "help-tip" }, slots.default?.()),
  });
  app.config.warnHandler = (message) => warnings.push(message);
  const html = await renderToString(app);
  assert.deepEqual(warnings, []);
  const inputs = html.match(/<input\b[^>]*>/g) || [];
  for (const id of ["server-one", "server-two"]) {
    const input = inputs.find((value) => value.includes(`value="${id}"`));
    assert.ok(input);
    assert.match(input, /type="checkbox"/);
    assert.match(input, /\bchecked(?:[\s=>])/);
  }
  assert.match(html, /同一服务器可关联多个域名/);
  assert.match(html, /不影响其他域名/);
});
