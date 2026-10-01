<script setup>
import HelpTip from "../HelpTip/HelpTip.vue";
</script>

<template>
  <section class="local-reset-guide">
    <div class="local-reset-guide__heading">
      <span>本机安全维护</span>
      <HelpTip>
        请联系部署管理员按照服务器本机安全维护流程处理。
        页面不会显示或接收维护命令、验证码和新密码。
      </HelpTip>
    </div>
    <p class="local-reset-guide__message">
      该账户需要由系统管理员在服务器本机完成安全维护流程。
    </p>
  </section>
</template>

<style scoped src="./LocalResetGuide.css"></style>
