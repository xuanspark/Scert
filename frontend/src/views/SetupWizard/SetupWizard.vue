<script setup>
import { useSetupWizard } from "./SetupWizard.js";
import AccessShell from "../components/AccessShell/AccessShell.vue";

const props = defineProps({
  status: {
    type: Object,
    required: true,
  },
});

const emit = defineEmits(["initialized"]);

const {
  scertLogo,
  submitting,
  error,
  resultMessage,
  dnsCatalogLoading,
  dnsCatalogError,
  dnsTesting,
  dnsTestError,
  dnsProviders,
  dnsCredentials,
  dnsTestDomain,
  dnsVerification,
  currentStep,
  targetType,
  form,
  targetLabel,
  resetURLPreview,
  selectedDNSProvider,
  dnsVerificationValid,
  formatVerificationExpiry,
  testDNS,
  handleSubmit,
  previousStep,
} = useSetupWizard(props, emit);
</script>

<template>
  <AccessShell variant="setup" aria-label="Scert 初始化向导">
    <template #brand>
      <img class="access-logo" :src="scertLogo" alt="Scert 标志" />
      <p class="access-eyebrow">SCERT 首次运行</p>
      <h1 class="access-brand-title">配置证书管理服务</h1>
      <p class="access-brand-description">
        Scert 与 Scert-admin 共享本次配置。完成初始化后，即可通过管理页面统一管理证书。
      </p>
      <div class="warning-card">
        <strong>初始化提示</strong>
        <span
          >请先完成 DNS API 在线验证。证书签发和 HTTPS
          切换将在后续任务中执行，本页不会伪报申请成功。</span
        >
      </div>
    </template>

    <section class="setup-card">
      <header>
        <div>
          <p class="setup-eyebrow">初始化向导</p>
          <h2>基础配置</h2>
        </div>
        <span class="local-badge">仅限本机</span>
      </header>

      <nav class="setup-progress" aria-label="初始化步骤">
        <span
          class="progress-step"
          :class="{ active: currentStep === 1, complete: currentStep > 1 }"
          ><b>1</b><strong>管理员</strong><small>账号安全</small></span
        >
        <i aria-hidden="true"></i>
        <span
          class="progress-step"
          :class="{ active: currentStep === 2, complete: currentStep > 2 }"
          ><b>2</b><strong>服务通知</strong><small>端口与邮件</small></span
        >
        <i aria-hidden="true"></i>
        <span
          class="progress-step"
          :class="{ active: currentStep === 3, complete: currentStep > 3 }"
          ><b>3</b><strong>DNS 验证</strong><small>权限测试</small></span
        >
        <i aria-hidden="true"></i>
        <span class="progress-step" :class="{ active: currentStep === 4 }"
          ><b>4</b><strong>管理页面证书</strong><small>访问方式</small></span
        >
      </nav>

      <form
        id="setup-form"
        name="setup_form"
        class="setup-form"
        @submit.prevent="handleSubmit"
      >
        <div class="setup-step-grid">
          <fieldset v-if="currentStep === 1" class="setup-step-card">
            <legend><span>01</span>超级管理员</legend>
            <div class="setup-grid two-columns">
              <label
                ><span>用户名</span
                ><input
                  id="admin-username"
                  name="admin_username"
                  v-model.trim="form.admin_username"
                  required
                  autocomplete="username"
                  placeholder="例如 scert-admin"
              /></label>
              <label
                ><span>绑定邮箱</span
                ><input
                  id="admin-email"
                  name="admin_email"
                  v-model.trim="form.admin_email"
                  required
                  type="email"
                  autocomplete="email"
                  placeholder="用于登录和安全通知"
              /></label>
              <label
                ><span>密码</span
                ><input
                  id="admin-password"
                  name="admin_password"
                  v-model="form.admin_password"
                  required
                  minlength="8"
                  type="password"
                  autocomplete="new-password"
                  placeholder="至少 8 个字符"
              /></label>
              <label
                ><span>确认密码</span
                ><input
                  id="confirm-password"
                  name="confirm_password"
                  v-model="form.confirm_password"
                  required
                  minlength="8"
                  type="password"
                  autocomplete="new-password"
                  placeholder="再次输入密码"
              /></label>
            </div>
          </fieldset>

          <fieldset v-if="currentStep === 2" class="setup-step-card">
            <legend><span>02</span>服务与通知</legend>
            <div class="setup-grid two-columns">
              <label
                ><span>Scert 主服务端口</span
                ><input
                  id="server-port"
                  name="server_port"
                  v-model.number="form.server_port"
                  required
                  min="1"
                  max="65535"
                  type="number"
              /></label>
              <label
                ><span>第三方邮箱服务标识</span
                ><input
                  id="email-provider"
                  name="email_provider"
                  v-model.trim="form.email_provider"
                  placeholder="例如 company-mail-api"
              /></label>
              <label
                ><span>邮件 API 地址</span
                ><input
                  id="email-endpoint"
                  name="email_endpoint"
                  v-model.trim="form.email_endpoint"
                  type="url"
                  placeholder="https://mail.example.com/send"
              /></label>
            </div>
            <div class="provider-description reset-preview">
              <strong>密码重置地址</strong><span>{{ resetURLPreview }}</span
              ><span
                >无需配置独立端口；只有手动启动 Scert-admin
                后，此地址才会临时可用。</span
              >
            </div>
            <label
              ><span>通知收件人</span
              ><input
                id="recipients"
                name="recipients"
                v-model="form.recipients"
                placeholder="多个地址使用逗号分隔"
            /></label>
          </fieldset>

          <fieldset v-if="currentStep === 3" class="setup-step-card">
            <legend><span>03</span>DNS 验证</legend>
            <div class="setup-grid two-columns">
              <label
                ><span>DNS 服务商</span
                ><select
                  id="dns-provider"
                  name="dns_provider"
                  v-model="form.dns_provider"
                  required
                  :disabled="dnsCatalogLoading"
                >
                  <option v-if="dnsCatalogLoading" value="">
                    正在读取服务商……
                  </option>
                  <option
                    v-for="provider in dnsProviders"
                    :key="provider.id"
                    :value="provider.id"
                  >
                    {{ provider.name
                    }}{{ provider.available ? "" : "（适配器待开放）" }}
                  </option>
                </select></label
              >
              <label
                ><span>DNS 权限测试域名</span
                ><input
                  id="dns-test-domain"
                  name="dns_test_domain"
                  v-model.trim="dnsTestDomain"
                  required
                  placeholder="例如 example.com"
                /><small
                  >使用域名时可填写管理页面域名；使用公网 IP
                  时必须另填一个由当前 DNS 账号管理的域名。</small
                ></label
              >
            </div>
            <p v-if="dnsCatalogError" class="inline-error">
              {{ dnsCatalogError }}
            </p>
            <div
              v-if="selectedDNSProvider"
              class="provider-description"
              :class="{ unavailable: !selectedDNSProvider.available }"
            >
              <strong>{{
                selectedDNSProvider.available
                  ? "在线验证可用"
                  : "当前仅提供字段模型"
              }}</strong>
              <span>{{ selectedDNSProvider.description }}</span>
            </div>
            <div
              v-if="selectedDNSProvider"
              class="setup-grid two-columns credential-grid"
            >
              <label
                v-for="field in selectedDNSProvider.credential_fields"
                :key="field.name"
              >
                <span
                  >{{ field.label
                  }}{{ field.required ? "（必填）" : "（可选）" }}</span
                >
                <textarea
                  v-if="field.name.includes('json')"
                  :id="`dns-credential-${field.name}`"
                  :name="`dns_credential_${field.name}`"
                  v-model="dnsCredentials[field.name]"
                  :required="field.required"
                  :placeholder="field.placeholder"
                  rows="4"
                  autocomplete="off"
                ></textarea>
                <input
                  v-else
                  :id="`dns-credential-${field.name}`"
                  :name="`dns_credential_${field.name}`"
                  v-model="dnsCredentials[field.name]"
                  :required="field.required"
                  :type="field.secret ? 'password' : 'text'"
                  :placeholder="field.placeholder"
                  autocomplete="off"
                />
              </label>
            </div>
            <div class="dns-test-actions">
              <button
                class="dns-test-button"
                type="button"
                :disabled="
                  dnsTesting ||
                  dnsCatalogLoading ||
                  !selectedDNSProvider?.available
                "
                @click="testDNS"
              >
                {{ dnsTesting ? "正在创建并清理测试记录……" : "测试 DNS API" }}
              </button>
              <span
                >测试会临时创建 DNS-01 TXT
                记录并自动清理，明文凭据不会写入普通配置文件。</span
              >
            </div>
            <p v-if="dnsTestError" class="inline-error">{{ dnsTestError }}</p>
            <div v-if="dnsVerificationValid" class="dns-verification-success">
              <strong>DNS API 在线验证通过</strong>
              <span
                >凭据已加密保存，本次验证结果有效至
                {{
                  formatVerificationExpiry(dnsVerification.expires_at)
                }}。修改服务商、测试域名或凭据后需要重新测试。</span
              >
            </div>
          </fieldset>

          <fieldset v-if="currentStep === 4" class="setup-step-card">
            <legend><span>04</span>管理页面与证书目录</legend>
            <div class="target-switch">
              <button
                type="button"
                :class="{ active: targetType === 'hostname' }"
                @click="targetType = 'hostname'"
              >
                使用域名</button
              ><button
                type="button"
                :class="{ active: targetType === 'ip' }"
                @click="targetType = 'ip'"
              >
                使用公网 IP
              </button>
            </div>
            <div class="setup-grid two-columns">
              <label v-if="targetType === 'hostname'"
                ><span>管理页面域名</span
                ><input
                  id="web-hostname"
                  name="web_hostname"
                  v-model.trim="form.web_hostname"
                  required
                  placeholder="scert.example.com"
              /></label>
              <label v-else
                ><span>公网 IP 地址</span
                ><input
                  id="web-public-ip"
                  name="web_public_ip"
                  v-model.trim="form.web_public_ip"
                  required
                  placeholder="203.0.113.10"
              /></label>
              <label
                ><span>证书部署根目录</span
                ><input
                  id="certificate-root"
                  name="certificate_root"
                  v-model.trim="form.certificate_root"
                  required
              /></label>
              <label
                ><span>HTTPS 模式</span
                ><select
                  id="https-mode"
                  name="https_mode"
                  v-model="form.https_mode"
                >
                  <option value="direct">默认：核心服务直接终止 HTTPS</option>
                  <option value="proxy">
                    高级：Nginx/Caddy 终止公网 HTTPS
                  </option>
                </select></label
              >
            </div>
            <label class="confirm-row"
              ><input
                id="web-enabled"
                name="web_enabled"
                v-model="form.web_enabled"
                type="checkbox"
              /><span
                ><strong>初始化完成后开启管理页面</strong
                ><small
                  >桌面端可通过此开关决定是否启用浏览器管理页面。</small
                ></span
              ></label
            >
          </fieldset>
        </div>

        <p v-if="error" class="setup-error">{{ error }}</p>
        <p v-if="resultMessage" class="setup-success">{{ resultMessage }}</p>
        <footer>
          <span
            >初始化程序将自动配置本机防火墙；云安全组只需放行主服务端口。配置完成前仅保存在内存中。</span
          >
          <div class="wizard-actions">
            <button
              v-if="currentStep > 1"
              class="back-button"
              type="button"
              @click="previousStep"
            >
              上一步</button
            ><button type="submit" :disabled="submitting || dnsTesting">
              {{
                currentStep < 4
                  ? "下一步"
                  : submitting
                    ? "正在保存……"
                    : "完成初始化"
              }}
            </button>
          </div>
        </footer>
      </form>
    </section>
  </AccessShell>
</template>

<style scoped src="./SetupWizard.css"></style>
