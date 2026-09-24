import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { parse, compileScript, compileTemplate } from "@vue/compiler-sfc";
import { useSettingsModal } from "./SettingsModal.js";

const source = readFileSync(new URL("./SettingsModal.vue", import.meta.url), "utf8");

test("settings.componentCompilesWithFiveAccessiblePanels", () => {
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
  assert.equal((source.match(/role="tab"/g) || []).length, 5);
  assert.equal((source.match(/role="tabpanel"/g) || []).length, 5);
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
  assert.equal(state.activeSection.value, "smtp");
  assert.deepEqual(focused, ["#settings-tab-smtp"]);
  await state.navigateTabs({ ...event, key: "Home" });
  assert.equal(state.activeSection.value, "service");
});
