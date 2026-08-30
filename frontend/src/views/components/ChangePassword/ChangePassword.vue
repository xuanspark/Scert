<script setup>
import { onBeforeUnmount, ref } from "vue";

const visible = ref(false);
const submitting = ref(false);
const success = ref(false);
const error = ref("");
const notice = ref("");
const form = ref({
  currentPassword: "",
  newPassword: "",
  confirmPassword: "",
});
let redirectTimer = 0;

function open() {
  visible.value = true;
  success.value = false;
  error.value = "";
  notice.value = "";
}

function close() {
  if (submitting.value || success.value) {
    return;
  }
  visible.value = false;
  error.value = "";
  notice.value = "";
  form.value = {
    currentPassword: "",
    newPassword: "",
    confirmPassword: "",
  };
}

async function submit() {
  if (submitting.value) {
    return;
  }
  error.value = "";
  notice.value = "";
  if (form.value.newPassword !== form.value.confirmPassword) {
    error.value = "两次输入的新密码不一致。";
    return;
  }
  submitting.value = true;
  try {
    const response = await fetch("/api/v1/auth/password", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
      },
      credentials: "include",
      body: JSON.stringify({
        current_password: form.value.currentPassword,
        new_password: form.value.newPassword,
        confirm_password: form.value.confirmPassword,
      }),
    });
    const result = await response.json().catch(() => ({}));
    if (!response.ok) {
      throw new Error(result.error || "修改密码失败，请稍后重试。");
    }
    success.value = true;
    notice.value = result.message || "密码修改成功，请重新登录。";
    redirectTimer = window.setTimeout(() => {
      window.location.href = "/login";
    }, 1600);
  } catch (submitError) {
    error.value = submitError.message || "修改密码失败，请稍后重试。";
  } finally {
    submitting.value = false;
  }
}

onBeforeUnmount(() => {
  if (redirectTimer) {
    window.clearTimeout(redirectTimer);
  }
});
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
          <span>新密码 <small>8 到 12 个字符，需同时包含字母和数字</small></span>
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

<style src="./ChangePassword.css"></style>
