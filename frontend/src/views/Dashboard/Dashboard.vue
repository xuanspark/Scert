<script setup>
import ChangePassword from "../components/ChangePassword/ChangePassword.vue";
import UserManagement from "../UserManagement/UserManagement.vue";
import SettingsModal from "./SettingsModal.vue";
import { useDashboard } from "./Dashboard.js";

const dashboard = useDashboard();
const {
  scertLogo,
  status,
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
        <button class="secondary-button" type="button" @click="runRenewalNow">
          立即检查续期
        </button>
        <button class="secondary-button" type="button" @click="openSettings">
          系统设置
        </button>
        <button
          class="secondary-button"
          type="button"
          @click="openSSHKeyCreate"
        >
          添加 SSH 私钥
        </button>
        <button
          class="secondary-button"
          type="button"
          @click="openServerCreate"
        >
          添加服务器
        </button>
        <button class="primary-button" type="button" @click="openCreate">
          添加域名
        </button>
      </div>
    </header>

    <section class="hero-card">
      <div>
        <span class="status-dot" :class="{ online: status }"></span>
        <span class="status-label">{{
          status ? "核心服务运行中" : "等待连接核心服务"
        }}</span>
        <h2>让证书续期和部署变得可追踪</h2>
        <p>
          统一管理单域名、泛域名和 SAN 证书，当前已接入 lego、DNS
          验证、自动续期、远程部署与部署后校验流程。
        </p>
      </div>
      <img class="hero-mark" :src="scertLogo" alt="Scert 标志" />
    </section>

    <p v-if="error" class="error-banner">{{ error }}</p>
    <p v-if="notice" class="notice-banner">{{ notice }}</p>

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

    <UserManagement />

    <section class="panel operations-panel">
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
          <div v-else class="record-list">
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
          <div v-else class="record-list">
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
          <div v-else class="record-list">
            <article v-for="item in auditLogs" :key="item.id" class="record-row">
              <div>
                <strong>{{ auditActionLabel(item.action) }}</strong>
                <span class="muted">{{ item.message || item.resource_type }}</span>
              </div>
              <span class="tag" :class="item.status === 'success' ? 'success' : 'danger'">{{ item.status === "success" ? "成功" : "失败" }}</span>
            </article>
          </div>
        </div>
      </div>
    </section>

    <section class="panel">
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

    <section class="panel server-panel">
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

    <section class="panel server-panel">
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
    </div>    <div v-if="showForm" class="modal-backdrop" @click.self="closeForm">
      <section
        class="modal-card"
        role="dialog"
        aria-modal="true"
        :aria-label="formTitle"
      >
        <div class="modal-heading">
          <div>
            <p class="eyebrow">域名配置</p>
            <h2>{{ formTitle }}</h2>
          </div>
          <button
            class="close-button"
            type="button"
            aria-label="关闭"
            @click="closeForm"
          >
            ×
          </button>
        </div>
        <form @submit.prevent="saveDomain">
          <label class="field" for="domain-name"
            ><span>主域名</span
            ><input
              id="domain-name"
              name="name"
              v-model="form.name"
              type="text"
              placeholder="例如 example.com"
              autocomplete="off"
          /></label>
          <label class="field" for="certificate-type"
            ><span>证书类型</span
            ><select
              id="certificate-type"
              name="certificate_type"
              v-model="form.certificate_type"
            >
              <option value="single">单域名</option>
              <option value="wildcard">泛域名</option>
              <option value="san">SAN 多域名</option>
            </select></label
          >
          <label
            v-if="form.certificate_type === 'san'"
            class="field"
            for="domain-sans"
            ><span>附加域名 <small>每行一个，也支持逗号分隔</small></span
            ><textarea
              id="domain-sans"
              name="sans"
              v-model="form.sans"
              rows="4"
              placeholder="www.example.com\napi.example.com"
            ></textarea>
          </label>
          <div class="form-grid">
            <label class="field" for="acme-environment"
              ><span>ACME 环境</span
              ><select
                id="acme-environment"
                name="acme_environment"
                v-model="form.acme_environment"
              >
                <option value="staging">测试环境</option>
                <option value="production">生产环境</option>
              </select></label
            >
            <label class="field" for="dns-provider"
              ><span>DNS 验证服务商</span
              ><select
                id="dns-provider"
                name="dns_provider"
                v-model="form.dns_provider"
              >
                <option value="">暂不选择</option>
                <option
                  v-for="provider in dnsProviders"
                  :key="provider.value"
                  :value="provider.value"
                >
                  {{ provider.label }}
                </option>
              </select></label
            >
          </div>
          <label class="switch-field" for="auto-renew"
            ><input
              id="auto-renew"
              name="auto_renew"
              v-model="form.auto_renew"
              type="checkbox"
            /><span
              ><strong>启用自动续期</strong
              ><small>证书剩余有效期达到阈值时自动创建续期任务</small></span
            ></label
          >
          <fieldset class="server-picker">
            <legend>关联部署服务器</legend>
            <label
              v-for="server in servers"
              :key="server.id"
              class="server-option"
              ><input
                v-model="form.server_ids"
                type="checkbox"
                :value="server.id"
              /><span
                ><strong>{{ server.name }}</strong
                ><small
                  >{{ server.address }} ·
                  {{ displayLabel(webServerLabels, server.web_server) }}</small
                ></span
              ></label
            >
            <p v-if="servers.length === 0" class="muted">
              暂无服务器配置，可先保存域名后添加服务器。
            </p>
          </fieldset>
          <p v-if="formError" class="form-error">{{ formError }}</p>
          <div class="modal-actions">
            <button
              class="secondary-button"
              type="button"
              :disabled="saving"
              @click="closeForm"
            >
              取消</button
            ><button class="primary-button" type="submit" :disabled="saving">
              {{ saving ? "保存中……" : "保存配置" }}
            </button>
          </div>
        </form>
      </section>
    </div>

    <div
      v-if="showServerForm"
      class="modal-backdrop"
      @click.self="closeServerForm"
    >
      <section
        class="modal-card server-modal"
        role="dialog"
        aria-modal="true"
        aria-label="服务器配置"
      >
        <div class="modal-heading">
          <div>
            <p class="eyebrow">远程部署</p>
            <h2>{{ serverEditingId ? "编辑部署服务器" : "添加部署服务器" }}</h2>
          </div>
          <button
            class="close-button"
            type="button"
            aria-label="关闭"
            @click="closeServerForm"
          >
            ×
          </button>
        </div>
        <form @submit.prevent="saveServer">
          <div class="form-grid">
            <label class="field"
              ><span>服务器名称</span
              ><input
                v-model="serverForm.name"
                required
                type="text"
                placeholder="例如生产 Nginx" /></label
            ><label class="field"
              ><span>网站服务</span
              ><select v-model="serverForm.web_server">
                <option value="nginx">Nginx</option>
                <option value="caddy">Caddy</option>
                <option value="apache">Apache</option>
                <option value="custom">自定义</option>
              </select></label
            >
          </div>
          <div class="form-grid">
            <label class="field"
              ><span>服务器地址</span
              ><input
                v-model="serverForm.address"
                required
                type="text"
                placeholder="IP 或主机名" /></label
            ><label class="field"
              ><span>SSH 端口</span
              ><input
                v-model.number="serverForm.port"
                required
                type="number"
                min="1"
                max="65535"
            /></label>
          </div>
          <div class="form-grid">
            <label class="field"
              ><span>SSH 用户</span
              ><input
                v-model="serverForm.username"
                required
                type="text" /></label
            ><label class="field"
              ><span class="field-label-row"
                ><span>SSH 私钥</span
                ><button
                  class="inline-button"
                  type="button"
                  @click="openSSHKeyCreate"
                >
                  新建或导入
                </button></span
              ><select v-model="serverForm.private_key_ref" required>
                <option value="">请选择已加密保存的私钥</option>
                <option
                  v-for="key in sshKeys"
                  :key="key.id"
                  :value="key.reference"
                >
                  {{ key.name }} · {{ key.algorithm }}
                </option></select
              ><small v-if="sshKeys.length === 0"
                >还没有私钥？点击“新建或导入”，按照引导生成、粘贴或读取私钥文件。</small
              ></label
            >
          </div>
          <label class="field"
            ><span>SSH 主机 SHA256 指纹</span
            ><input
              v-model="serverForm.host_key_fingerprint"
              required
              type="text"
              placeholder="SHA256:..."
          /></label>
          <label class="field"
            ><span>远程证书路径</span
            ><input
              v-model="serverForm.certificate_path"
              required
              type="text"
              :placeholder="defaultCertificatePath"
          /></label>
          <label class="field"
            ><span>远程完整证书链路径</span
            ><input
              v-model="serverForm.certificate_chain_path"
              required
              type="text"
              :placeholder="defaultCertificateChainPath"
          /></label>
          <label class="field"
            ><span>远程私钥路径</span
            ><input
              v-model="serverForm.private_key_path"
              required
              type="text"
              :placeholder="defaultPrivateKeyPath"
          /></label>
          <p class="field-hint">
            保存的是路径模板，部署时会将 {域名} 和 {证书类型}
            替换为实际值。证书类型目录固定为 single、wildcard 或 san。
          </p>
          <label class="field"
            ><span
              >配置校验命令
              <small
                >每行一个参数；Nginx、Caddy、Apache 留空使用默认命令，自定义服务必须填写</small
              ></span
            ><textarea
              v-model="serverForm.validation_command"
              rows="3"
              placeholder="nginx&#10;-t"
            ></textarea>
          </label>
          <label class="field"
            ><span
              >重载命令
              <small>每行一个参数；留空使用网站服务默认命令</small></span
            ><textarea
              v-model="serverForm.reload_command"
              rows="3"
              placeholder="nginx&#10;-s&#10;reload"
            ></textarea>
          </label>
          <p v-if="serverFormError" class="form-error">{{ serverFormError }}</p>
          <div class="modal-actions">
            <button
              class="secondary-button"
              type="button"
              :disabled="serverSaving"
              @click="closeServerForm"
            >
              取消</button
            ><button
              class="primary-button"
              type="submit"
              :disabled="serverSaving"
            >
              {{ serverSaving ? "保存中……" : "保存服务器" }}
            </button>
          </div>
        </form>
      </section>
    </div>

    <div
      v-if="showSSHKeyForm"
      class="modal-backdrop"
      @click.self="closeSSHKeyForm"
    >
      <section
        class="modal-card ssh-key-modal"
        role="dialog"
        aria-modal="true"
        aria-label="添加 SSH 私钥"
      >
        <div class="modal-heading">
          <div>
            <p class="eyebrow">安全凭据</p>
            <h2>添加 SSH 私钥</h2>
          </div>
          <button
            class="close-button"
            type="button"
            aria-label="关闭"
            @click="closeSSHKeyForm"
          >
            ×
          </button>
        </div>
        <form @submit.prevent="saveSSHKey">
          <section class="ssh-key-guide">
            <div class="ssh-key-guide-heading">
              <div>
                <strong>不会准备 SSH 密钥？</strong>
                <span>按下面三步操作，完成生成、安装公钥和录入私钥。</span>
              </div>
              <span class="guide-badge">新手引导</span>
            </div>
            <div class="ssh-key-steps">
              <article>
                <b>1</b>
                <div>
                  <strong>在本机生成密钥</strong>
                  <span>打开 PowerShell、终端或命令提示符，复制下面的命令执行。</span>
                </div>
              </article>
              <article>
                <b>2</b>
                <div>
                  <strong>把公钥放到服务器</strong>
                  <span>公钥放入服务器的授权文件，私钥不要粘贴到服务器。</span>
                </div>
              </article>
              <article>
                <b>3</b>
                <div>
                  <strong>录入私钥并保存</strong>
                  <span>可以直接粘贴，也可以选择本机私钥文件，Scert 会加密保存。</span>
                </div>
              </article>
            </div>
            <div class="ssh-key-platforms" role="tablist" aria-label="密钥生成平台">
              <button
                class="platform-tab"
                :class="{ active: sshKeyPlatform === 'windows' }"
                type="button"
                role="tab"
                :aria-selected="sshKeyPlatform === 'windows'"
                @click="sshKeyPlatform = 'windows'"
              >
                Windows
              </button>
              <button
                class="platform-tab"
                :class="{ active: sshKeyPlatform === 'linux' }"
                type="button"
                role="tab"
                :aria-selected="sshKeyPlatform === 'linux'"
                @click="sshKeyPlatform = 'linux'"
              >
                Linux / macOS
              </button>
            </div>
            <div class="ssh-key-path-line">
              <span>本次密钥文件名</span>
              <code>{{ sshKeyFileName }}</code>
              <span class="guide-badge">每次随机生成</span>
            </div>
            <div class="ssh-key-path-summary">
              <div>
                <span>本地私钥位置</span>
                <code>{{ sshKeyPrivatePath }}</code>
              </div>
              <div>
                <span>本地公钥位置</span>
                <code>{{ sshKeyPublicPath }}</code>
              </div>
              <div>
                <span>远程登录目标</span>
                <code>{{ sshKeyCommandUsername }}@{{ sshKeyCommandHost }}</code>
              </div>
            </div>
            <div class="ssh-command-card">
              <div class="ssh-command-heading">
                <span>生成命令</span>
                <button
                  class="inline-button"
                  type="button"
                  @click="
                    copySSHKeyCommand(
                      sshKeyCommands[sshKeyPlatform],
                      '密钥生成命令已复制'
                    )
                  "
                >
                  复制命令
                </button>
              </div>
              <code>{{ sshKeyCommands[sshKeyPlatform] }}</code>
            </div>
            <div class="ssh-command-card">
              <div class="ssh-command-heading">
                <span>安装公钥命令</span>
                <button
                  class="inline-button"
                  type="button"
                  @click="
                    copySSHKeyCommand(
                      sshPublicKeyCommands[sshKeyPlatform],
                      '公钥安装命令已复制'
                    )
                  "
                >
                  复制命令
                </button>
              </div>
              <code>{{ sshPublicKeyCommands[sshKeyPlatform] }}</code>
              <small>
                本地公钥文件就是上面显示的随机路径；安装后会追加到远程用户的
                <code>~/.ssh/authorized_keys</code>。每台主机、每次新建配置都会生成新的文件名。
              </small>
            </div>
            <p class="ssh-key-guide-note">
              懒人方式：如果你能登录服务器控制台，只需执行生成命令，再把随机路径中的公钥内容追加到授权文件，最后在下方选择私钥文件即可。
            </p>
          </section>
          <div class="ssh-key-form-fields">
            <label class="field"
              ><span>私钥名称</span
              ><input
                v-model="sshKeyForm.name"
                required
                type="text"
                maxlength="128"
                placeholder="例如生产环境部署私钥"
                autocomplete="off"
            /></label>
            <label class="field"
              ><span>读取本机私钥文件 <small>可选，读取后仍需点击加密保存</small></span
              ><input type="file" @change="importSSHKeyFile" /></label
            >
            <label class="field"
              ><span
                >私钥内容
                <small>支持 RSA、ECDSA、Ed25519 和 OpenSSH 私钥</small></span
              ><textarea
                v-model="sshKeyForm.private_key"
                required
                rows="11"
                spellcheck="false"
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
              ></textarea>
            </label>
            <label class="field"
              ><span class="field-label-row"
                ><span>私钥口令 <small>未加密私钥请留空</small></span
                ><button
                  class="inline-button"
                  type="button"
                  @click="sshKeyPassphraseVisible = !sshKeyPassphraseVisible"
                >
                  {{ sshKeyPassphraseVisible ? "隐藏口令" : "显示口令" }}
                </button></span
              ><input
                v-model="sshKeyForm.passphrase"
                :type="sshKeyPassphraseVisible ? 'text' : 'password'"
                maxlength="4096"
                autocomplete="new-password"
            /></label>
            <p class="security-hint">
              保存后私钥和口令只进入本机加密敏感信息存储，后续接口不会返回明文。
            </p>
            <p v-if="sshKeyFormError" class="form-error">{{ sshKeyFormError }}</p>
            <div class="modal-actions">
              <button
                class="secondary-button"
                type="button"
                :disabled="sshKeySaving"
                @click="closeSSHKeyForm"
              >
                取消</button
              ><button
                class="primary-button"
                type="submit"
                :disabled="sshKeySaving"
              >
                {{ sshKeySaving ? "加密保存中……" : "加密保存" }}
              </button>
            </div>
          </div>
        </form>
      </section>

    </div>
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
