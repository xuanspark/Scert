<script setup>
import { watch } from "vue";
import { dnsProjectLabel, dnsVerificationLabel, useDNSAccounts } from "./DNSAccounts.js";

const props = defineProps({ refreshKey: { type: Object, default: null } });
const {
  accounts, loading, error, pageSize, page, pageCount,
  visibleAccounts, loadAccounts, changePageSize,
} = useDNSAccounts();

watch(() => props.refreshKey, loadAccounts);
</script>

<template>
  <section class="dns-accounts-panel" :aria-busy="loading">
    <header class="dns-accounts-heading">
      <div>
        <h2>DNS 账户管理</h2>
        <span class="dns-accounts-count">{{ loading ? '正在加载' : error ? '加载失败' : `共 ${accounts.length} 个账号` }}</span>
      </div>
      <button class="dns-refresh" type="button" :disabled="loading" @click="loadAccounts">
        {{ loading ? '加载中…' : '刷新列表' }}
      </button>
    </header>

    <p v-if="error" class="dns-error" role="alert">{{ error }}</p>
    <p v-else-if="loading" class="dns-empty" role="status">正在读取已配置的 DNS 账号…</p>
    <p v-else-if="accounts.length === 0" class="dns-empty">暂无可查看的已配置 DNS 账号</p>
    <template v-else>
      <div class="dns-table-scroll" tabindex="0" role="region" aria-label="DNS 账户列表">
        <table class="dns-accounts-table">
          <thead>
            <tr>
              <th scope="col">账户名称</th>
              <th scope="col">DNS 服务商</th>
              <th scope="col">项目归属</th>
              <th scope="col" title="仅展示脱敏后的公开账号标识，不展示密钥或令牌">账号标识</th>
              <th scope="col" title="归属来自已保存的用户记录；历史配置未记录归属时显示未分配">归属用户</th>
              <th scope="col" title="已验证表示保存配置时通过验证，不代表此刻云端权限仍然有效">验证状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="account in visibleAccounts" :key="account.id">
              <td>{{ account.name }}</td>
              <td>{{ account.provider_name }}</td>
              <td>{{ dnsProjectLabel(account) }}</td>
              <td class="dns-identifier">{{ account.account_identifier }}</td>
              <td :class="{ 'dns-muted': account.owner_state !== 'assigned' }">{{ account.owner_username }}</td>
              <td>
                <span
                  class="dns-status"
                  :class="{ verified: account.credential_state === 'available' && account.verification_state === 'verified' }"
                >{{ dnsVerificationLabel(account) }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <footer class="dns-pagination">
        <label>
          每页
          <select v-model.number="pageSize" aria-label="DNS 账户每页条数" @change="changePageSize">
            <option v-for="size in [5, 10, 15, 30]" :key="size" :value="size">{{ size }}</option>
          </select>
          条
        </label>
        <div class="dns-page-actions">
          <button type="button" :disabled="page <= 1" @click="page -= 1">上一页</button>
          <span>第 {{ page }} / {{ pageCount }} 页</span>
          <button type="button" :disabled="page >= pageCount" @click="page += 1">下一页</button>
        </div>
      </footer>
    </template>
  </section>
</template>

<style scoped src="./DNSAccounts.css"></style>
