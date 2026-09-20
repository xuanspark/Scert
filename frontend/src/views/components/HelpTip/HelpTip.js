import { nextTick, onBeforeUnmount, onMounted, ref, useId } from "vue";
import { placeTooltip } from "./HelpTipPosition.js";

export function useHelpTip() {
  const trigger = ref(null);
  const tooltip = ref(null);
  const visible = ref(false);
  const position = ref({ left: "0px", top: "0px" });
  const tooltipId = `help-tip-${useId()}`;
  let timer;

  function cancelHide() {
    clearTimeout(timer);
  }

  function hide() {
    cancelHide();
    visible.value = false;
  }

  async function show() {
    cancelHide();
    visible.value = true;
    await nextTick();
    if (!visible.value || !trigger.value || !tooltip.value) return;
    const point = placeTooltip(
      trigger.value.getBoundingClientRect(),
      tooltip.value.getBoundingClientRect(),
      { width: window.innerWidth, height: window.innerHeight },
    );
    position.value = { left: `${point.left}px`, top: `${point.top}px` };
  }

  function scheduleHide() {
    cancelHide();
    timer = setTimeout(() => {
      if (document.activeElement !== trigger.value) hide();
    }, 140);
  }

  function toggle() {
    if (visible.value) hide();
    else show();
  }

  function dismissOnEscape(event) {
    if (event.key === "Escape") hide();
  }

  function dismissOutside(event) {
    if (!trigger.value?.contains(event.target) && !tooltip.value?.contains(event.target)) hide();
  }

  function dismissOnScroll(event) {
    if (!tooltip.value?.contains(event.target)) hide();
  }

  onMounted(() => {
    document.addEventListener("keydown", dismissOnEscape);
    document.addEventListener("pointerdown", dismissOutside);
    window.addEventListener("resize", hide);
    window.addEventListener("scroll", dismissOnScroll, true);
  });

  onBeforeUnmount(() => {
    hide();
    document.removeEventListener("keydown", dismissOnEscape);
    document.removeEventListener("pointerdown", dismissOutside);
    window.removeEventListener("resize", hide);
    window.removeEventListener("scroll", dismissOnScroll, true);
  });

  return {
    trigger, tooltip, tooltipId, visible, position,
    show, hide, scheduleHide, cancelHide, toggle,
  };
}
