<script setup>
import HelpTip from "../components/HelpTip/HelpTip.vue";

const props = defineProps({ state: { type: Object, required: true } });
const {
  showSSHKeyForm,
  closeSSHKeyForm,
  saveSSHKey,
  sshKeyPlatform,
  sshKeyFileName,
  sshKeyPrivatePath,
  sshKeyPublicPath,
  sshKeyCommandUsername,
  sshKeyCommandHost,
  copySSHKeyCommand,
  sshKeyCommands,
  sshPublicKeyCommands,
  sshKeyForm,
  importSSHKeyFile,
  sshKeyPassphraseVisible,
  sshKeyFormError,
  sshKeySaving,
} = props.state;
</script>

<template>
    <div
      v-if="showSSHKeyForm"
      class="modal-backdrop ssh-key-backdrop"
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
                <HelpTip>按下面三步操作，完成生成、安装公钥和录入私钥。</HelpTip>
              </div>
              <span class="guide-badge">新手引导</span>
            </div>
            <div class="ssh-key-steps">
              <article>
                <b>1</b>
                <div>
                  <strong>在本机生成密钥</strong>
                  <HelpTip>打开 PowerShell、终端或命令提示符，复制下面的命令执行。</HelpTip>
                </div>
              </article>
              <article>
                <b>2</b>
                <div>
                  <strong>把公钥放到服务器</strong>
                  <HelpTip>公钥放入服务器的授权文件，私钥不要粘贴到服务器。</HelpTip>
                </div>
              </article>
              <article>
                <b>3</b>
                <div>
                  <strong>录入私钥并保存</strong>
                  <HelpTip>可以直接粘贴，也可以选择本机私钥文件，Scert 会加密保存。</HelpTip>
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
              <HelpTip>
                本地公钥文件就是上面显示的随机路径；安装后会追加到远程用户的
                <code>~/.ssh/authorized_keys</code>。每台主机、每次新建配置都会生成新的文件名。
              </HelpTip>
            </div>
            <HelpTip>
              懒人方式：如果你能登录服务器控制台，只需执行生成命令，再把随机路径中的公钥内容追加到授权文件，最后在下方选择私钥文件即可。
            </HelpTip>
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
              ><span>读取本机私钥文件 <HelpTip>可选，读取后仍需点击加密保存</HelpTip></span
              ><input type="file" @change="importSSHKeyFile" /></label
            >
            <label class="field"
              ><span
                >私钥内容
                <HelpTip>支持 RSA、ECDSA、Ed25519 和 OpenSSH 私钥</HelpTip></span
              ><textarea
                v-model="sshKeyForm.private_key"
                required
                rows="5"
                spellcheck="false"
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
              ></textarea>
            </label>
            <label class="field"
              ><span class="field-label-row"
                ><span>私钥口令 <HelpTip>未加密私钥请留空</HelpTip></span
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
            <HelpTip>
              保存后私钥和口令只进入本机加密敏感信息存储，后续接口不会返回明文。
            </HelpTip>
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
</template>

<style scoped src="./Dashboard.css"></style>
<style scoped src="./SSHKeyModal.css"></style>
