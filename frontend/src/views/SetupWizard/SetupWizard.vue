<script setup>
import { useSetupWizard } from "./SetupWizard.js";
import AccessShell from "../components/AccessShell/AccessShell.vue";
import DatabaseSetup from "./DatabaseSetup.vue";

const props = defineProps({
  status: {
    type: Object,
    required: true,
  },
});

const emit = defineEmits(["initialized"]);

const {
  isBootstrap,
  completed,
  databaseForm,
  databaseTesting,
  databaseError,
  databaseVerified,
  testDatabase,
  scertLogo,
  submitting,
  error,
  resultMessage,
  dnsCatalogLoading,
  dnsCatalogError,
  dnsTesting,
  dnsTestError,
  dnsVerificationElapsedSeconds,
  dnsVerificationTimeoutSeconds,
  dnsProviders,
  dnsCredentials,
  dnsCredentialsVisible,
  maskCredential,
  emailCredentials,
  emailCredentialsVisible,
  dnsTestDomain,
  dnsVerification,
  currentStep,
  notificationAdvancedVisible,
  targetType,
  form,
  commonEmailDomains,
  selectedEmailDomain,
  emailLocalPart,
  emailInputElement,
  emailAutofillElement,
  handleEmailAutofill,
  targetLabel,
  resetURLPreview,
  emailCredentialLabel,
  emailCredentialPlaceholder,
  selectedDNSProvider,
  dnsVerificationValid,
  formatVerificationExpiry,
  formatVerificationStageStatus,
  applyEmailDomain,
  toggleNotificationAdvanced,
  handleEmailInput,
  synchronizeEmailField,
  handleEmailPaste,
  handleEmailBlur,
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
        <span>
          请先完成 DNS API 在线验证。提交初始化时将申请管理页面证书；只有证书校验和配置保存成功后才会完成初始化。
        </span>
      </div>
    </template>

    <section class="setup-card">
      <header>
        <div>
          <p class="setup-eyebrow">初始化向导</p>
          <h2>基础配置</h2>
        </div>
        <span class="local-badge">{{ isBootstrap ? "受保护的 HTTPS 初始化" : "仅限本机" }}</span>
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
          ><b>2</b><strong>服务通知</strong><small>邮件通知</small></span
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
          <fieldset v-if="currentStep === 1" class="setup-step-card" :disabled="submitting || completed">
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
                    ref="emailAutofillElement"
                    class="email-autofill-probe"
                    id="admin-email-autofill"
                    name="email"
                    autocomplete="section-scert email"
                    tabindex="-1"
                    aria-hidden="true"
                    type="email"
                    @input="handleEmailAutofill"
                    @change="handleEmailAutofill"
                    @blur="handleEmailAutofill"
                  />
                  <input
                    id="admin-email"
                    name="admin_email"
                    ref="emailInputElement"
                    :value="emailLocalPart"
                    @input="handleEmailInput($event.target.value)"
                    @paste="handleEmailPaste"
                    @change="synchronizeEmailField($event.target.value)"
                    @focus="synchronizeEmailField($event.target.value)"
                    @blur="handleEmailBlur($event.target.value)"
                    @animationstart="handleEmailAutofill"
                    type="text"
                    autocomplete="email"
                    inputmode="email"
                    placeholder="请输入邮箱名称"
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
                  maxlength="12"
                  type="password"
                  autocomplete="new-password"
                  placeholder="8-12 个字符"
              /></label>
              <label
                ><span>确认密码</span
                ><input
                  id="confirm-password"
                  name="confirm_password"
                  v-model="form.confirm_password"
                  required
                  minlength="8"
                  maxlength="12"
                  type="password"
                  autocomplete="new-password"
                  placeholder="再次输入密码"
              /></label>
            </div>
            <div class="setup-section-heading">
              <strong>服务访问配置</strong>
              <span>先确定 Scert 主服务和管理页面的访问方式，后续步骤会使用这里的配置。</span>
            </div>
            <div class="setup-grid two-columns">
              <label>
                <span>Scert 主服务端口</span>
                <input
                  id="server-port"
                  name="server_port"
                  v-model.number="form.server_port"
                  required
                  min="1"
                  max="65535"
                  type="number"
                />
                <small>管理页面和 API 对外提供服务使用的端口。</small>
              </label>
              <div class="access-target-field">
                <span>管理页面访问方式</span>
                <div class="target-switch" role="group" aria-label="管理页面访问方式">
                  <button
                    type="button"
                    :class="{ active: targetType === 'hostname' }"
                    :aria-pressed="targetType === 'hostname'"
                    @click="targetType = 'hostname'"
                  >
                    使用域名
                  </button>
                  <button
                    type="button"
                    :class="{ active: targetType === 'ip' }"
                    :aria-pressed="targetType === 'ip'"
                    @click="targetType = 'ip'"
                  >
                    使用公网 IP
                  </button>
                </div>
              </div>
              <label v-if="targetType === 'hostname'">
                <span>管理页面域名</span>
                <input
                  id="web-hostname"
                  name="web_hostname"
                  v-model.trim="form.web_hostname"
                  required
                  placeholder="scert.example.com"
                />
              </label>
              <label v-else>
                <span>公网 IP 地址</span>
                <input
                  id="web-public-ip"
                  name="web_public_ip"
                  v-model.trim="form.web_public_ip"
                  required
                  placeholder="203.0.113.10"
                />
              </label>
            </div>
            <DatabaseSetup
              v-if="isBootstrap"
              :form="databaseForm"
              :testing="databaseTesting"
              :verified="databaseVerified"
              :error="databaseError"
              @test="testDatabase"
            />
            <div class="provider-description reset-preview">
              <strong>密码重置地址预览</strong>
              <span>{{ resetURLPreview }}</span>
              <span>只有手动启动 Scert-admin 后，此地址才会临时可用。</span>
            </div>
          </fieldset>

          <fieldset
            v-if="currentStep === 2"
            class="setup-step-card setup-notification-step"
          >
            <legend><span>02</span>服务与通知</legend>

            <div class="notification-layout">
              <div class="notification-guide">
                <strong>先完成基础配置</strong>
                <span>填写邮件服务地址即可继续，其他项目已经提供合理默认值。</span>
              </div>

              <div class="notification-row notification-service-row">
                <label class="confirm-row compact-row">
                  <input
                    id="email-http-enabled"
                    v-model="form.email_http_enabled"
                    type="checkbox"
                  />
                  <span>
                    <strong>启用 HTTP 邮件通知</strong>
                    <small>通过第三方邮件 API 发送任务通知。</small>
                  </span>
                </label>
                <label class="confirm-row compact-row">
                  <input
                    id="email-smtp-enabled"
                    v-model="form.email_smtp_enabled"
                    type="checkbox"
                  />
                  <span>
                    <strong>启用 SMTP 通知</strong>
                    <small>通过 SMTP 出站客户端发送任务通知。</small>
                  </span>
                </label>
              </div>

              <div v-if="form.email_smtp_enabled" class="notification-basic-panel smtp-basic-panel">
                <div class="notification-section-heading">
                  <div>
                    <strong>SMTP 通知配置</strong>
                    <span>Scert 只作为 SMTP 出站客户端，不提供 SMTP、IMAP 或 POP3 服务端。</span>
                  </div>
                  <span class="notification-recommended-badge">推荐 STARTTLS</span>
                </div>

                <div class="notification-row smtp-config-row">
                  <label>
                    <span>SMTP 主机</span>
                    <input
                      id="email-smtp-host"
                      name="email_smtp_host"
                      v-model.trim="form.email_smtp_host"
                      placeholder="smtp.example.com"
                      autocomplete="off"
                    />
                  </label>
                  <label>
                    <span>SMTP 端口</span>
                    <input
                      id="email-smtp-port"
                      name="email_smtp_port"
                      v-model.number="form.email_smtp_port"
                      type="number"
                      min="1"
                      max="65535"
                      inputmode="numeric"
                    />
                  </label>
                  <label>
                    <span>SMTP 用户名</span>
                    <input
                      id="email-smtp-username"
                      name="email_smtp_username"
                      v-model.trim="form.email_smtp_username"
                      placeholder="发件邮箱或账号"
                      autocomplete="username"
                    />
                  </label>
                  <label>
                    <span>SMTP 密码</span>
                    <input
                      id="email-smtp-password"
                      name="email_smtp_password"
                      v-model="form.email_smtp_password"
                      type="password"
                      placeholder="请输入 SMTP 密码或授权码"
                      autocomplete="new-password"
                    />
                  </label>
                </div>

                <div class="notification-row smtp-options-row">
                  <label>
                    <span>连接模式</span>
                    <select v-model="form.email_smtp_mode">
                      <option value="starttls">STARTTLS（推荐）</option>
                      <option value="implicit_tls">隐式 TLS</option>
                      <option value="plain">明文</option>
                    </select>
                  </label>
                  <label class="confirm-row compact-row smtp-plaintext-row">
                    <input
                      id="email-smtp-allow-plaintext-auth"
                      v-model="form.email_smtp_allow_plaintext_auth"
                      type="checkbox"
                    />
                    <span>
                      <strong>允许明文认证</strong>
                      <small>仅在明文模式下使用，存在凭据泄露风险。</small>
                    </span>
                  </label>
                </div>

                <div class="notification-smtp-tip">
                  SMTP 密码只会写入加密敏感信息存储，不会回显到配置文件、接口响应或日志。建议优先使用 STARTTLS 或隐式 TLS。
                </div>
              </div>

              <div v-if="form.email_http_enabled" class="notification-basic-panel">
                <div class="notification-section-heading">
                  <div>
                    <strong>邮件通知基础配置</strong>
                    <span>填写服务地址、发件人和收件人即可，技术参数无需修改。</span>
                  </div>
                  <span class="notification-recommended-badge">推荐配置</span>
                </div>

                <div class="notification-row notification-basic-row">
                  <label>
                    <span>邮件服务地址</span>
                    <input
                      id="email-endpoint"
                      name="email_endpoint"
                      v-model.trim="form.email_endpoint"
                      type="url"
                      placeholder="https://mail.example.com"
                    />
                    <small>填写邮件服务提供的地址，不需要自行拼接接口路径。</small>
                  </label>
                  <label>
                    <span>邮件服务名称（可选）</span>
                    <input
                      id="email-provider"
                      name="email_provider"
                      v-model.trim="form.email_provider"
                      placeholder="例如：Smail"
                    />
                    <small>仅用于日志和通知内容中的识别。</small>
                  </label>
                </div>

                <div class="notification-row notification-sender-row">
                  <label>
                    <span>发件人名称</span>
                    <input
                      id="email-sender-name"
                      v-model.trim="form.email_sender_name"
                      placeholder="Scert"
                    />
                  </label>
                  <label>
                    <span>发件人地址（可选）</span>
                    <input
                      id="email-sender-email"
                      v-model.trim="form.email_sender_email"
                      type="email"
                      placeholder="留空使用管理员邮箱"
                    />
                  </label>
                  <label>
                    <span>通知收件人</span>
                    <input
                      id="recipients"
                      name="recipients"
                      v-model="form.recipients"
                      placeholder="多个地址使用逗号分隔"
                    />
                    <small>续期、部署和错误通知将发送到这些地址。</small>
                  </label>
                </div>
              </div>

              <div class="provider-description reset-preview">
                <strong>密码重置地址</strong>
                <span>{{ resetURLPreview }}</span>
                <span>只有手动启动 Scert-admin 后，此地址才会临时可用。</span>
              </div>

              <div v-if="form.email_http_enabled" class="notification-advanced-panel">
                <button
                  class="notification-advanced-toggle"
                  type="button"
                  :aria-expanded="notificationAdvancedVisible"
                  @click="toggleNotificationAdvanced"
                >
                  <span>
                    <strong>高级配置</strong>
                    <small>认证、请求路径、重试策略和通知内容选项</small>
                  </span>
                  <span class="notification-advanced-action">
                    {{ notificationAdvancedVisible ? "收起配置" : "展开配置" }}
                  </span>
                </button>

                <div v-if="notificationAdvancedVisible" class="notification-advanced-content">
                  <div class="notification-advanced-tip">
                    默认值适用于常见 HTTP 邮件服务。只有服务方提供了特殊要求时，才需要修改这里的内容。
                  </div>

                  <div class="notification-row notification-api-row">
                    <label>
                      <span>HTTP 请求路径</span>
                      <input
                        id="email-http-path"
                        v-model.trim="form.email_http_path"
                        placeholder="/api/email/send"
                      />
                    </label>
                    <label>
                      <span>HTTP 认证方式</span>
                      <select
                        id="email-http-auth-mode"
                        v-model="form.email_http_auth_mode"
                      >
                        <option value="none">无认证</option>
                        <option value="bearer">Bearer Token</option>
                        <option value="hmac_sha256">HMAC-SHA256</option>
                        <option value="session">账号登录会话</option>
                      </select>
                    </label>
                  </div>

                  <div v-if="form.email_http_auth_mode === 'session'" class="notification-row notification-auth-row">
                    <label>
                      <span>登录请求路径</span>
                      <input
                        id="email-http-login-path"
                        v-model.trim="form.email_http_login_path"
                        placeholder="/api/login"
                      />
                    </label>
                    <label v-if="!isBootstrap">
                      <span>已有凭据引用（可选）</span>
                      <input
                        id="email-http-credential-ref"
                        v-model.trim="form.email_http_credential_ref"
                        placeholder="已有安全凭据引用"
                      />
                    </label>
                  </div>

                  <div v-else class="notification-row notification-auth-row">
                    <label v-if="!isBootstrap">
                      <span>已有凭据引用（可选）</span>
                      <input
                        id="email-http-credential-ref"
                        v-model.trim="form.email_http_credential_ref"
                        placeholder="已有安全凭据引用"
                      />
                    </label>
                    <div v-if="form.email_http_auth_mode === 'none'" class="notification-auth-note">
                      当前接口不启用认证，不需要填写凭据。
                    </div>
                  </div>

                  <div
                    v-if="form.email_http_auth_mode !== 'none'"
                    class="notification-credential-field"
                  >
                    <div class="credential-visibility-row">
                      <span>{{ emailCredentialLabel }}</span>
                      <label class="credential-visibility-control">
                        <input
                          v-model="emailCredentialsVisible"
                          type="checkbox"
                        />
                        <span>显示凭据明文</span>
                      </label>
                    </div>
                    <textarea
                      v-if="emailCredentialsVisible"
                      id="email-http-credentials-visible"
                      v-model="emailCredentials"
                      rows="3"
                      autocomplete="off"
                      :placeholder="emailCredentialPlaceholder"
                    ></textarea>
                    <textarea
                      v-else
                      id="email-http-credentials-masked"
                      :value="maskCredential(emailCredentials)"
                      rows="3"
                      readonly
                      autocomplete="off"
                    ></textarea>
                    <small>
                      配置时允许明文显示；提交后仅保存加密凭据引用，普通配置文件不会保存密码或令牌。
                    </small>
                  </div>

                  <div class="notification-row notification-delivery-row">
                    <label>
                      <span>通知事件模式</span>
                      <select v-model="form.email_event_mode">
                        <option value="detailed">详细事件</option>
                        <option value="unified">统一事件</option>
                      </select>
                    </label>
                    <label>
                      <span>请求超时（秒）</span>
                      <input
                        v-model.number="form.email_timeout_secs"
                        type="number"
                        min="1"
                      />
                    </label>
                    <label>
                      <span>重试次数</span>
                      <input
                        v-model.number="form.email_retry_count"
                        type="number"
                        min="1"
                      />
                    </label>
                    <label>
                      <span>重试间隔（秒）</span>
                      <input
                        v-model.trim="form.email_retry_intervals_secs"
                        placeholder="30,120,600"
                      />
                    </label>
                  </div>

                  <div class="notification-bottom-row">
                    <label class="email-headers-field">
                      <span>HTTP 自定义请求头（JSON，可选）</span>
                      <textarea
                        v-model.trim="form.email_http_headers"
                        rows="2"
                        placeholder='例如：{"X-Tenant":"scert"}'
                      ></textarea>
                    </label>
                    <div class="setup-grid three-columns compact-options email-option-grid">
                      <label class="confirm-row compact-row">
                        <input
                          v-model="form.email_include_deployment_details"
                          type="checkbox"
                        />
                        <span>部署明细</span>
                      </label>
                      <label class="confirm-row compact-row">
                        <input
                          v-model="form.email_include_certificate_serial"
                          type="checkbox"
                        />
                        <span>证书序列号</span>
                      </label>
                      <label class="confirm-row compact-row">
                        <input
                          v-model="form.email_include_management_url"
                          type="checkbox"
                        />
                        <span>管理后台链接</span>
                      </label>
                    </div>
                  </div>
                </div>
              </div>
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
            <div class="setup-grid two-columns propagation-grid">
              <label>
                <span>DNS 传播最大等待时间（秒）</span>
                <input
                  id="propagation-timeout-secs"
                  name="propagation_timeout_secs"
                  v-model.number="form.propagation_timeout_secs"
                  type="number"
                  min="1"
                  max="86400"
                  required
                />
                <small>默认 300 秒，最长允许等待 86400 秒。</small>
              </label>
              <label>
                <span>DNS 传播检测间隔（秒）</span>
                <input
                  id="propagation-polling-interval-secs"
                  name="propagation_polling_interval_secs"
                  v-model.number="form.propagation_polling_interval_secs"
                  type="number"
                  min="1"
                  :max="form.propagation_timeout_secs"
                  required
                />
                <small>默认 5 秒，不能大于最大等待时间。</small>
              </label>
            </div>
            <p class="field-hint">
              阿里云 RAM 权限验证最多等待 6 秒：系统会创建、查询并清理临时 TXT
              记录。以上 300 秒和 5 秒用于实际申请证书时的公网 DNS-01
              传播等待与轮询。
            </p>
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
              <div class="credential-visibility-row">
                <span>DNS 凭据</span>
                <label class="credential-visibility-control">
                  <input
                    v-model="dnsCredentialsVisible"
                    type="checkbox"
                  />
                  <span>显示凭据明文</span>
                </label>
              </div>
              <label
                v-for="field in selectedDNSProvider.credential_fields"
                :key="field.name"
              >
                <span
                  >{{ field.label
                  }}{{ field.required ? "（必填）" : "（可选）" }}</span
                >
                <textarea
                  v-if="field.name.includes('json') && dnsCredentialsVisible"
                  :id="`dns-credential-json-${field.name}`"
                  :name="`dns_credential_${field.name}`"
                  v-model="dnsCredentials[field.name]"
                  :required="field.required"
                  :placeholder="field.placeholder"
                  rows="4"
                  autocomplete="off"
                ></textarea>
                <textarea
                  v-else-if="field.name.includes('json')"
                  :id="`dns-credential-masked-${field.name}`"
                  :name="`dns_credential_${field.name}`"
                  :value="maskCredential(dnsCredentials[field.name])"
                  rows="4"
                  readonly
                  autocomplete="off"
                ></textarea>
                <input
                  v-else
                  :id="`dns-credential-value-${field.name}`"
                  :name="`dns_credential_${field.name}`"
                  v-model="dnsCredentials[field.name]"
                  :required="field.required"
                  :type="field.secret && !dnsCredentialsVisible ? 'password' : 'text'"
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
                {{
                  dnsTesting
                    ? "正在创建、查询并清理测试记录……"
                    : "测试 DNS API"
                }}
              </button>
              <span
                >测试会临时创建 DNS-01 TXT
                记录并自动清理，明文凭据不会写入普通配置文件。</span
              >
            </div>
            <div
              v-if="dnsTesting"
              class="dns-verification-progress"
              role="status"
              aria-live="polite"
            >
              <strong>正在进行 DNS 验证，请等待</strong>
              <span>
                已等待 {{ dnsVerificationElapsedSeconds }} 秒，最长等待
                {{ dnsVerificationTimeoutSeconds }} 秒。
              </span>
              <small v-if="selectedDNSProvider?.id === 'aliyun'">
                正在通过阿里云 DNS API 创建、查询并清理临时 TXT 记录，不等待公网 DNS 传播，通常数秒即可完成；实际申请证书时，仍可能需要等待公网 DNS-01 记录传播。
              </small>
              <small v-else>
                系统正在创建临时 TXT、查询验证记录并自动清理。这是 DNS API 权限验证；实际申请证书时，仍可能需要等待公网 DNS-01 记录传播。
              </small>
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

            <div
              v-if="dnsVerification && dnsVerification.test_report"
              class="dns-verification-report"
            >
              <div class="dns-report-heading">
                <strong>在线测试过程</strong>
                <span v-if="dnsVerification.test_report.record_name">
                  临时记录：{{ dnsVerification.test_report.record_name }}
                </span>
              </div>
              <div
                v-for="stage in dnsVerification.test_report.stages"
                :key="stage.name"
                class="dns-verification-stage"
                :class="`is-${stage.status}`"
              >
                <span class="dns-stage-name">{{ stage.name }}</span>
                <strong>{{ formatVerificationStageStatus(stage.status) }}</strong>
                <small>{{ stage.message }}</small>
              </div>
            </div>
          </fieldset>

          <fieldset v-if="currentStep === 4" class="setup-step-card">
            <legend><span>04</span>证书目录与剩余设置</legend>
            <div class="setup-grid two-columns">
              <label>
                <span>证书部署根目录</span>
                <input
                  id="certificate-root"
                  name="certificate_root"
                  v-model.trim="form.certificate_root"
                  :readonly="isBootstrap"
                  required
                />
              </label>
              <label v-if="isBootstrap">
                <span>证书签发环境</span>
                <select v-model="form.acme_environment">
                  <option value="staging">测试环境（证书不受公共信任）</option>
                  <option value="production">正式环境（实际申请受信任域名证书）</option>
                </select>
                <small>IP 管理入口使用自签名证书，不会因选择正式环境而自动受信任。</small>
              </label>
              <label>
                <span>HTTPS 模式</span>
                <select
                  id="https-mode"
                  name="https_mode"
                  v-model="form.https_mode"
                >
                  <option value="direct">默认：核心服务直接终止 HTTPS</option>
                  <option value="proxy">
                    高级：Nginx/Caddy 终止公网 HTTPS
                  </option>
                </select>
              </label>
            </div>
            <label class="confirm-row">
              <input
                id="web-enabled"
                name="web_enabled"
                v-model="form.web_enabled"
                :disabled="isBootstrap"
                type="checkbox"
              />
              <span>
                <strong>初始化完成后开启管理页面</strong>
                <small>桌面端可通过此开关决定是否启用浏览器管理页面。</small>
              </span>
            </label>
          </fieldset>
        </div>

        <p v-if="error" class="setup-error">{{ error }}</p>
        <p v-if="resultMessage" class="setup-success">{{ resultMessage }}</p>
        <footer>
          <span
            >请放行初始化及正式服务端口。验证凭据加密保存；完成后关闭临时入口并切换正式管理地址。</span
          >
          <div class="wizard-actions">
            <button
              v-if="currentStep > 1"
              class="back-button"
              type="button"
              :disabled="submitting || completed"
              @click="previousStep"
            >
              上一步</button
            ><button type="submit" :disabled="submitting || dnsTesting || databaseTesting || completed">
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
