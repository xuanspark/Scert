<script setup>
import UserManagement from "./UserManagement.vue";
import ProjectManagement from "../ProjectManagement/ProjectManagement.vue";
import { useUserWorkspace } from "./UserWorkspace.js";

const { loggingOut, error, logout, section } = useUserWorkspace();
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
    <nav class="user-workspace-navigation" aria-label="用户中心导航">
      <button type="button" :aria-current="section === 'account' ? 'page' : undefined" @click="section = 'account'">账户与密码</button>
      <button type="button" :aria-current="section === 'projects' ? 'page' : undefined" @click="section = 'projects'">项目与成员</button>
    </nav>
    <UserManagement v-if="section === 'account'" />
    <ProjectManagement v-else-if="section === 'projects'" />
  </main>
</template>

<style scoped src="./UserWorkspace.css"></style>
