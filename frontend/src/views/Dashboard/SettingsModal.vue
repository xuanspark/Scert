<script setup>
import HelpTip from "../components/HelpTip/HelpTip.vue";
import { useSettingsModal } from "./SettingsModal.js";
import MailNotificationSettings from "./MailNotificationSettings.vue";

const props = defineProps({ state: { type: Object, required: true } });
const {
  settingsForm,
  showSettings,
  settingsSaving,
  settingsError,
  dnsProviderCatalog,
  selectedDNSProvider,
  dnsTesting,
  dnsTestError,
  dnsVerificationElapsedSeconds,
  dnsVerificationTimeoutSeconds,
  dnsReadyToSave,
  canManageSystemSettings,
  handleDNSProviderChange,
  clearDNSCredentials,
  testDNS,
  closeSettings,
  saveSettings,
  dnsVerificationRequired,
} = props.state;
const { activeSection, submitSettings, navigateTabs } = useSettingsModal(saveSettings);
</script>

<template>
    <div v-if="showSettings" class="modal-backdrop settings-backdrop" @click.self="closeSettings">
      <section class="modal-card settings-modal" role="dialog" aria-modal="true" aria-label="系统设置">
        <div class="modal-heading">
          <div>
            <p class="eyebrow">系统配置</p>
            <h2 id="settings-title">系统设置</h2>
          </div>
          <button class="close-button" type="button" aria-label="关闭" @click="closeSettings">×</button>
        </div>
        <p v-if="!canManageSystemSettings" class="settings-readonly-notice">
          当前账号仅可查看系统设置，修改配置和 DNS 在线验证需要超级管理员权限。
        </p>
        <div class="settings-tabs" role="tablist" aria-labelledby="settings-title">
          <button
            id="settings-tab-service"
            type="button"
            role="tab"
            aria-controls="settings-panel-service"
            :aria-selected="activeSection === 'service'"
            :tabindex="activeSection === 'service' ? 0 : -1"
            @click="activeSection = 'service'"
            @keydown="navigateTabs"
          >
            服务监听
          </button>
          <button
            id="settings-tab-acme"
            type="button"
            role="tab"
            aria-controls="settings-panel-acme"
            :aria-selected="activeSection === 'acme'"
            :tabindex="activeSection === 'acme' ? 0 : -1"
            @click="activeSection = 'acme'"
            @keydown="navigateTabs"
          >
            ACME 设置
          </button>
          <button
            id="settings-tab-dns"
            type="button"
            role="tab"
            aria-controls="settings-panel-dns"
            :aria-selected="activeSection === 'dns'"
            :tabindex="activeSection === 'dns' ? 0 : -1"
            @click="activeSection = 'dns'"
            @keydown="navigateTabs"
          >
            DNS API 验证
          </button>
          <button
            id="settings-tab-notification"
            type="button"
            role="tab"
            aria-controls="settings-panel-notification"
            :aria-selected="activeSection === 'notification'"
            :tabindex="activeSection === 'notification' ? 0 : -1"
            @click="activeSection = 'notification'"
            @keydown="navigateTabs"
          >
            邮件通知
          </button>
        </div>
        <form novalidate @submit.prevent="submitSettings">
          <fieldset :disabled="!canManageSystemSettings" class="settings-fields">
          <section
            v-show="activeSection === 'service'"
            id="settings-panel-service"
            data-settings-section="service"
            role="tabpanel"
            aria-labelledby="settings-tab-service"
            class="settings-panel"
          >
          <h3>服务监听</h3>
          <div class="form-grid">
            <label class="field">
              <span>监听地址</span>
              <input v-model="settingsForm.server.host" required />
              </label>
            <label class="field">
              <span>服务端口</span>
              <input v-model.number="settingsForm.server.port" type="number" min="1" max="65535" required />
              </label>
          </div>
          <div class="form-grid">
            <label class="field">
              <span>HTTPS 模式</span>
              <select v-model="settingsForm.server.https_mode">
              <option value="direct">直接终止 HTTPS</option>
              <option value="proxy">反向代理 HTTPS</option>
              </select>
              </label>
            <label v-if="settingsForm.server.https_mode === 'proxy'" class="field">
              <span>代理公开 HTTPS 端口（0 为默认 443）</span>
              <input v-model.number="settingsForm.server.proxy_https_port" type="number" min="0" max="65535" required />
            </label>
            <label class="field">
              <span>证书存储路径</span>
              <input v-model="settingsForm.paths.certificate" required />
              </label>
          </div>
          <div class="form-grid">
            <label class="field">
              <span>管理页面域名</span>
              <input v-model="settingsForm.web.hostname" placeholder="例如 ssl.example.com" />
              </label>
            <label class="field">
              <span>管理页面公网 IP</span>
              <input v-model="settingsForm.web.public_ip" placeholder="例如 203.0.113.10" />
              </label>
          </div>
          <div class="settings-web-controls">
            <label class="switch-field settings-switch-field">
              <input v-model="settingsForm.web.enabled" type="checkbox" />
              <span>
                <strong>开启 Web 管理页面</strong>
                <HelpTip>关闭后停止网络 Web 管理入口，桌面端本地管理功能不受影响。</HelpTip>
              </span>
            </label>
            <HelpTip>修改此开关后需要重启 Scert 服务，新的监听状态才会生效。</HelpTip>
          </div>
          </section>
          <section
            v-show="activeSection === 'acme'"
            id="settings-panel-acme"
            data-settings-section="acme"
            role="tabpanel"
            aria-labelledby="settings-tab-acme"
            class="settings-panel"
          >
          <h3>ACME 设置</h3>
          <div class="form-grid">
            <label class="field">
              <span>ACME 环境</span>
              <select v-model="settingsForm.acme.environment">
              <option value="staging">测试环境</option>
              <option value="production">生产环境</option>
              </select>
              </label>
            <label class="field">
              <span>自动续期阈值（天）</span>
              <input v-model.number="settingsForm.acme.renewal_threshold_days" type="number" min="1" max="365" />
              </label>
          </div>
          <div class="form-grid">
            <label class="field">
              <span>常规检查间隔（天）</span>
              <input v-model.number="settingsForm.acme.renewal_check_interval_days" type="number" min="1" max="30" />
              </label>
            <label class="field">
              <span>临近到期检查阈值（天）</span>
              <input v-model.number="settingsForm.acme.renewal_near_expiry_days" type="number" min="1" max="365" />
              </label>
          </div>
          <div class="form-grid">
            <label class="field">
              <span>紧急续期阈值（天）</span>
              <input v-model.number="settingsForm.acme.renewal_urgent_threshold_days" type="number" min="1" max="365" />
              </label>
            <label class="field">
              <span>紧急续期最大尝试次数</span>
              <input v-model.number="settingsForm.acme.renewal_max_retries" type="number" min="1" max="10" />
              </label>
          </div>
          <div class="form-grid">
            <label class="field">
              <span>续期重试间隔（分钟）</span>
              <input v-model.number="settingsForm.acme.renewal_retry_interval_mins" type="number" min="1" max="1440" />
              </label>
            <HelpTip>剩余有效期低于自动续期阈值时触发续期；低于紧急阈值时按配置重试。</HelpTip>
          </div>
          <div class="form-grid">
            <label class="field">
              <span>DNS 传播等待（秒）</span>
              <input
                v-model.number="settingsForm.acme.propagation_timeout_secs"
                type="number"
                min="1"
              />
            </label>
            <label class="field">
              <span>DNS 检测间隔（秒）</span>
              <input
                v-model.number="settingsForm.acme.propagation_polling_interval_secs"
                type="number"
                min="1"
              />
            </label>
          </div>
          <HelpTip>
            阿里云 RAM 权限验证最多等待 6 秒：系统会创建、查询并清理临时 TXT
            记录。以上 300 秒和 5 秒用于实际申请证书时的公网 DNS-01
            传播等待与轮询。
          </HelpTip>
          </section>
          <section
            v-show="activeSection === 'dns'"
            id="settings-panel-dns"
            data-settings-section="dns"
            role="tabpanel"
            aria-labelledby="settings-tab-dns"
            class="settings-panel"
          >
          <h3>DNS API 验证</h3>
          <HelpTip>
            <span>输入域名后，系统将使用当前阿里云账号立即创建、查询并清理临时 TXT 记录，以验证该域名的实际 DNS 操作权限。</span>
            <span>凭据只会在本次在线测试请求中提交，系统仅保存加密引用。</span>
          </HelpTip>
          <div class="form-grid">
            <label class="field" for="settings-dns-provider">
              <span>DNS 服务商</span>
              <select
                id="settings-dns-provider"
                v-model="settingsForm.dns.provider"
                @change="handleDNSProviderChange"
              >
                <option value="">暂不配置</option>
                <option
                  v-for="provider in dnsProviderCatalog"
                  :key="provider.id"
                  :value="provider.id"
                  :disabled="!provider.available"
                >
                  {{ provider.name }}{{ provider.available ? "" : "（暂不可用）" }}
                </option>
              </select>
            </label>
            <label class="field" for="settings-dns-test-domain">
              <span>在线测试域名</span>
              <input
                id="settings-dns-test-domain"
                v-model="settingsForm.dns.test_domain"
                placeholder="例如 example.com"
                autocomplete="off"
              />
            </label>
          </div>
          <HelpTip v-if="selectedDNSProvider">
            {{ selectedDNSProvider.description }}
          </HelpTip>
          <p
            v-if="selectedDNSProvider && !selectedDNSProvider.available"
            class="form-error"
          >
            当前服务商适配器暂未开放，无法进行在线测试。
          </p>
          <div v-if="selectedDNSProvider" class="dns-credentials-grid">
            <label
              v-for="field in selectedDNSProvider.credential_fields"
              :key="field.name"
              class="field"
              :for="`settings-dns-${field.name}`"
            >
              <span>
                {{ field.label }}
                <small v-if="field.required && dnsVerificationRequired">必填</small>
              </span>
              <input
                :id="`settings-dns-${field.name}`"
                v-model="settingsForm.dns.credentials[field.name]"
                :type="field.secret ? 'password' : 'text'"
                :placeholder="field.placeholder"
                :required="field.required && dnsVerificationRequired"
                autocomplete="off"
              />
            </label>
          </div>
          <div v-if="selectedDNSProvider" class="dns-test-actions">
            <button
              class="secondary-button"
              type="button"
              :disabled="dnsTesting || settingsSaving || !selectedDNSProvider.available"
              @click="testDNS"
            >
              {{ dnsTesting ? "测试中……" : "在线测试 DNS API" }}
            </button>
            <button
              class="text-button"
              type="button"
              :disabled="dnsTesting || settingsSaving"
              @click="clearDNSCredentials"
            >
              清空凭据
            </button>
            <span
              class="dns-verification-status"
              :class="settingsForm.dns.verification_state"
            >
              {{ settingsForm.dns.verification_state === "verified" ? "已验证" : "待验证" }}
            </span>
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
          <p v-if="dnsTestError" class="form-error">{{ dnsTestError }}</p>
          <div
            v-if="settingsForm.dns.test_report?.stages?.length"
            class="dns-test-report"
          >
            <div class="dns-test-report-heading">
              <strong>在线测试结果</strong>
              <span v-if="settingsForm.dns.test_report.record_name">
                临时记录：{{ settingsForm.dns.test_report.record_name }}
              </span>
            </div>
            <div
              v-for="stage in settingsForm.dns.test_report.stages"
              :key="stage.name"
              class="dns-test-stage"
            >
              <span class="dns-stage-indicator" :class="stage.status">
              </span>
              <div>
                <strong>{{ stage.name }}</strong>
                <span>{{ stage.message }}</span>
              </div>
            </div>
          </div>
          <HelpTip v-if="!dnsReadyToSave">
            DNS 服务商发生变化或尚未配置时，必须先完成 DNS API 在线测试。
          </HelpTip>
          </section>
          <section
            v-show="activeSection === 'notification'"
            id="settings-panel-notification"
            data-settings-section="notification"
            role="tabpanel"
            aria-labelledby="settings-tab-notification"
            class="settings-panel"
          >
            <h3>邮件通知</h3>
            <MailNotificationSettings :notification="settingsForm.notification" />
          </section>
          <p v-if="settingsError" class="form-error">{{ settingsError }}</p>
          <div class="modal-actions">
            <button class="secondary-button" type="button" :disabled="settingsSaving" @click="closeSettings">取消</button>
            <button
              class="primary-button"
              type="submit"
              :disabled="settingsSaving || !dnsReadyToSave"
            >
              {{ settingsSaving ? "保存中……" : "保存设置" }}
            </button>
          </div>
          </fieldset>
        </form>
      </section>
    </div>
</template>

<style scoped src="./Dashboard.css"></style>
<style scoped src="./SettingsModal.css"></style>
