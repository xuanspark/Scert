<script setup>
import { onMounted, ref } from 'vue'

const status = ref(null)
const domains = ref([])
const loading = ref(true)
const error = ref('')

const certificateTypeLabels = { single: '单域名', wildcard: '泛域名', san: 'SAN 多域名' }
const domainStatusLabels = { pending: '等待处理', active: '正常', error: '错误' }
const httpsModeLabels = { direct: '核心服务 HTTPS', proxy: '反向代理 HTTPS' }

function displayLabel(labels, value) {
  return labels[value] || (value ? '未知' : '—')
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
    error.value = '无法连接核心服务'
  } finally {
    loading.value = false
  }
}

onMounted(loadData)
</script>

<template>
  <main class="page-shell">
    <header class="topbar">
      <div>
        <p class="eyebrow">SCERT / SSL 证书管理</p>
        <h1>证书管理总览</h1>
      </div>
      <button class="secondary-button" type="button" @click="loadData">刷新状态</button>
    </header>

    <section class="hero-card">
      <div>
        <span class="status-dot" :class="{ online: status }"></span>
        <span class="status-label">{{ status ? '核心服务运行中' : '等待连接核心服务' }}</span>
        <h2>让证书续期和部署变得可追踪</h2>
        <p>首轮界面展示服务状态和托管域名，后续将接入 ACME、DNS 验证与远程部署流程。</p>
      </div>
      <div class="hero-mark">S</div>
    </section>

    <p v-if="error" class="error-banner">{{ error }}。请先执行 <code>scert serve</code>。</p>

    <section class="metrics-grid">
      <article class="metric-card"><span>服务版本</span><strong>{{ status?.version || '—' }}</strong></article>
      <article class="metric-card"><span>数据库</span><strong>{{ displayLabel({ sqlite: 'SQLite', mysql: 'MySQL' }, status?.database_driver) || '—' }}</strong></article>
      <article class="metric-card"><span>HTTPS 模式</span><strong>{{ displayLabel(httpsModeLabels, status?.https_mode) || '—' }}</strong></article>
      <article class="metric-card"><span>托管域名</span><strong>{{ domains.length }}</strong></article>
    </section>

    <section class="panel">
      <div class="panel-heading"><div><p class="eyebrow">域名列表</p><h2>托管域名</h2></div><span class="muted">首轮占位视图</span></div>
      <div v-if="loading" class="empty-state">正在读取域名状态……</div>
      <div v-else-if="domains.length === 0" class="empty-state">暂未添加托管域名，请通过 CLI 执行 <code>scert domain add</code>。</div>
      <div v-else class="domain-list">
        <div v-for="item in domains" :key="item.id" class="domain-row">
          <div><strong>{{ item.name }}</strong><span>{{ displayLabel(certificateTypeLabels, item.certificate_type) }} · {{ displayLabel(domainStatusLabels, item.status) }}</span></div>
          <span class="tag">待处理</span>
        </div>
      </div>
    </section>
  </main>
</template>
