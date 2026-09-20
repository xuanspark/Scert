<script setup>
import { useHelpTip } from "./HelpTip.js";

const {
  trigger,
  tooltip,
  tooltipId,
  visible,
  position,
  show,
  hide,
  scheduleHide,
  cancelHide,
  toggle,
} = useHelpTip();
</script>

<template>
  <span
    ref="trigger"
    class="help-tip-trigger"
    role="button"
    tabindex="0"
    :aria-labelledby="tooltipId"
    :aria-expanded="visible"
    @pointerenter="show"
    @pointerleave="scheduleHide"
    @focus="show"
    @blur="hide"
    @click.stop.prevent="toggle"
    @keydown.enter.stop.prevent="toggle"
    @keydown.space.stop.prevent="toggle"
    @keydown.esc.stop.prevent="hide"
  >
    <span aria-hidden="true">?</span>
  </span>
  <Teleport to="body">
    <span
      v-show="visible"
      :id="tooltipId"
      ref="tooltip"
      class="help-tip-content"
      role="tooltip"
      :style="position"
      @pointerenter="cancelHide"
      @pointerleave="scheduleHide"
    >
      <slot />
    </span>
  </Teleport>
</template>

<style scoped src="./HelpTip.css"></style>
