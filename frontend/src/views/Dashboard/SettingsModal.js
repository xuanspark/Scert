import { nextTick, ref } from "vue";

const sections = ["service", "acme", "dns", "notification"];

export function useSettingsModal(saveSettings) {
  const activeSection = ref(sections[0]);

  async function submitSettings(event) {
    const invalid = event.currentTarget.querySelector(
      "input:invalid, select:invalid, textarea:invalid",
    );
    if (invalid) {
      const section = invalid.closest("[data-settings-section]");
      if (section) activeSection.value = section.dataset.settingsSection;
      await nextTick();
      invalid.focus();
      invalid.reportValidity();
      return;
    }
    await saveSettings();
  }

  async function navigateTabs(event) {
    const current = sections.indexOf(activeSection.value);
    let next;
    if (event.key === "ArrowRight") next = (current + 1) % sections.length;
    else if (event.key === "ArrowLeft") next = (current + sections.length - 1) % sections.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = sections.length - 1;
    else return;
    event.preventDefault();
    const tablist = event.currentTarget.parentElement;
    activeSection.value = sections[next];
    await nextTick();
    tablist.querySelector(`#settings-tab-${sections[next]}`)?.focus();
  }

  return { activeSection, submitSettings, navigateTabs };
}
