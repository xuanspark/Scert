<script setup>
import ChangePassword from "../components/ChangePassword/ChangePassword.vue";
import UserManagement from "../UserManagement/UserManagement.vue";
import SettingsModal from "./SettingsModal.vue";
import DomainModal from "./DomainModal.vue";
import ServerModal from "./ServerModal.vue";
import SSHKeyModal from "./SSHKeyModal.vue";
import DashboardNavigation from "./DashboardNavigation.vue";
import { useDashboardNavigation } from "./DashboardNavigation.js";
import { useDashboard } from "./Dashboard.js";

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
        <ChangePassword />
        <button class="secondary-button" type="button" @click="loadData">
          刷新状态
        </button>
        <button v-if="activeSection === 'activity'" class="secondary-button" type="button" @click="runRenewalNow">
          立即检查续期
        </button>
        <button class="secondary-button" type="button" @click="openSettings">
          系统设置
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
      <DashboardNavigation
        :active-section="activeSection"
        :sections="sections"
        @select="selectSection"
      />
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
      <div v-if="loading" class="empty-state compact-empty">正在读取域名状态……</div>
      <div v-else-if="domains.length === 0" class="empty-state compact-empty">
        <strong>还没有托管域名</strong>
        <span>添加第一个域名，开始管理证书申请和自动续期。</span>
      </div>
      <div v-else class="overview-domain-list">
        <article v-for="item in domains" :key="item.id" class="overview-domain-row">
          <div class="overview-domain-main">
            <strong>{{ item.name }}</strong>
            <span class="tag" :class="statusClass(item.status)">
              {{ displayLabel(domainStatusLabels, item.status) }}
            </span>
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
    </section>

    <section class="metrics-grid">
      <article class="metric-card">
        <span>服务版本</span><strong>{{ status?.version || "—" }}</strong>
      </article>
      <article class="metric-card">
        <span>数据库</span
        ><strong>{{
          displayLabel(
            { sqlite: "SQLite", mysql: "MySQL" },
            status?.database_driver,
          )
        }}</strong>
      </article>
      <article class="metric-card">
        <span>HTTPS 模式</span
        ><strong>{{
          displayLabel(httpsModeLabels, status?.https_mode)
        }}</strong>
      </article>
      <article class="metric-card">
        <span>托管域名</span><strong>{{ domains.length }}</strong>
      </article>
    </section>

    </div>

    <UserManagement v-if="activeSection === 'users'" />

    <section v-if="activeSection === 'activity'" class="panel operations-panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">运行概览</p>
          <h2>最近活动</h2>
        </div>
        <span class="muted">最近 20 条记录</span>
      </div>
      <div class="operation-columns">
        <div>
          <h3>证书状态</h3>
          <div v-if="certificates.length === 0" class="empty-state compact-empty">暂无证书记录</div>
          <div v-else class="record-list" tabindex="0">
            <article v-for="item in certificates" :key="item.domain.id" class="record-row">
              <div>
                <strong>{{ item.domain.name }}</strong>
                <span v-if="item.certificate" class="muted">到期：{{ formatDate(item.certificate.saved.not_after) }}</span>
                <span v-else class="muted">{{ item.certificate_error || "读取失败" }}</span>
              </div>
              <span v-if="item.certificate" class="tag success">已保存</span>
              <span v-else class="tag warning">未保存</span>
            </article>
          </div>
        </div>
        <div>
          <h3>任务记录</h3>
          <div v-if="tasks.length === 0" class="empty-state compact-empty">暂无任务记录</div>
          <div v-else class="record-list" tabindex="0">
            <article v-for="item in tasks" :key="item.id" class="record-row">
              <div>
                <strong>{{ taskTypeLabel(item.task_type) }}</strong>
                <span class="muted">{{ item.message || "无附加信息" }}</span>
              </div>
              <div class="record-row-actions">
                <span class="tag" :class="statusClass(item.status)">{{ displayLabel(taskStatusLabels, item.status) }}</span>
                <button class="text-button" type="button" @click="openTaskDetails(item)">
                  查看详情
                </button>
                <button
                  v-if="item.task_type !== 'management_renewal' && (item.status === 'queued' || item.status === 'running')"
                  class="text-button"
                  type="button"
                  @click="cancelTask(item)"
                >
                  取消任务
                </button>
              </div>
            </article>
          </div>
        </div>
        <div>
          <h3>审计日志</h3>
          <div v-if="auditLogs.length === 0" class="empty-state compact-empty">暂无审计日志</div>
          <div v-else class="record-list" tabindex="0">
            <article v-for="item in auditLogs" :key="item.id" class="record-row">
              <div>
                <strong>{{ auditActionLabel(item.action) }}</strong>
                <span class="muted">{{ item.message || item.resource_type }}</span>
              </div>
              <span v-if="item.status === 'success' || item.status === 'failed'" class="tag" :class="statusClass(item.status)">{{ item.status === "success" ? "成功" : "失败" }}</span>
              <span v-else class="tag" :class="statusClass(item.status)">
                {{ displayLabel(taskStatusLabels, item.status) }}
              </span>
            </article>
          </div>
        </div>
      </div>
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
              ><span>私钥引用已配置</span
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
