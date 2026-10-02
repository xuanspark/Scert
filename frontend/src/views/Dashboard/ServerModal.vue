<script setup>
import HelpTip from "../components/HelpTip/HelpTip.vue";

const props = defineProps({ state: { type: Object, required: true } });
const {
  showServerForm,
  closeServerForm,
  serverEditingId,
  serverForm,
  defaultCertificatePath,
  defaultCertificateChainPath,
  defaultPrivateKeyPath,
  serverFormError,
  serverSaving,
  saveServer,
} = props.state;
</script>

<template>
    <div
      v-if="showServerForm"
      class="modal-backdrop server-backdrop"
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
        <form class="server-form" @submit.prevent="saveServer">
          <section class="server-form-section">
            <div class="server-form-section-heading">
              <div>
                <p class="eyebrow">服务器配置</p>
                <h3>基本信息与连接方式</h3>
              </div>
              <HelpTip>
                这里保存部署服务器的连接信息，登录验证使用已配置的 SSH 密码。
              </HelpTip>
            </div>
            <div class="server-form-grid">
              <label class="field">
                <span>服务器名称</span>
                <input
                  v-model="serverForm.name"
                  required
                  type="text"
                  placeholder="例如生产 Nginx"
                />
              </label>
              <label class="field">
                <span>网站服务</span>
                <select v-model="serverForm.web_server">
                  <option value="nginx">Nginx</option>
                  <option value="caddy">Caddy</option>
                  <option value="apache">Apache</option>
                  <option value="custom">自定义</option>
                </select>
              </label>
              <label class="field">
                <span>服务器地址</span>
                <input
                  v-model="serverForm.address"
                  required
                  type="text"
                  placeholder="IP 或主机名"
                />
              </label>
              <label class="field">
                <span>SSH 端口</span>
                <input
                  v-model.number="serverForm.port"
                  required
                  type="number"
                  min="1"
                  max="65535"
                />
              </label>
              <label class="field">
                <span>SSH 用户</span>
                <input v-model="serverForm.username" required type="text" />
              </label>
              <label class="field server-form-field-wide">
                <span>SSH 密码</span>
                <input
                  v-model="serverForm.password"
                  type="password"
                  :required="!serverEditingId || !serverForm.passwordConfigured"
                  autocomplete="new-password"
                  placeholder="编辑时留空表示保留原密码"
                />
              </label>
              <label class="field server-form-field-wide">
                <span>
                  服务器 SHA256 指纹
                  <HelpTip>请填写通过可信渠道核对过的服务器指纹，避免连接到错误的服务器。</HelpTip>
                </span>
                <input
                  v-model="serverForm.host_key_fingerprint"
                  required
                  type="text"
                  placeholder="SHA256:..."
                />
              </label>
            </div>
          </section>

          <section class="server-form-section">
            <div class="server-form-section-heading">
              <div>
                <p class="eyebrow">证书部署</p>
                <h3>远程文件路径</h3>
              </div>
              <HelpTip>
                路径支持 {域名} 和 {证书类型} 占位符，部署时会替换为实际目录。
              </HelpTip>
            </div>
            <div class="server-paths">
              <label class="field">
                <span>远程证书路径</span>
                <input
                  v-model="serverForm.certificate_path"
                  required
                  type="text"
                  :placeholder="defaultCertificatePath"
                />
              </label>
              <label class="field">
                <span>远程完整证书链路径</span>
                <input
                  v-model="serverForm.certificate_chain_path"
                  required
                  type="text"
                  :placeholder="defaultCertificateChainPath"
                />
              </label>
              <label class="field">
                <span>HTTPS 证书密钥路径</span>
                <input
                  v-model="serverForm.private_key_path"
                  required
                  type="text"
                  :placeholder="defaultPrivateKeyPath"
                />
              </label>
            </div>
          </section>

          <section class="server-form-section server-command-section">
            <div class="server-form-section-heading">
              <div>
                <p class="eyebrow">部署命令</p>
                <h3>服务校验与重载</h3>
              </div>
              <HelpTip>
                每行填写一个命令参数；标准 Nginx、Caddy、Apache 可以留空使用默认命令，自定义服务需要填写。
              </HelpTip>
            </div>
            <div class="server-form-grid">
              <label class="field">
                <span>配置校验命令</span>
                <textarea
                  v-model="serverForm.validation_command"
                  rows="2"
                  placeholder="nginx&#10;-t"
                ></textarea>
              </label>
              <label class="field">
                <span>重载命令</span>
                <textarea
                  v-model="serverForm.reload_command"
                  rows="2"
                  placeholder="nginx&#10;-s&#10;reload"
                ></textarea>
              </label>
            </div>
          </section>

          <p v-if="serverFormError" class="form-error">{{ serverFormError }}</p>
          <div class="modal-actions">
            <button
              class="secondary-button"
              type="button"
              :disabled="serverSaving"
              @click="closeServerForm"
            >
              取消
            </button>
            <button class="primary-button" type="submit" :disabled="serverSaving">
              {{ serverSaving ? "保存中……" : "保存服务器配置" }}
            </button>
          </div>
        </form>
      </section>
    </div>
</template>

<style scoped src="./Dashboard.css"></style>
<style scoped src="./ServerModal.css"></style>
