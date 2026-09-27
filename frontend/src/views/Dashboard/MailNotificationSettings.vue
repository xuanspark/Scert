<script setup>
import HelpTip from "../components/HelpTip/HelpTip.vue";

defineProps({ notification: { type: Object, required: true } });
</script>

<template>
  <div class="mail-notification-settings">
    <fieldset class="mail-channel">
      <legend>SMTP 服务</legend>
      <label class="mail-switch">
        <input v-model="notification.smtp_enabled" type="checkbox" />
        <span>启用 SMTP 发送</span>
        <HelpTip>默认使用 Smail 系统通知账户发信，发件人地址由系统自动生成。</HelpTip>
      </label>
      <div class="mail-smtp-fields">
          <label class="mail-field">
            <span>SMTP 主机</span>
            <input v-model="notification.smtp_host" placeholder="例如 smtp.example.com" />
          </label>
          <label class="mail-field">
            <span>SMTP 端口</span>
            <input v-model.number="notification.smtp_port" type="number" min="1" max="65535" />
          </label>
          <label class="mail-field">
            <span>SMTP 用户名</span>
            <input v-model="notification.smtp_username" autocomplete="username" />
          </label>
          <label class="mail-field">
            <span>SMTP 密码
              <HelpTip>{{ notification.smtp_credential_configured ? "凭据已配置；密码不会回显，留空表示不更新。" : "凭据尚未配置，请按 SMTP 服务要求填写。" }}</HelpTip>
            </span>
            <input v-model="notification.smtp_password" type="password" autocomplete="new-password" placeholder="留空保留已保存密码" />
          </label>
          <label class="mail-field">
            <span>连接模式</span>
            <select v-model="notification.smtp_mode">
              <option value="plain">明文连接</option>
              <option value="starttls">STARTTLS</option>
              <option value="implicit_tls">隐式 TLS</option>
            </select>
          </label>
          <label class="mail-switch mail-auth-switch">
            <input v-model="notification.smtp_allow_plaintext_auth" type="checkbox" />
            <span>允许明文认证</span>
            <HelpTip>仅在明确确认 SMTP 网络受保护时开启。</HelpTip>
          </label>
      </div>
      <label class="mail-field mail-recipient-field">
        <span>收件人 <HelpTip>多个地址用逗号分隔。</HelpTip></span>
        <input
          :value="notification.recipients.join(', ')"
          @input="notification.recipients = $event.target.value.split(/[,，]/).map((item) => item.trim()).filter(Boolean)"
          placeholder="多个地址用逗号分隔"
        />
      </label>
    </fieldset>
  </div>
</template>

<style scoped src="./MailNotificationSettings.css"></style>
