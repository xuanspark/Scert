<script setup>
defineProps({
  activeSection: { type: String, required: true },
  sections: { type: Array, required: true },
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
          <g v-else-if="section.id === 'keys'">
            <circle cx="8" cy="8" r="5" />
            <path d="m12 12 9 9m-6-6 3-3m0 6 3-3" />
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
    <p class="navigation-footer">SSL 证书管理</p>
  </nav>
</template>

<style scoped src="./DashboardNavigation.css"></style>
