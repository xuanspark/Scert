<template>
  <main class="access-denied-page">
    <section class="access-denied-card" role="status" aria-live="polite">
      <img class="access-denied-mark" src="/Scert.png" alt="Scert 标志" />
      <p class="eyebrow">SCERT / 访问控制</p>
      <h1>当前账号没有管理权限</h1>
      <p class="access-denied-description">
        你已经登录成功，但当前账号只能使用普通用户功能，不能访问证书、服务器和系统设置管理页面。
      </p>
      <p v-if="error" class="access-denied-error">{{ error }}</p>
      <button class="primary-button" type="button" :disabled="loggingOut" @click="logout">
        {{ loggingOut ? "正在退出……" : "退出并切换账号" }}
      </button>
    </section>
  </main>
</template>

<script setup>
import { useAccessDenied } from "./AccessDenied.js";

const { loggingOut, error, logout } = useAccessDenied();
</script>

<style scoped>
@import "./AccessDenied.css";
</style>
