<script setup>
import HelpTip from "../components/HelpTip/HelpTip.vue";
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
        <span v-if="isBootstrap">
          Linux 服务由管理员预先配置防火墙和云安全组。请确认正式管理端口已按需放行；反向代理模式仅允许代理访问内部端口。初始化完成不代表外部网络已验证可达。
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
            <div class="setup-section-heading admin-account-heading">
              <strong>用户信息</strong>
            </div>
            <div class="setup-grid two-columns admin-account-grid">
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
                ><span>
                  绑定邮箱
                  <HelpTip>
                    可直接输入完整邮箱，也可先输入邮箱名称再选择常用邮箱。
                  </HelpTip>
                </span><div class="email-input-group">
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
              <strong>
                服务访问配置
                <HelpTip>
                  先确定 Scert 主服务和管理页面的访问方式，后续步骤会使用这里的配置。
                </HelpTip>
              </strong>
            </div>
            <div class="setup-grid service-access-grid">
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
              <label>
                <span>
                  Scert 主服务端口
                  <HelpTip>
                    管理页面和 API 对外提供服务使用的端口。
                  </HelpTip>
                </span>
                <input
                  id="server-port"
                  name="server_port"
                  v-model.number="form.server_port"
                  required
                  min="1"
                  max="65535"
                  type="number"
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
              <div class="notification-row notification-service-row">
                <label class="confirm-row compact-row">
                  <input
                    id="email-smtp-enabled"
                    v-model="form.email_smtp_enabled"
                    type="checkbox"
                  />
                  <span>
                    <strong>
                      启用 SMTP 通知
                      <HelpTip>
                        通过 SMTP 出站客户端发送任务通知。
                      </HelpTip>
                    </strong>
                  </span>
                </label>
              </div>

              <div v-if="form.email_smtp_enabled" class="notification-basic-panel smtp-basic-panel">
                <div class="notification-section-heading">
                  <div>
                    <strong>
                      SMTP 通知配置
                      <HelpTip>
                        Scert 只作为 SMTP 出站客户端，不提供 SMTP、IMAP 或 POP3 服务端。
                      </HelpTip>
                    </strong>
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
                    <span>
                      SMTP 用户名
                      <HelpTip>
                        使用 Smail 系统通知页提供的 notify:账号@邮件域名和专用发信凭据，自动使用系统通知发件人，不使用管理员邮箱。
                      </HelpTip>
                    </span>
                    <input
                      id="email-smtp-username"
                      name="email_smtp_username"
                      v-model.trim="form.email_smtp_username"
                      placeholder="notify:账号@邮件域名"
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
                      placeholder="请输入 Smail 系统通知发信凭据"
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

                <div class="notification-row notification-recipient-row">
                  <label>
                    <span>
                      通知收件人
                      <HelpTip>
                        续期、部署和错误通知将发送到这些地址。
                      </HelpTip>
                    </span>
                    <input
                      id="recipients"
                      name="recipients"
                      v-model="form.recipients"
                      placeholder="多个地址使用逗号分隔"
                    />
                  </label>
                </div>
              </div>

              <div v-if="form.email_smtp_enabled" class="notification-advanced-panel">
                <button
                  class="notification-advanced-toggle"
                  type="button"
                  :aria-expanded="notificationAdvancedVisible"
                  @click="toggleNotificationAdvanced"
                >
                  <span>
                    <strong>高级配置</strong>
                  </span>
                  <span class="notification-advanced-action">
                    {{ notificationAdvancedVisible ? "收起配置" : "展开配置" }}
                  </span>
                </button>

                <div v-if="notificationAdvancedVisible" class="notification-advanced-content">
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
          <fieldset v-if="currentStep === 3" class="setup-step-card setup-dns-step">
            <legend><span>03</span>DNS 验证</legend>
            <div class="dns-settings-grid">
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
                ><span>
                  DNS 权限测试域名
                  <HelpTip>
                    使用域名时可填写管理页面域名；使用公网 IP
                    时必须另填一个由当前 DNS 账号管理的域名。
                  </HelpTip>
                </span><input
                  id="dns-test-domain"
                  name="dns_test_domain"
                  v-model.trim="dnsTestDomain"
                  required
                  placeholder="例如 example.com"
                /></label
              >
              <label>
                <span>
                  DNS 传播等待（秒）
                  <HelpTip>
                    实际申请证书时，等待公网 DNS-01 传播的最长时间。默认 300 秒，最多 86400 秒；不影响 DNS API 权限验证的等待时间。
                  </HelpTip>
                </span>
                <input
                  id="propagation-timeout-secs"
                  name="propagation_timeout_secs"
                  v-model.number="form.propagation_timeout_secs"
                  type="number"
                  min="1"
                  max="86400"
                  required
                />
              </label>
              <label>
                <span>
                  传播检测间隔（秒）
                  <HelpTip>
                    默认 5 秒，不能大于最大等待时间。
                  </HelpTip>
                </span>
                <input
                  id="propagation-polling-interval-secs"
                  name="propagation_polling_interval_secs"
                  v-model.number="form.propagation_polling_interval_secs"
                  type="number"
                  min="1"
                  :max="form.propagation_timeout_secs"
                  required
                />
              </label>
            </div>
            <p v-if="dnsCatalogError" class="inline-error">
              {{ dnsCatalogError }}
            </p>
            <div
              v-if="selectedDNSProvider"
              class="dns-provider-status"
              :class="{ unavailable: !selectedDNSProvider.available }"
            >
              <strong>{{
                selectedDNSProvider.available
                  ? "在线验证可用"
                  : "当前仅提供字段模型"
              }}</strong>
              <HelpTip>{{ selectedDNSProvider.description }}</HelpTip>
            </div>
            <div
              v-if="selectedDNSProvider"
              class="setup-grid two-columns credential-grid"
            >
              <div class="credential-visibility-row">
                <span>DNS 凭据</span>
                <label class="credential-visibility-control">
                  <input
                    id="dns-credentials-visible"
                    name="dns_credentials_visible"
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
                    ? "正在验证……"
                    : "测试 DNS API"
                }}
              </button>
              <HelpTip>
                测试会创建、查询并清理临时 TXT 记录，验证 DNS API 操作权限，不等待公网传播。阿里云验证最多等待 6 秒；明文凭据不会写入普通配置文件。
              </HelpTip>
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
            </div>
            <p v-if="dnsTestError" class="inline-error">{{ dnsTestError }}</p>
            <div v-if="dnsVerificationValid" class="dns-verification-success">
              <strong>DNS API 在线验证通过</strong>
              <span
                >有效至
                {{
                  formatVerificationExpiry(dnsVerification.expires_at)
                }}</span
              >
              <HelpTip>
                凭据已加密保存。修改服务商、测试域名或凭据后需要重新测试，验证过期后也必须重新验证。
              </HelpTip>
            </div>

            <div
              v-if="dnsVerification && dnsVerification.test_report"
              class="dns-verification-report"
            >
              <div class="dns-report-heading">
                <strong>在线测试过程</strong>
                <HelpTip v-if="dnsVerification.test_report.record_name">
                  临时记录：{{ dnsVerification.test_report.record_name }}
                </HelpTip>
              </div>
              <div
                v-for="stage in dnsVerification.test_report.stages"
                :key="stage.name"
                class="dns-verification-stage"
                :class="`is-${stage.status}`"
              >
                <span class="dns-stage-name">{{ stage.name }}</span>
                <strong>{{ formatVerificationStageStatus(stage.status) }}</strong>
                <HelpTip v-if="stage.status === 'completed'">{{ stage.message }}</HelpTip>
                <small v-else>{{ stage.message }}</small>
              </div>
            </div>
          </fieldset>

          <fieldset v-if="currentStep === 4" class="setup-step-card" :disabled="submitting || completed">
            <legend><span>04</span>证书目录与剩余设置</legend>
            <div class="setup-grid two-columns">
              <label>
                <span>
                  证书部署根目录
                  <HelpTip>
                    填写服务器绝对路径，缺失目录将自动创建。Linux 会通过本机目录服务配置最小写入权限；支持 /ssl、/var/lib/scert/certificates 及安装时批准的存储范围，不接管其他账户或系统目录。
                  </HelpTip>
                </span>
                <input
                  id="certificate-root"
                  name="certificate_root"
                  v-model.trim="form.certificate_root"
                  maxlength="4096"
                  required
                />
              </label>
              <label v-if="isBootstrap">
                <span>证书签发环境<HelpTip>反向代理管理入口需要正式域名证书。测试证书仅用于直接 HTTPS 测试，浏览器不会公共信任该证书。</HelpTip></span>
                <select v-model="form.acme_environment">
                  <option value="staging">测试环境（证书不受公共信任）</option>
                  <option value="production">正式环境（实际申请受信任域名证书）</option>
                </select>
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
              <label v-if="form.https_mode === 'proxy'">
                <span>代理公开 HTTPS 端口</span>
                <input
                  id="proxy-https-port"
                  name="proxy_https_port"
                  v-model.number="form.proxy_https_port"
                  type="number"
                  min="1"
                  max="65535"
                  required
                />
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
                <strong>
                  初始化完成后开启管理页面
                  <HelpTip>
                    桌面端可通过此开关决定是否启用浏览器管理页面。
                  </HelpTip>
                </strong>
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
<style scoped src="./DNSSetup.css"></style>
<style src="./SetupWizardLayout.css"></style>
