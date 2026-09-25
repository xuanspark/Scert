<script setup>
import UserManagement from "./UserManagement.vue";
import { useAccessDenied } from "../AccessDenied/AccessDenied.js";

const { loggingOut, error, logout } = useAccessDenied();
</script>

<template>
  <main class="user-workspace">
    <header>
      <h1>Scert 用户中心</h1>
      <button type="button" :disabled="loggingOut" @click="logout">
        {{ loggingOut ? "正在退出……" : "退出登录" }}
      </button>
    </header>
    <p v-if="error" role="alert">{{ error }}</p>
    <UserManagement />
  </main>
</template>

<style scoped src="./UserWorkspace.css"></style>
