<script setup>
import { computed, onMounted, ref } from 'vue'
import SetupWizard from './components/SetupWizard.vue'

const scertLogo = '/Scert.png'

const status = ref(null)
const setupState = ref(null)
const bootstrapping = ref(true)
const domains = ref([])
const loading = ref(true)
const saving = ref(false)
const error = ref('')
const notice = ref('')
const formError = ref('')
const showForm = ref(false)
const editingId = ref('')

const certificateTypeLabels = { single: '单域名', wildcard: '泛域名', san: 'SAN 多域名' }
const domainStatusLabels = { pending: '等待申请', active: '正常', error: '错误' }
const httpsModeLabels = { direct: '核心服务 HTTPS', proxy: '反向代理 HTTPS' }
const environmentLabels = { staging: '测试环境', production: '生产环境' }
const dnsProviders = [
  { value: 'cloudflare', label: 'Cloudflare' },
  { value: 'aliyun', label: '阿里云 DNS' },
  { value: 'dnspod', label: 'DNSPod' },
  { value: 'cname', label: 'CNAME 委派' },
  { value: 'huaweicloud', label: '华为云' },
  { value: 'tencentcloud', label: '腾讯云' },
  { value: 'route53', label: 'AWS Route 53' },
  { value: 'googlecloud', label: 'Google Cloud DNS' },
  { value: 'other', label: '其他服务商' },
]

const isEditing = computed(() => Boolean(editingId.value))
const formTitle = computed(() => (isEditing.value ? '编辑域名配置' : '添加托管域名'))

const form = ref(createEmptyForm())

function createEmptyForm() {
  return {
    name: '',
    certificate_type: 'single',
    sans: '',
    auto_renew: true,
    acme_environment: 'staging',
    dns_provider: '',
  }
}

function displayLabel(labels, value) {
  return labels[value] || (value ? '未知' : '—')
}

function formatDate(value) {
  if (!value) return '尚未签发'
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeZone: 'Asia/Shanghai' }).format(new Date(value))
}

function daysRemaining(value) {
  if (!value) return '待申请'
  const days = Math.ceil((new Date(value).getTime() - Date.now()) / 86400000)
  return days >= 0 ? `剩余 ${days} 天` : `已过期 ${Math.abs(days)} 天`
}

function statusClass(value) {
  return value === 'active' ? 'success' : value === 'error' ? 'danger' : 'warning'
}

function openCreate() {
  editingId.value = ''
  form.value = createEmptyForm()
  formError.value = ''
  showForm.value = true
}

function openEdit(item) {
  editingId.value = item.id
  form.value = {
    name: item.name,
    certificate_type: item.certificate_type,
    sans: (item.sans || []).filter((name) => name !== item.name).join('\n'),
    auto_renew: item.auto_renew !== false,
    acme_environment: item.acme_environment || 'staging',
    dns_provider: item.dns_provider || '',
  }
  formError.value = ''
  showForm.value = true
}

function closeForm() {
  if (!saving.value) showForm.value = false
}

function parseSANs(value) {
  return value.split(/[\n,，]/).map((item) => item.trim()).filter(Boolean)
}

async function saveDomain() {
  formError.value = ''
  notice.value = ''
  if (!form.value.name.trim()) {
    formError.value = '请输入主域名'
    return
  }
  if (form.value.certificate_type === 'san' && parseSANs(form.value.sans).length === 0) {
    formError.value = 'SAN 证书至少需要填写一个附加域名'
    return
  }
  saving.value = true
  try {
    const payload = {
      name: form.value.name,
      certificate_type: form.value.certificate_type,
      sans: parseSANs(form.value.sans),
      auto_renew: form.value.auto_renew,
      acme_environment: form.value.acme_environment,
      dns_provider: form.value.dns_provider,
    }
    const path = isEditing.value ? `/api/v1/domains/${editingId.value}` : '/api/v1/domains'
    const response = await fetch(path, {
      method: isEditing.value ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    })
    const result = await response.json().catch(() => ({}))
    if (!response.ok) throw new Error(result.error || '保存域名失败')
    showForm.value = false
    notice.value = isEditing.value ? '域名配置已更新' : '域名已添加，等待申请证书'
    await loadData()
  } catch (err) {
    formError.value = err.message || '保存域名失败'
  } finally {
    saving.value = false
  }
}

async function deleteDomain(item) {
  if (!window.confirm(`确定删除域名“${item.name}”吗？删除前请确认没有正在执行的任务。`)) return
  error.value = ''
  notice.value = ''
  try {
    const response = await fetch(`/api/v1/domains/${item.id}`, { method: 'DELETE' })
    if (!response.ok) {
      const result = await response.json().catch(() => ({}))
      throw new Error(result.error || '删除域名失败')
    }
    notice.value = '域名已删除'
    await loadData()
  } catch (err) {
    error.value = err.message || '删除域名失败'
  }
}

async function loadData() {
  loading.value = true
  error.value = ''
  try {
    const [statusResponse, domainResponse] = await Promise.all([
      fetch('/api/v1/status'),
      fetch('/api/v1/domains'),
    ])
    if (!statusResponse.ok || !domainResponse.ok) throw new Error('服务接口返回失败')
    status.value = await statusResponse.json()
    domains.value = (await domainResponse.json()).items || []
  } catch {
    error.value = '无法连接核心服务，请先启动 scert serve'
  } finally {
    loading.value = false
  }
}

async function bootstrap() {
  bootstrapping.value = true
  error.value = ''
  try {
    const response = await fetch('/api/v1/setup/status')
    const result = await response.json().catch(() => ({}))
    if (!response.ok) throw new Error(result.error || '读取初始化状态失败')
    setupState.value = result
    if (result.initialized) await loadData()
  } catch (err) {
    error.value = err.message || '无法连接核心服务，请先启动 scert serve'
  } finally {
    bootstrapping.value = false
  }
}

async function handleInitialized(result) {
  setupState.value = { ...setupState.value, initialized: true }
  notice.value = result.message || '初始化配置已保存，请重启服务。'
  await loadData()
}

onMounted(bootstrap)
</script>

<template>
  <main v-if="bootstrapping" class="bootstrap-screen">
    <img class="bootstrap-mark" :src="scertLogo" alt="Scert 标志" />
    <strong>正在检查 Scert 初始化状态……</strong>
    <p v-if="error">{{ error }}</p>
  </main>
  <SetupWizard v-else-if="setupState && !setupState.initialized" :status="setupState" @initialized="handleInitialized" />
  <main v-else class="page-shell">
    <header class="topbar">
      <div>
        <p class="eyebrow">SCERT / SSL 证书管理</p>
        <h1>证书管理总览</h1>
      </div>
      <div class="topbar-actions">
        <button class="secondary-button" type="button" @click="loadData">刷新状态</button>
        <button class="primary-button" type="button" @click="openCreate">添加域名</button>
      </div>
    </header>

    <section class="hero-card">
      <div>
        <span class="status-dot" :class="{ online: status }"></span>
        <span class="status-label">{{ status ? '核心服务运行中' : '等待连接核心服务' }}</span>
        <h2>让证书续期和部署变得可追踪</h2>
        <p>统一管理单域名、泛域名和 SAN 证书，后续将接入 lego、DNS 验证与远程部署流程。</p>
      </div>
      <img class="hero-mark" :src="scertLogo" alt="Scert 标志" />
    </section>

    <p v-if="error" class="error-banner">{{ error }}</p>
    <p v-if="notice" class="notice-banner">{{ notice }}</p>

    <section class="metrics-grid">
      <article class="metric-card"><span>服务版本</span><strong>{{ status?.version || '—' }}</strong></article>
      <article class="metric-card"><span>数据库</span><strong>{{ displayLabel({ sqlite: 'SQLite', mysql: 'MySQL' }, status?.database_driver) }}</strong></article>
      <article class="metric-card"><span>HTTPS 模式</span><strong>{{ displayLabel(httpsModeLabels, status?.https_mode) }}</strong></article>
      <article class="metric-card"><span>托管域名</span><strong>{{ domains.length }}</strong></article>
    </section>

    <section class="panel">
      <div class="panel-heading">
        <div><p class="eyebrow">域名配置</p><h2>托管域名</h2></div>
        <span class="muted">共 {{ domains.length }} 个配置</span>
      </div>
      <div v-if="loading" class="empty-state">正在读取域名状态……</div>
      <div v-else-if="domains.length === 0" class="empty-state">
        <strong>还没有托管域名</strong>
        <span>添加第一个域名，开始管理证书申请和自动续期。</span>
        <button class="primary-button" type="button" @click="openCreate">添加托管域名</button>
      </div>
      <div v-else class="domain-list">
        <article v-for="item in domains" :key="item.id" class="domain-row">
          <div class="domain-main">
            <div class="domain-title-line"><strong>{{ item.name }}</strong><span class="tag" :class="statusClass(item.status)">{{ displayLabel(domainStatusLabels, item.status) }}</span></div>
            <div class="domain-meta">
              <span>{{ displayLabel(certificateTypeLabels, item.certificate_type) }}</span>
              <span>{{ displayLabel(environmentLabels, item.acme_environment) }}</span>
              <span>{{ item.auto_renew ? '自动续期已开启' : '自动续期已关闭' }}</span>
              <span>{{ (item.sans || []).length }} 个证书标识</span>
            </div>
          </div>
          <div class="domain-expiry"><strong>{{ daysRemaining(item.expires_at) }}</strong><span>{{ formatDate(item.expires_at) }}</span></div>
          <div class="row-actions"><button class="text-button" type="button" @click="openEdit(item)">编辑</button><button class="text-button danger-text" type="button" @click="deleteDomain(item)">删除</button></div>
        </article>
      </div>
    </section>

    <div v-if="showForm" class="modal-backdrop" @click.self="closeForm">
      <section class="modal-card" role="dialog" aria-modal="true" :aria-label="formTitle">
        <div class="modal-heading"><div><p class="eyebrow">域名配置</p><h2>{{ formTitle }}</h2></div><button class="close-button" type="button" aria-label="关闭" @click="closeForm">×</button></div>
        <form @submit.prevent="saveDomain">
          <label class="field" for="domain-name"><span>主域名</span><input id="domain-name" name="name" v-model="form.name" type="text" placeholder="例如 example.com" autocomplete="off" /></label>
          <label class="field" for="certificate-type"><span>证书类型</span><select id="certificate-type" name="certificate_type" v-model="form.certificate_type"><option value="single">单域名</option><option value="wildcard">泛域名</option><option value="san">SAN 多域名</option></select></label>
          <label v-if="form.certificate_type === 'san'" class="field" for="domain-sans"><span>附加域名 <small>每行一个，也支持逗号分隔</small></span><textarea id="domain-sans" name="sans" v-model="form.sans" rows="4" placeholder="www.example.com&#10;api.example.com"></textarea></label>
          <div class="form-grid">
            <label class="field" for="acme-environment"><span>ACME 环境</span><select id="acme-environment" name="acme_environment" v-model="form.acme_environment"><option value="staging">测试环境</option><option value="production">生产环境</option></select></label>
            <label class="field" for="dns-provider"><span>DNS 验证服务商</span><select id="dns-provider" name="dns_provider" v-model="form.dns_provider"><option value="">暂不选择</option><option v-for="provider in dnsProviders" :key="provider.value" :value="provider.value">{{ provider.label }}</option></select></label>
          </div>
          <label class="switch-field" for="auto-renew"><input id="auto-renew" name="auto_renew" v-model="form.auto_renew" type="checkbox" /><span><strong>启用自动续期</strong><small>证书剩余有效期达到阈值时自动创建续期任务</small></span></label>
          <p v-if="formError" class="form-error">{{ formError }}</p>
          <div class="modal-actions"><button class="secondary-button" type="button" :disabled="saving" @click="closeForm">取消</button><button class="primary-button" type="submit" :disabled="saving">{{ saving ? '保存中……' : '保存配置' }}</button></div>
        </form>
      </section>
    </div>
  </main>
</template>
