<script setup>
import { useDashboard } from "./Dashboard.js";

const {
  scertLogo,
  status,
  domains,
  servers,
  sshKeys,
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
  httpsModeLabels,
  environmentLabels,
  webServerLabels,
  isEditing,
  formTitle,
  form,
  serverForm,
  sshKeyForm,
  sshKeyPlatform,
  sshKeyPassphraseVisible,
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
  displayLabel,
  formatDate,
  daysRemaining,
  statusClass,
} = useDashboard();
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
          统一管理单域名、泛域名和 SAN 证书，后续将接入 lego、DNS
          验证与远程部署流程。
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

    <div v-if="showForm" class="modal-backdrop" @click.self="closeForm">
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
              >自定义重载命令
              <small>每行一个参数；留空使用网站服务默认命令</small></span
            ><textarea
              v-model="serverForm.reload_command"
              rows="3"
              placeholder="nginx -s reload"
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
        class="modal-card"
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
                Linux 服务器默认位置：<code>~/.ssh/authorized_keys</code>；Windows
                OpenSSH 服务器请按照系统说明配置授权文件。
              </small>
            </div>
            <p class="ssh-key-guide-note">
              懒人方式：如果你能登录服务器控制台，只需执行生成命令，再把生成的
              <code>.pub</code> 公钥内容追加到授权文件，最后在下方选择私钥文件即可。
            </p>
          </section>
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
        </form>
      </section>
    </div>
  </main>
</template>
<style scoped src="./Dashboard.css"></style>
