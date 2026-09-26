<script setup>
import UserManagement from "../UserManagement/UserManagement.vue";
import DNSAccounts from "../DNSAccounts/DNSAccounts.vue";
import SettingsModal from "./SettingsModal.vue";
import DomainModal from "./DomainModal.vue";
import ServerModal from "./ServerModal.vue";
import RuntimeOverview from "./RuntimeOverview.vue";
import SSHKeyModal from "./SSHKeyModal.vue";
import DashboardNavigation from "./DashboardNavigation.vue";
import { useDashboardNavigation } from "./DashboardNavigation.js";
import { useDashboard } from "./Dashboard.js";
import DeploymentResult from "./DeploymentResult.vue";

const dashboard = useDashboard();
const {
  status,
  currentUser,
  domains,
  servers,
  sshKeys,
  certificates,
  tasks,
  auditLogs,
  settings,
  settingsForm,
  domainServers,
  domainPage,
  domainPageSize,
  domainPageSizeOptions,
  domainPageCount,
  paginatedDomains,
  domainPageStart,
  domainPageEnd,
  previousDomainPage,
  nextDomainPage,
  loading,
  saving,
  error,
  notice,
  formError,
  showForm,
  editingId,
  showServerForm,
  serverEditingId,
  serverSaving,
  serverFormError,
  showSSHKeyForm,
  sshKeySaving,
  sshKeyFormError,
  certificateTypeLabels,
  domainStatusLabels,
  taskStatusLabels,
  taskTypeLabel,
  auditActionLabel,
  taskStageLabel,
  attemptStatusLabel,
  httpsModeLabels,
  dnsProviders,
  environmentLabels,
  webServerLabels,
  isEditing,
  formTitle,
  form,
  serverForm,
  sshKeyForm,
  sshKeyPlatform,
  sshKeyPassphraseVisible,
  sshKeyCommandUsername,
  sshKeyCommandHost,
  sshKeyFileName,
  sshKeyPath,
  sshKeyPrivatePath,
  sshKeyPublicPath,
  showSettings,
  showCertificateImport,
  importDomain,
  importSaving,
  importError,
  importResult,
  openCertificateImport,
  closeCertificateImport,
  selectCertificateImportFile,
  importCertificate,
  settingsSaving,
  settingsError,
  dnsProviderCatalog,
  selectedDNSProvider,
  dnsTesting,
  dnsTestError,
  dnsVerificationElapsedSeconds,
  dnsVerificationTimeoutSeconds,
  dnsReadyToSave,
  handleDNSProviderChange,
  clearDNSCredentials,
  testDNS,
  sshKeyCommands,
  sshPublicKeyCommands,
  defaultCertificatePath,
  defaultCertificateChainPath,
  defaultPrivateKeyPath,
  openCreate,
  openEdit,
  closeForm,
  openServerCreate,
  openServerEdit,
  closeServerForm,
  openSSHKeyCreate,
  closeSSHKeyForm,
  importSSHKeyFile,
  copySSHKeyCommand,
  saveSSHKey,
  deleteSSHKey,
  saveServer,
  deleteServer,
  saveDomain,
  deleteDomain,
  loadData,
  openSettings,
  closeSettings,
  saveSettings,
  issueDomain,
  validateDomain,
  deployDomain,
  runRenewalNow,
  cancelTask,
  openTaskDetails,
  closeTaskDetails,
  showTaskDetails,
  taskDetails,
  taskAttempts,
  taskDetailsLoading,
  taskDetailsError,
  deploymentResult,
  serverAuthLabel,
  displayLabel,
  formatDate,
  daysRemaining,
  statusClass,
} = dashboard;
const { activeSection, activeLabel, sections, selectSection } = useDashboardNavigation(currentUser);
</script>

<template>
  <main class="page-shell">
    <header class="topbar">
      <div>
        <p class="eyebrow">SCERT / SSL 证书管理</p>
        <h1>证书管理总览</h1>
      </div>
      <div class="topbar-actions">
        <button class="secondary-button" type="button" @click="loadData">
          刷新状态
        </button>
        <button v-if="activeSection === 'activity'" class="secondary-button" type="button" @click="runRenewalNow">
          立即检查续期
        </button>
        <button
          class="secondary-button"
          type="button"
          @click="openSSHKeyCreate"
          v-if="activeSection === 'keys'"
        >
          添加 SSH 私钥
        </button>
        <button
          class="secondary-button"
          type="button"
          @click="openServerCreate"
          v-if="activeSection === 'servers'"
        >
          添加服务器
        </button>
        <button v-if="activeSection === 'domains'" class="primary-button" type="button" @click="openCreate">
          添加域名
        </button>
      </div>
    </header>

    <div class="dashboard-messages" aria-live="polite">
      <p v-if="error" class="error-banner">{{ error }}</p>
      <p v-if="notice" class="notice-banner">{{ notice }}</p>
    </div>

    <div class="dashboard-workspace">
      <div
        :key="activeSection"
        class="dashboard-content"
        role="region"
        :aria-label="activeLabel"
        tabindex="0"
      >
    <div v-if="activeSection === 'overview'" class="overview-section">

    <section class="panel overview-panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">首页概览</p>
          <h2>基本信息</h2>
        </div>
        <span class="muted">当前登录与托管资源</span>
      </div>
      <div class="overview-summary-grid">
        <article class="overview-summary-card">
          <span>当前用户</span>
          <strong>{{ currentUser?.account || "—" }}</strong>
          <small>{{ currentUser?.email || "未绑定邮箱" }}</small>
        </article>
        <article class="overview-summary-card">
          <span>服务状态</span>
          <strong class="overview-status">
            <i class="status-dot" :class="{ online: status }"></i>
            {{ status ? "核心服务运行中" : "等待连接核心服务" }}
          </strong>
          <small>版本 {{ status?.version || "—" }}</small>
        </article>
        <article class="overview-summary-card">
          <span>托管域名</span>
          <strong>{{ domains.length }} 个</strong>
          <small>已配置的证书管理对象</small>
        </article>
      </div>
      <div class="overview-details-grid">
        <article class="overview-detail-card">
          <span>服务版本</span>
          <strong>{{ status?.version || "—" }}</strong>
        </article>
        <article class="overview-detail-card">
          <span>数据库</span>
          <strong>{{
            displayLabel(
              { sqlite: "SQLite", mysql: "MySQL" },
              status?.database_driver,
            )
          }}</strong>
        </article>
        <article class="overview-detail-card">
          <span>HTTPS 模式</span>
          <strong>{{ displayLabel(httpsModeLabels, status?.https_mode) }}</strong>
        </article>
        <article class="overview-detail-card">
          <span>托管域名</span>
          <strong>{{ domains.length }} 个</strong>
        </article>
      </div>

      <div class="overview-domain-heading">
        <div>
          <h3>当前托管域名</h3>
          <span class="muted">证书状态、到期时间和部署服务器</span>
        </div>
        <label class="overview-page-size">
          <span>每页显示</span>
          <select v-model.number="domainPageSize" aria-label="每页显示条数">
            <option v-for="size in domainPageSizeOptions" :key="size" :value="size">
              {{ size }} 条
            </option>
          </select>
        </label>
      </div>
      <div v-if="loading" class="empty-state compact-empty">正在读取域名状态……</div>
      <div v-else-if="domains.length === 0" class="empty-state compact-empty">
        <strong>还没有托管域名</strong>
        <span>添加第一个域名，开始管理证书申请和自动续期。</span>
      </div>
      <div v-else class="overview-domain-list">
        <article v-for="item in paginatedDomains" :key="item.id" class="overview-domain-row">
          <div class="overview-domain-main">
            <strong>{{ item.name }}</strong>
            <small>{{ displayLabel(certificateTypeLabels, item.certificate_type) }}</small>
          </div>
          <div class="overview-domain-field">
            <span>证书状态</span>
            <strong class="overview-certificate-status">
              <span class="tag" :class="statusClass(item.status)">
                {{ displayLabel(domainStatusLabels, item.status) }}
              </span>
            </strong>
          </div>
          <div class="overview-domain-field">
            <span>到期时间</span>
            <strong>{{ formatDate(item.expires_at) }}</strong>
            <small>{{ daysRemaining(item.expires_at) }}</small>
          </div>
          <div class="overview-domain-field">
            <span>部署服务器</span>
            <strong>
              {{
                (domainServers[item.id] || [])
                  .map((server) => server.name)
                  .join("、") || "未配置"
              }}
            </strong>
            <small>{{ item.auto_renew ? "自动续期已开启" : "自动续期已关闭" }}</small>
          </div>
        </article>
      </div>
      <div v-if="domains.length > 0" class="overview-pagination">
        <span class="overview-pagination-info">
          显示第 {{ domainPageStart }}-{{ domainPageEnd }} 条，共 {{ domains.length }} 条
        </span>
        <div class="overview-pagination-actions">
          <button
            class="secondary-button"
            type="button"
            :disabled="domainPage <= 1"
            @click="previousDomainPage"
          >
            上一页
          </button>
          <span>第 {{ domainPage }} / {{ domainPageCount }} 页</span>
          <button
            class="secondary-button"
            type="button"
            :disabled="domainPage >= domainPageCount"
            @click="nextDomainPage"
          >
            下一页
          </button>
        </div>
      </div>
    </section>

    </div>

    <UserManagement v-if="activeSection === 'users'" />
    <DNSAccounts v-if="activeSection === 'dns-accounts'" :refresh-key="settings" />

    <section v-if="activeSection === 'activity'" class="panel operations-panel">
      <div class="panel-heading">
        <div>
          <h2>运行概览</h2>
        </div>
        <span class="muted">证书有效期 · 自动续期 · 任务执行</span>
      </div>
      <RuntimeOverview :state="dashboard" @navigate="selectSection" />
    </section>

    <section v-if="activeSection === 'domains'" class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">域名配置</p>
          <h2>托管域名</h2>
        </div>
        <span class="muted">共 {{ domains.length }} 个配置</span>
      </div>
      <div v-if="loading" class="empty-state">正在读取域名状态……</div>
      <div v-else-if="domains.length === 0" class="empty-state">
        <strong>还没有托管域名</strong>
        <span>添加第一个域名，开始管理证书申请和自动续期。</span>
        <button class="primary-button" type="button" @click="openCreate">
          添加托管域名
        </button>
      </div>
      <div v-else class="domain-list">
        <article v-for="item in domains" :key="item.id" class="domain-row">
          <div class="domain-main">
            <div class="domain-title-line">
              <strong>{{ item.name }}</strong
              ><span class="tag" :class="statusClass(item.status)">{{
                displayLabel(domainStatusLabels, item.status)
              }}</span>
            </div>
            <div class="domain-meta">
              <span>{{
                displayLabel(certificateTypeLabels, item.certificate_type)
              }}</span>
              <span>{{
                displayLabel(environmentLabels, item.acme_environment)
              }}</span>
              <span>{{
                item.auto_renew ? "自动续期已开启" : "自动续期已关闭"
              }}</span>
              <span>{{ (item.sans || []).length }} 个证书标识</span>
              <span
                >部署服务器：{{
                  (domainServers[item.id] || [])
                    .map((server) => server.name)
                    .join("、") || "未配置"
                }}</span
              >
            </div>
          </div>
          <div class="domain-expiry">
            <strong>{{ daysRemaining(item.expires_at) }}</strong
            ><span>{{ formatDate(item.expires_at) }}</span>
          </div>
          <div class="row-actions">
            <button class="text-button" type="button" @click="issueDomain(item)">
              申请证书
            </button>
            <button class="text-button" type="button" @click="validateDomain(item)">
              校验证书
            </button>
            <button class="text-button" type="button" @click="deployDomain(item)">
              部署证书
            </button>
            <button class="text-button" type="button" @click="openCertificateImport(item)">
              导入证书
            </button>
            <button class="text-button" type="button" @click="openEdit(item)">
              编辑</button
            ><button
              class="text-button danger-text"
              type="button"
              @click="deleteDomain(item)"
            >
              删除
            </button>
          </div>
        </article>
      </div>
    </section>

    <section v-if="activeSection === 'keys'" class="panel server-panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">安全凭据</p>
          <h2>SSH 私钥</h2>
        </div>
        <span class="muted">共 {{ sshKeys.length }} 个加密引用</span>
      </div>
      <div v-if="sshKeys.length === 0" class="empty-state">
        <strong>还没有 SSH 私钥</strong
        ><span>私钥会加密保存，页面和接口只显示算法、指纹与安全引用。</span
        ><button class="primary-button" type="button" @click="openSSHKeyCreate">
          添加 SSH 私钥
        </button>
      </div>
      <div v-else class="domain-list">
        <article v-for="item in sshKeys" :key="item.id" class="domain-row">
          <div class="domain-main">
            <div class="domain-title-line">
              <strong>{{ item.name }}</strong
              ><span class="tag success">{{ item.algorithm }}</span>
            </div>
            <div class="domain-meta">
              <span class="key-fingerprint"
                >公钥指纹：{{ item.fingerprint }}</span
              ><span>更新时间：{{ formatDate(item.updated_at) }}</span>
            </div>
          </div>
          <div class="row-actions">
            <button
              class="text-button danger-text"
              type="button"
              @click="deleteSSHKey(item)"
            >
              删除
            </button>
          </div>
        </article>
      </div>
    </section>

    <section v-if="activeSection === 'servers'" class="panel server-panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">远程部署</p>
          <h2>部署服务器</h2>
        </div>
        <span class="muted">共 {{ servers.length }} 台配置</span>
      </div>
      <div v-if="servers.length === 0" class="empty-state">
        <strong>还没有部署服务器</strong
        ><span>添加 SSH 服务器后，证书申请成功可自动部署。</span
        ><button class="primary-button" type="button" @click="openServerCreate">
          添加部署服务器
        </button>
      </div>
      <div v-else class="domain-list">
        <article v-for="item in servers" :key="item.id" class="domain-row">
          <div class="domain-main">
            <div class="domain-title-line">
              <strong>{{ item.name }}</strong
              ><span class="tag">{{ displayLabel(webServerLabels, item.web_server) }}</span>
            </div>
            <div class="domain-meta">
              <span>{{ item.username }}@{{ item.address }}:{{ item.port }}</span
              ><span v-if="item.auth_method === 'private_key' && item.private_key_ref">私钥引用已配置</span
              ><span v-else>{{ serverAuthLabel(item) }}</span
              ><span>指纹：{{ item.host_key_fingerprint }}</span>
            </div>
          </div>
          <div class="row-actions">
            <button
              class="text-button"
              type="button"
              @click="openServerEdit(item)"
            >
              编辑</button
            ><button
              class="text-button danger-text"
              type="button"
              @click="deleteServer(item)"
            >
              删除
            </button>
          </div>
        </article>
      </div>
    </section>

      </div>
      <DashboardNavigation
        :active-section="activeSection"
        :sections="sections"
        @select="selectSection"
        @settings="openSettings"
      />
    </div>

    <SettingsModal v-if="showSettings" :state="dashboard" />


    <div v-if="showCertificateImport" class="modal-backdrop" @click.self="closeCertificateImport">
      <section class="modal-card certificate-import-modal" role="dialog" aria-modal="true" aria-label="导入证书">
        <div class="modal-heading">
          <div>
            <p class="eyebrow">证书管理</p>
            <h2>导入证书</h2>
            <p class="muted">目标域名：{{ importDomain?.name }}</p>
          </div>
          <button class="close-button" type="button" aria-label="关闭" @click="closeCertificateImport">×</button>
        </div>
        <form @submit.prevent="importCertificate">
          <label class="field">
            <span>证书文件 <small>必选，支持 PEM / CRT</small></span>
            <input type="file" accept=".pem,.crt,.cer,.txt" required @change="selectCertificateImportFile('certificate', $event)" />
          </label>
          <label class="field">
            <span>私钥文件 <small>必选，内容只上传到服务端并按安全流程处理</small></span>
            <input type="file" accept=".pem,.key,.txt" required @change="selectCertificateImportFile('private_key', $event)" />
          </label>
          <label class="field">
            <span>证书链文件 <small>可选，支持 PEM / CRT</small></span>
            <input type="file" accept=".pem,.crt,.cer,.txt" @change="selectCertificateImportFile('issuer_bundle', $event)" />
          </label>
          <p class="security-hint">单个文件不能超过 20 MB。导入成功后会自动刷新证书、任务和审计数据。</p>
          <p v-if="importError" class="form-error">{{ importError }}</p>
          <div v-if="importResult" class="import-result">
            <strong>证书导入完成</strong>
            <span v-if="importResult.deployment">部署结果已返回，请在任务记录中查看详情。</span>
            <span v-else>证书已保存，可以稍后执行部署。</span>
          </div>
          <div class="modal-actions">
            <button class="secondary-button" type="button" :disabled="importSaving" @click="closeCertificateImport">关闭</button>
            <button class="primary-button" type="submit" :disabled="importSaving">{{ importSaving ? "导入中……" : "导入证书" }}</button>
          </div>
        </form>
      </section>
    </div>

    <DomainModal v-if="showForm" :state="dashboard" />

    <ServerModal v-if="showServerForm" :state="dashboard" />

    <SSHKeyModal v-if="showSSHKeyForm" :state="dashboard" />

    <DeploymentResult
      v-if="deploymentResult"
      :result="deploymentResult"
      @close="deploymentResult = null"
    />

    <div v-if="showTaskDetails" class="modal-backdrop" @click.self="closeTaskDetails">
      <section class="modal-card task-details-modal" role="dialog" aria-modal="true" aria-label="任务详情">
        <div class="modal-heading">
          <div>
            <p class="eyebrow">任务中心</p>
            <h2>任务详情</h2>
            <p v-if="taskDetails" class="muted">{{ taskTypeLabel(taskDetails.task_type) }} · {{ taskDetails.id }}</p>
          </div>
          <button class="close-button" type="button" aria-label="关闭" @click="closeTaskDetails">×</button>
        </div>
        <div v-if="taskDetailsLoading" class="empty-state">正在读取任务详情……</div>
        <p v-else-if="taskDetailsError" class="form-error">{{ taskDetailsError }}</p>
        <template v-else-if="taskDetails">
          <div class="task-detail-summary">
            <div><span>当前状态</span><strong class="tag" :class="statusClass(taskDetails.status)">{{ displayLabel(taskStatusLabels, taskDetails.status) }}</strong></div>
            <div><span>执行阶段</span><strong>{{ taskStageLabel(taskDetails.stage) }}</strong></div>
            <div><span>创建时间</span><strong>{{ formatDate(taskDetails.created_at) }}</strong></div>
            <div><span>完成时间</span><strong>{{ taskDetails.finished_at ? formatDate(taskDetails.finished_at) : "未完成" }}</strong></div>
          </div>
          <div class="task-detail-message">
            <span>任务信息</span>
            <p>{{ taskDetails.message || "无附加信息" }}</p>
          </div>
          <h3 class="modal-section-title">执行尝试</h3>
          <div v-if="taskAttempts.length === 0" class="empty-state compact-empty">暂无执行尝试记录</div>
          <div v-else class="attempt-list">
            <article v-for="attempt in taskAttempts" :key="attempt.id" class="attempt-row">
              <div>
                <strong>第 {{ attempt.attempt_number }} 次尝试 · {{ taskStageLabel(attempt.stage) }}</strong>
                <span class="muted">{{ attempt.message || "无附加信息" }}</span>
                <span v-if="attempt.error" class="attempt-error">{{ attempt.error }}</span>
              </div>
              <span class="tag" :class="statusClass(attempt.status)">{{ attemptStatusLabel(attempt.status) }}</span>
            </article>
          </div>
        </template>
        <div class="modal-actions">
          <button class="secondary-button" type="button" @click="closeTaskDetails">关闭</button>
        </div>
      </section>
    </div>

  </main>
</template>
<style scoped src="./Dashboard.css"></style>
<style scoped src="./DashboardLayout.css"></style>
