<script setup>
import { useAppShell } from "./App.js";
import Dashboard from "./views/Dashboard/Dashboard.vue";
import Login from "./views/Login/Login.vue";
import PasswordReset from "./views/PasswordReset/PasswordReset.vue";
import SetupWizard from "./views/SetupWizard/SetupWizard.vue";
import SuperAdminReset from "./views/SuperAdminReset/SuperAdminReset.vue";

const {
  setupState,
  bootstrapping,
  error,
  routePath,
  bootstrap,
  handleInitialized,
} = useAppShell();
</script>

<template>
  <Login v-if="routePath === '/login'" />

  <PasswordReset v-else-if="routePath === '/password-reset'" />

  <SuperAdminReset v-else-if="routePath === '/reset'" />

  <main v-else-if="bootstrapping" class="bootstrap-screen">
    <img class="bootstrap-mark" src="/Scert.png" alt="Scert 标志" />
    <strong>正在检查 Scert 初始化状态……</strong>
  </main>

  <main v-else-if="!setupState" class="bootstrap-screen">
    <img class="bootstrap-mark" src="/Scert.png" alt="Scert 标志" />
    <strong>无法连接 Scert 核心服务</strong>
    <p>{{ error || "请检查核心服务是否已经启动。" }}</p>
    <button type="button" @click="bootstrap">重新连接</button>
  </main>

  <SetupWizard
    v-else-if="setupState && !setupState.initialized"
    :status="setupState"
    @initialized="handleInitialized"
  />

  <Dashboard v-else />
</template>
