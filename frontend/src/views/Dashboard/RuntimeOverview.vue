<script setup>
import HelpTip from "../components/HelpTip/HelpTip.vue";
import { runtimeTime, useRuntimeOverview } from "./RuntimeOverview.js";

const props = defineProps({ state: { type: Object, required: true } });
const emit = defineEmits(["navigate"]);
const {
  loading, error, auditLogs, taskTypeLabel, taskStatusLabels,
  statusClass, displayLabel, auditActionLabel, openTaskDetails, cancelTask,
} = props.state;
const { detailView, summary, visibleTasks, taskDomain } = useRuntimeOverview(props.state);
</script>

<template>
  <div class="runtime-overview">
    <p v-if="loading" class="runtime-empty" role="status">正在读取运行状态……</p>
    <template v-else>
      <p v-if="error" class="runtime-stale" role="alert">本次数据读取失败，以下内容可能不是最新状态。</p>
      <div class="runtime-toolbar">
        <span>{{ detailView ? (detailView === "audit" ? "审计日志 · 最近 20 条" : "任务记录 · 最近 20 条") : "当前托管资源" }}</span>
        <div class="runtime-actions">
          <button v-if="detailView" type="button" @click="detailView = ''">返回运行概览</button>
          <button v-if="detailView !== 'tasks'" type="button" @click="detailView = 'tasks'">任务记录</button>
          <button v-if="detailView !== 'audit'" type="button" @click="detailView = 'audit'">审计日志</button>
        </div>
      </div>
      <template v-if="!detailView">
        <div class="runtime-metrics">
          <article class="runtime-metric">
            <span>有效期内证书 <HelpTip>根据已保存证书的有效期统计，不代表远程站点已完成部署校验。</HelpTip></span>
            <strong>{{ summary.valid }}<small> / {{ summary.total }}</small></strong>
            <p>当前托管域名</p>
          </article>
          <article class="runtime-metric" :class="{ attention: summary.alerts.length }">
            <span>证书待关注</span>
            <strong>{{ summary.alerts.length }}<small> 个</small></strong>
            <p>临期 {{ summary.expiring }} · 到期 {{ summary.expired }} · 不可用 {{ summary.missing }}</p>
          </article>
          <article class="runtime-metric">
            <span>自动续期开启 <HelpTip>表示配置已开启，实际续期结果请查看任务记录。</HelpTip></span>
            <strong>{{ summary.autoRenew }}<small> / {{ summary.total }}</small></strong>
            <p>托管域名续期覆盖</p>
          </article>
          <article class="runtime-metric">
            <span>进行中任务 <HelpTip>仅统计最近 20 条任务中的排队和执行中任务，不是全部历史任务。</HelpTip></span>
            <strong>{{ summary.activeTasks }}<small> 项</small></strong>
            <p>最近 {{ summary.tasks.length }} 条中失败 {{ summary.failedTasks }} 项</p>
          </article>
        </div>
        <div class="runtime-focus-grid">
          <section class="runtime-focus">
            <div class="runtime-section-heading">
              <h3>需要关注</h3>
              <button type="button" @click="emit('navigate', 'domains')">管理域名</button>
            </div>
            <p v-if="summary.total === 0" class="runtime-empty">尚未添加托管域名</p>
            <p v-else-if="summary.alerts.length === 0" class="runtime-clear">当前无临期或不可用证书</p>
            <ul v-else class="runtime-alerts">
              <li v-for="item in summary.alerts.slice(0, 3)" :key="item.id">
                <strong>{{ item.name }}</strong>
                <span :class="item.severity">{{ item.message }}</span>
              </li>
            </ul>
            <small v-if="summary.alerts.length > 3">另有 {{ summary.alerts.length - 3 }} 项，请在域名配置中查看。</small>
          </section>
          <section class="runtime-focus">
            <div class="runtime-section-heading">
              <h3>部署配置 <HelpTip>这里只展示部署配置覆盖，不推断服务器在线状态或证书推送结果。</HelpTip></h3>
              <button type="button" @click="emit('navigate', 'servers')">管理服务器</button>
            </div>
            <dl class="runtime-deployment">
              <div><dt>已配置服务器</dt><dd>{{ summary.configuredServers }} 台</dd></div>
              <div><dt>已绑定部署域名</dt><dd>{{ summary.boundDomains }} / {{ summary.total }}</dd></div>
            </dl>
          </section>
        </div>
      </template>
      <section v-if="detailView !== 'audit'" class="runtime-tasks">
        <div class="runtime-section-heading">
          <h3>{{ detailView === "tasks" ? "任务记录" : "最近任务" }}</h3>
          <span>显示 {{ visibleTasks.length }} 条{{ detailView ? "" : " · 最多 5 条" }}</span>
        </div>
        <p v-if="visibleTasks.length === 0" class="runtime-empty">暂无任务记录</p>
        <div v-else class="runtime-table-scroll" tabindex="0" aria-label="证书任务列表">
          <table>
            <thead><tr><th scope="col">任务 / 对象</th><th scope="col">状态</th><th scope="col">创建时间</th><th scope="col">操作</th></tr></thead>
            <tbody>
              <tr v-for="item in visibleTasks" :key="item.id">
                <td><strong>{{ taskTypeLabel(item.task_type) }}</strong><small>{{ taskDomain(item) }}</small></td>
                <td><span class="runtime-tag" :class="statusClass(item.status)">{{ displayLabel(taskStatusLabels, item.status) }}</span></td>
                <td>{{ runtimeTime(item.created_at) }}</td>
                <td>
                  <button type="button" @click="openTaskDetails(item)">查看详情</button>
                  <button
                    v-if="item.task_type === 'certificate_issue' && (item.status === 'queued' || item.status === 'running')"
                    type="button"
                    @click="cancelTask(item)"
                  >取消任务</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
      <section v-else class="runtime-audit" aria-label="审计日志">
        <p v-if="auditLogs.length === 0" class="runtime-empty">暂无审计日志</p>
        <div v-else class="runtime-audit-list" tabindex="0" aria-label="最近审计记录">
          <article v-for="item in auditLogs" :key="item.id" class="runtime-audit-row">
            <div>
              <strong>{{ auditActionLabel(item.action) }}</strong>
              <small>{{ item.message || item.resource_type }}</small>
            </div>
            <span v-if="item.status === 'success' || item.status === 'failed'" class="runtime-tag" :class="statusClass(item.status)">{{ item.status === "success" ? "成功" : "失败" }}</span>
            <span v-else class="runtime-tag" :class="statusClass(item.status)">{{ displayLabel(taskStatusLabels, item.status) }}</span>
          </article>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped src="./RuntimeOverview.css"></style>
