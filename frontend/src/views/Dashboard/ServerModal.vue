<script setup>
import HelpTip from "../components/HelpTip/HelpTip.vue";

const props = defineProps({ state: { type: Object, required: true } });
const {
  showServerForm,
  closeServerForm,
  serverEditingId,
  serverForm,
  sshKeys,
  openSSHKeyCreate,
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
              ><span>认证方式</span
              ><select v-model="serverForm.auth_method">
                <option value="password">SSH 密码</option>
                <option value="private_key">SSH 私钥</option>
              </select></label
            >
          </div>
          <label v-if="serverForm.auth_method === 'password'" class="field">
            <span>SSH 密码</span>
            <input
              v-model="serverForm.password"
              type="password"
              :required="!serverEditingId || !serverForm.passwordConfigured"
              autocomplete="new-password"
              placeholder="编辑时留空表示保留原密码"
            />
          </label>
          <div v-else class="form-grid">
            <label class="field"
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
              ><HelpTip v-if="sshKeys.length === 0"
                >还没有私钥？点击“新建或导入”，按照引导生成、粘贴或读取私钥文件。</HelpTip
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
          <div class="server-paths">
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
            ><span>远程私钥路径 <HelpTip>
            保存的是路径模板，部署时会将 {域名} 和 {证书类型}
            替换为实际值。证书类型目录固定为 single、wildcard 或 san。
          </HelpTip></span
            ><input
              v-model="serverForm.private_key_path"
              required
              type="text"
              :placeholder="defaultPrivateKeyPath"
          /></label>
          </div>

          <label class="field"
            ><span
              >配置校验命令
              <HelpTip
                >每行一个参数；Nginx、Caddy、Apache 留空使用默认命令，自定义服务必须填写</HelpTip
              ></span
            ><textarea
              v-model="serverForm.validation_command"
              rows="2"
              placeholder="nginx&#10;-t"
            ></textarea>
          </label>
          <label class="field"
            ><span
              >重载命令
              <HelpTip>每行一个参数；留空使用网站服务默认命令</HelpTip></span
            ><textarea
              v-model="serverForm.reload_command"
              rows="2"
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
</template>

<style scoped src="./Dashboard.css"></style>
<style scoped src="./ServerModal.css"></style>
