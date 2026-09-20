<script setup>
import HelpTip from "../HelpTip/HelpTip.vue";
</script>

<template>
  <section class="local-reset-guide">
    <div class="local-reset-guide__heading">
      <span>本机管理命令</span>
      <HelpTip>
        请由部署管理员在 Scert 所在主机执行命令。Linux 使用 root 终端；
        Windows 使用提升权限的控制台，在安装目录运行。
        验证码和新密码仅在本机终端输入，本页面不会开启重置会话。
      </HelpTip>
    </div>
    <dl class="local-reset-guide__commands">
      <div>
        <dt>Linux</dt>
        <dd><code>systemctl start Scert-admin</code></dd>
      </div>
      <div>
        <dt>Windows</dt>
        <dd><code>.\scert-admin.exe</code></dd>
      </div>
    </dl>
  </section>
</template>

<style scoped src="./LocalResetGuide.css"></style>