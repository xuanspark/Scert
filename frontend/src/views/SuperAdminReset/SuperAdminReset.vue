<script setup>
import AccessShell from "../components/AccessShell/AccessShell.vue";
import { useSuperAdminReset } from "./SuperAdminReset.js";

const {
  verificationCode,
  password,
  confirmPassword,
  message,
  error,
  loading,
  submitting,
  success,
  redirectSeconds,
  submit,
  loadSession,
} = useSuperAdminReset();
</script>

<template>
  <AccessShell variant="compact" aria-label="Scert 超级管理员密码重置">
    <template #brand>
      <img class="access-logo" src="/Scert.png" alt="Scert 标志" />
      <p class="access-eyebrow">SCERT-ADMIN 安全入口</p>
      <h1 class="access-brand-title">超级管理员密码重置</h1>
      <p class="access-brand-description">
        此入口仅配合部署主机上的临时管理服务使用，不提供公开常驻访问。
      </p>
    </template>

    <section class="access-card reset-card">
      <header class="access-card-header">
        <p class="access-eyebrow">临时授权</p>
        <h2 class="access-card-title">确认安全重置</h2>
        <p class="access-card-description">
          请输入一次性校验码和新的超级管理员密码。
        </p>
      </header>

      <form class="access-form" @submit.prevent="submit">
        <label class="access-field">
          <span>一次性校验码</span>
          <input
            id="verification-code"
            name="verification_code"
            v-model.trim="verificationCode"
            type="text"
            autocomplete="one-time-code"
            placeholder="请输入一次性校验码"
            :disabled="loading || submitting || success"
            required
          />
        </label>

        <label class="access-field">
          <span>新密码</span>
          <input
            id="super-admin-password"
            name="password"
            v-model="password"
            type="password"
            autocomplete="new-password"
            placeholder="8-12 个字符"
            minlength="8"
            maxlength="12"
            :disabled="loading || submitting || success"
            required
          />
        </label>

        <label class="access-field">
          <span>确认新密码</span>
          <input
            id="super-admin-confirm-password"
            name="confirm_password"
            v-model="confirmPassword"
            type="password"
            autocomplete="new-password"
            placeholder="请再次输入新密码"
            minlength="8"
            maxlength="12"
            :disabled="loading || submitting || success"
            required
          />
        </label>

        <p v-if="loading" class="access-message">正在验证本次重置入口……</p>
        <p v-if="message" class="access-message">{{ message }}</p>
        <p v-if="error" class="access-error">{{ error }}</p>
        <button
          class="access-submit"
          type="submit"
          :disabled="loading || submitting || success"
        >
          {{ submitting ? "正在提交" : "确认重置" }}
        </button>
      </form>

      <p class="access-note">
        本页面仅在 Scert-admin 临时启用后使用。提交成功后，本次授权会立即销毁且不可再次使用。
      </p>
      <a class="access-return" href="/login">返回登录</a>
    </section>

    <div v-if="success" class="reset-success-overlay" role="dialog" aria-modal="true">
      <div class="reset-success-dialog">
        <div class="reset-success-icon" aria-hidden="true">✓</div>
        <h2>密码重置成功</h2>
        <p>本次重置授权已销毁。</p>
        <p>{{ redirectSeconds }} 秒后返回登录页面。</p>
      </div>
    </div>
  </AccessShell>
</template>

<style src="./SuperAdminReset.css"></style>
