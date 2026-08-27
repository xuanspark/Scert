<script setup>
import { accessShellProps } from "./AccessShell.js";

defineProps(accessShellProps);
</script>

<template>
  <main
    class="access-shell"
    :class="`access-shell--${variant}`"
    :aria-label="ariaLabel"
  >
    <aside class="access-brand">
      <div class="access-brand-content">
        <slot name="brand">
          <img class="access-logo" src="/Scert.png" alt="Scert 标志" />
          <p class="access-eyebrow">SCERT</p>
          <h1 class="access-brand-title">统一证书管理服务</h1>
          <p class="access-brand-description">
            安全管理证书申请、续期、部署与状态校验。
          </p>
        </slot>
      </div>
    </aside>

    <section class="access-panel">
      <div class="access-panel-inner">
        <slot></slot>
      </div>
    </section>
  </main>
</template>

<style src="./AccessShell.css"></style>
