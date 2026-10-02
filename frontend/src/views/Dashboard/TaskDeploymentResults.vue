<script setup>
import { deploymentSummary, deploymentStatusLabel } from "./DeploymentFeedback.js";

defineProps({ report: { type: Object, required: true } });
</script>

<template>
  <section aria-label="最近一次尝试的部署结果">
    <h3 class="modal-section-title">本次部署结果</h3>
    <p role="status">{{ deploymentSummary(report) }}</p>
    <div class="attempt-list">
      <article v-for="(item, index) in report.results" :key="item.server_id || index" class="attempt-row">
        <div>
          <strong>{{ item.server || "未命名服务器" }}</strong>
          <span class="muted">{{ item.message || "无附加信息" }}</span>
        </div>
        <span class="tag">{{ deploymentStatusLabel(item.status) }}</span>
      </article>
    </div>
    <p class="muted">逐服务器结果不代表整体部署或站点 HTTPS 校验成功，请结合任务状态判断。</p>
  </section>
</template>

<style scoped src="./Dashboard.css"></style>
