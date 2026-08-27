<script setup>
import AccessShell from "../components/AccessShell/AccessShell.vue";
import { usePasswordReset } from "./PasswordReset.js";

const {
  username,
  password,
  confirmPassword,
  message,
  error,
  submit,
} = usePasswordReset();
</script>

<template>
  <AccessShell variant="compact" aria-label="Scert 管理员密码重置">
    <template #brand>
      <img class="access-logo" src="/Scert.png" alt="Scert 标志" />
      <p class="access-eyebrow">SCERT 安全中心</p>
      <h1 class="access-brand-title">保护管理后台安全</h1>
      <p class="access-brand-description">
        密码重置入口仅用于已接入认证流程的管理员账号。
      </p>
    </template>

    <section class="access-card password-reset-card">
      <header class="access-card-header">
        <p class="access-eyebrow">账户安全</p>
        <h2 class="access-card-title">密码重置</h2>
        <p class="access-card-description">填写账号和新密码，提交密码重置申请。</p>
      </header>

      <form class="access-form" @submit.prevent="submit">
        <label class="access-field">
          <span>管理员账号</span>
          <input
            id="reset-username"
            name="username"
            v-model.trim="username"
            type="text"
            autocomplete="username"
            placeholder="请输入管理员账号"
            required
          />
        </label>

        <label class="access-field">
          <span>新密码</span>
          <input
            id="reset-password"
            name="password"
            v-model="password"
            type="password"
            autocomplete="new-password"
            placeholder="至少 8 个字符"
            minlength="8"
            required
          />
        </label>

        <label class="access-field">
          <span>确认新密码</span>
          <input
            id="reset-confirm-password"
            name="confirm_password"
            v-model="confirmPassword"
            type="password"
            autocomplete="new-password"
            placeholder="请再次输入新密码"
            minlength="8"
            required
          />
        </label>

        <p v-if="message" class="access-message">{{ message }}</p>
        <p v-if="error" class="access-error">{{ error }}</p>
        <button class="access-submit" type="submit">提交重置申请</button>
      </form>

      <a class="access-return" href="/login">返回登录</a>
    </section>
  </AccessShell>
</template>

<style src="./PasswordReset.css"></style>
