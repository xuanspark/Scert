<script setup>
import AccessShell from "../components/AccessShell/AccessShell.vue";
import { usePasswordReset } from "./PasswordReset.js";

const {
  identifier,
  resolution,
  message,
  error,
  submitting,
  submit,
  reset,
} = usePasswordReset();
</script>

<template>
  <AccessShell variant="compact" aria-label="Scert 管理员密码重置">
    <template #brand>
      <img class="access-logo" src="/Scert.png" alt="Scert 标志" />
      <p class="access-eyebrow">SCERT 安全中心</p>
      <h1 class="access-brand-title">保护管理后台安全</h1>
      <p class="access-brand-description">
        Scert 会根据账户标识返回安全的密码处理方式，不会在此页面直接接收新密码。
      </p>
    </template>

    <section class="access-card password-reset-card">
      <header class="access-card-header">
        <p class="access-eyebrow">账户安全</p>
        <h2 class="access-card-title">密码重置</h2>
        <p class="access-card-description">
          输入用户名或绑定邮箱，查询该账户可用的重置入口。
        </p>
      </header>

      <form class="access-form" @submit.prevent="submit">
        <label class="access-field">
          <span>管理员账号或邮箱</span>
          <input
            id="reset-identifier"
            name="identifier"
            v-model.trim="identifier"
            type="text"
            autocomplete="username"
            placeholder="请输入用户名或绑定邮箱"
            required
          />
        </label>

        <p v-if="message && !resolution" class="access-message">{{ message }}</p>
        <p v-if="error" class="access-error">{{ error }}</p>

        <div v-if="resolution" class="reset-resolution" aria-live="polite">
          <span class="reset-resolution-label">处理方式</span>
          <strong>{{ resolution.title }}</strong>
          <p>{{ resolution.description }}</p>
          <a
            v-if="resolution.reset_path"
            class="access-submit reset-entry-link"
            :href="resolution.reset_path"
          >
            前往超级管理员重置入口
          </a>
          <button
            v-else
            class="access-secondary"
            type="button"
            @click="reset"
          >
            重新输入账户
          </button>
        </div>

        <button class="access-submit" type="submit" :disabled="submitting">
          {{ submitting ? "正在查询…" : "查询重置方式" }}
        </button>
      </form>

      <a class="access-return" href="/login">返回登录</a>
    </section>
  </AccessShell>
</template>

<style src="./PasswordReset.css"></style>
