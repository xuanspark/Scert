<script setup>
import { useAccessDenied } from "../AccessDenied/AccessDenied.js";

const { loggingOut, error, logout } = useAccessDenied();

const roleLabels = {
  super_admin: "超级管理员",
  admin: "管理员",
  user: "普通用户",
};

defineProps({
  activeSection: { type: String, required: true },
  sections: { type: Array, required: true },
  currentUser: { type: Object, default: null },
});

defineEmits(["select", "settings"]);
</script>

<template>
  <nav class="dashboard-navigation" aria-label="管理板块">
    <div class="navigation-heading">
      <img src="/Scert.png" alt="" width="32" height="32" />
      <div>
        <strong>Scert</strong>
        <span>证书管理控制台</span>
      </div>
    </div>
    <template v-for="(section, index) in sections" :key="section.id">
      <p v-if="index === 0 || sections[index - 1].group !== section.group" class="navigation-group">
        {{ section.group }}
      </p>
      <button
        :id="`navigation-${section.id}`"
        class="navigation-item"
        :class="{ active: activeSection === section.id }"
        :aria-current="activeSection === section.id ? 'page' : undefined"
        :title="section.hint"
        type="button"
        @click="$emit('select', section.id)"
      >
        <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path v-if="section.id === 'overview'" d="m3 10 9-7 9 7v10H3V10Zm6 10v-7h6v7" />
          <path v-else-if="section.id === 'activity'" d="M3 12h4l3-8 4 16 3-8h4" />
          <g v-else-if="section.id === 'domains'">
            <circle cx="12" cy="12" r="9" />
            <ellipse cx="12" cy="12" rx="4" ry="9" />
            <path d="M3 12h18" />
          </g>
          <g v-else-if="section.id === 'servers'">
            <rect x="3" y="3" width="18" height="7" rx="2" />
            <rect x="3" y="14" width="18" height="7" rx="2" />
            <path d="M7 6.5h.01M7 17.5h.01M12 6.5h5M12 17.5h5" />
          </g>
          <g v-else-if="section.id === 'dns-accounts'">
            <rect x="3" y="4" width="18" height="16" rx="3" />
            <circle cx="9" cy="10" r="2" />
            <path d="M5 17a4 4 0 0 1 8 0m2-8h4m-4 4h4" />
          </g>
          <g v-else>
            <circle cx="12" cy="8" r="4" />
            <path d="M4 21v-2a8 8 0 0 1 16 0v2" />
          </g>
        </svg>
        <span>{{ section.label }}</span>
        <i v-if="activeSection === section.id" class="navigation-current" aria-hidden="true"></i>
      </button>
    </template>
    <button
      v-if="currentUser?.role === 'super_admin'"
      class="navigation-item"
      type="button"
      title="配置服务、证书签发和邮件通知"
      aria-haspopup="dialog"
      @click="$emit('settings')"
    >
      <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <path d="M4 7h16M4 17h16M8 4v6M16 14v6" />
      </svg>
      <span>系统设置</span>
    </button>
    <div class="navigation-session">
      <div v-if="currentUser" class="navigation-account-card" aria-label="当前登录账号">
        <div class="navigation-account-label">当前账号</div>
        <div class="navigation-account-row">
          <strong class="navigation-account-name" :title="currentUser.account">{{ currentUser.account || "未命名账号" }}</strong>
          <span
            class="navigation-role-badge"
            :class="`role-${currentUser.role}`"
          >{{ roleLabels[currentUser.role] || "未知权限" }}</span>
          <button
            class="navigation-logout-btn navigation-logout"
            type="button"
            :disabled="loggingOut"
            :aria-busy="loggingOut"
            title="注销登录"
            aria-label="注销登录"
            @click="logout"
          >
            <svg class="navigation-power-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
              <path d="M18.36 6.64a9 9 0 1 1-12.73 0M12 2v10" />
            </svg>
            <span class="navigation-logout-text">注销</span>
            <span class="navigation-logout-sr">{{ loggingOut ? "退出中…" : "退出登录" }}</span>
          </button>
        </div>
      </div>
      <div v-else class="navigation-session-anon">
        <button
          class="navigation-logout-btn navigation-logout"
          type="button"
          :disabled="loggingOut"
          :aria-busy="loggingOut"
          title="注销登录"
          aria-label="注销登录"
          @click="logout"
        >
          <svg class="navigation-power-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M18.36 6.64a9 9 0 1 1-12.73 0M12 2v10" />
          </svg>
          <span class="navigation-logout-text">注销</span>
          <span class="navigation-logout-sr">{{ loggingOut ? "退出中…" : "退出登录" }}</span>
        </button>
      </div>
      <p v-if="error" class="navigation-error" role="alert">{{ error }}</p>
      <p class="navigation-footer">SSL 证书管理</p>
    </div>
  </nav>
</template>

<style scoped src="./DashboardNavigation.css"></style>
