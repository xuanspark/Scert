import { computed, onBeforeUnmount, onMounted, ref } from "vue";

const dayMilliseconds = 86400000;

export function summarizeRuntime(data, now = Date.now()) {
  const domains = data.domains || [];
  const certificates = new Map((data.certificates || []).map((item) => [item.domain?.id, item]));
  const configuredThreshold = Number(data.settings?.acme?.renewal_near_expiry_days);
  const threshold = Number.isFinite(configuredThreshold) && configuredThreshold > 0 ? configuredThreshold : 10;
  const alerts = [];
  let valid = 0;
  let expiring = 0;
  let expired = 0;
  let missing = 0;
  for (const domain of domains) {
    const entry = certificates.get(domain.id);
    const saved = entry?.certificate?.saved;
    const expiresAt = Date.parse(saved?.not_after);
    const startsAt = Date.parse(saved?.not_before);
    let message;
    let severity = "warning";
    if (!saved || !Number.isFinite(expiresAt)) {
      missing += 1;
      message = entry?.certificate_error || "暂无可读取证书";
    } else if (expiresAt <= now) {
      expired += 1;
      message = "证书已到期";
      severity = "danger";
    } else if (Number.isFinite(startsAt) && startsAt > now) {
      missing += 1;
      message = "证书尚未生效";
    } else {
      valid += 1;
      const remaining = Math.ceil((expiresAt - now) / dayMilliseconds);
      if (expiresAt - now <= threshold * dayMilliseconds) {
        expiring += 1;
        message = `证书将在 ${remaining} 天内到期`;
      }
    }
    if (message) alerts.push({ id: domain.id, name: domain.name, message, severity });
  }
  alerts.sort((left, right) => Number(right.severity === "danger") - Number(left.severity === "danger"));
  const tasks = [...(data.tasks || [])].sort((left, right) =>
    (Date.parse(right.created_at) || 0) - (Date.parse(left.created_at) || 0));
  return {
    total: domains.length,
    valid,
    expiring,
    expired,
    missing,
    threshold,
    alerts,
    autoRenew: domains.filter((item) => item.auto_renew === true).length,
    configuredServers: (data.servers || []).length,
    boundDomains: domains.filter((item) => (data.domainServers?.[item.id] || []).length > 0).length,
    activeTasks: tasks.filter((item) => ["queued", "running", "retry_wait", "cancelling"].includes(item.status)).length,
    failedTasks: tasks.filter((item) => item.status === "failed" || item.status === "error").length,
    tasks,
  };
}

export function runtimeTime(value) {
  const date = new Date(value);
  return value && Number.isFinite(date.getTime()) ? date.toLocaleString("zh-CN", { hour12: false }) : "时间未记录";
}

export function useRuntimeOverview(state) {
  const detailView = ref("");
  const clock = ref(Date.now());
  let timer;
  const summary = computed(() => summarizeRuntime({
    domains: state.domains.value,
    certificates: state.certificates.value,
    tasks: state.tasks.value,
    servers: state.servers.value,
    domainServers: state.domainServers.value,
    settings: state.settings.value,
  }, clock.value));
  const visibleTasks = computed(() => detailView.value === "tasks" ? summary.value.tasks : summary.value.tasks.slice(0, 5));
  function taskDomain(task) {
    if (task.task_type === "management_renewal") return "管理页面证书";
    return state.domains.value.find((item) => item.id === task.domain_id)?.name || "系统任务或已移除域名";
  }
  onMounted(() => { timer = window.setInterval(() => { clock.value = Date.now(); }, 60000); });
  onBeforeUnmount(() => { if (timer) window.clearInterval(timer); });
  return { detailView, summary, visibleTasks, taskDomain };
}
