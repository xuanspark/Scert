<script setup>
import HelpTip from "../components/HelpTip/HelpTip.vue";

const props = defineProps({ state: { type: Object, required: true } });
const {
  showForm,
  formTitle,
  closeForm,
  saveDomain,
  form,
  dnsProviders,
  dnsAccountID,
  dnsBindingLoading,
  dnsBindingReady,
  dnsBindingError,
  dnsBound,
  domainDNSAccountOptions,
  dnsUnavailableSelection,
  reloadDomainDNS,
  domainServerOptions,
  domainBindingsLoading,
  domainBindingsReady,
  domainSaveUncertain,
  reloadDomainBindings,
  displayLabel,
  webServerLabels,
  formError,
  saving,
} = props.state;
</script>

<template>
    <div v-if="showForm" class="modal-backdrop domain-backdrop" @click.self="closeForm">
      <section
        class="modal-card domain-modal"
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
            :disabled="saving"
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
              :disabled="saving || domainSaveUncertain"
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
              :disabled="saving || domainSaveUncertain"
            >
              <option value="single">单域名</option>
              <option value="wildcard">泛域名</option>
              <option value="san">SAN 多域名</option>
            </select></label
          >
          <label
            v-if="form.certificate_type === 'san'"
            class="field domain-sans-field"
            for="domain-sans"
            ><span>附加域名 <HelpTip>每行一个，也支持逗号分隔</HelpTip></span
            ><textarea
              id="domain-sans"
              name="sans"
              v-model="form.sans"
              :disabled="saving || domainSaveUncertain"
              rows="2"
              :placeholder="'www.example.com\napi.example.com'"
            ></textarea>
          </label>
          <div class="form-grid">
            <label class="field" for="acme-environment"
              ><span>ACME 环境</span
              ><select
                id="acme-environment"
                name="acme_environment"
                v-model="form.acme_environment"
                :disabled="saving || domainSaveUncertain"
              >
                <option value="staging">测试环境</option>
                <option value="production">生产环境</option>
              </select></label
            >
            <label class="field" for="challenge-type">
              <span>验证方式 <HelpTip>HTTP-01 要求全部域名的公网 80 端口挑战路径反代到 Scert，不支持泛域名；其余业务请求保持原站点配置。</HelpTip></span>
              <select
                id="challenge-type"
                name="challenge_type"
                v-model="form.challenge_type"
                :disabled="saving || domainSaveUncertain || dnsBound || Boolean(dnsAccountID) || !dnsBindingReady"
              >
                <option value="dns-01">DNS-01（DNS 验证）</option>
                <option value="http-01">HTTP-01（网站验证）</option>
              </select>
            </label>
            <label v-if="form.challenge_type !== 'http-01'" class="field" for="domain-dns-account">
              <span>DNS 账户 <HelpTip>只可选择本项目内已启用且验证通过的账户。更换账户会校验当前绑定和账户版本；解除后保存，再切换验证方式。历史系统配置尚未导入独立账户。</HelpTip></span>
              <select
                id="domain-dns-account"
                name="dns_account_id"
                v-model="dnsAccountID"
                :disabled="saving || domainSaveUncertain || dnsBindingLoading || !dnsBindingReady"
              >
                <option value="">{{ dnsBound ? "解除独立绑定（恢复历史配置）" : "历史系统配置 / 暂不绑定" }}</option>
                <option v-if="dnsUnavailableSelection" :value="dnsAccountID" disabled>当前绑定账户不可用（可保留或更换）</option>
                <option v-for="account in domainDNSAccountOptions" :key="account.id" :value="account.id">
                  {{ account.name }} · {{ account.provider_name || account.provider }} · {{ account.owner_username }}
                </option>
              </select>
            </label>
            <label v-if="form.challenge_type !== 'http-01' && !dnsAccountID && !dnsBound" class="field" for="dns-provider"
              ><span>历史 DNS 服务商 <HelpTip>仅兼容尚未迁移的系统配置；独立账户选择后由账户决定服务商。</HelpTip></span
              ><select
                id="dns-provider"
                name="dns_provider"
                v-model="form.dns_provider"
                :disabled="saving || domainSaveUncertain"
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
          <p v-if="dnsBindingLoading" class="domain-dns-status" role="status">正在读取 DNS 账户和现有绑定……</p>
          <p v-else-if="dnsBindingError" class="form-error" role="alert">{{ dnsBindingError }}</p>
          <label class="switch-field" for="auto-renew"
            ><input
              id="auto-renew"
              name="auto_renew"
              v-model="form.auto_renew"
              :disabled="saving || domainSaveUncertain"
              type="checkbox"
            /><span
              ><strong>启用自动续期</strong
              ><HelpTip>证书剩余有效期达到阈值时自动创建续期任务</HelpTip></span
            ></label
          >
          <fieldset
            class="server-picker"
            :disabled="saving || domainBindingsLoading || !domainBindingsReady || domainSaveUncertain"
            :aria-busy="domainBindingsLoading"
          >
            <legend>
              关联部署服务器
              <HelpTip>仅列出当前域名所属项目的服务器，新域名使用默认项目。可多选；同一服务器可关联多个域名。取消选择只解除当前域名的关联，不删除服务器，也不影响其他域名。</HelpTip>
            </legend>
            <label
              v-for="server in domainServerOptions"
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
            <p v-if="domainBindingsLoading" class="muted" role="status">
              正在读取服务器关联……
            </p>
            <p v-else-if="domainServerOptions.length === 0" class="muted">
              当前项目暂无可用服务器，可先保存域名后添加服务器。
            </p>
          </fieldset>
          <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
          <div class="modal-actions">
            <button
              v-if="!dnsBindingReady"
              class="secondary-button"
              type="button"
              :disabled="saving || dnsBindingLoading || domainSaveUncertain"
              @click="reloadDomainDNS"
            >
              重新读取 DNS 绑定
            </button>
            <button
              v-if="!domainBindingsReady"
              class="secondary-button"
              type="button"
              :disabled="saving || domainBindingsLoading"
              @click="reloadDomainBindings"
            >
              重新读取关联
            </button>
            <button
              class="secondary-button"
              type="button"
              :disabled="saving"
              @click="closeForm"
            >
              取消</button
            ><button
              class="primary-button"
              type="submit"
              :disabled="saving || domainBindingsLoading || !domainBindingsReady || domainSaveUncertain || dnsBindingLoading || !dnsBindingReady"
            >
              {{ saving ? "保存中……" : "保存配置" }}
            </button>
          </div>
        </form>
      </section>
    </div>
</template>

<style scoped src="./Dashboard.css"></style>
<style scoped src="./DomainModal.css"></style>
