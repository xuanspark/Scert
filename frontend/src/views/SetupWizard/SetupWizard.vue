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
  commonEmailDomains,
  selectedEmailDomain,
  emailLocalPart,
  targetLabel,
  resetURLPreview,
  selectedDNSProvider,
  dnsVerificationValid,
  formatVerificationExpiry,
  applyEmailDomain,
  handleEmailInput,
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
              <label class="email-field"
                ><span>绑定邮箱</span
                ><div class="email-input-group">
                  <input
                  id="admin-email"
                  name="admin_email"
                  :value="emailLocalPart"
                  @input="handleEmailInput($event.target.value)"
                  required
                  type="text"
                  autocomplete="email"
                  inputmode="email"
                  placeholder="输入邮箱名称，例如 likb_blue"
              />
                  <select
                    id="admin-email-domain"
                    v-model="selectedEmailDomain"
                    aria-label="常用邮箱"
                    @change="applyEmailDomain(selectedEmailDomain)"
                  >
                    <option
                      v-for="option in commonEmailDomains"
                      :key="option.value"
                      :value="option.value"
                    >
                      {{ option.label }}
                    </option>
                  </select>
                </div>
                <small>可直接输入完整邮箱，也可先输入邮箱名称再选择常用邮箱。</small>
              </label>
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

          <fieldset v-if="currentStep === 2" class="setup-step-card setup-notification-step">
            <legend><span>02</span>服务与通知</legend>
            <div class="setup-grid two-columns email-primary-grid">
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
              <label class="confirm-row compact-row"
                ><input
                  id="email-http-enabled"
                  v-model="form.email_http_enabled"
                  type="checkbox"
                /><span><strong>启用 HTTP 邮件通知</strong><small>兼容 Smail 等 HTTP 邮件服务。</small></span></label
              >
              <label v-if="form.email_http_enabled"
                ><span>第三方邮箱服务标识</span
                ><input
                  id="email-provider"
                  name="email_provider"
                  v-model.trim="form.email_provider"
                  placeholder="例如 company-mail-api"
              /></label>
              <label v-if="form.email_http_enabled"
                ><span>邮件 API 地址</span
                ><input
                  id="email-endpoint"
                  name="email_endpoint"
                  v-model.trim="form.email_endpoint"
                  type="url"
                  placeholder="https://mail.example.com/send"
              /></label>
              <label v-if="form.email_http_enabled"
                ><span>HTTP 请求路径</span
                ><input
                  id="email-http-path"
                  v-model.trim="form.email_http_path"
                  placeholder="/api/email/send"
              /></label>
              <label v-if="form.email_http_enabled"
                ><span>HTTP 认证方式</span
                ><select
                  id="email-http-auth-mode"
                  v-model="form.email_http_auth_mode"
                >
                  <option value="none">无认证</option>
                  <option value="bearer">Bearer Token</option>
                  <option value="hmac_sha256">HMAC-SHA256</option>
                  <option value="session">账号登录会话</option>
                </select></label
              >
              <label
                v-if="
                  form.email_http_enabled &&
                  form.email_http_auth_mode === 'session'
                "
                ><span>登录请求路径</span
                ><input
                  id="email-http-login-path"
                  v-model.trim="form.email_http_login_path"
                  placeholder="/api/login"
              /></label>
              <label v-if="form.email_http_enabled"
                ><span>凭据引用</span
                ><input
                  id="email-http-credential-ref"
                  v-model.trim="form.email_http_credential_ref"
                  placeholder="secret://notification/mail"
              /></label>
              <label v-if="form.email_http_enabled"
                ><span>发件人名称</span
                ><input
                  id="email-sender-name"
                  v-model.trim="form.email_sender_name"
                  placeholder="Scert"
              /></label>
              <label v-if="form.email_http_enabled"
                ><span>发件人地址</span
                ><input
                  id="email-sender-email"
                  v-model.trim="form.email_sender_email"
                  type="email"
                  placeholder="留空使用管理员邮箱"
              /></label>
            </div>
            <div class="provider-description reset-preview">
              <strong>密码重置地址</strong><span>{{ resetURLPreview }}</span
              ><span
                >无需配置独立端口；只有手动启动 Scert-admin
                后，此地址才会临时可用。</span
              >
            </div>
            <label v-if="form.email_http_enabled"
              ><span>通知收件人</span
              ><input
                id="recipients"
                name="recipients"
                v-model="form.recipients"
                placeholder="多个地址使用逗号分隔"
            /></label>
            <div v-if="form.email_http_enabled" class="setup-grid two-columns email-delivery-grid">
              <label
                ><span>通知事件模式</span
                ><select v-model="form.email_event_mode">
                  <option value="detailed">详细事件</option>
                  <option value="unified">统一事件</option>
                </select></label
              >
              <label
                ><span>请求超时（秒）</span
                ><input
                  v-model.number="form.email_timeout_secs"
                  type="number"
                  min="1"
              /></label>
              <label
                ><span>重试次数</span
                ><input
                  v-model.number="form.email_retry_count"
                  type="number"
                  min="1"
              /></label>
              <label
                ><span>重试间隔（秒）</span
                ><input
                  v-model.trim="form.email_retry_intervals_secs"
                  placeholder="30,120,600"
              /></label>
            </div>
            <label v-if="form.email_http_enabled" class="email-headers-field"
              ><span>HTTP 自定义请求头（JSON）</span
              ><textarea
                v-model.trim="form.email_http_headers"
                rows="2"
                placeholder='例如：{"X-Tenant":"scert"}'
              ></textarea
            ></label>
            <div v-if="form.email_http_enabled" class="setup-grid three-columns compact-options email-option-grid">
              <label class="confirm-row compact-row"
                ><input v-model="form.email_include_deployment_details" type="checkbox" /><span>部署明细</span></label
              >
              <label class="confirm-row compact-row"
                ><input v-model="form.email_include_certificate_serial" type="checkbox" /><span>证书序列号</span></label
              >
              <label class="confirm-row compact-row"
                ><input v-model="form.email_include_management_url" type="checkbox" /><span>管理后台链接</span></label
              >
            </div>
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
