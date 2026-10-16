import { nextTick, ref } from "vue";

const sections = ["service", "acme", "dns", "notification"];

export function useSettingsModal(saveSettings) {
  const activeSection = ref(sections[0]);

  async function submitSettings(event) {
    const form = event && event.currentTarget ? event.currentTarget : null;
    let invalid = null;
    const currentPanel = form && form.querySelector ? form.querySelector(`[data-settings-section="${activeSection.value}"]`) : null;
    if (currentPanel && typeof currentPanel.querySelector === "function") {
      invalid = currentPanel.querySelector("input:invalid, select:invalid, textarea:invalid");
    } else if (form && typeof form.querySelector === "function") {
      invalid = form.querySelector("input:invalid, select:invalid, textarea:invalid");
    }

    if (invalid) {
      const section = invalid.closest && invalid.closest("[data-settings-section]");
      if (section && section.dataset && section.dataset.settingsSection) {
        activeSection.value = section.dataset.settingsSection;
      }
      await nextTick();
      if (invalid.focus) invalid.focus();
      if (invalid.reportValidity) invalid.reportValidity();
      return;
    }
    await saveSettings(activeSection.value);
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
    const targetTab = tablist && tablist.querySelector ? tablist.querySelector('#settings-tab-' + sections[next]) : null;
    if (targetTab && targetTab.focus) targetTab.focus();
  }

  return { activeSection, submitSettings, navigateTabs };
}
