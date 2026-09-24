<script setup>
import HelpTip from "../HelpTip/HelpTip.vue";
import { useChangePassword } from "./ChangePassword.js";

const {
  visible,
  submitting,
  success,
  error,
  notice,
  form,
  open,
  close,
  submit,
} = useChangePassword();
</script>

<template>
  <button class="secondary-button" type="button" @click="open">
    修改密码
  </button>

  <div v-if="visible" class="modal-backdrop" @click.self="close">
    <section
      class="modal-card change-password-modal"
      role="dialog"
      aria-modal="true"
      aria-labelledby="change-password-title"
    >
      <div class="modal-heading">
        <div>
          <p class="eyebrow">账户安全</p>
          <h2 id="change-password-title">修改登录密码</h2>
        </div>
        <button
          class="close-button"
          type="button"
          aria-label="关闭"
          :disabled="submitting || success"
          @click="close"
        >
          ×
        </button>
      </div>

      <form class="change-password-form" @submit.prevent="submit">
        <label class="field">
          <span>当前密码</span>
          <input
            v-model="form.currentPassword"
            type="password"
            autocomplete="current-password"
            placeholder="请输入当前密码"
            :disabled="submitting || success"
            required
          />
        </label>
        <label class="field">
          <span>新密码 <HelpTip>8 到 12 个字符，需同时包含字母和数字</HelpTip></span>
          <input
            v-model="form.newPassword"
            type="password"
            autocomplete="new-password"
            minlength="8"
            maxlength="12"
            placeholder="请输入新密码"
            :disabled="submitting || success"
            required
          />
        </label>
        <label class="field">
          <span>确认新密码</span>
          <input
            v-model="form.confirmPassword"
            type="password"
            autocomplete="new-password"
            minlength="8"
            maxlength="12"
            placeholder="请再次输入新密码"
            :disabled="submitting || success"
            required
          />
        </label>
        <p v-if="notice" class="change-password-notice">{{ notice }}</p>
        <p v-if="error" class="change-password-error">{{ error }}</p>
        <div class="modal-actions">
          <button
            class="secondary-button"
            type="button"
            :disabled="submitting || success"
            @click="close"
          >
            取消
          </button>
          <button class="primary-button" type="submit" :disabled="submitting || success">
            {{ submitting ? "正在保存" : success ? "已完成" : "保存新密码" }}
          </button>
        </div>
      </form>
    </section>
  </div>
</template>

<style scoped src="../../Dashboard/Dashboard.css"></style>
<style scoped src="./ChangePassword.css"></style>
