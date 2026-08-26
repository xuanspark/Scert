<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'

const scertLogo = '/Scert.png'

const props = defineProps({ status: { type: Object, required: true } })
const emit = defineEmits(['initialized'])

const submitting = ref(false)
const error = ref('')
const resultMessage = ref('')
const dnsCatalogLoading = ref(true)
const dnsCatalogError = ref('')
const dnsTesting = ref(false)
const dnsTestError = ref('')
const dnsProviders = ref([])
const dnsCredentials = ref({})
const dnsTestDomain = ref(props.status.web_hostname || '')
const dnsVerification = ref(null)
const verificationClock = ref(Date.now())
let verificationTimer
const targetType = ref(props.status.web_public_ip ? 'ip' : 'hostname')
const form = ref({
  admin_username: '',
  admin_password: '',
  confirm_password: '',
  server_port: props.status.server_port || 8080,
  reset_service_port: props.status.reset_service_port || 8091,
  email_provider: '',
  email_endpoint: '',
  recipients: '',
  dns_provider: props.status.dns_provider || 'cloudflare',
  certificate_root: props.status.certificate_root || '',
  web_hostname: props.status.web_hostname || '',
  web_public_ip: props.status.web_public_ip || '',
  web_enabled: props.status.web_enabled !== false,
  https_mode: props.status.https_mode || 'direct',
})

const targetLabel = computed(() => (targetType.value === 'hostname' ? 'Web 管理域名' : '公网 IP 地址'))
const selectedDNSProvider = computed(() => dnsProviders.value.find((provider) => provider.id === form.value.dns_provider) || null)
const dnsVerificationValid = computed(() => {
  verificationClock.value
  if (!dnsVerification.value?.verification_token || !dnsVerification.value?.credential_ref) return false
  return new Date(dnsVerification.value.expires_at).getTime() > Date.now()
})

function invalidateDNSVerification() {
  dnsVerification.value = null
  dnsTestError.value = ''
}

function formatVerificationExpiry(value) {
  if (!value) return ''
  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'medium',
    timeZone: 'Asia/Shanghai',
  }).format(new Date(value))
}

async function loadDNSProviders() {
  dnsCatalogLoading.value = true
  dnsCatalogError.value = ''
  try {
    const response = await fetch('/api/v1/setup/dns/providers')
    const result = await response.json().catch(() => ({}))
    if (!response.ok) throw new Error(result.error || '读取 DNS 服务商列表失败')
    dnsProviders.value = Array.isArray(result.items) ? result.items : []
    if (!dnsProviders.value.some((provider) => provider.id === form.value.dns_provider)) {
      form.value.dns_provider = dnsProviders.value.find((provider) => provider.available)?.id || ''
    }
  } catch (err) {
    dnsCatalogError.value = err.message || '读取 DNS 服务商列表失败'
  } finally {
    dnsCatalogLoading.value = false
  }
}

function credentialPayload() {
  const payload = {}
  for (const field of selectedDNSProvider.value?.credential_fields || []) {
    const value = String(dnsCredentials.value[field.name] || '').trim()
    if (value) payload[field.name] = value
  }
  return payload
}

async function testDNS() {
  dnsTestError.value = ''
  resultMessage.value = ''
  invalidateDNSVerification()
  const provider = selectedDNSProvider.value
  if (!provider) {
    dnsTestError.value = '请选择 DNS 服务商。'
    return
  }
  if (!provider.available) {
    dnsTestError.value = '该 DNS 服务商在线验证适配器尚未开放。'
    return
  }
  const testDomain = dnsTestDomain.value.trim()
  if (!testDomain) {
    dnsTestError.value = '请填写 DNS 权限测试域名。'
    return
  }
  for (const field of provider.credential_fields || []) {
    if (field.required && !String(dnsCredentials.value[field.name] || '').trim()) {
      dnsTestError.value = `请填写${field.label}。`
      return
    }
  }

  dnsTesting.value = true
  try {
    const response = await fetch('/api/v1/setup/dns/test', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        provider: provider.id,
        test_domain: testDomain,
        credentials: credentialPayload(),
      }),
    })
    const result = await response.json().catch(() => ({}))
    if (!response.ok) throw new Error(result.error || 'DNS API 在线验证失败')
    dnsVerification.value = result
  } catch (err) {
    dnsTestError.value = err.message || 'DNS API 在线验证失败'
  } finally {
    dnsTesting.value = false
  }
}

function recipients() {
  return form.value.recipients.split(/[\n,，]/).map((item) => item.trim()).filter(Boolean)
}

async function submit() {
  error.value = ''
  resultMessage.value = ''
  if (form.value.admin_password.length < 8) {
    error.value = '超级管理员密码至少需要 8 个字符。'
    return
  }
  if (form.value.admin_password !== form.value.confirm_password) {
    error.value = '两次输入的超级管理员密码不一致。'
    return
  }
  if (Number(form.value.server_port) === Number(form.value.reset_service_port)) {
    error.value = '主服务端口与密码重置服务端口不能相同。'
    return
  }
  if (!dnsVerificationValid.value) {
    error.value = '必须先完成 DNS API 在线验证；验证结果已过期时请重新测试。'
    return
  }
  const hostname = targetType.value === 'hostname' ? form.value.web_hostname.trim() : ''
  const publicIP = targetType.value === 'ip' ? form.value.web_public_ip.trim() : ''
  if (!hostname && !publicIP) {
    error.value = `请填写${targetLabel.value}。`
    return
  }

  submitting.value = true
  try {
    const response = await fetch('/api/v1/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        admin_username: form.value.admin_username,
        admin_password: form.value.admin_password,
        server_port: Number(form.value.server_port),
        reset_service_port: Number(form.value.reset_service_port),
        email_provider: form.value.email_provider,
        email_endpoint: form.value.email_endpoint,
        recipients: recipients(),
        dns_provider: form.value.dns_provider,
        dns_credential_ref: dnsVerification.value.credential_ref,
        dns_verification_token: dnsVerification.value.verification_token,
        dns_test_domain: dnsTestDomain.value.trim(),
        certificate_root: form.value.certificate_root,
        web_hostname: hostname,
        web_public_ip: publicIP,
        web_enabled: form.value.web_enabled,
        https_mode: form.value.https_mode,
      }),
    })
    const result = await response.json().catch(() => ({}))
    if (!response.ok) throw new Error(result.error || '保存初始化配置失败')
    resultMessage.value = result.message || '初始化配置已保存。'
    emit('initialized', result)
  } catch (err) {
    error.value = err.message || '保存初始化配置失败'
  } finally {
    submitting.value = false
  }
}

watch(() => form.value.dns_provider, () => {
  dnsCredentials.value = {}
  invalidateDNSVerification()
})

watch(dnsCredentials, invalidateDNSVerification, { deep: true })
watch(dnsTestDomain, invalidateDNSVerification)

onMounted(() => {
  loadDNSProviders()
  verificationTimer = window.setInterval(() => { verificationClock.value = Date.now() }, 30000)
})

onUnmounted(() => {
  if (verificationTimer) window.clearInterval(verificationTimer)
})
</script>

<template>
  <main class="setup-shell">
    <section class="setup-intro">
      <img class="brand-mark" :src="scertLogo" alt="Scert 标志" />
      <p class="setup-eyebrow">SCERT 首次运行</p>
      <h1>配置证书管理服务</h1>
      <p>Scert 与 Scert-admin 将共享本次配置。初始化接口仅允许从本机访问，完成后请重启服务。</p>
      <div class="service-summary">
        <span><strong>Scert</strong> 证书管理与 Web 服务</span>
        <span><strong>Scert-admin</strong> 超级管理员密码重置</span>
      </div>
      <div class="warning-card">
        <strong>当前开发阶段说明</strong>
        <span>DNS API 在线测试与凭据加密存储已经接入；go-acme/lego 证书签发和 HTTPS 切换仍在后续任务中，本页不会伪报证书申请成功。</span>
      </div>
    </section>

    <section class="setup-card">
      <header>
        <div><p class="setup-eyebrow">初始化向导</p><h2>基础配置</h2></div>
        <span class="local-badge">仅限本机</span>
      </header>

      <form id="setup-form" name="setup_form" @submit.prevent="submit">
        <fieldset>
          <legend><span>01</span>超级管理员</legend>
          <div class="setup-grid three-columns">
            <label><span>用户名</span><input id="admin-username" name="admin_username" v-model.trim="form.admin_username" required autocomplete="username" placeholder="例如 scert-admin" /></label>
            <label><span>密码</span><input id="admin-password" name="admin_password" v-model="form.admin_password" required minlength="8" type="password" autocomplete="new-password" placeholder="至少 8 个字符" /></label>
            <label><span>确认密码</span><input id="confirm-password" name="confirm_password" v-model="form.confirm_password" required minlength="8" type="password" autocomplete="new-password" placeholder="再次输入密码" /></label>
          </div>
        </fieldset>

        <fieldset>
          <legend><span>02</span>服务与通知</legend>
          <div class="setup-grid two-columns">
            <label><span>Scert 主服务端口</span><input id="server-port" name="server_port" v-model.number="form.server_port" required min="1" max="65535" type="number" /></label>
            <label><span>Scert-admin 重置端口</span><input id="reset-service-port" name="reset_service_port" v-model.number="form.reset_service_port" required min="1" max="65535" type="number" /></label>
            <label><span>第三方邮箱服务标识</span><input id="email-provider" name="email_provider" v-model.trim="form.email_provider" placeholder="例如 company-mail-api" /></label>
            <label><span>邮件 API 地址</span><input id="email-endpoint" name="email_endpoint" v-model.trim="form.email_endpoint" type="url" placeholder="https://mail.example.com/send" /></label>
          </div>
          <label><span>通知收件人</span><input id="recipients" name="recipients" v-model="form.recipients" placeholder="多个地址使用逗号分隔" /></label>
        </fieldset>

        <fieldset>
          <legend><span>03</span>DNS 验证</legend>
          <div class="setup-grid two-columns">
            <label><span>DNS 服务商</span><select id="dns-provider" name="dns_provider" v-model="form.dns_provider" required :disabled="dnsCatalogLoading"><option v-if="dnsCatalogLoading" value="">正在读取服务商……</option><option v-for="provider in dnsProviders" :key="provider.id" :value="provider.id">{{ provider.name }}{{ provider.available ? '' : '（适配器待开放）' }}</option></select></label>
            <label><span>DNS 权限测试域名</span><input id="dns-test-domain" name="dns_test_domain" v-model.trim="dnsTestDomain" required placeholder="例如 example.com" /><small>使用域名时可填写 Web 管理域名；使用公网 IP 时必须另填一个由当前 DNS 账号管理的域名。</small></label>
          </div>
          <p v-if="dnsCatalogError" class="inline-error">{{ dnsCatalogError }}</p>
          <div v-if="selectedDNSProvider" class="provider-description" :class="{ unavailable: !selectedDNSProvider.available }">
            <strong>{{ selectedDNSProvider.available ? '在线验证可用' : '当前仅提供字段模型' }}</strong>
            <span>{{ selectedDNSProvider.description }}</span>
          </div>
          <div v-if="selectedDNSProvider" class="setup-grid two-columns credential-grid">
            <label v-for="field in selectedDNSProvider.credential_fields" :key="field.name">
              <span>{{ field.label }}{{ field.required ? '（必填）' : '（可选）' }}</span>
              <textarea v-if="field.name.includes('json')" :id="`dns-credential-${field.name}`" :name="`dns_credential_${field.name}`" v-model="dnsCredentials[field.name]" :required="field.required" :placeholder="field.placeholder" rows="4" autocomplete="off"></textarea>
              <input v-else :id="`dns-credential-${field.name}`" :name="`dns_credential_${field.name}`" v-model="dnsCredentials[field.name]" :required="field.required" :type="field.secret ? 'password' : 'text'" :placeholder="field.placeholder" autocomplete="off" />
            </label>
          </div>
          <div class="dns-test-actions">
            <button class="dns-test-button" type="button" :disabled="dnsTesting || dnsCatalogLoading || !selectedDNSProvider?.available" @click="testDNS">{{ dnsTesting ? '正在创建并清理测试记录……' : '测试 DNS API' }}</button>
            <span>测试会临时创建 DNS-01 TXT 记录并自动清理，明文凭据不会写入普通配置文件。</span>
          </div>
          <p v-if="dnsTestError" class="inline-error">{{ dnsTestError }}</p>
          <div v-if="dnsVerificationValid" class="dns-verification-success">
            <strong>DNS API 在线验证通过</strong>
            <span>凭据已加密保存，本次验证结果有效至 {{ formatVerificationExpiry(dnsVerification.expires_at) }}。修改服务商、测试域名或凭据后需要重新测试。</span>
          </div>
        </fieldset>

        <fieldset>
          <legend><span>04</span>Web 与证书目录</legend>
          <div class="target-switch"><button type="button" :class="{ active: targetType === 'hostname' }" @click="targetType = 'hostname'">使用域名</button><button type="button" :class="{ active: targetType === 'ip' }" @click="targetType = 'ip'">使用公网 IP</button></div>
          <div class="setup-grid two-columns">
            <label v-if="targetType === 'hostname'"><span>Web 管理域名</span><input id="web-hostname" name="web_hostname" v-model.trim="form.web_hostname" required placeholder="scert.example.com" /></label>
            <label v-else><span>公网 IP 地址</span><input id="web-public-ip" name="web_public_ip" v-model.trim="form.web_public_ip" required placeholder="203.0.113.10" /></label>
            <label><span>证书部署根目录</span><input id="certificate-root" name="certificate_root" v-model.trim="form.certificate_root" required /></label>
            <label><span>HTTPS 模式</span><select id="https-mode" name="https_mode" v-model="form.https_mode"><option value="direct">默认：核心服务直接终止 HTTPS</option><option value="proxy">高级：Nginx/Caddy 终止公网 HTTPS</option></select></label>
          </div>
          <label class="confirm-row"><input id="web-enabled" name="web_enabled" v-model="form.web_enabled" type="checkbox" /><span><strong>初始化完成后开启 Web 页面</strong><small>桌面端可通过此开关决定是否启用浏览器管理页面。</small></span></label>
        </fieldset>

        <p v-if="error" class="setup-error">{{ error }}</p>
        <p v-if="resultMessage" class="setup-success">{{ resultMessage }}</p>
        <footer><span>初始化程序将自动配置本机防火墙；云安全组仍需放行主服务端口。Scert-admin 只允许本机访问。</span><button type="submit" :disabled="submitting || dnsTesting">{{ submitting ? '正在保存……' : '保存初始化配置' }}</button></footer>
      </form>
    </section>
  </main>
</template>

<style scoped>
.setup-shell { min-height: 100dvh; display: grid; grid-template-columns: minmax(280px, .72fr) minmax(640px, 1.4fr); background: #f4f7fb; color: #172033; }
.setup-intro { position: sticky; top: 0; height: 100dvh; display: flex; flex-direction: column; justify-content: center; padding: clamp(34px, 5vw, 78px); overflow: hidden; color: #fff; background: radial-gradient(circle at 18% 12%, rgba(111,160,255,.6), transparent 32%), linear-gradient(145deg, #132752, #244c92 62%, #2e64b8); }
.brand-mark { display: block; width: 96px; height: 96px; margin-bottom: 22px; object-fit: contain; filter: drop-shadow(0 14px 22px rgba(0,0,0,.2)); }
.setup-eyebrow { margin: 0 0 8px; color: #446a9f; font-size: 12px; font-weight: 900; letter-spacing: .14em; text-transform: uppercase; }
.setup-intro .setup-eyebrow { color: #bad2ff; }
.setup-intro h1 { margin: 0 0 18px; font-size: clamp(34px, 4vw, 58px); line-height: 1.08; }
.setup-intro > p:not(.setup-eyebrow) { max-width: 580px; margin: 0; color: #d7e4fb; line-height: 1.8; }
.service-summary { display: grid; gap: 10px; margin-top: 34px; }
.service-summary span { padding: 12px 15px; border: 1px solid rgba(255,255,255,.17); border-radius: 12px; background: rgba(255,255,255,.08); color: #dce8fb; font-size: 13px; }
.service-summary strong { color: #fff; }
.warning-card { display: grid; gap: 7px; margin-top: 28px; padding: 16px; border-left: 3px solid #ffd37a; background: rgba(8,21,51,.25); color: #dce8fb; font-size: 12px; line-height: 1.65; }
.warning-card strong { color: #ffe5ad; }
.setup-card { align-self: start; width: min(100%, 1080px); margin: 0 auto; padding: clamp(28px, 4vw, 58px); }
.setup-card > header { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 28px; }
.setup-card h2 { margin: 0; font-size: 32px; }
.local-badge { padding: 7px 11px; border-radius: 99px; color: #2f63b2; background: #e5efff; font-size: 12px; font-weight: 800; }
form { display: grid; gap: 18px; }
fieldset { min-width: 0; margin: 0; padding: 22px; border: 1px solid #dde5f0; border-radius: 18px; background: rgba(255,255,255,.92); box-shadow: 0 10px 35px rgba(31,50,87,.04); }
legend { padding: 0 8px; color: #243653; font-size: 15px; font-weight: 900; }
legend span { display: inline-grid; place-items: center; width: 28px; height: 28px; margin-right: 9px; border-radius: 9px; color: #fff; background: #315faa; font-size: 11px; }
.setup-grid { display: grid; gap: 14px; }
.three-columns { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.two-columns { grid-template-columns: repeat(2, minmax(0, 1fr)); }
label { display: grid; gap: 7px; margin-top: 14px; color: #34445f; font-size: 12px; font-weight: 800; }
input, select, textarea { min-width: 0; width: 100%; padding: 0 12px; border: 1px solid #d4deeb; border-radius: 10px; outline: 0; color: #172033; background: #fbfcfe; font: inherit; font-weight: 500; }
input, select { height: 43px; }
textarea { min-height: 96px; padding-top: 11px; padding-bottom: 11px; resize: vertical; line-height: 1.5; }
input:focus, select:focus, textarea:focus { border-color: #4f7ed5; box-shadow: 0 0 0 3px rgba(79,126,213,.12); }
label > small { color: #53647b; font-weight: 500; line-height: 1.5; }
.provider-description, .dns-verification-success { display: grid; gap: 5px; margin-top: 14px; padding: 13px 14px; border-radius: 12px; font-size: 12px; line-height: 1.55; }
.provider-description { color: #435571; background: #f4f7fb; }
.provider-description.unavailable { color: #805f20; background: #fff8e8; }
.provider-description strong, .dns-verification-success strong { font-size: 13px; }
.credential-grid { margin-top: 2px; }
.dns-test-actions { display: flex; align-items: center; gap: 14px; margin-top: 16px; color: #53647b; font-size: 12px; line-height: 1.5; }
.dns-test-button { flex: 0 0 auto; min-width: 160px; padding: 10px 15px; border: 0; border-radius: 10px; color: #fff; background: #315faa; font-weight: 900; }
.inline-error { margin: 13px 0 0; padding: 11px 13px; border: 1px solid #efc6c6; border-radius: 10px; color: #a33d3d; background: #fff1f1; font-size: 12px; }
.dns-verification-success { border: 1px solid #bfe7d1; color: #24734b; background: #effbf4; }
.confirm-row { grid-template-columns: auto 1fr; align-items: flex-start; padding: 13px 14px; border-radius: 12px; background: #f6f8fc; }
.confirm-row input { width: 18px; height: 18px; margin-top: 2px; accent-color: #315faa; }
.confirm-row strong, .confirm-row small { display: block; }
.confirm-row small { margin-top: 5px; color: #53647b; font-weight: 500; line-height: 1.5; }
.target-switch { display: inline-flex; gap: 4px; margin-top: 14px; padding: 4px; border-radius: 10px; background: #edf2f8; }
.target-switch button { padding: 8px 13px; border: 0; border-radius: 8px; color: #4d6078; background: transparent; font-weight: 800; }
.target-switch button.active { color: #274f91; background: #fff; box-shadow: 0 3px 10px rgba(36,64,107,.1); }
.setup-error, .setup-success { margin: 0; padding: 13px 16px; border-radius: 11px; font-size: 13px; }
.setup-error { border: 1px solid #efc6c6; color: #a33d3d; background: #fff1f1; }
.setup-success { border: 1px solid #bfe7d1; color: #24734b; background: #effbf4; }
footer { display: flex; justify-content: space-between; align-items: center; gap: 24px; padding: 5px 2px 30px; color: #53647b; font-size: 12px; line-height: 1.6; }
footer button { flex: 0 0 auto; min-width: 170px; padding: 12px 18px; border: 0; border-radius: 11px; color: #fff; background: #315faa; font-weight: 900; box-shadow: 0 8px 20px rgba(49,95,170,.22); }
button { cursor: pointer; }
button:disabled { cursor: wait; opacity: .62; }
@media (max-width: 1050px) { .setup-shell { grid-template-columns: 1fr; } .setup-intro { position: static; height: auto; min-height: 330px; padding: 44px clamp(24px, 6vw, 64px); } .setup-intro h1 { font-size: 40px; } .service-summary { grid-template-columns: 1fr 1fr; } .warning-card { margin-top: 18px; } }
@media (max-width: 760px) { .setup-intro { min-height: auto; padding: 34px 20px; } .brand-mark { width: 72px; height: 72px; margin-bottom: 18px; } .setup-intro h1 { font-size: 34px; } .service-summary, .three-columns, .two-columns { grid-template-columns: 1fr; } .setup-card { padding: 28px 15px; } .setup-card h2 { font-size: 27px; } fieldset { padding: 17px 14px; } .dns-test-actions { align-items: stretch; flex-direction: column; } .dns-test-button { width: 100%; } footer { align-items: stretch; flex-direction: column; } footer button { width: 100%; } }
</style>
