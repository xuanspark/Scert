import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { parse, compileScript, compileTemplate } from "@vue/compiler-sfc";
import { useSettingsModal } from "./SettingsModal.js";
import { settingsPayload } from "./SettingsPayload.js";

const source = readFileSync(new URL("./SettingsModal.vue", import.meta.url), "utf8");

test("邮件通知组件保留两种通道且公共字段只出现一次", () => {
  const mailSource = readFileSync(new URL("./MailNotificationSettings.vue", import.meta.url), "utf8");
  const { descriptor, errors } = parse(mailSource);
  assert.deepEqual(errors, []);
  const script = compileScript(descriptor, { id: "mail-settings-test" });
  const compiled = compileTemplate({
    source: descriptor.template.content,
    filename: "MailNotificationSettings.vue",
    id: "mail-settings-test",
    compilerOptions: { bindingMetadata: script.bindings },
  });
  assert.deepEqual(compiled.errors, []);
  for (const field of ["http_enabled", "smtp_enabled", "sender_email", "smtp_password"]) {
    assert.equal(mailSource.split(`v-model="notification.${field}"`).length - 1, 1);
  }
  assert.equal(mailSource.split(':value="notification.recipients.join').length - 1, 1);
  assert.match(source, /<MailNotificationSettings :notification="settingsForm.notification"/);
  assert.doesNotMatch(mailSource, /role="tab"/);
});

test("合并通知页面不改变通道开关和已保存配置", () => {
  for (const http of [false, true]) {
    for (const smtp of [false, true]) {
      const notification = {
        http_enabled: http,
        smtp_enabled: smtp,
        endpoint: "https://mail.example.com",
        sender_email: "sender@example.com",
        recipients: ["recipient@example.com"],
        smtp_host: "smtp.example.com",
        smtp_port: 587,
        smtp_username: "sender",
        smtp_password: "",
        smtp_mode: "starttls",
        smtp_credential_configured: true,
      };
      const payload = settingsPayload({ notification }).notification;
      const { smtp_credential_configured, ...expected } = notification;
      assert.deepEqual(payload, expected);
      assert.equal(notification.smtp_credential_configured, true);
    }
  }
});

test("通知字段校验失败时切换到合并后的邮件通知页", async () => {
  let saved = false;
  const state = useSettingsModal(() => { saved = true; });
  const sections = [];
  const invalid = {
    closest: () => ({ dataset: { settingsSection: "notification" } }),
    focus: () => sections.push(state.activeSection.value),
    reportValidity: () => sections.push(state.activeSection.value),
  };
  await state.submitSettings({ currentTarget: { querySelector: () => invalid } });
  assert.equal(saved, false);
  assert.deepEqual(sections, ["notification", "notification"]);
});

test("settings.componentCompilesWithFourAccessiblePanels", () => {
  const { descriptor, errors } = parse(source);
  assert.deepEqual(errors, []);
  const script = compileScript(descriptor, { id: "settings-test" });
  assert.ok(script.bindings.saveSettings);
  const result = compileTemplate({
    source: descriptor.template.content,
    filename: "SettingsModal.vue",
    id: "settings-test",
    compilerOptions: { bindingMetadata: script.bindings },
  });
  assert.deepEqual(result.errors, []);
  assert.equal((source.match(/role="tab"/g) || []).length, 4);
  assert.equal((source.match(/role="tabpanel"/g) || []).length, 4);
  assert.match(source, /id="settings-tab-notification"/);
  assert.doesNotMatch(source, /settings-tab-(http|smtp)/);
  assert.match(source, /:required="field.required && dnsVerificationRequired"/);
  assert.match(source, /<form novalidate @submit.prevent="submitSettings">/);
  assert.ok(!source.includes('<p class="field-hint"'));
});

test("settings.invalidHiddenFieldOpensItsPanelBeforeReporting", async () => {
  let saved = false;
  const calls = [];
  const state = useSettingsModal(() => { saved = true; });
  const invalid = {
    closest: () => ({ dataset: { settingsSection: "acme" } }),
    focus: () => calls.push(["focus", state.activeSection.value]),
    reportValidity: () => calls.push(["report", state.activeSection.value]),
  };
  await state.submitSettings({ currentTarget: { querySelector: () => invalid } });
  assert.equal(saved, false);
  assert.deepEqual(calls, [["focus", "acme"], ["report", "acme"]]);
});

test("settings.validFormUsesExistingSaveFlow", async () => {
  let count = 0;
  const state = useSettingsModal(async () => { count += 1; });
  await state.submitSettings({ currentTarget: { querySelector: () => null } });
  assert.equal(count, 1);
});

test("settings.keyboardNavigationWrapsAndFocusesTabs", async () => {
  const state = useSettingsModal(() => {});
  const focused = [];
  const event = {
    key: "ArrowLeft",
    preventDefault() {},
    currentTarget: { parentElement: {
      querySelector: (selector) => ({ focus: () => focused.push(selector) }),
    } },
  };
  await state.navigateTabs(event);
  assert.equal(state.activeSection.value, "notification");
  assert.deepEqual(focused, ["#settings-tab-notification"]);
  await state.navigateTabs({ ...event, key: "Home" });
  assert.equal(state.activeSection.value, "service");
});
