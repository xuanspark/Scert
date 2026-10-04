<script setup>
import AccessShell from "../components/AccessShell/AccessShell.vue";
</script>

<template>
  <AccessShell variant="compact" aria-label="Scert 密码重置">
    <template #brand>
      <img class="access-logo" src="/Scert.png" alt="Scert 标志" />
      <p class="access-eyebrow">SCERT 安全边界</p>
      <h1 class="access-brand-title">密码重置不可用</h1>
      <p class="access-brand-description">
        超级管理员密码重置不通过普通 Web 页面执行。
      </p>
    </template>

    <section class="access-card password-reset-card">
      <header class="access-card-header">
        <p class="access-eyebrow">安全边界</p>
        <h2 class="access-card-title">当前方式无法使用</h2>
        <p class="access-card-description">
          请联系运维人员辅助解决。
        </p>
      </header>

      <div class="reset-blocked-message" role="status">
        <strong>超级管理员密码重置只能通过部署主机上的本机受保护终端执行。</strong>
        <p>
          当前页面不会读取账户、发送验证码、接收新密码或创建公网重置会话。
        </p>
      </div>

      <a class="access-return" href="/login">返回登录</a>
    </section>
  </AccessShell>
</template>

<style src="./PasswordReset.css"></style>
