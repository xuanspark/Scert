<script setup>
import AccessShell from "../components/AccessShell/AccessShell.vue";
import { useLogin } from "./Login.js";

const {
  identifier,
  password,
  remember,
  submitting,
  error,
  stage,
  accountName,
  identifierLabel,
  identifierPlaceholder,
  stageTitle,
  stageMessage,
  resetStage,
  submit,
} = useLogin();
</script>

<template>
  <AccessShell variant="compact" aria-label="Scert 用户登录">
    <template #brand>
      <img class="access-logo" src="/Scert.png" alt="Scert 标志" />
      <p class="access-eyebrow">SCERT 管理后台</p>
      <h1 class="access-brand-title">统一证书管理服务</h1>
      <p class="access-brand-description">
        集中管理域名证书、自动续期，并安全部署到关联服务器。
      </p>
    </template>

    <section class="access-card login-card">
      <header class="access-card-header">
        <p class="access-eyebrow">安全访问</p>
        <h2 class="access-card-title">用户登录</h2>
        <p class="access-card-description">请输入用户账号，进入 Scert 管理后台。</p>
      </header>

      <section v-if="stage === 'credentials'">
        <form class="access-form" @submit.prevent="submit">
          <label class="access-field">
            <span>{{ identifierLabel }}</span>
            <input
              id="login-identifier"
              name="identifier"
              v-model.trim="identifier"
              type="text"
              autocomplete="username"
              :placeholder="identifierPlaceholder"
              required
            />
          </label>

          <label class="access-field">
            <span>密码</span>
            <input
              id="login-password"
              name="password"
              v-model="password"
              type="password"
              autocomplete="current-password"
              placeholder="请输入用户密码"
              required
            />
          </label>

          <div class="access-options">
            <label class="access-check">
              <input
                id="remember-login"
                name="remember"
                v-model="remember"
                type="checkbox"
              />
              <span>记住登录状态</span>
            </label>
            <a class="access-link" href="/password-reset">忘记密码</a>
          </div>

          <p v-if="error" class="access-error">{{ error }}</p>
          <button class="access-submit" type="submit" :disabled="submitting">
            {{ submitting ? "正在登录……" : "登录" }}
          </button>
        </form>
      </section>

      <section v-else class="login-next-step" aria-live="polite">
        <p class="login-account">登录账户：{{ accountName }}</p>
        <p class="login-stage-title">{{ stageTitle }}</p>
        <p v-if="stage === 'complete'" class="login-success">{{ stageMessage }}</p>
        <p v-else class="access-error">{{ stageMessage }}</p>
        <button class="access-submit" type="button" @click="resetStage">返回登录</button>
      </section>
    </section>
  </AccessShell>
</template>

<style src="./Login.css"></style>
