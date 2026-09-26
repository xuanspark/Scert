<script setup>
import { deploymentStatusLabel, deploymentSummary } from "./DeploymentFeedback.js";

defineProps({ result: { type: Object, required: true } });
defineEmits(["close"]);
</script>

<template>
  <div class="modal-backdrop" @click.self="!result.running && $emit('close')">
    <section
      class="modal-card deployment-result"
      role="dialog"
      aria-modal="true"
      aria-labelledby="deployment-result-title"
      :aria-busy="result.running"
    >
      <div class="modal-heading">
        <div>
          <h2 id="deployment-result-title">{{ result.certificateImported ? "证书导入与部署结果" : "证书部署结果" }}</h2>
          <p class="muted">{{ result.domain }}</p>
        </div>
        <button
          class="close-button"
          type="button"
          aria-label="关闭"
          :disabled="result.running"
          @click="$emit('close')"
        >×</button>
      </div>
      <p v-if="result.running" role="status">正在连接服务器、推送证书并校验重载，请稍候……</p>
      <template v-else>
        <p v-if="result.certificateImported" role="status">证书已导入并保存。</p>
        <p v-if="result.error" class="form-error" role="alert">{{ result.error }}</p>
        <p v-if="result.report" class="deployment-summary" role="status">
          {{ deploymentSummary(result.report) }}
        </p>
        <div v-if="result.report" class="deployment-targets" tabindex="0" aria-label="各服务器部署结果">
          <article
            v-for="(item, index) in result.report.results"
            :key="item.server_id || index"
            class="deployment-target"
          >
            <div>
              <strong>{{ item.server || "未命名服务器" }}</strong>
              <p>{{ item.message || "无附加信息" }}</p>
            </div>
            <span class="tag" :class="item.status === 'success' ? 'status-success' : 'status-error'">
              {{ deploymentStatusLabel(item.status) }}
            </span>
          </article>
        </div>
        <p class="muted">此处显示本次推送结果；远程站点是否使用新证书，仍需访问站点验证。</p>
      </template>
      <div class="modal-actions">
        <button class="secondary-button" type="button" :disabled="result.running" @click="$emit('close')">
          关闭
        </button>
      </div>
    </section>
  </div>
</template>

<style scoped src="./Dashboard.css"></style>
<style scoped src="./DeploymentResult.css"></style>
